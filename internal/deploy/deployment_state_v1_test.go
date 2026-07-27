package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestOperationLockSetsInstallationWithoutChangingEnvironmentState(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	before := readOverlayTestState(t, statePath)
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })
	installation := installationStateV1Fixture(dir)

	state, changed, err := lock.SetInstallationStateV1(installation)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || state.Deployment == nil || !reflect.DeepEqual(state.Deployment.Installation, installation) {
		t.Fatalf("installation update = %#v, changed=%v", state.Deployment, changed)
	}
	stateWithoutDeployment := state
	stateWithoutDeployment.Deployment = nil
	if !reflect.DeepEqual(stateWithoutDeployment, before) {
		t.Fatalf("environment state changed: before=%#v after=%#v", before, stateWithoutDeployment)
	}
	loaded := readOverlayTestState(t, statePath)
	if !reflect.DeepEqual(loaded, state) {
		t.Fatalf("stored state = %#v, want %#v", loaded, state)
	}

	if err := os.Chmod(statePath, 0o640); err != nil {
		t.Fatal(err)
	}
	_, changed, err = lock.SetInstallationStateV1(installation)
	if err != nil || changed {
		t.Fatalf("repeated update changed=%v error=%v", changed, err)
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("no-op installation update rewrote state: mode=%o", info.Mode().Perm())
	}
}

func TestOperationLockClearsExactInstallationWithoutChangingCurrentBuild(t *testing.T) {
	dir := t.TempDir()
	writeOverlayTestState(t, dir)
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	installation := installationStateV1Fixture(dir)
	installed, _, err := lock.SetInstallationStateV1(installation)
	if err != nil {
		t.Fatal(err)
	}
	cleared, changed, err := lock.ClearInstallationStateV1(installation)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || cleared.Deployment != nil || !reflect.DeepEqual(cleared.Current, installed.Current) {
		t.Fatalf("cleared state = %#v, changed=%v", cleared, changed)
	}
	if _, _, err := lock.ClearInstallationStateV1(installation); err == nil {
		t.Fatal("clearing an already-staged deployment unexpectedly succeeded")
	}
}

func TestOperationLockRejectsInstallationWithoutCurrentBuild(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })
	state := StateV1{
		Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t), Platform: stateV1TestPlatform(t),
		Overlay: EmptyRequestOverlayV1(),
	}
	if err := lock.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}

	if _, _, err := lock.SetInstallationStateV1(installationStateV1Fixture(dir)); err == nil || !strings.Contains(err.Error(), "current build") {
		t.Fatalf("missing build error = %v", err)
	}
	loaded, found, err := lock.ReadStateV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, state) {
		t.Fatalf("state after rejected installation = %#v, found=%v error=%v", loaded, found, err)
	}
}

func TestOperationLockRejectsInstallationForAnotherDeployment(t *testing.T) {
	dir := t.TempDir()
	statePath := writeOverlayTestState(t, dir)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })
	installation := installationStateV1Fixture(filepath.Join(dir, "other"))

	if _, _, err := lock.SetInstallationStateV1(installation); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("target mismatch error = %v", err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("rejected installation changed state")
	}
}

func TestOperationLockMarksMatchingConfiguringInstallationReady(t *testing.T) {
	dir := t.TempDir()
	writeOverlayTestState(t, dir)
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })
	ready := installationStateV1Fixture(dir)
	configuring := ready
	configuring.Status = InstallationStatusConfiguring
	if _, _, err := lock.SetInstallationStateV1(configuring); err != nil {
		t.Fatal(err)
	}

	state, changed, err := lock.MarkInstallationReadyV1(ready)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || state.Deployment == nil || !reflect.DeepEqual(state.Deployment.Installation, ready) {
		t.Fatalf("ready state = %#v, changed=%v", state.Deployment, changed)
	}
	state, changed, err = lock.MarkInstallationReadyV1(ready)
	if err != nil || changed || state.Deployment == nil || state.Deployment.Installation.Status != InstallationStatusReady {
		t.Fatalf("repeated ready transition state=%#v changed=%v error=%v", state.Deployment, changed, err)
	}
}

func TestOperationLockRejectsReadyTransitionWhenInstallationFactsChanged(t *testing.T) {
	dir := t.TempDir()
	writeOverlayTestState(t, dir)
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })
	ready := installationStateV1Fixture(dir)
	configuring := ready
	configuring.Status = InstallationStatusConfiguring
	if _, _, err := lock.SetInstallationStateV1(configuring); err != nil {
		t.Fatal(err)
	}
	ready.ContainerName = "another-container"

	if _, _, err := lock.MarkInstallationReadyV1(ready); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("changed installation error = %v", err)
	}
	state, found, err := lock.ReadStateV1()
	if err != nil || !found || state.Deployment == nil || !reflect.DeepEqual(state.Deployment.Installation, configuring) {
		t.Fatalf("state after rejected ready transition=%#v found=%v error=%v", state.Deployment, found, err)
	}
}

func TestOperationLockCommitsInstalledDestinationFromStagedSource(t *testing.T) {
	sourceDir := t.TempDir()
	sourcePath := writeOverlayTestState(t, sourceDir)
	source := readOverlayTestState(t, sourcePath)
	source.BlueprintSource = "blueprint: retained exactly\n"
	source.Staging = &StagingStateV1{Schema: StagingStateSchemaV1}
	destinationDir := t.TempDir()
	destinationLock, err := AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destinationLock.Unlock() })
	installation := installationStateV1Fixture(destinationDir)
	destinationGeneration := *source.Current
	destinationGeneration.Reference = "reploy/env/overlay-test:g-destination"

	installed, changed, err := destinationLock.CommitInstalledStateV1(nil, source, destinationGeneration, installation)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || installed.Deployment == nil || !reflect.DeepEqual(installed.Deployment.Installation, installation) {
		t.Fatalf("installed state = %#v, changed=%v", installed, changed)
	}
	want := source
	want.Current = &destinationGeneration
	want.Staging = nil
	want.Deployment = nil
	withoutDeployment := installed
	withoutDeployment.Deployment = nil
	if !reflect.DeepEqual(withoutDeployment, want) {
		t.Fatalf("installed environment differs from source: source=%#v installed=%#v", source, installed)
	}
	stored, found, err := destinationLock.ReadStateV1()
	if err != nil || !found || !reflect.DeepEqual(stored, installed) {
		t.Fatalf("stored installed state=%#v found=%v error=%v", stored, found, err)
	}
}

func TestOperationLockInstalledCommitPreservesPriorDestinationOnConflict(t *testing.T) {
	sourceDir := t.TempDir()
	source := readOverlayTestState(t, writeOverlayTestState(t, sourceDir))
	destinationDir := t.TempDir()
	destinationPath := writeOverlayTestState(t, destinationDir)
	before, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	destinationLock, err := AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destinationLock.Unlock() })
	wrong := &EnvironmentGenerationState{}
	destinationGeneration := *source.Current
	destinationGeneration.Reference = "reploy/env/overlay-test:g-destination"

	_, _, err = destinationLock.CommitInstalledStateV1(wrong, source, destinationGeneration, installationStateV1Fixture(destinationDir))
	if err == nil || !strings.Contains(err.Error(), "changed before") {
		t.Fatalf("destination conflict error = %v", err)
	}
	after, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("conflicting installed commit changed prior destination state")
	}
}

func TestOperationLockInstalledCommitRejectsDeploymentLocalSourceState(t *testing.T) {
	sourceDir := t.TempDir()
	source := readOverlayTestState(t, writeOverlayTestState(t, sourceDir))
	source.Deployment = &DeploymentStateV1{
		Schema: DeploymentStateSchemaV1, Installation: installationStateV1Fixture(sourceDir),
	}
	destinationDir := t.TempDir()
	destinationLock, err := AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destinationLock.Unlock() })
	destinationGeneration := *source.Current
	destinationGeneration.Reference = "reploy/env/overlay-test:g-destination"

	_, _, err = destinationLock.CommitInstalledStateV1(nil, source, destinationGeneration, installationStateV1Fixture(destinationDir))
	if err == nil || !strings.Contains(err.Error(), "without deployment-local facts") {
		t.Fatalf("deployment-local source error = %v", err)
	}
	if _, found, err := destinationLock.ReadStateV1(); err != nil || found {
		t.Fatalf("destination state after rejected source found=%v error=%v", found, err)
	}
}

func TestOperationLockInstalledCommitRejectsChangedDestinationGeneration(t *testing.T) {
	sourceDir := t.TempDir()
	source := readOverlayTestState(t, writeOverlayTestState(t, sourceDir))
	destinationDir := t.TempDir()
	destinationLock, err := AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destinationLock.Unlock() })
	destinationGeneration := *source.Current
	destinationGeneration.Reference = "reploy/env/overlay-test:g-destination"
	destinationGeneration.ImageDigest = canonical.Digest("sha256:" + strings.Repeat("b", 64))

	_, _, err = destinationLock.CommitInstalledStateV1(nil, source, destinationGeneration, installationStateV1Fixture(destinationDir))
	if err == nil || !strings.Contains(err.Error(), "except for its reference") {
		t.Fatalf("changed destination generation error = %v", err)
	}
	if _, found, err := destinationLock.ReadStateV1(); err != nil || found {
		t.Fatalf("destination state after changed generation found=%v error=%v", found, err)
	}
}

func TestOperationLockInstalledCommitRejectsSourceGenerationReference(t *testing.T) {
	sourceDir := t.TempDir()
	source := readOverlayTestState(t, writeOverlayTestState(t, sourceDir))
	destinationDir := t.TempDir()
	destinationLock, err := AcquireOperationLock(t.Context(), destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destinationLock.Unlock() })

	_, _, err = destinationLock.CommitInstalledStateV1(nil, source, *source.Current, installationStateV1Fixture(destinationDir))
	if err == nil || !strings.Contains(err.Error(), "destination-local reference") {
		t.Fatalf("source generation reference error = %v", err)
	}
}

func installationStateV1Fixture(targetDir string) InstallationStateV1 {
	return InstallationStateV1{
		Schema: InstallationSchemaV1, Status: InstallationStatusReady,
		TargetDir: targetDir, Scope: "system", Service: "demo",
		UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo-1",
		ContainerName: "demo", NetworkName: "demo", Ports: []InstallationPortBindingV1{},
	}
}
