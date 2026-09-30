package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
)

func generationReferenceFixture(t *testing.T) (string, EnvironmentImageReferences, providers.RealizedImageV1) {
	t.Helper()
	dir := t.TempDir()
	references, err := newEnvironmentImageReferences("demo", dir, bytes.NewReader(bytes.Repeat([]byte{0x44}, environmentReferenceRandomBytes*2)))
	if err != nil {
		t.Fatal(err)
	}
	image := providers.RealizedImageV1{Digest: rendererDigest("1"), ConfigDigest: rendererDigest("2"), RootFSSubject: rendererDigest("3")}
	return dir, references, image
}

func TestCreateEnvironmentImageReferenceTagsAndVerifiesKnownImage(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		switch len(calls) {
		case 1:
			return "", errors.New("not found")
		case 2:
			return "", nil
		case 3:
			return string(image.ConfigDigest) + "\n", nil
		default:
			t.Fatalf("unexpected Docker call: %v", args)
			return "", nil
		}
	}
	if err := createEnvironmentImageReference(context.Background(), image, references, EnvironmentReferenceGeneration, "demo", dir, run); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"image", "inspect", "--format", "{{.Id}}", references.Generation},
		{"image", "tag", string(image.Digest), references.Generation},
		{"image", "inspect", "--format", "{{.Id}}", references.Generation},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("Docker calls = %#v", calls)
	}
}

func TestCreateEnvironmentImageReferenceRejectsExistingReference(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	calls := 0
	err := createEnvironmentImageReference(context.Background(), image, references, EnvironmentReferenceTemporary, "demo", dir, func(context.Context, ...string) (string, error) {
		calls++
		return string(rendererDigest("9")), nil
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") || calls != 1 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestCreateEnvironmentImageReferenceRemovesMismatchedNewReference(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		switch len(calls) {
		case 1:
			return "", errors.New("not found")
		case 2:
			return "", nil
		case 3:
			return string(rendererDigest("8")), nil
		case 4:
			return "", nil
		default:
			t.Fatalf("unexpected Docker call: %v", args)
			return "", nil
		}
	}
	err := createEnvironmentImageReference(context.Background(), image, references, EnvironmentReferenceTemporary, "demo", dir, run)
	if err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(calls[3], []string{"image", "rm", references.Temporary}) {
		t.Fatalf("cleanup call = %v", calls[3])
	}
}

func TestRemoveEnvironmentImageReferenceIsAbsentSafeAndExact(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	t.Run("absent", func(t *testing.T) {
		calls := 0
		err := removeEnvironmentImageReference(context.Background(), image, references, EnvironmentReferenceTemporary, "demo", dir, func(_ context.Context, args ...string) (string, error) {
			calls++
			if !reflect.DeepEqual(args, []string{"image", "ls", "--quiet", "--no-trunc", references.Temporary}) {
				t.Fatalf("Docker args = %v", args)
			}
			return "", nil
		})
		if err != nil || calls != 1 {
			t.Fatalf("calls = %d, error = %v", calls, err)
		}
	})
	t.Run("present", func(t *testing.T) {
		var calls [][]string
		err := removeEnvironmentImageReference(context.Background(), image, references, EnvironmentReferenceGeneration, "demo", dir, func(_ context.Context, args ...string) (string, error) {
			calls = append(calls, append([]string{}, args...))
			if len(calls) == 1 {
				return string(image.ConfigDigest) + "\n", nil
			}
			return "", nil
		})
		if err != nil {
			t.Fatal(err)
		}
		want := [][]string{
			{"image", "ls", "--quiet", "--no-trunc", references.Generation},
			{"image", "rm", "--force", references.Generation},
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("Docker calls = %#v", calls)
		}
	})
}

func TestRemoveEnvironmentImageReferenceRejectsRetargetedReference(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	calls := 0
	err := removeEnvironmentImageReference(context.Background(), image, references, EnvironmentReferenceGeneration, "demo", dir, func(context.Context, ...string) (string, error) {
		calls++
		return string(rendererDigest("9")), nil
	})
	if err == nil || !strings.Contains(err.Error(), "no longer names expected") || calls != 1 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestRemoveEnvironmentGenerationReferenceUsesExactRecordedReference(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	var calls [][]string
	err := removeEnvironmentGenerationReference(context.Background(), image, references.Generation, "demo", dir, func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		if len(calls) == 1 {
			return string(image.ConfigDigest), nil
		}
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"image", "ls", "--quiet", "--no-trunc", references.Generation},
		{"image", "rm", "--force", references.Generation},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("Docker calls = %#v", calls)
	}
}

func TestRemoveEnvironmentGenerationReferenceRejectsAnotherDeploymentBeforeDocker(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	calls := 0
	err := removeEnvironmentGenerationReference(context.Background(), image, references.Generation, "demo", t.TempDir(), func(context.Context, ...string) (string, error) {
		calls++
		return "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "not owned by this deployment") {
		t.Fatalf("ownership error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("ownership failure made %d Docker calls for source dir %s", calls, dir)
	}
}

func TestRemoveEnvironmentGenerationReferenceRejectsRetargetedReference(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	calls := 0
	err := removeEnvironmentGenerationReference(context.Background(), image, references.Generation, "demo", dir, func(context.Context, ...string) (string, error) {
		calls++
		return string(rendererDigest("9")), nil
	})
	if err == nil || !strings.Contains(err.Error(), "no longer names expected") || calls != 1 {
		t.Fatalf("calls = %d, error = %v", calls, err)
	}
}

func TestRemoveLegacyEnvironmentGenerationReferenceUsesOwnedTagWithoutBuildRecord(t *testing.T) {
	dir, references, _ := generationReferenceFixture(t)
	var calls [][]string
	err := removeLegacyEnvironmentGenerationReference(
		context.Background(),
		references.Generation,
		"demo",
		dir,
		func(_ context.Context, args ...string) (string, error) {
			calls = append(calls, append([]string{}, args...))
			if len(calls) == 1 {
				return string(rendererDigest("9")), nil
			}
			return "", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"image", "ls", "--quiet", "--no-trunc", references.Generation},
		{"image", "rm", "--force", references.Generation},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("Docker calls = %#v", calls)
	}
}

func TestRemoveLegacyEnvironmentGenerationReferenceRejectsAnotherDeploymentBeforeDocker(t *testing.T) {
	_, references, _ := generationReferenceFixture(t)
	calls := 0
	err := removeLegacyEnvironmentGenerationReference(
		context.Background(),
		references.Generation,
		"demo",
		t.TempDir(),
		func(context.Context, ...string) (string, error) {
			calls++
			return "", nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "not owned by this deployment") {
		t.Fatalf("ownership error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("ownership failure made %d Docker calls", calls)
	}
}

func TestPortableRuntimeLayerReferenceCreatesAndRemovesExactImage(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		switch len(calls) {
		case 1:
			return "", nil
		case 2:
			return "", nil
		case 3:
			return string(image.ConfigDigest), nil
		case 4:
			return string(image.ConfigDigest), nil
		default:
			return "", nil
		}
	}
	if err := createPortableRuntimeLayerReference(
		context.Background(), image,
		mustPortableGenerationReference(t, image, references.Generation, "demo", dir),
		"demo", dir, false, run,
	); err != nil {
		t.Fatal(err)
	}
	portable := mustPortableGenerationReference(t, image, references.Generation, "demo", dir)
	if err := removePortableRuntimeLayerReference(context.Background(), image, portable, "demo", dir, false, run); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, [][]string{
		{"image", "ls", "--quiet", "--no-trunc", portable},
		{"image", "tag", string(image.Digest), portable},
		{"image", "inspect", "--format", "{{.Id}}", portable},
		{"image", "ls", "--quiet", "--no-trunc", portable},
		{"image", "rm", "--force", portable},
	}) {
		t.Fatalf("Docker calls = %#v", calls)
	}
}

func TestCreatePortableRuntimeLayerReferenceDoesNotTagAfterLookupFailure(t *testing.T) {
	dir, references, image := generationReferenceFixture(t)
	portable := mustPortableGenerationReference(t, image, references.Generation, "demo", dir)
	lookupErr := errors.New("Docker daemon unavailable")
	calls := 0
	run := func(_ context.Context, args ...string) (string, error) {
		calls++
		if !reflect.DeepEqual(args, []string{"image", "ls", "--quiet", "--no-trunc", portable}) {
			t.Fatalf("unexpected Docker call: %v", args)
		}
		return "", lookupErr
	}
	err := createPortableRuntimeLayerReference(t.Context(), image, portable, "demo", dir, false, run)
	if !errors.Is(err, lookupErr) || calls != 1 {
		t.Fatalf("lookup failure = %v; Docker calls = %d", err, calls)
	}
}

func TestPortableRuntimeLayerDigestReferenceRemovesPersistedExactIdentity(t *testing.T) {
	dir, _, image := generationReferenceFixture(t)
	reference, err := NewEnvironmentPortableRuntimeLayerReference(image.ConfigDigest, "demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	run := func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		if len(calls) == 1 {
			return string(image.ConfigDigest), nil
		}
		if len(calls) == 2 {
			return "", nil
		}
		t.Fatalf("unexpected Docker call: %v", args)
		return "", nil
	}
	if err := removePortableRuntimeLayerDigestReferenceByReference(
		context.Background(), reference, "demo", dir, run,
	); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, [][]string{
		{"image", "ls", "--quiet", "--no-trunc", reference},
		{"image", "rm", "--force", reference},
	}) {
		t.Fatalf("Docker calls = %#v", calls)
	}
}

func mustPortableGenerationReference(t *testing.T, image providers.RealizedImageV1, generation, environment, dir string) string {
	t.Helper()
	reference, err := NewEnvironmentPortableRuntimeLayerReferenceForGeneration(image.ConfigDigest, generation, environment, dir)
	if err != nil {
		t.Fatal(err)
	}
	return reference
}
