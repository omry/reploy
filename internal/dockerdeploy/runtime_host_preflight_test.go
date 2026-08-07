package dockerdeploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func TestValidateRuntimeHostSourcesV1AcceptsExactPlanWithoutGeneratedSources(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{
		{Destination: "/mnt/config", SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true},
		{Destination: environmentTemporaryHome, SourceKind: deploy.RuntimeMountSourceGenerated},
	})
	sources := []RuntimeHostSourceV1{{
		Destination: "/mnt/config", HostPath: config, SourceKind: deploy.RuntimeMountSourceDirectory,
		Authority: runtimeHostAuthorityInputV1, ReadOnly: true,
	}}
	if err := ValidateRuntimeHostSourcesV1(policy, "command/check", 1000, sources); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRuntimeHostSourcesV1RejectsPlanAndSourceDrift(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "config")
	if err := os.WriteFile(file, []byte("value=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{
		Destination: "/mnt/config", SourceKind: deploy.RuntimeMountSourceFile, ReadOnly: true,
	}})
	valid := RuntimeHostSourceV1{
		Destination: "/mnt/config", HostPath: file, SourceKind: deploy.RuntimeMountSourceFile,
		Authority: runtimeHostAuthorityInputV1, ReadOnly: true,
	}
	for _, test := range []struct {
		name    string
		planID  string
		sources []RuntimeHostSourceV1
		want    string
	}{
		{name: "unknown plan", planID: "command/other", sources: []RuntimeHostSourceV1{valid}, want: "absent"},
		{name: "missing", planID: "command/check", sources: []RuntimeHostSourceV1{}, want: "missing"},
		{name: "access", planID: "command/check", sources: []RuntimeHostSourceV1{{Destination: valid.Destination, HostPath: file, SourceKind: valid.SourceKind}}, want: "access policy changed"},
		{name: "kind", planID: "command/check", sources: []RuntimeHostSourceV1{{Destination: valid.Destination, HostPath: file, SourceKind: deploy.RuntimeMountSourceDirectory, Authority: valid.Authority, ReadOnly: true}}, want: "kind or access"},
		{name: "authority", planID: "command/check", sources: []RuntimeHostSourceV1{{Destination: valid.Destination, HostPath: file, SourceKind: valid.SourceKind, Authority: runtimeHostAuthoritySharedStateV1, ReadOnly: true}}, want: "kind or access"},
		{name: "unexpected", planID: "command/check", sources: []RuntimeHostSourceV1{valid, {Destination: "/mnt/extra", HostPath: root, SourceKind: deploy.RuntimeMountSourceDirectory}}, want: "unexpected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRuntimeHostSourcesV1(policy, test.planID, 1000, test.sources)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateRuntimeHostSourcesV1RejectsChangedFilesystemKind(t *testing.T) {
	root := t.TempDir()
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{
		Destination: "/mnt/config", SourceKind: deploy.RuntimeMountSourceFile, ReadOnly: true,
	}})
	err := ValidateRuntimeHostSourcesV1(policy, "command/check", 1000, []RuntimeHostSourceV1{{
		Destination: "/mnt/config", HostPath: root, SourceKind: deploy.RuntimeMountSourceFile,
		Authority: runtimeHostAuthorityInputV1, ReadOnly: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("filesystem kind error = %v", err)
	}
}

func TestValidateRuntimeHostSourcesV1RejectsProtectedHostSystemTrees(t *testing.T) {
	root := string(filepath.Separator)
	if volume := filepath.VolumeName(t.TempDir()); volume != "" {
		root = volume + string(filepath.Separator)
	}
	type testCase struct {
		name string
		path string
		kind string
	}
	tests := []testCase{
		{name: "filesystem root", path: root, kind: deploy.RuntimeMountSourceDirectory},
	}
	if runtime.GOOS != "windows" {
		tests = append(tests,
			testCase{name: "proc descendant", path: "/proc/self", kind: deploy.RuntimeMountSourceDirectory},
			testCase{name: "proc process-relative directory", path: "/proc/self/cwd", kind: deploy.RuntimeMountSourceDirectory},
			testCase{name: "dev descendant", path: "/dev/null", kind: deploy.RuntimeMountSourceFile},
			testCase{name: "sys", path: "/sys", kind: deploy.RuntimeMountSourceDirectory},
		)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := os.Stat(test.path); os.IsNotExist(err) {
				t.Skipf("host path %q is absent", test.path)
			} else if err != nil {
				t.Fatal(err)
			}
			policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{
				Destination: "/mnt/host", SourceKind: test.kind, ReadOnly: true,
			}})
			err := ValidateRuntimeHostSourcesV1(policy, "command/check", 1000, []RuntimeHostSourceV1{{
				Destination: "/mnt/host", HostPath: test.path, SourceKind: test.kind,
				Authority: runtimeHostAuthorityInputV1, ReadOnly: true,
			}})
			if err == nil || !strings.Contains(err.Error(), "protected host system source") {
				t.Fatalf("protected host tree error = %v", err)
			}
		})
	}
}

func TestValidateRuntimeHostSourcesV1RejectsProtectedHostSystemTreeAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a directory symlink requires additional privileges on Windows")
	}
	protected := "/dev"
	if _, err := os.Stat(protected); os.IsNotExist(err) {
		t.Skipf("host path %q is absent", protected)
	} else if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "device-alias")
	if err := os.Symlink(protected, alias); err != nil {
		t.Fatal(err)
	}
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{
		Destination: "/mnt/host", SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true,
	}})
	err := ValidateRuntimeHostSourcesV1(policy, "command/check", 1000, []RuntimeHostSourceV1{{
		Destination: "/mnt/host", HostPath: alias, SourceKind: deploy.RuntimeMountSourceDirectory,
		Authority: runtimeHostAuthorityInputV1, ReadOnly: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "protected host system source") {
		t.Fatalf("protected host tree alias error = %v", err)
	}
}

func TestValidateRuntimeHostSourcesV1RejectsEveryHostAuthorityForRoot(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name      string
		mount     deploy.RuntimeMountV1
		authority string
		want      string
	}{
		{
			name: "host input", mount: deploy.RuntimeMountV1{
				Destination: "/mnt/input", SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true,
			}, authority: runtimeHostAuthorityInputV1, want: "host input mount",
		},
		{
			name: "shared state", mount: deploy.RuntimeMountV1{
				Destination: "/mnt/state", SourceKind: deploy.RuntimeMountSourceDirectory,
			}, authority: runtimeHostAuthoritySharedStateV1, want: "host shared-state mount",
		},
		{
			name: "explicit output", mount: deploy.RuntimeMountV1{
				Destination: runtimeOutputRoot, SourceKind: deploy.RuntimeMountSourceDirectory,
			}, authority: runtimeHostAuthorityOutputV1, want: "root-safe output contract",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := runtimeHostPolicy([]deploy.RuntimeMountV1{test.mount})
			err := ValidateRuntimeHostSourcesV1(policy, "command/check", 0, []RuntimeHostSourceV1{{
				Destination: test.mount.Destination, HostPath: root, SourceKind: test.mount.SourceKind,
				Authority: test.authority, ReadOnly: test.mount.ReadOnly,
			}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("root authority error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateRuntimeHostSourcesV1AllowsGeneratedStorageForRoot(t *testing.T) {
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{
		Destination: "/mnt/data", SourceKind: deploy.RuntimeMountSourceGenerated,
	}})
	if err := ValidateRuntimeHostSourcesV1(policy, "command/check", 0, []RuntimeHostSourceV1{}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRootRuntimeHostAuthorityV1RejectsBeforeHostInspection(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	plan := DockerExecutionPlan{
		Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 0, GID: 0, DockerUser: "0:0"}),
		Mounts: []MountExecutionPlan{{
			Name: "config", Mode: blueprint.MountBind, Source: missing,
			SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/mnt/config", ReadOnly: true,
		}},
	}
	policy := runtimeHostPolicy([]deploy.RuntimeMountV1{{
		Destination: "/mnt/config", SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true,
	}})
	policy.Plans[0].ID = runtimeShellPlanID
	err := ValidateRootRuntimeHostAuthorityV1(policy, plan)
	if err == nil || !strings.Contains(err.Error(), "root application runtime") || strings.Contains(err.Error(), "no such file") {
		t.Fatalf("early root authority error = %v", err)
	}
}

func TestRuntimeHostSourcesV1IncludesOnlyBindAndExplicitOutputMounts(t *testing.T) {
	root := t.TempDir()
	output := &transientOutputMount{HostDirectory: root, Variable: runtimeOutputDirectoryVariable, ContainerPath: runtimeOutputRoot}
	sources, err := RuntimeHostSourcesV1(DockerExecutionPlan{Mounts: []MountExecutionPlan{
		{Name: "config", Mode: "managed-bind", Source: root, SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/mnt/config", ReadOnly: true},
		{Name: "data", Mode: "volume", SourceKind: deploy.RuntimeMountSourceGenerated, Target: "/mnt/data"},
	}}, output)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 ||
		sources[0].Destination != "/mnt/config" || sources[0].Authority != runtimeHostAuthorityInputV1 ||
		sources[1].Destination != runtimeOutputRoot || sources[1].Authority != runtimeHostAuthorityOutputV1 {
		t.Fatalf("host sources = %#v", sources)
	}
}

func runtimeHostPolicy(mounts []deploy.RuntimeMountV1) deploy.RuntimePolicyV1 {
	return deploy.RuntimePolicyV1{
		Schema: deploy.RuntimePolicySchemaV1, StartupVerifier: deploy.ApplicationStartupVerifierContractV1(),
		Network:        blueprint.RuntimeNetwork{Public: blueprint.NetworkAccessDeny, Local: blueprint.NetworkAccessDeny, Ambiguous: blueprint.AmbiguousNetworkAccessRequireBoth},
		ProtectedPaths: []deploy.ProtectedPathV1{}, Plans: []deploy.RuntimePlanV1{{
			ID: "command/check", InboundTCP: []string{}, Mounts: mounts, Executables: []providers.QualifiedOutput{},
		}},
	}
}
