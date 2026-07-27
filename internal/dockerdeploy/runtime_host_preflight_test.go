package dockerdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		Destination: "/mnt/config", HostPath: config, SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true,
	}}
	if err := ValidateRuntimeHostSourcesV1(policy, "command/check", sources); err != nil {
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
		Destination: "/mnt/config", HostPath: file, SourceKind: deploy.RuntimeMountSourceFile, ReadOnly: true,
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
		{name: "kind", planID: "command/check", sources: []RuntimeHostSourceV1{{Destination: valid.Destination, HostPath: file, SourceKind: deploy.RuntimeMountSourceDirectory, ReadOnly: true}}, want: "kind or access"},
		{name: "unexpected", planID: "command/check", sources: []RuntimeHostSourceV1{valid, {Destination: "/mnt/extra", HostPath: root, SourceKind: deploy.RuntimeMountSourceDirectory}}, want: "unexpected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRuntimeHostSourcesV1(policy, test.planID, test.sources)
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
	err := ValidateRuntimeHostSourcesV1(policy, "command/check", []RuntimeHostSourceV1{{
		Destination: "/mnt/config", HostPath: root, SourceKind: deploy.RuntimeMountSourceFile, ReadOnly: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("filesystem kind error = %v", err)
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
	if len(sources) != 2 || sources[0].Destination != "/mnt/config" || sources[1].Destination != runtimeOutputRoot {
		t.Fatalf("host sources = %#v", sources)
	}
}

func runtimeHostPolicy(mounts []deploy.RuntimeMountV1) deploy.RuntimePolicyV1 {
	return deploy.RuntimePolicyV1{
		Schema:         deploy.RuntimePolicySchemaV1,
		ProtectedPaths: []deploy.ProtectedPathV1{}, Plans: []deploy.RuntimePlanV1{{
			ID: "command/check", Mounts: mounts, Executables: []providers.QualifiedOutput{},
		}},
	}
}
