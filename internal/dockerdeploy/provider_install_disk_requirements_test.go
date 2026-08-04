package dockerdeploy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestProviderInstallDiskRequirementsV1CountsPublicationPeakAndCandidates(t *testing.T) {
	sourceDir, _, sourceStore, source := installedBuildPublicationSourceFixture(t)
	destinationDir := t.TempDir()
	destinationStore, err := providerstore.NewStore(destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	references := fixedPublicationReferences(t, destinationDir, 0xc1)
	installation := installedBuildPublicationInstallation(destinationDir)
	installation.Status = deploy.InstallationStatusConfiguring
	publication := InstalledBuildPublicationInputV1{
		Environment: "demo", SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
		Source: source, Build: source.Lock, Installation: installation, References: references,
	}
	candidates := []providerInstallFileCandidateV1{
		{Path: filepath.Join(destinationDir, DockerEnvFileName), Content: []byte("env"), Mode: 0o600},
		{Path: filepath.Join(destinationDir, ComposeFileName), Content: []byte("compose"), Mode: 0o644},
	}
	requirements, err := providerInstallDiskRequirementsV1(sourceStore, destinationStore, publication, nil, candidates, []PathUpdateAction{})
	if err != nil {
		t.Fatal(err)
	}
	if len(requirements) != 3 || requirements[0].Path != destinationStore.Root() {
		t.Fatalf("requirements = %#v", requirements)
	}
	if requirements[1].Path != candidates[0].Path || requirements[1].Bytes != uint64(len(candidates[0].Content)) || requirements[2].Path != candidates[1].Path || requirements[2].Bytes != uint64(len(candidates[1].Content)) {
		t.Fatalf("candidate requirements = %#v", requirements[1:])
	}

	closure, closureBytes, err := deploy.InspectBuildLockStoreClosure(source.Lock, sourceStore, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	lockContent, err := deploy.EncodeBuildLockV1(source.Lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	destinationGeneration := source.Generation
	destinationGeneration.Reference = references.Generation
	destinationState := source.State
	destinationState.Current = &destinationGeneration
	destinationState.Deployment = &deploy.DeploymentStateV1{Schema: deploy.DeploymentStateSchemaV1, Installation: installation}
	stateContent, err := deploy.EncodeStateV1(destinationState)
	if err != nil {
		t.Fatal(err)
	}
	pending := deploy.PendingBuildV1{
		Schema: deploy.PendingBuildSchemaV1, Old: nil,
		Candidate: deploy.PendingCandidateV1{
			TemporaryReference: references.Temporary, GenerationReference: references.Generation,
			Image: source.Lock.FinalImage, BuildLockDigest: source.Generation.BuildLockDigest, StoreObjects: closure,
		},
		Cleanup: publicationCleanupItems(references, nil),
	}
	maximumPending := 0
	for _, phase := range []string{deploy.PendingBuildPhaseValidated, deploy.PendingBuildPhaseGenerationCreated, deploy.PendingBuildPhaseLockPublished, deploy.PendingBuildPhaseStateCommitted, deploy.PendingBuildPhaseCleanup} {
		pending.Phase = phase
		content, err := deploy.EncodePendingBuild(pending)
		if err != nil {
			t.Fatal(err)
		}
		if len(content) > maximumPending {
			maximumPending = len(content)
		}
	}
	want := closureBytes + uint64(len(lockContent)) + uint64(len(stateContent)) + 2*uint64(maximumPending)
	if requirements[0].Bytes != want {
		t.Fatalf("destination requirement = %d, want %d", requirements[0].Bytes, want)
	}
}

func TestProviderInstallDiskRequirementsV1RequiresConfiguringStateAndSortedCandidates(t *testing.T) {
	sourceDir, _, sourceStore, source := installedBuildPublicationSourceFixture(t)
	destinationDir := t.TempDir()
	destinationStore, err := providerstore.NewStore(destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	publication := InstalledBuildPublicationInputV1{
		Environment: "demo", SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
		Source: source, Build: source.Lock, Installation: installedBuildPublicationInstallation(destinationDir),
		References: fixedPublicationReferences(t, destinationDir, 0xc2),
	}
	if _, err := providerInstallDiskRequirementsV1(sourceStore, destinationStore, publication, nil, []providerInstallFileCandidateV1{}, []PathUpdateAction{}); err == nil {
		t.Fatal("expected ready installation rejection")
	}
	publication.Installation.Status = deploy.InstallationStatusConfiguring
	path := filepath.Join(destinationDir, "same")
	if _, err := providerInstallDiskRequirementsV1(sourceStore, destinationStore, publication, nil, []providerInstallFileCandidateV1{
		{Path: path, Content: []byte("a"), Mode: 0o600},
		{Path: path, Content: []byte("b"), Mode: 0o600},
	}, []PathUpdateAction{}); err == nil {
		t.Fatal("expected duplicate candidate rejection")
	}
}

func TestProviderInstallDiskRequirementsV1CountsManagedBindCopy(t *testing.T) {
	sourceDir, _, sourceStore, source := installedBuildPublicationSourceFixture(t)
	destinationDir := t.TempDir()
	destinationStore, err := providerstore.NewStore(destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	managedSource := filepath.Join(sourceDir, "conf")
	if err := os.MkdirAll(filepath.Join(managedSource, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedSource, "a"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedSource, "nested", "b"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	installation := installedBuildPublicationInstallation(destinationDir)
	installation.Status = deploy.InstallationStatusConfiguring
	publication := InstalledBuildPublicationInputV1{
		Environment: "demo", SourceDeploymentDir: sourceDir, DestinationDeploymentDir: destinationDir,
		Source: source, Build: source.Lock, Installation: installation, References: fixedPublicationReferences(t, destinationDir, 0xc3),
	}
	target := filepath.Join(destinationDir, "conf")
	requirements, err := providerInstallDiskRequirementsV1(
		sourceStore, destinationStore, publication, nil, []providerInstallFileCandidateV1{},
		[]PathUpdateAction{{Name: "config", Kind: PathPreserveManagedBind, Source: managedSource, Target: target}},
	)
	if err != nil {
		t.Fatal(err)
	}
	last := requirements[len(requirements)-1]
	if last.Path != target || last.Bytes != 8 {
		t.Fatalf("managed bind requirement = %#v", last)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	requirements, err = providerInstallDiskRequirementsV1(
		sourceStore, destinationStore, publication, nil, []providerInstallFileCandidateV1{},
		[]PathUpdateAction{{Name: "config", Kind: PathPreserveManagedBind, Source: managedSource, Target: target}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(requirements) != 1 {
		t.Fatalf("preserved existing target requirements = %#v", requirements)
	}
}
