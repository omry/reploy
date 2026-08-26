package toolcatalog

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestEmbeddedJavaCatalogMatchesCanonicalAuthoringV1(t *testing.T) {
	entriesPayload, err := os.ReadFile(filepath.Join("authoring", "entries.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []PortableToolAuthoringEntryV1
	if err := json.Unmarshal(entriesPayload, &entries); err != nil {
		t.Fatal(err)
	}
	result, err := LoadPortableToolAuthoringV1("authoring", entries)
	if err != nil {
		t.Fatalf("load embedded Java authoring: %v", err)
	}
	if len(result.Records) != 14 || len(result.Sources) != 14 {
		t.Fatalf("Java authoring emitted %d records from %d sources, want 14 and 14", len(result.Records), len(result.Sources))
	}
	for _, record := range result.Records {
		payload, err := fs.ReadFile(definitionFilesV1, "definitions/"+record.Path)
		if err != nil {
			t.Fatalf("read embedded record %q: %v", record.Path, err)
		}
		if !bytes.Equal(payload, record.CanonicalBytes) {
			t.Errorf("embedded record %q differs from canonical authoring output", record.Path)
		}
	}
	if catalog, err := loadCatalogV1(definitionFilesV1, "definitions"); err != nil || !reflect.DeepEqual(catalog.Names(), []string{"java"}) {
		t.Fatalf("embedded catalog = names %v, error %v", catalogNamesV1(catalog), err)
	}
}

func TestEmbeddedJavaCatalogSelectsVettedBuildDefinitionV1(t *testing.T) {
	catalog := mustLoadEmbeddedCatalogV1()
	vettedTargets := []struct {
		target          TargetIdentityV1
		baseImage       string
		baseImageDigest canonical.Digest
	}{
		{javaTargetV1("debian", "12"), "docker.io/library/debian:12-slim", "sha256:362e64223cc0da95422b3b13c045186fc0a81250e765d31c025fbddf257f6143"},
		{javaTargetV1("debian", "13"), "docker.io/library/debian:13-slim", "sha256:38a76d01668772e381ad2826d876627c89e7133e2f8a0f5d567306798b0f2a16"},
		{javaTargetV1("ubuntu", "25.10"), "docker.io/library/ubuntu:25.10", "sha256:e0b84ef30bbe766773e6056c60a3e706712e4119508e3da12516f1eddd6f761b"},
		{javaTargetV1("ubuntu", "26.04"), "docker.io/library/ubuntu:26.04", "sha256:7b202b0e2e0028c6250f5fcf41d04df492d145a1654c6995a6553f0c1f6f1960"},
	}
	for _, vetted := range vettedTargets {
		target := vetted.target
		candidates, err := catalog.SelectReleaseCandidatesV1(javaGroupV1("==21", "build"), target,
			ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}}, nil)
		if err != nil {
			t.Fatalf("select Java for %s %s: %v", target.OSReleaseID, target.VersionID, err)
		}
		if len(candidates) != 1 {
			t.Fatalf("Java candidate count = %d, want 1", len(candidates))
		}
		candidate := candidates[0]
		if candidate.Manifest.Version != "21" || candidate.Manifest.Revision != "1" ||
			!reflect.DeepEqual(candidate.Exports, []ToolExportV1{
				{Name: "java", Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/java"},
				{Name: "javac", Path: "/opt/reploy/tools/java/jdk-21.0.12+8/bin/javac"},
			}) || candidate.Fixture.Target != target || candidate.Fixture.BaseImage != vetted.baseImage ||
			candidate.Fixture.BaseImageDigest != vetted.baseImageDigest || len(candidate.Profiles) != 1 {
			t.Fatalf("selected Java candidate = %#v", candidate)
		}
	}

	payload := embeddedRecordV1[*PayloadRecordV1](t, "tool:java/releases/21/payloads/jdk-linux-amd64")
	if payload.UpstreamVersion != "21.0.12+8" || payload.Revision != "8" || payload.Size != "207486543" ||
		payload.SHA256 != canonical.Digest("sha256:e4446ff06a276155697597cc0f1b15da004ff083f4964a35271ecee567177370") ||
		payload.Entries != "542" || payload.UnpackedSize != "361144464" || payload.Resolver != "https-sha256" ||
		payload.LogicalPath != "tools/java/21.0.12+8/OpenJDK21U-jdk_x64_linux_hotspot_21.0.12_8.tar.gz" ||
		payload.InstallDirectory != "jdk-21.0.12+8" || payload.ArchiveRoot != "jdk-21.0.12+8" ||
		!reflect.DeepEqual(payload.Executables, []string{"jdk-21.0.12+8/bin/java", "jdk-21.0.12+8/bin/javac"}) {
		t.Fatalf("embedded Java payload = %#v", payload)
	}
	source := embeddedRecordV1[*ArtifactSourceRecordV1](t, "tool:java/releases/21/revisions/1/sources/jdk-linux-amd64")
	const releaseURL = "https://github.com/adoptium/temurin21-binaries/releases/tag/jdk-21.0.12%2B8"
	if source.SHA256 != payload.SHA256 || !reflect.DeepEqual(source.Mirrors, []string{
		"https://github.com/adoptium/temurin21-binaries/releases/download/jdk-21.0.12%2B8/OpenJDK21U-jdk_x64_linux_hotspot_21.0.12_8.tar.gz",
	}) || !reflect.DeepEqual(source.Provenance, []string{releaseURL}) {
		t.Fatalf("embedded Java source = %#v", source)
	}
}

func TestEmbeddedJavaCatalogRejectsUnsupportedRequestsBeforeAcquisitionV1(t *testing.T) {
	catalog := mustLoadEmbeddedCatalogV1()
	tests := []struct {
		name    string
		group   CanonicalRequirementGroupV1
		target  TargetIdentityV1
		wantSub string
	}{
		{name: "runtime context", group: javaGroupV1("==21", "runtime"), target: javaTargetV1("debian", "12"), wantSub: "context"},
		{name: "wrong version", group: javaGroupV1("==17", "build"), target: javaTargetV1("debian", "12"), wantSub: "empty intersection"},
		{name: "unsupported OS", group: javaGroupV1("==21", "build"), target: javaTargetV1("ubuntu", "24.04"), wantSub: "no target leaf"},
		{name: "unsupported architecture", group: javaGroupV1("==21", "build"), target: func() TargetIdentityV1 {
			target := javaTargetV1("debian", "12")
			target.Platform, target.OCIArchitecture, target.NativeArchitecture = "linux/arm64", "arm64", "arm64"
			return target
		}(), wantSub: "no target leaf"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := catalog.SelectReleaseCandidatesV1(testCase.group, testCase.target,
				ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}}, nil)
			if err == nil || !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantSub)
			}
		})
	}
}

func javaGroupV1(version string, context string) CanonicalRequirementGroupV1 {
	return CanonicalRequirementGroupV1{
		Scope: "source-builder", Tool: "java", VersionConstraints: []string{version}, Context: context,
		Binding: CanonicalBindingDemandV1{Infer: true}, Selections: map[string][]string{},
	}
}

func javaTargetV1(osID string, version string) TargetIdentityV1 {
	return TargetIdentityV1{Platform: "linux/amd64", OSReleaseID: osID, VersionID: version,
		OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt"}
}

func embeddedRecordV1[T any](t *testing.T, id string) T {
	t.Helper()
	for key, record := range mustLoadEmbeddedCatalogV1().records {
		if key.ID == id {
			value, ok := record.Value.(T)
			if !ok {
				t.Fatalf("embedded record %q has type %T", id, record.Value)
			}
			return value
		}
	}
	var zero T
	t.Fatalf("embedded record %q is missing", id)
	return zero
}

func catalogNamesV1(catalog *CatalogV1) []string {
	if catalog == nil {
		return nil
	}
	return catalog.Names()
}
