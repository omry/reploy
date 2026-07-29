package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestTemporaryBuildBaseReferenceIsDeploymentScoped(t *testing.T) {
	root := t.TempDir()
	store, err := providerstore.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		t.Fatal(err)
	}
	reference, err := temporaryBuildBaseReference(store.Root(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := pathIdentityHash(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reference, temporaryBuildReferencePrefix+hash+":build-") {
		t.Fatalf("temporary reference = %q", reference)
	}
	other, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := temporaryBuildBaseReference(other.Root(), workspace); err == nil || !strings.Contains(err.Error(), "belong") {
		t.Fatalf("cross-deployment workspace error = %v", err)
	}
}

func TestPrepareTemporaryBuildBaseReferenceCreatesVerifiesAndRemoves(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		t.Fatal(err)
	}
	image := providers.RealizedImageV1{
		Digest: rendererDigest("1"), ConfigDigest: rendererDigest("2"), RootFSSubject: rendererDigest("3"),
	}
	var calls [][]string
	run := statefulTemporaryBuildReferenceRunner(t, image.ConfigDigest, &calls)
	reference, cleanup, err := prepareTemporaryBuildBaseReference(context.Background(), store.Root(), workspace, image, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantOperations := []string{"ls", "tag", "inspect", "ls", "rm"}
	operations := make([]string, len(calls))
	for index, call := range calls {
		operations[index] = call[1]
	}
	if !reflect.DeepEqual(operations, wantOperations) {
		t.Fatalf("Docker operations = %v", operations)
	}
	if calls[1][2] != string(image.ConfigDigest) || calls[1][3] != reference || calls[4][2] != reference {
		t.Fatalf("Docker calls = %#v", calls)
	}
}

func TestPrepareTemporaryBuildBaseReferenceCleansFailedVerification(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		t.Fatal(err)
	}
	image := providers.RealizedImageV1{
		Digest: rendererDigest("1"), ConfigDigest: rendererDigest("2"), RootFSSubject: rendererDigest("3"),
	}
	created := false
	removed := false
	run := func(_ context.Context, args ...string) (string, error) {
		switch args[1] {
		case "ls":
			if created {
				return string(image.ConfigDigest), nil
			}
			return "", nil
		case "tag":
			created = true
			return "", nil
		case "inspect":
			return "", errors.New("injected inspection failure")
		case "rm":
			removed = true
			created = false
			return "", nil
		default:
			t.Fatalf("unexpected Docker args: %v", args)
			return "", nil
		}
	}
	if _, _, err := prepareTemporaryBuildBaseReference(context.Background(), store.Root(), workspace, image, run); err == nil || !removed {
		t.Fatalf("removed = %t, error = %v", removed, err)
	}
}

func TestCleanupTemporaryBuildBaseReferenceRemovesCandidateWhenUntagFails(t *testing.T) {
	candidate := BuiltImageCandidate{ImageID: rendererDigest("4")}
	var removed []string
	err := cleanupTemporaryBuildBaseReferenceAfterBuild(
		context.Background(),
		func(context.Context) error { return errors.New("injected untag failure") },
		candidate,
		func(_ context.Context, args ...string) (string, error) {
			removed = append([]string{}, args...)
			return "", nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "untag failure") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"image", "rm", string(candidate.ImageID)}) {
		t.Fatalf("candidate cleanup = %v", removed)
	}
}

func TestCleanupTemporaryBuildBaseReferencePreservesWorkspaceForRecovery(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		t.Fatal(err)
	}
	reference, err := temporaryBuildOutputReference(store.Root(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	candidate := BuiltImageCandidate{
		ImageID:            rendererDigest("4"),
		TemporaryReference: reference,
		Workspace:          workspace,
	}
	err = cleanupTemporaryBuildBaseReferenceAfterBuild(
		t.Context(),
		func(context.Context) error { return errors.New("injected base untag failure") },
		candidate,
		func(_ context.Context, args ...string) (string, error) {
			if args[1] == "ls" {
				return string(candidate.ImageID), nil
			}
			return "", nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "base untag failure") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Lstat(workspace); err != nil {
		t.Fatalf("recovery workspace was removed: %v", err)
	}
}

func stubMaterializationBuildBaseReference(t *testing.T, expected canonical.Digest) {
	t.Helper()
	original := runMaterializationBuildReferenceDocker
	runMaterializationBuildReferenceDocker = statefulTemporaryBuildReferenceRunner(t, expected, nil)
	t.Cleanup(func() { runMaterializationBuildReferenceDocker = original })
}

func stubFinalizationBuildBaseReference(t *testing.T, expected canonical.Digest) {
	t.Helper()
	original := runFinalizationBuildReferenceDocker
	runFinalizationBuildReferenceDocker = statefulTemporaryBuildReferenceRunner(t, expected, nil)
	t.Cleanup(func() { runFinalizationBuildReferenceDocker = original })
}

func statefulTemporaryBuildReferenceRunner(t *testing.T, expected canonical.Digest, calls *[][]string) dockerOutputRunner {
	t.Helper()
	created := map[string]bool{}
	t.Cleanup(func() {
		if len(created) != 0 {
			t.Errorf("temporary build references leaked from test: %#v", created)
		}
	})
	return func(_ context.Context, args ...string) (string, error) {
		if calls != nil {
			*calls = append(*calls, append([]string{}, args...))
		}
		if len(args) < 3 || args[0] != "image" {
			t.Fatalf("unexpected Docker args: %v", args)
		}
		switch args[1] {
		case "ls":
			reference := args[len(args)-1]
			if created[reference] {
				return string(expected) + "\n", nil
			}
			return "", nil
		case "tag":
			if args[2] != string(expected) || !strings.HasPrefix(args[3], temporaryBuildReferencePrefix) {
				t.Fatalf("temporary tag args = %v", args)
			}
			created[args[3]] = true
			return "", nil
		case "inspect":
			reference := args[len(args)-1]
			if !created[reference] {
				t.Fatal("inspected temporary reference before creating it")
			}
			return string(expected) + "\n", nil
		case "rm":
			if !created[args[2]] {
				t.Fatal("removed temporary reference before creating it")
			}
			delete(created, args[2])
			return "", nil
		default:
			t.Fatalf("unexpected Docker args: %v", args)
			return "", nil
		}
	}
}
