package dockerdeploy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPrepareProviderInstallDestinationV1ChecksSpaceBeforeWritingPrivateCandidates(t *testing.T) {
	locked := providerInstallPrepareDestinationFixture(t)

	prepared, err := prepareProviderInstallDestinationV1(t.Context(), locked, "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Cleanup() })
	if len(prepared.Files) != 4 {
		t.Fatalf("prepared files = %#v", prepared.Files)
	}
	for _, file := range prepared.Files {
		if _, err := os.Stat(file.TemporaryPath); err != nil {
			t.Fatalf("prepared candidate %q: %v", file.TemporaryPath, err)
		}
		if _, err := os.Stat(file.FinalPath); !os.IsNotExist(err) {
			t.Fatalf("live destination %q changed before publication: %v", file.FinalPath, err)
		}
	}
}

func TestPrepareProviderInstallDestinationV1DoesNotWriteAfterDiskFailure(t *testing.T) {
	locked := providerInstallPrepareDestinationFixture(t)
	want := errors.New("insufficient disk space")
	preparedCalled := false
	backend := providerInstallPrepareDestinationBackendV1{
		files: providerInstallFilesV1,
		diskRequirements: func(_ providerstore.Store, _ providerstore.Store, _ InstalledBuildPublicationInputV1, _ *deploy.EnvironmentGenerationState, candidates []providerInstallFileCandidateV1, _ []PathUpdateAction) ([]providerInstallDiskRequirementV1, error) {
			return []providerInstallDiskRequirementV1{{Path: candidates[0].Path, Bytes: 1}}, nil
		},
		preflight: func([]providerInstallDiskRequirementV1) error { return want },
		prepare: func([]providerInstallFileCandidateV1) (preparedProviderInstallFilesV1, error) {
			preparedCalled = true
			return preparedProviderInstallFilesV1{}, nil
		},
	}
	_, err := prepareProviderInstallDestinationWithV1(t.Context(), locked, "", false, backend)
	if !errors.Is(err, want) || preparedCalled {
		t.Fatalf("error=%v prepared=%v", err, preparedCalled)
	}
	for _, path := range []string{
		filepath.Join(locked.Input.DestinationDeploymentDir, DockerEnvFileName),
		filepath.Join(locked.Input.DestinationDeploymentDir, ComposeFileName),
		filepath.Join(locked.Input.DestinationDeploymentDir, locked.Plan.ControlScript),
		filepath.Join(locked.Input.DestinationDeploymentDir, filepath.FromSlash(embeddedRuntimeFileName())),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("destination %q changed after disk failure: %v", path, err)
		}
	}
}

func providerInstallPrepareDestinationFixture(t *testing.T) lockedProviderInstallV1 {
	t.Helper()
	sourceDir, sourceOperation, sourceStore, source := installedBuildPublicationSourceFixture(t)
	destinationDir := t.TempDir()
	destinationOperation, err := deploy.AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destinationOperation.Unlock() })
	destinationStore, err := providerstore.NewStore(destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, destinationDir, 0xc5)
	plan := providerInstallRunPlanFixture(destinationDir, references)
	plan.Backend = installBackendDockerManaged
	plan.Installation.Scope = "user"
	plan.Installation.UnitPath = ""
	return lockedProviderInstallV1{
		SourceOperation: sourceOperation, DestinationOperation: destinationOperation,
		SourceStore: sourceStore, DestinationStore: destinationStore, SourceBuild: source,
		Plan: plan, References: references,
		Input: providerInstallRunInputV1{SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir},
	}
}
