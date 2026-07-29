package dockerdeploy

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/omry/reploy/internal/providerstore"
)

func stubNoAbandonedBuildReferences(t *testing.T) {
	t.Helper()
	previous := runAbandonedBuildReferenceCleanupDocker
	t.Cleanup(func() { runAbandonedBuildReferenceCleanupDocker = previous })
	runAbandonedBuildReferenceCleanupDocker = func(
		_ context.Context,
		args ...string,
	) (string, error) {
		t.Fatalf("unexpected abandoned-reference command without a build workspace: %v", args)
		return "", nil
	}
}

func TestRemoveAbandonedBuildReferencesDerivesExactWorkspaceReferences(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	buildWorkspace, err := store.NewWorkspace("build-*")
	if err != nil {
		t.Fatal(err)
	}
	finalizeWorkspace, err := store.NewWorkspace("finalize-*")
	if err != nil {
		t.Fatal(err)
	}
	references := []string{}
	for _, workspace := range []string{buildWorkspace, finalizeWorkspace} {
		baseReference, err := temporaryBuildBaseReference(store.Root(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		outputReference, err := temporaryBuildOutputReference(store.Root(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		references = append(references, baseReference, outputReference)
	}
	sort.Strings(references)
	previous := runAbandonedBuildReferenceCleanupDocker
	t.Cleanup(func() { runAbandonedBuildReferenceCleanupDocker = previous })
	var inspected []string
	var removed []string
	runAbandonedBuildReferenceCleanupDocker = func(
		_ context.Context,
		args ...string,
	) (string, error) {
		switch args[1] {
		case "ls":
			reference := args[len(args)-1]
			inspected = append(inspected, reference)
			if !reflect.DeepEqual(
				args,
				[]string{"image", "ls", "--quiet", "--no-trunc", reference},
			) {
				t.Fatalf("inspect command = %v", args)
			}
			return string(rendererDigest("a")) + "\n", nil
		case "rm":
			removed = append(removed, args[2])
			return "", nil
		default:
			t.Fatalf("unexpected Docker command: %v", args)
			return "", nil
		}
	}
	if err := removeAbandonedBuildReferences(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inspected, references) ||
		!reflect.DeepEqual(removed, references) {
		t.Fatalf(
			"inspected/removed references = %v/%v, want %v",
			inspected,
			removed,
			references,
		)
	}
}
