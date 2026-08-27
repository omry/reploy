package providerstore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omry/reploy/internal/canonical"
)

func TestAcquireArtifactUsesVerifiedCacheWithoutNetworking(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("cached artifact")
	descriptor := acquisitionTestDescriptor(content)
	if _, err := store.PublishExpected(context.Background(), descriptor, strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	client := acquisitionTestClient(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected network request")
	})

	result, err := store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source:   ArtifactSource{ID: "tool:demo/source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/artifact"}},
		Client:   client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provenance.Outcome != AcquisitionOutcomeCacheHit || len(result.Provenance.Attempts) != 0 {
		t.Fatalf("cache provenance = %#v", result.Provenance)
	}
	if requests.Load() != 0 {
		t.Fatalf("cache hit made %d network requests", requests.Load())
	}
}

func TestGeneratedAcquisitionOperationIDsAreProcessUnique(t *testing.T) {
	const count = 256
	ids := make(chan string, count)
	var group sync.WaitGroup
	group.Add(count)
	for range count {
		go func() {
			defer group.Done()
			ids <- newAcquisitionOperationID()
		}()
	}
	group.Wait()
	close(ids)

	seen := make(map[string]struct{}, count)
	for id := range ids {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("generated duplicate operation ID %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != count {
		t.Fatalf("generated %d unique operation IDs, want %d", len(seen), count)
	}
}

func TestAcquireArtifactFallsBackInDeclaredOrderAndRecordsFailures(t *testing.T) {
	content := []byte("verified artifact")
	descriptor := acquisitionTestDescriptor(content)
	var paths []string
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/status":
			return acquisitionTestResponse(request, http.StatusServiceUnavailable, "unavailable", ""), nil
		case "/wrong":
			return acquisitionTestResponse(request, http.StatusOK, "rejected artifact", ""), nil
		case "/good":
			return acquisitionTestResponse(request, http.StatusOK, string(content), ""), nil
		default:
			return acquisitionTestResponse(request, http.StatusNotFound, "", ""), nil
		}
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source: ArtifactSource{
			ID:      "tool:demo/releases/1/sources/demo",
			SHA256:  descriptor.SHA256,
			Mirrors: []string{"https://mirror.example.test/status", "https://mirror.example.test/wrong", "https://mirror.example.test/good"},
		},
		Client:      client,
		OperationID: "fallback-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/status,/wrong,/good" {
		t.Fatalf("mirror order = %v", paths)
	}
	if result.Provenance.SuccessfulMirror != "https://mirror.example.test/good" || len(result.Provenance.Attempts) != 2 {
		t.Fatalf("provenance = %#v", result.Provenance)
	}
	if result.Provenance.Attempts[0].Outcome != AcquisitionOutcomeHTTPStatus || result.Provenance.Attempts[1].Outcome != AcquisitionOutcomeDigestMismatch {
		t.Fatalf("failure outcomes = %#v", result.Provenance.Attempts)
	}
	if err := store.VerifyArtifact(descriptor); err != nil {
		t.Fatalf("published artifact is not verified: %v", err)
	}
	attemptsPath, err := store.AcquisitionAttemptsPath(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	records, err := filepath.Glob(filepath.Join(attemptsPath, "fallback-test", "*.json"))
	if err != nil || len(records) != 2 {
		t.Fatalf("failure records = %v, err = %v", records, err)
	}
	for _, recordPath := range records {
		recordContent, err := os.ReadFile(recordPath)
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]any
		if err := json.Unmarshal(recordContent, &record); err != nil {
			t.Fatal(err)
		}
		if _, found := record["response_body"]; found {
			t.Fatalf("failure record retained response bytes: %s", recordContent)
		}
		if record["mirror"] == "" || record["source_id"] == "" || record["outcome"] == "" {
			t.Fatalf("failure record omitted identity/outcome: %s", recordContent)
		}
	}
	tmpEntries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil || len(tmpEntries) != 0 {
		t.Fatalf("temporary acquisition entries = %v, err = %v", tmpEntries, err)
	}
}

func TestAcquireArtifactClassifiesPublicationFailureBeforeCancel(t *testing.T) {
	content := []byte("published artifact")
	descriptor := acquisitionTestDescriptor(content)
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blobPath, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	var conflictErr error
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		return acquisitionTestResponseWithBody(request, http.StatusOK, &acquisitionConflictBody{
			reader: strings.NewReader(string(content)),
			onEOF: func() {
				if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
					conflictErr = err
					return
				}
				conflictErr = os.Mkdir(blobPath, 0o755)
			},
		}, int64(len(content))), nil
	})

	_, err = store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source: ArtifactSource{
			ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/publish"},
		},
		Client:      client,
		OperationID: "publication-failure-test",
	})
	if conflictErr != nil {
		t.Fatalf("create publication conflict: %v", conflictErr)
	}
	if err == nil || !errors.Is(err, errAcquisitionStorage) {
		t.Fatalf("publication error = %v", err)
	}
	attemptsPath, err := store.AcquisitionAttemptsPath(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	records, err := filepath.Glob(filepath.Join(attemptsPath, "publication-failure-test", "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("publication failure records = %v, err = %v", records, err)
	}
	recordContent, err := os.ReadFile(records[0])
	if err != nil {
		t.Fatal(err)
	}
	var record acquisitionAttemptRecordV1
	if err := json.Unmarshal(recordContent, &record); err != nil {
		t.Fatal(err)
	}
	if record.Outcome != AcquisitionOutcomeTransport {
		t.Fatalf("publication failure outcome = %q, want %q", record.Outcome, AcquisitionOutcomeTransport)
	}
	if record.Outcome == AcquisitionOutcomeTimeout {
		t.Fatal("publication failure was persisted as a timeout")
	}
}

func TestAcquireArtifactRecordsSizeMismatchAndCleansStaging(t *testing.T) {
	content := []byte("expected")
	descriptor := acquisitionTestDescriptor(content)
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		return acquisitionTestResponse(request, http.StatusOK, "short", ""), nil
	})
	_, err = store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source:   ArtifactSource{ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/short"}},
		Policy: func() AcquisitionPolicy {
			policy := DefaultAcquisitionPolicy()
			policy.AttemptsPerMirror = 1
			policy.MaxAttempts = 1
			return policy
		}(),
		Client:      client,
		OperationID: "size-mismatch-test",
	})
	if err == nil || !strings.Contains(err.Error(), "outcome=size-mismatch") {
		t.Fatalf("size mismatch error = %v", err)
	}
	if path, pathErr := store.BlobPath(descriptor.SHA256); pathErr != nil {
		t.Fatal(pathErr)
	} else if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("size-mismatch cache path = %v", statErr)
	}
	attemptsPath, err := store.AcquisitionAttemptsPath(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	records, err := filepath.Glob(filepath.Join(attemptsPath, "size-mismatch-test", "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("size-mismatch records = %v, err = %v", records, err)
	}
	var record acquisitionAttemptRecordV1
	recordContent, err := os.ReadFile(records[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(recordContent, &record); err != nil {
		t.Fatal(err)
	}
	if record.Outcome != AcquisitionOutcomeSizeMismatch {
		t.Fatalf("size-mismatch durable outcome = %q", record.Outcome)
	}
	acquisitionTestAssertTemporaryEntriesEmpty(t, store)
}

func TestAcquireArtifactFallsBackAfterTransportError(t *testing.T) {
	content := []byte("transport fallback")
	descriptor := acquisitionTestDescriptor(content)
	var paths []string
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		if request.URL.Path == "/offline" {
			return nil, errors.New("mirror unavailable")
		}
		return acquisitionTestResponse(request, http.StatusOK, string(content), ""), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultAcquisitionPolicy()
	policy.AttemptsPerMirror = 1
	policy.MaxAttempts = 2
	result, err := store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source: ArtifactSource{
			ID: "source", SHA256: descriptor.SHA256,
			Mirrors: []string{"https://mirror.example.test/offline", "https://mirror.example.test/good"},
		},
		Policy:      policy,
		Client:      client,
		OperationID: "transport-fallback-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/offline,/good" {
		t.Fatalf("transport fallback order = %v", paths)
	}
	if len(result.Provenance.Attempts) != 1 || result.Provenance.Attempts[0].Outcome != AcquisitionOutcomeTransport {
		t.Fatalf("transport fallback provenance = %#v", result.Provenance.Attempts)
	}
}

func TestAcquireArtifactStopsBeforeExceedingAggregateBytes(t *testing.T) {
	content := []byte("expected")
	descriptor := acquisitionTestDescriptor(content)
	var requests atomic.Int32
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return acquisitionTestResponse(request, http.StatusOK, "rejected", ""), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultAcquisitionPolicy()
	policy.AttemptsPerMirror = 1
	policy.MaxAttempts = 2
	policy.MaxBytesTotal = int64(len(content) + 1)
	_, err = store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source: ArtifactSource{
			ID: "source", SHA256: descriptor.SHA256,
			Mirrors: []string{"https://mirror.example.test/first", "https://mirror.example.test/second"},
		},
		Policy:      policy,
		Client:      client,
		OperationID: "aggregate-bytes-test",
	})
	if err == nil || !strings.Contains(err.Error(), "aggregate byte limit reached before the next attempt") {
		t.Fatalf("aggregate byte-limit error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("aggregate byte-limit requests = %d", requests.Load())
	}
	acquisitionTestAssertTemporaryEntriesEmpty(t, store)
}

func TestAcquireArtifactRecordsAttemptTimeoutAndCleansStaging(t *testing.T) {
	content := []byte("timeout")
	descriptor := acquisitionTestDescriptor(content)
	var requests atomic.Int32
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return acquisitionTestResponseWithBody(request, http.StatusOK, acquisitionBlockingBody{ctx: request.Context()}, -1), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultAcquisitionPolicy()
	policy.AttemptTimeout = 5 * time.Millisecond
	policy.TotalTimeout = time.Second
	policy.AttemptsPerMirror = 1
	policy.MaxAttempts = 1
	_, err = store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact:    descriptor,
		Source:      ArtifactSource{ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/attempt-timeout"}},
		Policy:      policy,
		Client:      client,
		OperationID: "attempt-timeout-test",
	})
	if err == nil || !strings.Contains(err.Error(), "outcome=timeout") {
		t.Fatalf("attempt-timeout error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("attempt-timeout requests = %d", requests.Load())
	}
	acquisitionTestAssertTemporaryEntriesEmpty(t, store)
}

func TestAcquireArtifactFallsBackAfterAttemptTimeoutDuringPublication(t *testing.T) {
	content := []byte("publication timeout fallback")
	descriptor := acquisitionTestDescriptor(content)
	var paths []string
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		if request.URL.Path == "/slow-publish" {
			return acquisitionTestResponseWithBody(request, http.StatusOK, &acquisitionEOFBody{
				content: content,
				onRead:  func() { <-request.Context().Done() },
			}, int64(len(content))), nil
		}
		return acquisitionTestResponse(request, http.StatusOK, string(content), ""), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultAcquisitionPolicy()
	policy.AttemptsPerMirror = 1
	policy.MaxAttempts = 2
	policy.AttemptTimeout = 20 * time.Millisecond
	result, err := store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source: ArtifactSource{
			ID: "source", SHA256: descriptor.SHA256,
			Mirrors: []string{
				"https://mirror.example.test/slow-publish",
				"https://mirror.example.test/good",
			},
		},
		Policy: policy,
		Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/slow-publish,/good" {
		t.Fatalf("publication-timeout fallback paths = %v", paths)
	}
	if len(result.Provenance.Attempts) != 1 || result.Provenance.Attempts[0].Outcome != AcquisitionOutcomeTimeout {
		t.Fatalf("publication-timeout provenance = %#v", result.Provenance.Attempts)
	}
	if result.Provenance.SuccessfulMirror != "https://mirror.example.test/good" {
		t.Fatalf("successful mirror = %q", result.Provenance.SuccessfulMirror)
	}
	acquisitionTestAssertTemporaryEntriesEmpty(t, store)
}

func TestAcquireArtifactRecordsAggregateTimeoutAndDoesNotRetry(t *testing.T) {
	content := []byte("timeout")
	descriptor := acquisitionTestDescriptor(content)
	var requests atomic.Int32
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return acquisitionTestResponseWithBody(request, http.StatusOK, acquisitionBlockingBody{ctx: request.Context()}, -1), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultAcquisitionPolicy()
	policy.AttemptTimeout = time.Second
	policy.TotalTimeout = 5 * time.Millisecond
	policy.AttemptsPerMirror = 1
	policy.MaxAttempts = 2
	_, err = store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source: ArtifactSource{
			ID: "source", SHA256: descriptor.SHA256,
			Mirrors: []string{"https://mirror.example.test/first", "https://mirror.example.test/second"},
		},
		Policy:      policy,
		Client:      client,
		OperationID: "aggregate-timeout-test",
	})
	if err == nil || !strings.Contains(err.Error(), "total acquisition time limit reached") || !strings.Contains(err.Error(), "outcome=timeout") {
		t.Fatalf("aggregate-timeout error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("aggregate-timeout requests = %d", requests.Load())
	}
	acquisitionTestAssertTemporaryEntriesEmpty(t, store)
}

func TestAcquireArtifactExhaustionIsDeterministicAndDoesNotPublish(t *testing.T) {
	content := []byte("expected")
	descriptor := acquisitionTestDescriptor(content)
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		return acquisitionTestResponse(request, http.StatusOK, "wrongxxx", ""), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := AcquisitionRequest{
		Artifact:    descriptor,
		Source:      ArtifactSource{ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/a"}},
		Client:      client,
		OperationID: "exhaustion-test",
	}
	_, err = store.AcquireArtifact(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "outcome=digest-mismatch") || strings.Contains(err.Error(), "wrongxxx") {
		t.Fatalf("exhaustion error = %v", err)
	}
	path, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed artifact cache path = %v", statErr)
	}
	entries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary entries after exhaustion = %v, err = %v", entries, err)
	}
}

func TestAcquireArtifactEnforcesAttemptCapsAndTighteningOnly(t *testing.T) {
	content := []byte("content")
	descriptor := acquisitionTestDescriptor(content)
	var requests atomic.Int32
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return acquisitionTestResponse(request, http.StatusBadGateway, "down", ""), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultAcquisitionPolicy()
	policy.AttemptsPerMirror = 2
	policy.MaxAttempts = 3
	_, err = store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source: ArtifactSource{
			ID: "source", SHA256: descriptor.SHA256,
			Mirrors: []string{"https://mirror.example.test/first", "https://mirror.example.test/second"},
		},
		Policy:      policy,
		Client:      client,
		OperationID: "caps-test",
	})
	if err == nil {
		t.Fatal("bounded failed acquisition unexpectedly succeeded")
	}
	if requests.Load() != 3 || !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("attempt cap requests=%d error=%v", requests.Load(), err)
	}

	tooLarge := DefaultAcquisitionPolicy()
	tooLarge.MaxAttempts = CoreMaxArtifactAttempts + 1
	if _, err := store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source:   ArtifactSource{ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/only"}},
		Policy:   tooLarge,
		Client:   client,
	}); err == nil || !strings.Contains(err.Error(), "core cap") {
		t.Fatalf("raised cap was accepted: %v", err)
	}
}

func TestAcquireArtifactBoundsRedirectsWithoutLeakingTarget(t *testing.T) {
	content := []byte("redirected")
	descriptor := acquisitionTestDescriptor(content)
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/start" {
			return acquisitionTestResponse(request, http.StatusFound, "", "https://mirror.example.test/end"), nil
		}
		return acquisitionTestResponse(request, http.StatusOK, string(content), ""), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultAcquisitionPolicy()
	policy.MaxRedirects = 0
	_, err = store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact:    descriptor,
		Source:      ArtifactSource{ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/start"}},
		Policy:      policy,
		Client:      client,
		OperationID: "redirect-test",
	})
	if err == nil || !strings.Contains(err.Error(), "outcome=redirect") || strings.Contains(err.Error(), "/end") {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestAcquireArtifactAllowsQueryOnRedirectTargetWithoutRecordingIt(t *testing.T) {
	content := []byte("redirect query")
	descriptor := acquisitionTestDescriptor(content)
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/start" {
			return acquisitionTestResponse(request, http.StatusFound, "", "https://mirror.example.test/end?sig=secret&expires=123"), nil
		}
		if request.URL.Path != "/end" || request.URL.RawQuery != "sig=secret&expires=123" {
			return nil, fmt.Errorf("unexpected redirected request URL %s", request.URL)
		}
		return acquisitionTestResponse(request, http.StatusOK, string(content), ""), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source: ArtifactSource{
			ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/start"},
		},
		Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provenance.SuccessfulMirror != "https://mirror.example.test/start" {
		t.Fatalf("successful mirror = %q", result.Provenance.SuccessfulMirror)
	}
	if strings.Contains(fmt.Sprintf("%#v", result.Provenance), "secret") {
		t.Fatalf("redirect query leaked into provenance: %#v", result.Provenance)
	}
}

func acquisitionTestDescriptor(content []byte) ArtifactDescriptor {
	digest := sha256.Sum256(content)
	return ArtifactDescriptor{
		LogicalPath: "tools/demo/artifact",
		Kind:        "archive",
		Size:        strconv.Itoa(len(content)),
		SHA256:      canonical.Digest("sha256:" + fmt.Sprintf("%x", digest)),
	}
}

type acquisitionRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip acquisitionRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func acquisitionTestClient(roundTrip acquisitionRoundTripper) *http.Client {
	return &http.Client{Transport: roundTrip}
}

func acquisitionTestResponse(request *http.Request, status int, body string, location string) *http.Response {
	response := acquisitionTestResponseWithBody(request, status, io.NopCloser(strings.NewReader(body)), int64(len(body)))
	if location != "" {
		response.Header.Set("Location", location)
	}
	return response
}

func acquisitionTestResponseWithBody(request *http.Request, status int, body io.ReadCloser, contentLength int64) *http.Response {
	header := make(http.Header)
	return &http.Response{
		StatusCode:    status,
		Status:        strconv.Itoa(status),
		Header:        header,
		Body:          body,
		ContentLength: contentLength,
		Request:       request,
	}
}

type acquisitionEOFBody struct {
	content []byte
	onRead  func()
	read    bool
}

func (body *acquisitionEOFBody) Read(buffer []byte) (int, error) {
	if body.read {
		return 0, io.EOF
	}
	body.read = true
	copy(buffer, body.content)
	if body.onRead != nil {
		body.onRead()
	}
	return len(body.content), io.EOF
}

func (body *acquisitionEOFBody) Close() error { return nil }

type acquisitionConflictBody struct {
	reader    *strings.Reader
	onEOF     func()
	triggered bool
}

func (body *acquisitionConflictBody) Read(buffer []byte) (int, error) {
	read, err := body.reader.Read(buffer)
	if err == io.EOF && !body.triggered {
		body.triggered = true
		body.onEOF()
	}
	return read, err
}

func (body *acquisitionConflictBody) Close() error { return nil }

type acquisitionBlockingBody struct{ ctx context.Context }

func (body acquisitionBlockingBody) Read([]byte) (int, error) {
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (body acquisitionBlockingBody) Close() error { return nil }

func acquisitionTestAssertTemporaryEntriesEmpty(t *testing.T, store Store) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.Root(), "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary acquisition entries = %v, err = %v", entries, err)
	}
}
