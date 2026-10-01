package dockerdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

type companionDockerFixtureV1 struct {
	dir          string
	operation    *deploy.OperationLock
	generation   deploy.EnvironmentGenerationState
	owned        OwnedImageReferenceV1
	images       map[string]string
	calls        [][]string
	inspectError error
	afterTag     string
	removeError  error
}

func newCompanionDockerFixtureV1(t *testing.T) *companionDockerFixtureV1 {
	t.Helper()
	dir := t.TempDir()
	_, lock := publicationLockFixture(t, dir, "a", "b", "c")
	owner := ownedReferenceGenerationFixtureV1(t, dir, "demo", lock)
	operation, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := operation.Unlock(); err != nil {
			t.Error(err)
		}
	})
	reference, err := portableGenerationReferenceV1(owner.Reference, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	owned := OwnedImageReferenceV1{Reference: reference, Image: lock.FinalImage}
	// Locally built companion images use the immutable config ID as Digest.
	owned.Image.Digest = owned.Image.ConfigDigest
	f := &companionDockerFixtureV1{dir: dir, operation: operation, generation: owner, owned: owned, images: map[string]string{}}
	record := dockerImageInspectRecord{ID: string(owned.Image.ConfigDigest), OS: "linux", Architecture: "amd64", Config: &dockerImageConfig{}}
	record.RootFS.Layers = []string{string(rendererDigest("c")), string(rendererDigest("e")), string(rendererDigest("f"))}
	content, err := json.Marshal([]dockerImageInspectRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	f.images[string(owned.Image.Digest)] = string(content)
	f.images[string(owned.Image.ConfigDigest)] = string(content)
	return f
}

func (f *companionDockerFixtureV1) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{}, args...))
	switch strings.Join(args[:2], " ") {
	case "image inspect":
		if f.inspectError != nil {
			return "", f.inspectError
		}
		if image, ok := f.images[args[2]]; ok {
			return image, nil
		}
		return "", errors.New("Error response from daemon: No such image: " + args[2])
	case "image tag":
		f.images[args[3]] = f.images[args[2]]
		if f.afterTag != "" {
			f.images[args[3]] = f.afterTag
		}
		return "", nil
	case "image rm":
		if f.removeError != nil {
			return "", f.removeError
		}
		if len(args) != 4 || args[2] != "--force" || args[3] != f.owned.Reference {
			return "", errors.New("removal was not the exact forced untag")
		}
		delete(f.images, args[3])
		return "", nil
	default:
		return "", errors.New("unexpected Docker command")
	}
}

func (f *companionDockerFixtureV1) create(ctx context.Context) error {
	return createPortableEnvironmentReferenceV1(ctx, f.operation, f.owned, f.generation, "demo", f.dir, f.run)
}

func (f *companionDockerFixtureV1) remove(ctx context.Context) error {
	return removePortableEnvironmentReferenceV1(ctx, f.operation, f.owned, f.generation, "demo", f.dir, f.run)
}

func TestPortableCompanionCreateVerifyRetireAndReplayV1(t *testing.T) {
	f := newCompanionDockerFixtureV1(t)
	if err := f.create(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"image", "inspect", f.owned.Reference}, {"image", "inspect", string(f.owned.Image.Digest)}, {"image", "tag", string(f.owned.Image.ConfigDigest), f.owned.Reference}, {"image", "inspect", f.owned.Reference}}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("create calls = %#v", f.calls)
	}
	f.calls = nil
	if err := f.create(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("retry mutated an existing reference: %#v", f.calls)
	}
	if err := verifyPortableEnvironmentReferenceV1(t.Context(), f.operation, f.owned, f.generation, "demo", f.dir, f.run); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := f.remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, [][]string{{"image", "inspect", f.owned.Reference}, {"image", "rm", "--force", f.owned.Reference}}) {
		t.Fatalf("retirement calls = %#v", f.calls)
	}
	if _, present := f.images[string(f.owned.Image.ConfigDigest)]; !present {
		t.Fatal("retirement removed immutable image authority")
	}
	if err := f.remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := verifyPortableEnvironmentReferenceV1(t.Context(), f.operation, f.owned, f.generation, "demo", f.dir, f.run); err == nil {
		t.Fatal("verification accepted an absent reference")
	}
}

func TestPortableCompanionRejectsAbsentAuthorityAndLockV1(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*companionDockerFixtureV1)
	}{
		{"no retained pair", func(f *companionDockerFixtureV1) { f.owned = OwnedImageReferenceV1{} }},
		{"no image authority", func(f *companionDockerFixtureV1) { f.owned.Image.ConfigDigest = "" }},
		{"primary role", func(f *companionDockerFixtureV1) { f.owned.Reference = f.generation.Reference }},
		{"another generation", func(f *companionDockerFixtureV1) {
			f.generation.Reference = f.generation.Reference[:len(f.generation.Reference)-32] + strings.Repeat("02", 16)
		}},
		{"another environment", func(f *companionDockerFixtureV1) {
			f.generation.Reference = strings.Replace(f.generation.Reference, "demo-", "other-", 1)
		}},
		{"another deployment", func(f *companionDockerFixtureV1) { f.dir = t.TempDir() }},
		{"no lock", func(f *companionDockerFixtureV1) { f.operation = nil }},
		{"released lock", func(f *companionDockerFixtureV1) {
			if err := f.operation.Unlock(); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newCompanionDockerFixtureV1(t)
			test.mutate(f)
			if err := f.create(t.Context()); err == nil {
				t.Fatal("creation accepted invalid retained authority")
			}
			if err := f.remove(t.Context()); err == nil {
				t.Fatal("absent removal accepted invalid retained authority")
			}
			if len(f.calls) != 0 {
				t.Fatalf("invalid scope reached Docker: %#v", f.calls)
			}
		})
	}
}

func TestPortableCompanionRejectsNilContextAndMalformedGenerationBeforeDockerV1(t *testing.T) {
	for _, test := range []struct {
		name   string
		ctx    context.Context
		mutate func(*companionDockerFixtureV1)
	}{
		{"nil context", nil, func(*companionDockerFixtureV1) {}},
		{"malformed final generation image", t.Context(), func(f *companionDockerFixtureV1) {
			f.generation.ImageDigest = "not-a-valid-digest"
		}},
	} {
		for _, operation := range []struct {
			name string
			run  func(*companionDockerFixtureV1, context.Context) error
		}{
			{"create", (*companionDockerFixtureV1).create},
			{"absent remove", (*companionDockerFixtureV1).remove},
		} {
			t.Run(test.name+"/"+operation.name, func(t *testing.T) {
				f := newCompanionDockerFixtureV1(t)
				test.mutate(f)
				if err := f.owned.Image.Validate(); err != nil {
					t.Fatalf("fixture companion image is not valid: %v", err)
				}
				wantReference, err := portableGenerationReferenceV1(f.generation.Reference, "demo", f.dir)
				if err == nil && wantReference != f.owned.Reference {
					err = errors.New("fixture companion reference is not retained for this scope")
				}
				if err != nil {
					t.Fatalf("fixture portable pair is not valid: %v", err)
				}
				// The fixture starts without the companion tag, so remove reaches the
				// absence path if invalid context or generation authority is skipped.
				if err := operation.run(f, test.ctx); err == nil {
					t.Fatal("accepted invalid context or malformed generation authority")
				}
				if len(f.calls) != 0 {
					t.Fatalf("invalid context or generation reached Docker: %#v", f.calls)
				}
			})
		}
	}
}

func TestPortableCompanionPreservesInspectionFailuresAndMismatchesV1(t *testing.T) {
	for _, phase := range []string{"collision", "source", "post-tag", "remove"} {
		for _, mismatch := range []string{"config", "rootfs", "platform", "malformed"} {
			t.Run(phase+"/"+mismatch, func(t *testing.T) {
				f := newCompanionDockerFixtureV1(t)
				var records []dockerImageInspectRecord
				if err := json.Unmarshal([]byte(f.images[string(f.owned.Image.Digest)]), &records); err != nil {
					t.Fatal(err)
				}
				switch mismatch {
				case "config":
					records[0].ID = string(rendererDigest("9"))
				case "rootfs":
					records[0].RootFS.Layers = []string{string(rendererDigest("9"))}
				case "platform":
					records[0].Architecture = "arm64"
				}
				data, err := json.Marshal(records)
				if err != nil {
					t.Fatal(err)
				}
				if mismatch == "malformed" {
					data = []byte("invalid JSON")
				}
				switch phase {
				case "collision", "remove":
					f.images[f.owned.Reference] = string(data)
				case "source":
					f.images[string(f.owned.Image.Digest)] = string(data)
				case "post-tag":
					f.afterTag = string(data)
				}
				if phase == "remove" {
					err = f.remove(t.Context())
				} else {
					err = f.create(t.Context())
				}
				if err == nil {
					t.Fatal("mismatch was accepted")
				}
				for _, call := range f.calls {
					if call[1] == "rm" || (phase != "post-tag" && call[1] == "tag") {
						t.Fatalf("mismatch triggered mutation: %#v", f.calls)
					}
				}
				if phase != "source" && f.images[f.owned.Reference] != string(data) {
					t.Fatal("observed mismatched tag was not preserved")
				}
			})
		}
	}
	for _, command := range []string{"create", "remove"} {
		t.Run(command+"/uncertain inspection", func(t *testing.T) {
			f := newCompanionDockerFixtureV1(t)
			f.inspectError = errors.New("Docker daemon unavailable")
			var err error
			if command == "create" {
				err = f.create(t.Context())
			} else {
				err = f.remove(t.Context())
			}
			if err == nil || len(f.calls) != 1 {
				t.Fatalf("uncertain inspection treated as absence: %v, %#v", err, f.calls)
			}
		})
	}
}

func TestPortableCompanionCancellationMissingSourceAndFailedRetirementV1(t *testing.T) {
	f := newCompanionDockerFixtureV1(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.create(ctx); !errors.Is(err, context.Canceled) || len(f.calls) != 0 {
		t.Fatalf("cancelled create = %v, %#v", err, f.calls)
	}
	delete(f.images, string(f.owned.Image.Digest))
	if err := f.create(t.Context()); err == nil {
		t.Fatal("creation accepted missing source")
	}
	for _, call := range f.calls {
		if call[1] == "tag" {
			t.Fatal("missing source was tagged")
		}
	}
	f = newCompanionDockerFixtureV1(t)
	if err := f.create(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.removeError = errors.New("Docker removal failed")
	if err := f.remove(t.Context()); err == nil {
		t.Fatal("failed retirement was accepted")
	}
	if f.images[f.owned.Reference] == "" {
		t.Fatal("failed retirement discarded retry authority")
	}
	f.removeError = nil
	if err := f.remove(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestPortableCompanionIndependentOwnersKeepIdenticalImageV1(t *testing.T) {
	f := newCompanionDockerFixtureV1(t)
	if err := f.create(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := f.owned
	f.generation.Reference = f.generation.Reference[:len(f.generation.Reference)-32] + strings.Repeat("02", 16)
	reference, err := portableGenerationReferenceV1(f.generation.Reference, "demo", f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.owned.Reference = reference
	if err := f.create(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.images[first.Reference] == "" {
		t.Fatal("one owner retired another owner's identical image")
	}
	// Exact untag uses --force even if an exited container retains the image.
	// No image-ID removal or shared cache-tag discovery is permitted.
}

func TestPortableCompanionExitedContainerDockerIntegrationV1(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run companion Docker integration evidence")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	base := os.Getenv("REPLOY_COMPANION_INTEGRATION_IMAGE")
	if base == "" {
		base = "debian:bookworm-slim"
	}
	runDockerIntegration(t, ctx, "pull", base)
	var records []dockerImageInspectRecord
	if err := json.Unmarshal([]byte(runDockerIntegration(t, ctx, "image", "inspect", base)), &records); err != nil || len(records) != 1 {
		t.Fatalf("inspect integration source: %v, records %d", err, len(records))
	}
	platform, err := blueprint.ParsePlatform(records[0].OS + "/" + records[0].Architecture)
	if err != nil {
		t.Fatal(err)
	}
	image, err := inspectBuiltImageCandidate(ctx, BuiltImageCandidate{ImageID: canonical.Digest(records[0].ID)}, platform, runDockerOutput)
	if err != nil {
		t.Fatal(err)
	}
	f := newCompanionDockerFixtureV1(t)
	f.generation.Platform, f.owned.Image = platform, image.Image
	secondGeneration := f.generation
	secondGeneration.Reference = f.generation.Reference[:len(f.generation.Reference)-32] + strings.Repeat("02", 16)
	second := f.owned
	second.Reference, err = portableGenerationReferenceV1(secondGeneration.Reference, "demo", f.dir)
	if err != nil {
		t.Fatal(err)
	}
	// This synthetic caller retains its exact pairs before invoking creation.
	retained, err := json.Marshal([]OwnedImageReferenceV1{f.owned, second})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "retained-companions.json"), retained, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		for _, pair := range []struct {
			owned      OwnedImageReferenceV1
			generation deploy.EnvironmentGenerationState
		}{{f.owned, f.generation}, {second, secondGeneration}} {
			if err := RemovePortableEnvironmentReferenceV1(cleanupCtx, f.operation, pair.owned, pair.generation, "demo", f.dir); err != nil {
				t.Error(err)
			}
		}
	})
	for _, pair := range []struct {
		owned      OwnedImageReferenceV1
		generation deploy.EnvironmentGenerationState
	}{{f.owned, f.generation}, {second, secondGeneration}} {
		if err := CreatePortableEnvironmentReferenceV1(ctx, f.operation, pair.owned, pair.generation, "demo", f.dir); err != nil {
			t.Fatal(err)
		}
	}
	container := uniqueDockerIntegrationName("reploy-companion-exited")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if _, err := runDockerOutput(cleanupCtx, "container", "rm", "--force", container); err != nil {
			t.Error(err)
		}
	})
	runDockerIntegration(t, ctx, "run", "--name", container, "--pull", "never", f.owned.Reference, "/bin/sh", "-c", "exit 0")
	if err := RemovePortableEnvironmentReferenceV1(ctx, f.operation, f.owned, f.generation, "demo", f.dir); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPortableEnvironmentReferenceV1(ctx, f.operation, second, secondGeneration, "demo", f.dir); err != nil {
		t.Fatal(err)
	}
	if status := strings.TrimSpace(runDockerIntegration(t, ctx, "container", "inspect", "--format", "{{.State.Status}}", container)); status != "exited" {
		t.Fatalf("exact untag removed or changed the exited container: %q", status)
	}
}
