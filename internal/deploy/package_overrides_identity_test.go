package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
)

func mappingSidecarV1(mapping string) string {
	return "environment:\n  id: demo\n  runtime:\n    identity_mapping: " + mapping + "\n"
}

func decodeMappingSidecarV1(t *testing.T, text string) PackageOverridesV1 {
	t.Helper()
	overrides, err := DecodePackageOverridesV1([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return overrides
}

func TestMappingSidecarStrictSchemaAndNullPresence(t *testing.T) {
	for _, text := range []string{
		mappingSidecarV1(`{}`),
		mappingSidecarV1(`{schema: 1, profile: exact}`),
		mappingSidecarV1(`{uids: [{container_start: 0, host_start: "???", count: 1}]}`),
	} {
		overrides := decodeMappingSidecarV1(t, text)
		content, err := EncodePackageOverridesV1(overrides)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "package_overrides") || strings.Contains(string(content), "kind:") {
			t.Fatalf("mapping-only sidecar has artificial packages or internal scalar encoding: %s", content)
		}
		decoded := decodeMappingSidecarV1(t, string(content))
		second, err := EncodePackageOverridesV1(decoded)
		if err != nil || !bytes.Equal(content, second) {
			t.Fatalf("unstable mapping sidecar: %s / %s / %v", content, second, err)
		}
	}
	for _, text := range []string{
		mappingSidecarV1("null"), mappingSidecarV1("[]"), mappingSidecarV1("{profile: null}"),
		mappingSidecarV1("{schema: 1.0}"), mappingSidecarV1("{schema: 1, schema: 1}"),
		mappingSidecarV1("{gids: null}"), mappingSidecarV1("{gids: []}"),
		mappingSidecarV1("{extra: true}"),
		mappingSidecarV1("{}") + "  package_overrides: null\n",
		mappingSidecarV1("{}") + "  base: {image: debian:13, typo: true}\n",
		mappingSidecarV1("{}") + "  package_overrides: {python: {demo: {path: ., typo: true}}}\n",
		mappingSidecarV1("{}") + "  typo: true\n",
		"environment: {id: demo, runtime: null, package_overrides: {}}\n",
		"environment: {id: demo, runtime: {typo: true}, package_overrides: {}}\n",
		"environment: {id: demo, runtime: {identity_mapping: {}, identity_mapping: {}}, package_overrides: {}}\n",
		"environment: {id: demo, runtime: {identity_mapping: {}}, runtime: {identity_mapping: {}}}\n",
		"environment: {id: demo}\n",
		"environment: {<<: {id: demo, runtime: null}, package_overrides: {}}\n",
		"environment: {<<: {id: demo, runtime: {identity_mapping: null}}, package_overrides: {}}\n",
		"environment: {id: demo, runtime: {<<: {identity_mapping: null}}, package_overrides: {}}\n",
		"environment: {<<: [{id: demo}, {runtime: {identity_mapping: null}}], package_overrides: {}}\n",
		"environment: {id: demo, vars: {unset: &unset null}, runtime: {identity_mapping: {}}, package_overrides: *unset}\n",
	} {
		t.Run(text, func(t *testing.T) {
			if _, err := DecodePackageOverridesV1([]byte(text)); err == nil {
				t.Fatalf("accepted invalid mapping sidecar: %s", text)
			}
		})
	}
}

func TestMappingSidecarPreservesOrdinaryBytesAndBuildIdentity(t *testing.T) {
	empty := EmptyPackageOverridesV1("demo")
	content, err := EncodePackageOverridesV1(empty)
	if err != nil || string(content) != "environment:\n    id: demo\n    package_overrides: {}\n" {
		t.Fatalf("ordinary empty sidecar bytes changed: %s / %v", content, err)
	}
	// Existing ordinary merges keep their previous effective-field semantics.
	ordinaryMerge := decodeMappingSidecarV1(t, "environment: {<<: {id: demo, package_overrides: null}, package_overrides: {}}\n")
	mergedBytes, err := EncodePackageOverridesV1(ordinaryMerge)
	if err != nil || !bytes.Equal(content, mergedBytes) {
		t.Fatalf("ordinary merge semantics changed: %s / %v", mergedBytes, err)
	}
	want, err := PackageOverridesDigestV1(empty)
	if err != nil {
		t.Fatal(err)
	}
	first := decodeMappingSidecarV1(t, mappingSidecarV1(`{uids: [{container_start: 0, host_start: 100000, count: 1}]}`))
	second := decodeMappingSidecarV1(t, mappingSidecarV1(`{uids: [{container_start: 0, host_start: 300000, count: 1}]}`))
	for _, mapping := range []PackageOverridesV1{first, second} {
		got, err := PackageOverridesDigestV1(mapping)
		if err != nil || got != want {
			t.Fatalf("deployment-only host mapping invalidated empty build intent: %s / %v", got, err)
		}
	}
	before, _ := EncodePackageOverridesV1(first)
	first.Environment.Base = &BaseImageOverrideV1{Image: "debian:13"}
	baseDigest, err := PackageOverridesDigestV1(first)
	if err != nil || baseDigest == want {
		t.Fatalf("base image ceased to be a build input: %v", err)
	}
	first.Environment.Base = nil
	first.Environment.Vars = map[string]any{"name": "private"}
	varsDigest, err := PackageOverridesDigestV1(first)
	if err != nil || varsDigest == want {
		t.Fatalf("existing sidecar variables ceased to be build inputs: %v", err)
	}
	first.Environment.Vars = nil
	after, _ := EncodePackageOverridesV1(first)
	if !bytes.Equal(before, after) || first.IdentityMappingOverride() == nil {
		t.Fatal("build projection modified caller-owned deployment input")
	}
	invalid := decodeMappingSidecarV1(t, mappingSidecarV1(`{}`))
	badSchema := 2
	invalid.IdentityMappingOverride().Schema = &badSchema
	if _, err := PackageOverridesDigestV1(invalid); err == nil {
		t.Fatal("projection hid an invalid mapping override")
	}
}

func TestMappingSidecarExistingChoicesRoundTrip(t *testing.T) {
	text := mappingSidecarV1(`{profile: bounded, uids: [{container_start: 0, host_start: 100000, count: 1}]}`) +
		"  vars: {root: /source}\n  base: {image: debian:13}\n  package_additions: {os: [curl]}\n  package_overrides: {python: {demo: {path: '{{ root }}/demo', exclude: [cache, dist]}}}\n"
	original := decodeMappingSidecarV1(t, text)
	encoded, err := EncodePackageOverridesV1(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeMappingSidecarV1(t, string(encoded))
	if decoded.Environment.Base.Image != "debian:13" || decoded.Environment.Vars["root"] != "/source" || decoded.Environment.PackageOverrides["python"]["demo"].Path != "{{ root }}/demo" || decoded.IdentityMappingOverride().Profile.Value != "bounded" {
		t.Fatalf("unrelated existing choices lost: %#v", decoded)
	}
	// Mapping variables come only from the portable blueprint, not this sidecar.
	document := overlayTestDocument()
	document.Environment.ID = "demo"
	document.Environment.Runtime.IdentityMapping = &blueprint.IdentityMappingV1{
		Schema: 1, Profile: blueprint.MappingScalarV1{Kind: "string", Value: "exact"},
		UIDs: []blueprint.MappingIntervalV1{{ContainerStart: blueprint.MappingScalarV1{Kind: "integer", Value: "0"}, HostStart: blueprint.MappingScalarV1{Kind: "integer", Value: "100000"}, Count: blueprint.MappingScalarV1{Kind: "integer", Value: "1"}}},
		GIDs: []blueprint.MappingIntervalV1{{ContainerStart: blueprint.MappingScalarV1{Kind: "integer", Value: "0"}, HostStart: blueprint.MappingScalarV1{Kind: "integer", Value: "200000"}, Count: blueprint.MappingScalarV1{Kind: "integer", Value: "1"}}},
	}
	shadow := decodeMappingSidecarV1(t, mappingSidecarV1(`{uids: [{container_start: 0, host_start: "{{ selected }}", count: 1}]}`)+"  vars: {selected: 300000}\n")
	if _, err := blueprint.ResolveIdentityMappingV1(document, shadow.IdentityMappingOverride()); err == nil || !strings.Contains(err.Error(), ".uids[0].host_start") {
		t.Fatalf("sidecar variable shadowed original blueprint authority: %v", err)
	}
}

func TestMappingSidecarStageUpdatePreservesOrRejectsBeforeStateWrite(t *testing.T) {
	dir := t.TempDir()
	document := overlayTestDocument()
	document.Environment.ID = "demo"
	platform := desiredStateTestPlatform(t, "linux/amd64")
	if _, err := SetDesiredStateV1(t.Context(), dir, document, platform, overlayTestPackageValidator); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, ".reploy", "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(mappingSidecarV1(`{profile: exact}`))
	if err := os.WriteFile(filepath.Join(dir, PackageOverridesFilename), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetDesiredStateV1(t.Context(), dir, document, platform, overlayTestPackageValidator); err == nil || !strings.Contains(err.Error(), "declaration") {
		t.Fatalf("stage accepted override without a declaration: %v", err)
	}
	after, _ := os.ReadFile(statePath)
	retained, _ := os.ReadFile(filepath.Join(dir, PackageOverridesFilename))
	if !bytes.Equal(before, after) || !bytes.Equal(content, retained) {
		t.Fatal("rejected stage update changed state or deployment choices")
	}
}

func TestMappingSidecarInstalledStateDoesNotDiscoverStagingInput(t *testing.T) {
	dir := t.TempDir()
	path := writeOverlayTestState(t, dir)
	state := readOverlayTestState(t, path)
	state.Deployment = &DeploymentStateV1{Schema: DeploymentStateSchemaV1, Installation: installationStateV1Fixture(dir)}
	encoded, err := EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	// This file is deliberately invalid. Installed operations must not discover
	// deployment-side staging choices, even when such a file exists.
	unselected := []byte("environment: [not, installed, authority]\n")
	sidecar := filepath.Join(dir, PackageOverridesFilename)
	if err := os.WriteFile(sidecar, unselected, 0o600); err != nil {
		t.Fatal(err)
	}
	document := overlayTestDocument()
	document.Blueprint.Version = "updated"
	result, err := SetDesiredStateV1(t.Context(), dir, document, state.Platform, overlayTestPackageValidator)
	if err != nil || !reflect.DeepEqual(result.State.Deployment, state.Deployment) {
		t.Fatalf("installed update discovered staging input or changed installation facts: %v", err)
	}
	after, _ := os.ReadFile(sidecar)
	if !bytes.Equal(unselected, after) || result.State.Deployment.Installation.TargetDir != dir {
		t.Fatal("installed update modified or adopted unselected staging input")
	}
}
