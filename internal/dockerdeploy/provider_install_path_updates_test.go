package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

func TestApplyProviderInstallManagedBindV1PreservesExistingAndSeedsMissingTarget(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := filepath.Join(sourceRoot, "conf")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "config.json"), []byte("staged"), 0o640); err != nil {
		t.Fatal(err)
	}
	action := PathUpdateAction{
		Name: "config", Kind: PathPreserveManagedBind,
		Source: source, Target: filepath.Join(destinationRoot, "conf"),
	}
	locked := providerInstallPathUpdateFixture(destinationRoot, action)
	if err := applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(context.Context, string) (bool, error) { return false, nil },
		run:          func(CommandSpec, RunOptions) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(action.Target, "nested", "config.json"))
	if err != nil || string(content) != "staged" {
		t.Fatalf("seeded content = %q, error = %v", content, err)
	}
	if err := os.WriteFile(filepath.Join(action.Target, "nested", "config.json"), []byte("installed"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "config.json"), []byte("new staging"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(context.Context, string) (bool, error) { return false, nil },
		run:          func(CommandSpec, RunOptions) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(filepath.Join(action.Target, "nested", "config.json"))
	if err != nil || string(content) != "installed" {
		t.Fatalf("preserved content = %q, error = %v", content, err)
	}
}

func TestApplyProviderInstallManagedBindV1ReplacesExistingTarget(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := filepath.Join(sourceRoot, "data")
	target := filepath.Join(destinationRoot, "data")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "new"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "old"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	action := PathUpdateAction{Name: "data", Kind: PathReplaceManagedBind, Source: source, Target: target}
	locked := providerInstallPathUpdateFixture(destinationRoot, action)
	if err := applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(context.Context, string) (bool, error) { return false, nil },
		run:          func(CommandSpec, RunOptions) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "old")); !os.IsNotExist(err) {
		t.Fatalf("old content survived replacement: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(target, "new"))
	if err != nil || string(content) != "new" {
		t.Fatalf("replacement content = %q, error = %v", content, err)
	}
}

func TestApplyProviderInstallManagedBindV1RejectsSymlinks(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	source := filepath.Join(sourceRoot, "conf")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("outside", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	action := PathUpdateAction{Name: "config", Kind: PathReplaceManagedBind, Source: source, Target: filepath.Join(destinationRoot, "conf")}
	err := applyProviderInstallPathUpdatesWithV1(t.Context(), providerInstallPathUpdateFixture(destinationRoot, action), providerInstallPathUpdateBackendV1{
		volumeExists: func(context.Context, string) (bool, error) { return false, nil },
		run:          func(CommandSpec, RunOptions) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to copy symlink") {
		t.Fatalf("symlink error = %v", err)
	}
	if _, err := os.Stat(action.Target); !os.IsNotExist(err) {
		t.Fatalf("failed copy published a target: %v", err)
	}
}

func TestApplyProviderInstallVolumeV1ReplacesFromExistingStagingVolume(t *testing.T) {
	destinationRoot := t.TempDir()
	action := PathUpdateAction{Name: "data", Kind: PathReplaceVolume, Source: "staging-data", Target: "installed-data"}
	locked := providerInstallPathUpdateFixture(destinationRoot, action)
	locked.HostTools.DockerPath = "/usr/bin/docker"
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	locked.SourceBuild.Lock.Platform = platform
	locked.SourceBuild.Lock.FinalImage.ConfigDigest = rendererDigest("a")
	locked.InstallBuild = locked.SourceBuild.Lock
	workspace := testPreparedProbeWorkspace(t, platform, t.TempDir())
	commands := []CommandSpec{}
	exists := map[string]bool{"staging-data": true, "installed-data": true}
	cleaned := false
	err = applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(_ context.Context, name string) (bool, error) { return exists[name], nil },
		prepareProbeWorkspace: func(_ context.Context, _ providerstore.Store, got blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
			if got != platform {
				t.Fatalf("helper platform = %#v", got)
			}
			return workspace, func() error { cleaned = true; return nil }, nil
		},
		run: func(spec CommandSpec, _ RunOptions) error {
			commands = append(commands, spec)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	copyCommand, err := providerInstallVolumeCopyCommandV1(
		"/usr/bin/docker", providerInstallVolumeCopyContainerNameV1("installed-data"), "staging-data", "installed-data",
		locked.InstallBuild.FinalImage.ConfigDigest, platform, workspace,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []CommandSpec{
		{Name: "/usr/bin/docker", Args: []string{"container", "rm", "--force", providerInstallVolumeCopyContainerNameV1("installed-data")}},
		{Name: "/usr/bin/docker", Args: []string{"volume", "rm", "installed-data"}},
		{Name: "/usr/bin/docker", Args: []string{"volume", "create", "--name", "installed-data"}},
		copyCommand,
	}
	if !reflect.DeepEqual(commands, want) || !cleaned {
		t.Fatalf("volume commands = %#v, want %#v", commands, want)
	}
}

func TestApplyProviderInstallVolumeV1CreatesEmptyTargetWhenStagingVolumeIsAbsent(t *testing.T) {
	destinationRoot := t.TempDir()
	action := PathUpdateAction{Name: "data", Kind: PathPreserveVolume, Source: "staging-data", Target: "installed-data"}
	locked := providerInstallPathUpdateFixture(destinationRoot, action)
	locked.HostTools.DockerPath = "/usr/bin/docker"
	commands := []CommandSpec{}
	err := applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(context.Context, string) (bool, error) { return false, nil },
		run: func(spec CommandSpec, _ RunOptions) error {
			commands = append(commands, spec)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []CommandSpec{{Name: "/usr/bin/docker", Args: []string{"volume", "create", "--name", "installed-data"}}}
	want = append([]CommandSpec{{Name: "/usr/bin/docker", Args: []string{"container", "rm", "--force", providerInstallVolumeCopyContainerNameV1("installed-data")}}}, want...)
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("volume commands = %#v, want %#v", commands, want)
	}
}

func TestApplyProviderInstallVolumeV1CleansContainerAndPartialTargetAfterCopyFailure(t *testing.T) {
	destinationRoot := t.TempDir()
	action := PathUpdateAction{Name: "data", Kind: PathReplaceVolume, Source: "staging-data", Target: "installed-data"}
	locked := providerInstallPathUpdateFixture(destinationRoot, action)
	locked.HostTools.DockerPath = "/usr/bin/docker"
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	locked.SourceBuild.Lock.Platform = platform
	locked.SourceBuild.Lock.FinalImage.ConfigDigest = rendererDigest("b")
	locked.InstallBuild = locked.SourceBuild.Lock
	workspace := testPreparedProbeWorkspace(t, platform, t.TempDir())
	wantCopy := errors.New("copy failed")
	commands := []CommandSpec{}
	copyAttempts := 0
	err = applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(_ context.Context, name string) (bool, error) { return name == action.Source, nil },
		prepareProbeWorkspace: func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
			return workspace, func() error { return nil }, nil
		},
		run: func(spec CommandSpec, _ RunOptions) error {
			commands = append(commands, spec)
			if len(spec.Args) > 0 && spec.Args[0] == "run" {
				copyAttempts++
				return wantCopy
			}
			return nil
		},
	})
	if !errors.Is(err, wantCopy) || copyAttempts != 1 {
		t.Fatalf("copy error = %v, attempts = %d", err, copyAttempts)
	}
	containerName := providerInstallVolumeCopyContainerNameV1(action.Target)
	wantTail := []CommandSpec{
		{Name: "/usr/bin/docker", Args: []string{"container", "rm", "--force", containerName}},
		{Name: "/usr/bin/docker", Args: []string{"volume", "rm", action.Target}},
	}
	if len(commands) < len(wantTail) || !reflect.DeepEqual(commands[len(commands)-len(wantTail):], wantTail) {
		t.Fatalf("cleanup commands = %#v, want tail %#v", commands, wantTail)
	}
}

func TestApplyProviderInstallVolumeV1RemovesNewPreserveTargetWhenHelperPreparationFails(t *testing.T) {
	destinationRoot := t.TempDir()
	action := PathUpdateAction{Name: "data", Kind: PathPreserveVolume, Source: "staging-data", Target: "installed-data"}
	locked := providerInstallPathUpdateFixture(destinationRoot, action)
	locked.HostTools.DockerPath = "/usr/bin/docker"
	wantPrepare := errors.New("helper unavailable")
	commands := []CommandSpec{}
	err := applyProviderInstallPathUpdatesWithV1(t.Context(), locked, providerInstallPathUpdateBackendV1{
		volumeExists: func(_ context.Context, name string) (bool, error) { return name == action.Source, nil },
		prepareProbeWorkspace: func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
			return PreparedProbeWorkspace{}, nil, wantPrepare
		},
		run: func(spec CommandSpec, _ RunOptions) error {
			commands = append(commands, spec)
			return nil
		},
	})
	if !errors.Is(err, wantPrepare) {
		t.Fatalf("preparation error = %v", err)
	}
	wantLast := CommandSpec{Name: "/usr/bin/docker", Args: []string{"volume", "rm", action.Target}}
	if len(commands) == 0 || !reflect.DeepEqual(commands[len(commands)-1], wantLast) {
		t.Fatalf("commands = %#v, want final cleanup %#v", commands, wantLast)
	}
}

func TestProviderInstallVolumeExistsV1UsesInspectedDockerPathAndClassifiesOnlyMissingVolume(t *testing.T) {
	wantMissing := errors.New("docker failed: No such volume: demo")
	seen := CommandSpec{}
	exists, err := providerInstallVolumeExistsWithV1(t.Context(), "/opt/docker", "demo", RunOptions{}, func(spec CommandSpec, options RunOptions) error {
		seen = spec
		if options.Context != t.Context() {
			t.Fatal("volume inspection did not use its operation context")
		}
		return wantMissing
	})
	if err != nil || exists {
		t.Fatalf("missing volume result = %v, %v", exists, err)
	}
	wantCommand := CommandSpec{Name: "/opt/docker", Args: []string{"volume", "inspect", "demo"}}
	if !reflect.DeepEqual(seen, wantCommand) {
		t.Fatalf("volume inspection command = %#v", seen)
	}
	wantOther := errors.New("docker failed: executable file not found")
	if _, err := providerInstallVolumeExistsWithV1(t.Context(), "/opt/docker", "demo", RunOptions{}, func(CommandSpec, RunOptions) error { return wantOther }); !errors.Is(err, wantOther) {
		t.Fatalf("non-volume error = %v", err)
	}
}

func providerInstallPathUpdateFixture(destinationRoot string, action PathUpdateAction) lockedProviderInstallV1 {
	return lockedProviderInstallV1{
		Plan: providerInstallationPlanV1{PathUpdates: []PathUpdateAction{action}},
		Input: providerInstallRunInputV1{
			DestinationDeploymentDir: destinationRoot,
			Install:                  providerInstallOptionsV1{Scope: InstallScopeUser},
		},
	}
}
