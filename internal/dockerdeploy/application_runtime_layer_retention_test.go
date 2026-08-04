package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
)

func retainedApplicationRuntimeLayerFixture() providers.RealizedImageV1 {
	return providers.RealizedImageV1{
		Digest:        rendererDigest("7"),
		ConfigDigest:  rendererDigest("7"),
		RootFSSubject: rendererDigest("8"),
	}
}

func TestRetainVerifiedApplicationRuntimeLayerCreatesContentAddressedReference(t *testing.T) {
	image := retainedApplicationRuntimeLayerFixture()
	candidate := BuiltImageCandidate{
		ImageID: image.ConfigDigest, TemporaryReference: temporaryBuildReferencePrefix + "12345678:build-output",
	}
	reference := verifiedApplicationRuntimeLayerReference(image)
	calls := [][]string{}
	err := retainVerifiedApplicationRuntimeLayer(t.Context(), candidate, image, func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		switch len(calls) {
		case 1:
			return "", errors.New("Error response from daemon: No such image: " + reference)
		case 2:
			return "", nil
		case 3, 4:
			return string(image.ConfigDigest) + "\n", nil
		case 5:
			return "", nil
		default:
			t.Fatalf("unexpected Docker call %d: %#v", len(calls), args)
			return "", nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"image", "inspect", "--format", "{{.Id}}", reference},
		{"image", "tag", string(image.ConfigDigest), reference},
		{"image", "inspect", "--format", "{{.Id}}", reference},
		{"image", "ls", "--quiet", "--no-trunc", candidate.TemporaryReference},
		{"image", "rm", candidate.TemporaryReference},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("Docker calls = %#v, want %#v", calls, want)
	}
}

func TestRetainVerifiedApplicationRuntimeLayerReusesExactReference(t *testing.T) {
	image := retainedApplicationRuntimeLayerFixture()
	candidate := BuiltImageCandidate{
		ImageID: image.ConfigDigest, TemporaryReference: temporaryBuildReferencePrefix + "12345678:build-output",
	}
	reference := verifiedApplicationRuntimeLayerReference(image)
	calls := [][]string{}
	err := retainVerifiedApplicationRuntimeLayer(t.Context(), candidate, image, func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		switch len(calls) {
		case 1, 2:
			return string(image.ConfigDigest), nil
		case 3:
			return "", nil
		default:
			t.Fatalf("unexpected Docker call %d: %#v", len(calls), args)
			return "", nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"image", "inspect", "--format", "{{.Id}}", reference},
		{"image", "ls", "--quiet", "--no-trunc", candidate.TemporaryReference},
		{"image", "rm", candidate.TemporaryReference},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("Docker calls = %#v, want %#v", calls, want)
	}
}

func TestRetainVerifiedApplicationRuntimeLayerRejectsReferenceMismatch(t *testing.T) {
	image := retainedApplicationRuntimeLayerFixture()
	reference := verifiedApplicationRuntimeLayerReference(image)
	calls := 0
	err := retainVerifiedApplicationRuntimeLayer(t.Context(), BuiltImageCandidate{ImageID: image.ConfigDigest}, image, func(_ context.Context, args ...string) (string, error) {
		calls++
		if !reflect.DeepEqual(args, []string{"image", "inspect", "--format", "{{.Id}}", reference}) {
			t.Fatalf("Docker call = %#v", args)
		}
		return string(rendererDigest("9")), nil
	})
	if err == nil || !strings.Contains(err.Error(), "want "+string(image.ConfigDigest)) || calls != 1 {
		t.Fatalf("calls = %d; mismatch error = %v", calls, err)
	}
}

func TestRetainVerifiedApplicationRuntimeLayerRollsBackUnverifiedReference(t *testing.T) {
	image := retainedApplicationRuntimeLayerFixture()
	reference := verifiedApplicationRuntimeLayerReference(image)
	calls := [][]string{}
	err := retainVerifiedApplicationRuntimeLayer(t.Context(), BuiltImageCandidate{ImageID: image.ConfigDigest}, image, func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, append([]string{}, args...))
		switch len(calls) {
		case 1:
			return "", errors.New("Error response from daemon: No such image: " + reference)
		case 2:
			return "", nil
		case 3:
			return "", errors.New("inspect failed")
		case 4:
			return string(image.ConfigDigest), nil
		case 5:
			return "", nil
		default:
			t.Fatalf("unexpected Docker call %d: %#v", len(calls), args)
			return "", nil
		}
	})
	if err == nil || !strings.Contains(err.Error(), "inspect failed") {
		t.Fatalf("rollback error = %v", err)
	}
	want := [][]string{
		{"image", "inspect", "--format", "{{.Id}}", reference},
		{"image", "tag", string(image.ConfigDigest), reference},
		{"image", "inspect", "--format", "{{.Id}}", reference},
		{"image", "ls", "--quiet", "--no-trunc", reference},
		{"image", "rm", "--force", reference},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("Docker calls = %#v, want %#v", calls, want)
	}
}
