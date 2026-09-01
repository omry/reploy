package providerstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/omry/reploy/internal/canonical"
)

// The acquisition caps are core safety limits. A request may select tighter
// values, but it cannot raise any of these limits.
const (
	CoreMaxArtifactMirrors           = 8
	CoreMaxArtifactAttemptsPerMirror = 3
	CoreMaxArtifactAttempts          = 16
	CoreMaxArtifactBytesPerAttempt   = int64(1 << 30)
	CoreMaxArtifactBytesTotal        = int64(4 << 30)
	CoreMaxArtifactElapsed           = 10 * time.Minute
	CoreMaxArtifactRedirects         = 3

	acquisitionAttemptRecordSchemaV1 = "portable-tool-acquisition-attempt-v1"
	acquisitionFailureRecordDir      = "acquisitions"
)

const (
	AcquisitionOutcomeCacheHit       = "cache-hit"
	AcquisitionOutcomeNetwork        = "network"
	AcquisitionOutcomeHTTPStatus     = "http-status"
	AcquisitionOutcomeTransport      = "transport"
	AcquisitionOutcomeTimeout        = "timeout"
	AcquisitionOutcomeRedirect       = "redirect"
	AcquisitionOutcomeSizeMismatch   = "size-mismatch"
	AcquisitionOutcomeDigestMismatch = "digest-mismatch"
)

var acquisitionOperationSequence atomic.Uint64

// ArtifactSource is the bounded, ordered source declaration for one pinned
// artifact. SHA256 must match Artifact.SHA256; mirrors never carry distinct
// content identities.
type ArtifactSource struct {
	ID      string
	SHA256  canonical.Digest
	Mirrors []string
}

// AcquisitionPolicy contains the definition-selected limits for one
// acquisition. Zero policy means DefaultAcquisitionPolicy.
type AcquisitionPolicy struct {
	MaxMirrors         int
	AttemptsPerMirror  int
	MaxAttempts        int
	MaxBytesPerAttempt int64
	MaxBytesTotal      int64
	AttemptTimeout     time.Duration
	TotalTimeout       time.Duration
	MaxRedirects       int
}

func DefaultAcquisitionPolicy() AcquisitionPolicy {
	return AcquisitionPolicy{
		MaxMirrors:         CoreMaxArtifactMirrors,
		AttemptsPerMirror:  1,
		MaxAttempts:        CoreMaxArtifactAttempts,
		MaxBytesPerAttempt: CoreMaxArtifactBytesPerAttempt,
		MaxBytesTotal:      CoreMaxArtifactBytesTotal,
		AttemptTimeout:     time.Minute,
		TotalTimeout:       CoreMaxArtifactElapsed,
		MaxRedirects:       CoreMaxArtifactRedirects,
	}
}

// AcquisitionRequest is the complete immutable input to one acquisition.
// OperationID is optional; when omitted, the store creates a unique local ID
// for the durable failure records.
type AcquisitionRequest struct {
	Artifact    ArtifactDescriptor
	Source      ArtifactSource
	Policy      AcquisitionPolicy
	OperationID string
	// client and network are package-internal hermetic test seams. They are
	// intentionally unavailable to ordinary callers, whose requests always
	// use the safe production transport.
	client  *http.Client
	network *acquisitionNetwork
}

// AcquisitionAttempt is the in-memory summary of one failed network attempt.
// It intentionally contains observed metadata only, never response bytes.
type AcquisitionAttempt struct {
	Mirror         string
	Attempt        int
	Outcome        string
	HTTPStatus     int
	ObservedBytes  int64
	ObservedSHA256 canonical.Digest
	ContentLength  int64
	Redirects      int
}

// AcquisitionProvenance is the outcome that a later lock or plan can carry.
// A cache hit has no successful locator because no locator was contacted.
type AcquisitionProvenance struct {
	OperationID      string
	Outcome          string
	SourceID         string
	SuccessfulMirror string
	// Redirects is the number of sanitized redirect hops followed by the
	// successful attempt. Redirect target locators are deliberately never
	// retained in acquisition provenance.
	Redirects int
	Attempts  []AcquisitionAttempt
}

type AcquisitionResult struct {
	Artifact   ArtifactDescriptor
	Provenance AcquisitionProvenance
}

// AcquireArtifact verifies a pinned artifact from the store's ordered source
// mirrors. The returned descriptor refers only to bytes already verified and
// atomically published in the content-addressed store; callers can use
// OpenVerifiedArtifact to consume those bytes.
func (store Store) AcquireArtifact(ctx context.Context, request AcquisitionRequest) (AcquisitionResult, error) {
	if ctx == nil {
		return AcquisitionResult{}, fmt.Errorf("artifact acquisition context is required")
	}
	if err := request.Artifact.Validate(); err != nil {
		return AcquisitionResult{}, fmt.Errorf("artifact acquisition descriptor: %w", err)
	}
	if err := validateArtifactSource(request.Source, request.Artifact); err != nil {
		return AcquisitionResult{}, err
	}
	policy := request.Policy
	if policy == (AcquisitionPolicy{}) {
		policy = DefaultAcquisitionPolicy()
	}
	if err := validateAcquisitionPolicy(policy, request.Source, request.Artifact); err != nil {
		return AcquisitionResult{}, err
	}
	operationID := request.OperationID
	if operationID == "" {
		operationID = newAcquisitionOperationID()
	}
	if err := validateAcquisitionOperationID(operationID); err != nil {
		return AcquisitionResult{}, err
	}

	cached, err := store.lookupCachedArtifact(request.Artifact)
	if err != nil {
		return AcquisitionResult{}, fmt.Errorf("look up cached artifact: %w", err)
	}
	if cached {
		return AcquisitionResult{
			Artifact: request.Artifact,
			Provenance: AcquisitionProvenance{
				OperationID: operationID,
				Outcome:     AcquisitionOutcomeCacheHit,
				SourceID:    request.Source.ID,
			},
		}, nil
	}

	acquisitionContext := ctx
	cancel := func() {}
	if policy.TotalTimeout > 0 {
		acquisitionContext, cancel = context.WithTimeout(ctx, policy.TotalTimeout)
	}
	defer cancel()

	expectedSize, _ := strconv.ParseInt(request.Artifact.Size, 10, 64)
	bytesRead := int64(0)
	totalAttempts := 0
	failures := make([]AcquisitionAttempt, 0)

	for _, mirror := range request.Source.Mirrors {
		for attempt := 1; attempt <= policy.AttemptsPerMirror && totalAttempts < policy.MaxAttempts; attempt++ {
			if err := acquisitionContext.Err(); err != nil {
				return acquisitionFailure(request.Artifact, failures, totalAttempts, "total acquisition time limit reached")
			}
			remaining := policy.MaxBytesTotal - bytesRead
			if remaining < expectedSize+1 {
				return acquisitionFailure(request.Artifact, failures, totalAttempts, "aggregate byte limit reached before the next attempt")
			}

			totalAttempts++
			attemptContext := acquisitionContext
			attemptCancel := func() {}
			if policy.AttemptTimeout > 0 {
				attemptContext, attemptCancel = context.WithTimeout(acquisitionContext, policy.AttemptTimeout)
			}
			observation, publish, err := store.acquireAttempt(
				attemptContext, request, policy, mirror, attempt, remaining, expectedSize,
			)
			if err != nil && observation.Outcome == "" {
				observation.Outcome = classifyAcquisitionError(attemptContext, err)
			}
			attemptCancel()
			bytesRead += observation.ObservedBytes
			if err != nil {
				if observation.Mirror == "" {
					observation.Mirror = mirror
				}
				if recordErr := store.recordAcquisitionFailure(operationID, request.Artifact, request.Source, observation, totalAttempts); recordErr != nil {
					return AcquisitionResult{}, fmt.Errorf("record failed artifact acquisition attempt: %w", recordErr)
				}
				failures = append(failures, observation.AcquisitionAttempt)
				if errors.Is(err, errAcquisitionStorage) && observation.Outcome != AcquisitionOutcomeTimeout {
					return AcquisitionResult{}, err
				}
				continue
			}
			if !publish {
				return AcquisitionResult{}, fmt.Errorf("artifact acquisition attempt produced no publication")
			}
			return AcquisitionResult{
				Artifact: request.Artifact,
				Provenance: AcquisitionProvenance{
					OperationID:      operationID,
					Outcome:          AcquisitionOutcomeNetwork,
					SourceID:         request.Source.ID,
					SuccessfulMirror: mirror,
					Redirects:        observation.Redirects,
					Attempts:         failures,
				},
			}, nil
		}
		if totalAttempts >= policy.MaxAttempts {
			break
		}
	}

	terminal := "all declared mirrors exhausted"
	if totalAttempts >= policy.MaxAttempts {
		terminal = "aggregate attempt limit reached"
	}
	return acquisitionFailure(request.Artifact, failures, totalAttempts, terminal)
}

// AcquireArtifact is the package-level form for callers that keep the store
// separate from the request construction.
func AcquireArtifact(ctx context.Context, store Store, request AcquisitionRequest) (AcquisitionResult, error) {
	return store.AcquireArtifact(ctx, request)
}

var (
	errAcquisitionAttempt = errors.New("artifact acquisition attempt failed")
	errAcquisitionStorage = errors.New("artifact acquisition storage failure")
)

type acquisitionObservation struct {
	AcquisitionAttempt
}

func (store Store) acquireAttempt(
	ctx context.Context,
	request AcquisitionRequest,
	policy AcquisitionPolicy,
	mirror string,
	attempt int,
	remaining int64,
	expectedSize int64,
) (acquisitionObservation, bool, error) {
	client, redirectCounter := cloneAcquisitionClient(request.client, request.network, policy.MaxRedirects)
	defer client.CloseIdleConnections()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, mirror, nil)
	if err != nil {
		return acquisitionObservation{AcquisitionAttempt: AcquisitionAttempt{Mirror: mirror, Attempt: attempt, Outcome: AcquisitionOutcomeTransport}}, false, err
	}
	if err := pinAcquisitionRequest(httpRequest, request.network); err != nil {
		return acquisitionObservation{AcquisitionAttempt: AcquisitionAttempt{Mirror: mirror, Attempt: attempt, Outcome: AcquisitionOutcomeTransport}}, false, err
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		outcome := AcquisitionOutcomeTransport
		if errors.Is(err, errRejectedRedirect) {
			outcome = AcquisitionOutcomeRedirect
		} else if ctx.Err() != nil {
			outcome = AcquisitionOutcomeTimeout
		}
		return acquisitionObservation{AcquisitionAttempt: AcquisitionAttempt{Mirror: mirror, Attempt: attempt, Outcome: outcome, Redirects: redirectCounter.count}}, false, err
	}
	defer response.Body.Close()
	observation := acquisitionObservation{AcquisitionAttempt: AcquisitionAttempt{
		Mirror:        mirror,
		Attempt:       attempt,
		HTTPStatus:    response.StatusCode,
		ContentLength: response.ContentLength,
		Redirects:     redirectCounter.count,
	}}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		observation.Outcome = AcquisitionOutcomeHTTPStatus
		return observation, false, errAcquisitionAttempt
	}

	workspace, err := store.NewWorkspace("acquisition-*")
	if err != nil {
		return observation, false, fmt.Errorf("%w: create acquisition workspace: %v", errAcquisitionStorage, err)
	}
	defer os.RemoveAll(workspace)
	stagedPath := filepath.Join(workspace, "response")
	staged, err := os.OpenFile(stagedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return observation, false, fmt.Errorf("%w: create staged artifact: %v", errAcquisitionStorage, err)
	}
	hash := sha256.New()
	readLimit := expectedSize + 1
	if policy.MaxBytesPerAttempt < readLimit {
		readLimit = policy.MaxBytesPerAttempt
	}
	if remaining < readLimit {
		readLimit = remaining
	}
	read := io.LimitReader(response.Body, readLimit)
	readBytes, copyErr := io.Copy(io.MultiWriter(staged, hash), read)
	observation.ObservedBytes = readBytes
	closeErr := staged.Close()
	if copyErr != nil || closeErr != nil {
		if ctx.Err() != nil {
			observation.Outcome = AcquisitionOutcomeTimeout
		} else {
			observation.Outcome = AcquisitionOutcomeTransport
		}
		return observation, false, errors.Join(copyErr, closeErr)
	}
	if readBytes != expectedSize {
		observation.Outcome = AcquisitionOutcomeSizeMismatch
		return observation, false, errAcquisitionAttempt
	}
	observation.ObservedSHA256 = canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
	if observation.ObservedSHA256 != request.Artifact.SHA256 {
		observation.Outcome = AcquisitionOutcomeDigestMismatch
		return observation, false, errAcquisitionAttempt
	}

	verified, err := os.Open(stagedPath)
	if err != nil {
		return observation, false, fmt.Errorf("%w: reopen staged artifact: %v", errAcquisitionStorage, err)
	}
	defer verified.Close()
	if _, err := store.PublishExpected(ctx, request.Artifact, verified); err != nil {
		return observation, false, fmt.Errorf("%w: publish verified artifact: %v", errAcquisitionStorage, err)
	}
	return observation, true, nil
}

func validateArtifactSource(source ArtifactSource, artifact ArtifactDescriptor) error {
	if source.ID == "" || len(source.ID) > 512 || strings.IndexFunc(source.ID, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return fmt.Errorf("artifact acquisition source identity is invalid")
	}
	if err := source.SHA256.Validate(); err != nil {
		return fmt.Errorf("artifact acquisition source digest: %w", err)
	}
	if source.SHA256 != artifact.SHA256 {
		return fmt.Errorf("artifact acquisition source digest does not match artifact descriptor")
	}
	if len(source.Mirrors) == 0 || len(source.Mirrors) > CoreMaxArtifactMirrors {
		return fmt.Errorf("artifact acquisition source mirror count must be between 1 and %d", CoreMaxArtifactMirrors)
	}
	seen := make(map[string]struct{}, len(source.Mirrors))
	for index, mirror := range source.Mirrors {
		if !credentialFreeAcquisitionURLString(mirror) {
			return fmt.Errorf("artifact acquisition mirror %d is not a credential-free HTTPS URL", index)
		}
		if _, found := seen[mirror]; found {
			return fmt.Errorf("artifact acquisition mirrors must be unique")
		}
		seen[mirror] = struct{}{}
	}
	return nil
}

// ValidateArtifactSource applies the same immutable source and locator checks
// used by acquisition. Lock decoders use it to reject source evidence that
// could never have authorized a production acquisition.
func ValidateArtifactSource(source ArtifactSource, artifact ArtifactDescriptor) error {
	return validateArtifactSource(source, artifact)
}

func validateAcquisitionPolicy(policy AcquisitionPolicy, source ArtifactSource, artifact ArtifactDescriptor) error {
	expectedSize, err := strconv.ParseInt(artifact.Size, 10, 64)
	if err != nil || expectedSize < 0 || expectedSize == int64(^uint64(0)>>1) {
		return fmt.Errorf("artifact acquisition expected size is not representable")
	}
	if policy.MaxMirrors < 1 || policy.MaxMirrors > CoreMaxArtifactMirrors || len(source.Mirrors) > policy.MaxMirrors {
		return fmt.Errorf("artifact acquisition mirror limit must admit the declared mirrors and not exceed core cap %d", CoreMaxArtifactMirrors)
	}
	if policy.AttemptsPerMirror < 1 || policy.AttemptsPerMirror > CoreMaxArtifactAttemptsPerMirror {
		return fmt.Errorf("artifact acquisition attempts per mirror must not exceed core cap %d", CoreMaxArtifactAttemptsPerMirror)
	}
	if policy.MaxAttempts < 1 || policy.MaxAttempts > CoreMaxArtifactAttempts {
		return fmt.Errorf("artifact acquisition aggregate attempts must not exceed core cap %d", CoreMaxArtifactAttempts)
	}
	if policy.MaxBytesPerAttempt < 1 || policy.MaxBytesPerAttempt > CoreMaxArtifactBytesPerAttempt || policy.MaxBytesTotal < 1 || policy.MaxBytesTotal > CoreMaxArtifactBytesTotal {
		return fmt.Errorf("artifact acquisition byte limits exceed core caps")
	}
	if expectedSize >= policy.MaxBytesPerAttempt || expectedSize >= policy.MaxBytesTotal {
		return fmt.Errorf("artifact acquisition byte limits must allow one byte beyond the expected artifact size")
	}
	if policy.AttemptTimeout <= 0 || policy.AttemptTimeout > CoreMaxArtifactElapsed || policy.TotalTimeout <= 0 || policy.TotalTimeout > CoreMaxArtifactElapsed {
		return fmt.Errorf("artifact acquisition time limits exceed core cap")
	}
	if policy.MaxRedirects < 0 || policy.MaxRedirects > CoreMaxArtifactRedirects {
		return fmt.Errorf("artifact acquisition redirects must not exceed core cap %d", CoreMaxArtifactRedirects)
	}
	return nil
}

func validateAcquisitionOperationID(operationID string) error {
	if len(operationID) == 0 || len(operationID) > 96 {
		return fmt.Errorf("artifact acquisition operation ID is invalid")
	}
	for index, char := range operationID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			if index == 0 && char == '.' {
				return fmt.Errorf("artifact acquisition operation ID is invalid")
			}
			continue
		}
		return fmt.Errorf("artifact acquisition operation ID is invalid")
	}
	return nil
}

func newAcquisitionOperationID() string {
	sequence := acquisitionOperationSequence.Add(1)
	return fmt.Sprintf("acquisition-%d-%d", time.Now().UTC().UnixNano(), sequence)
}

func classifyAcquisitionError(ctx context.Context, err error) string {
	if errors.Is(err, errRejectedRedirect) {
		return AcquisitionOutcomeRedirect
	}
	if ctx != nil && ctx.Err() != nil {
		return AcquisitionOutcomeTimeout
	}
	return AcquisitionOutcomeTransport
}

func acquisitionFailure(artifact ArtifactDescriptor, failures []AcquisitionAttempt, attempts int, terminal string) (AcquisitionResult, error) {
	parts := make([]string, 0, len(failures)+1)
	for _, failure := range failures {
		part := fmt.Sprintf("attempt=%d mirror=%s outcome=%s", failure.Attempt, failure.Mirror, failure.Outcome)
		if failure.HTTPStatus != 0 {
			part += " status=" + strconv.Itoa(failure.HTTPStatus)
		}
		if failure.ObservedBytes != 0 {
			part += " bytes=" + strconv.FormatInt(failure.ObservedBytes, 10)
		}
		parts = append(parts, part)
	}
	parts = append(parts, terminal)
	return AcquisitionResult{}, fmt.Errorf("artifact acquisition exhausted for %s after %d attempts: %s", artifact.SHA256, attempts, strings.Join(parts, "; "))
}

func (store Store) lookupCachedArtifact(descriptor ArtifactDescriptor) (bool, error) {
	parentInfo, err := os.Lstat(filepath.Dir(store.root))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("provider store parent must be a real directory")
	}
	rootInfo, err := os.Lstat(store.root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("provider store root must be a real directory")
	}
	path, err := store.BlobPath(descriptor.SHA256)
	if err != nil {
		return false, err
	}
	current := store.root
	hex := strings.TrimPrefix(string(descriptor.SHA256), "sha256:")
	for _, part := range []string{"blobs", "sha256", hex[:2]} {
		current = filepath.Join(current, part)
		info, inspectErr := os.Lstat(current)
		if errors.Is(inspectErr, os.ErrNotExist) {
			return false, nil
		}
		if inspectErr != nil {
			return false, inspectErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("provider store cache directory must be a real directory: %s", current)
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("provider store cached artifact must be a regular file")
	}
	if err := store.VerifyArtifact(descriptor); err != nil {
		return false, err
	}
	return true, nil
}

type acquisitionAttemptRecordV1 struct {
	Schema         string           `json:"schema"`
	OperationID    string           `json:"operation_id"`
	ArtifactSHA256 canonical.Digest `json:"artifact_sha256"`
	SourceID       string           `json:"source_id"`
	Mirror         string           `json:"mirror"`
	Attempt        string           `json:"attempt"`
	Outcome        string           `json:"outcome"`
	HTTPStatus     string           `json:"http_status"`
	ObservedBytes  string           `json:"observed_bytes"`
	ObservedSHA256 canonical.Digest `json:"observed_sha256"`
	ContentLength  string           `json:"content_length"`
	Redirects      string           `json:"redirects"`
}

func (store Store) recordAcquisitionFailure(operationID string, artifact ArtifactDescriptor, source ArtifactSource, observation acquisitionObservation, sequence int) error {
	record := acquisitionAttemptRecordV1{
		Schema:         acquisitionAttemptRecordSchemaV1,
		OperationID:    operationID,
		ArtifactSHA256: artifact.SHA256,
		SourceID:       source.ID,
		Mirror:         observation.Mirror,
		Attempt:        strconv.Itoa(observation.Attempt),
		Outcome:        observation.Outcome,
		HTTPStatus:     strconv.Itoa(observation.HTTPStatus),
		ObservedBytes:  strconv.FormatInt(observation.ObservedBytes, 10),
		ObservedSHA256: observation.ObservedSHA256,
		ContentLength:  strconv.FormatInt(observation.ContentLength, 10),
		Redirects:      strconv.Itoa(observation.Redirects),
	}
	content, err := canonical.Marshal(record)
	if err != nil {
		return err
	}
	finalPath, err := store.acquisitionAttemptPath(artifact, operationID, sequence)
	if err != nil {
		return err
	}
	temporary, err := store.writeTemporary(context.Background(), "acquisition-record-*", strings.NewReader(string(content)))
	if err != nil {
		return err
	}
	defer os.Remove(temporary.path)
	hex := strings.TrimPrefix(string(artifact.SHA256), "sha256:")
	for _, directory := range []string{
		filepath.Join(store.root, acquisitionFailureRecordDir),
		filepath.Join(store.root, acquisitionFailureRecordDir, "sha256"),
		filepath.Join(store.root, acquisitionFailureRecordDir, "sha256", hex[:2]),
		filepath.Join(store.root, acquisitionFailureRecordDir, "sha256", hex[:2], hex),
		filepath.Dir(finalPath),
	} {
		if err := ensureRealDirectory(directory); err != nil {
			return err
		}
	}
	if err := publishTemporary(temporary.path, finalPath, func() error {
		existing, readErr := readRegularFile(finalPath, "provider store acquisition attempt")
		if readErr != nil {
			return readErr
		}
		if string(existing) != string(content) {
			return fmt.Errorf("existing provider store acquisition attempt differs")
		}
		return nil
	}); err != nil {
		return fmt.Errorf("publish provider store acquisition attempt: %w", err)
	}
	return nil
}

func (store Store) acquisitionAttemptPath(artifact ArtifactDescriptor, operationID string, sequence int) (string, error) {
	if err := artifact.Validate(); err != nil {
		return "", err
	}
	if err := validateAcquisitionOperationID(operationID); err != nil {
		return "", err
	}
	if sequence < 1 {
		return "", fmt.Errorf("artifact acquisition attempt sequence must be positive")
	}
	hex := strings.TrimPrefix(string(artifact.SHA256), "sha256:")
	return filepath.Join(store.root, acquisitionFailureRecordDir, "sha256", hex[:2], hex, operationID, fmt.Sprintf("attempt-%06d.json", sequence)), nil
}

// AcquisitionAttemptsPath returns the durable failure-record directory for an
// artifact. It does not create the directory.
func (store Store) AcquisitionAttemptsPath(descriptor ArtifactDescriptor) (string, error) {
	if err := descriptor.Validate(); err != nil {
		return "", err
	}
	hex := strings.TrimPrefix(string(descriptor.SHA256), "sha256:")
	return filepath.Join(store.root, acquisitionFailureRecordDir, "sha256", hex[:2], hex), nil
}
