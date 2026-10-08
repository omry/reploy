package toolcatalog

import (
	"reflect"
	"strings"
	"testing"
)

func TestEmbeddedBashExactNativeCasesV1(t *testing.T) {
	cases, err := EmbeddedIntegrationCasesV1()
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]string{"debian/12": "5.2.15", "debian/13": "5.2.37", "ubuntu/26.04": "5.3.9"}
	packages := map[string]string{"5.2.15": "bash=5.2.15-2+b13", "5.2.37": "bash=5.2.37-2+b10", "5.3.9": "bash=5.3-2ubuntu1"}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Manifest.Tool != "bash" {
			continue
		}
		key := c.Target.Target.OSReleaseID + "/" + c.Target.Target.VersionID
		if c.Manifest.Version != versions[key] || len(c.Support.Bindings) != 0 || len(c.Support.Selections) != 0 {
			t.Fatalf("unexpected Bash tuple: %#v", c)
		}
		arch := c.Target.Target.OCIArchitecture
		if arch != "amd64" && arch != "arm64" || c.Target.Target.NativeArchitecture != arch {
			t.Fatal("unadvertised architecture")
		}
		if c.Support.Context != "build" && c.Support.Context != "runtime" {
			t.Fatal("unadvertised context")
		}
		id := key + "/" + arch + "/" + c.Support.Context
		if seen[id] {
			t.Fatal("duplicate Bash case", id)
		}
		seen[id] = true
		scope := "application:case"
		if c.Support.Context == "build" {
			scope = "source-builder:case"
		}
		closure, err := EmbeddedSelectedClosureForIntegrationCaseV1(c, scope)
		if err != nil {
			t.Fatal(err)
		}
		if len(closure.Records.PackageSets) != 1 || len(closure.Records.Payloads) != 0 || len(closure.Records.BindingArtifacts) != 0 || len(closure.Records.BindingContracts) != 0 {
			t.Fatal("Bash is not native-only")
		}
		if !reflect.DeepEqual(closure.Records.PackageSets[0].Record.Requirements, []string{packages[c.Manifest.Version]}) {
			t.Fatal("package/upstream coordinate conflation")
		}
		plan, err := CompilePortableToolPlanV1([]SelectedClosureV1{closure})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Tools[0].Runtime != nil {
			t.Fatal("native-only Bash invented an archive install root")
		}
		wantPath := "/usr/bin/bash"
		if key == "debian/12" {
			wantPath = "/bin/bash"
		}
		if len(plan.Tools[0].Exports) != 1 || plan.Tools[0].Exports[0].Name != "bash" || plan.Tools[0].Exports[0].Path != wantPath {
			t.Fatal("wrong package-owned export")
		}
		if len(c.Profiles) != 1 || c.Profiles[0].Tool != "bash" || c.Profiles[0].Version != c.Manifest.Version || len(c.Profiles[0].Probes) != 1 || c.Profiles[0].Probes[0].Path != wantPath || !reflect.DeepEqual(c.Profiles[0].Probes[0].Args, []string{"--version"}) {
			t.Fatal("Bash profile is not a direct fixed version probe")
		}
		if c.Fixture.BaseImageDigest == "" || !strings.HasPrefix(c.Fixture.BaseImage, "docker.io/library/") {
			t.Fatal("fixture is not immutable first-party OS")
		}
	}
	if len(seen) != 12 {
		t.Fatalf("Bash cases = %d, want 12", len(seen))
	}
}

func TestEmbeddedBashRejectsUnsupportedTupleAndMissingAPTCapabilityV1(t *testing.T) {
	catalog := mustLoadEmbeddedCatalogV1()
	group := javaOwnedBuilderGroupV1()
	group.Tool = "bash"
	group.VersionConstraints = []string{"==5.2.15"}
	client, err := EmbeddedClientCapabilitiesV1("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []TargetIdentityV1{javaTargetV1("debian", "11"), javaTargetV1("ubuntu", "25.10"), javaTargetV1("debian", "13")} {
		if _, err := catalog.SelectReleaseCandidatesV1(group, target, client, nil); err == nil {
			t.Fatalf("unsupported exact version/OS tuple accepted: %#v", target)
		}
	}
	client.ResolverPrimitives = []string{"https-sha256"}
	if _, err := catalog.SelectReleaseCandidatesV1(group, javaTargetV1("debian", "12"), client, nil); err == nil {
		t.Fatal("native Bash resolved without APT capability")
	}
}
