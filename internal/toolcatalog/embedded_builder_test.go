package toolcatalog

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestClientReployVersionV1MapsBuildsToTheirReleaseCoordinate(t *testing.T) {
	for raw, want := range map[string]string{
		"0.7.0.dev1": "0.7.0", "0.7.0rc1": "0.7.0", "1.2.3": "1.2.3", "1.2.3-dev4": "1.2.3", "2.0.0.post1": "2.0.0",
	} {
		got, err := ClientReployVersionV1(raw)
		if err != nil || got != want {
			t.Fatalf("ClientReployVersionV1(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "1.2", "v1.2.3", "1.2.3.4", "1.2.3-beta", "latest"} {
		if _, err := ClientReployVersionV1(raw); err == nil {
			t.Fatalf("ClientReployVersionV1(%q) accepted a non-release coordinate", raw)
		}
	}
	capabilities, err := EmbeddedClientCapabilitiesV1("0.7.0.dev1")
	if err != nil || capabilities.ReployVersion != "0.7.0" || len(capabilities.ResolverPrimitives) != 1 || capabilities.ResolverPrimitives[0] != "https-sha256" {
		t.Fatalf("capabilities = %#v, %v", capabilities, err)
	}
}

func TestEmbeddedPortableToolLockRecordsV1ResolvesJavaManifestAndSource(t *testing.T) {
	_, resolution, err := ResolveEmbeddedPortableToolPlanV1(
		[]CanonicalRequirementGroupV1{javaOwnedBuilderGroupV1()},
		javaTargetV1("debian", "12"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil,
		[]ProviderDomainSetV1{ownedBuilderDomainsV1("source-builder:omegaconf")},
		solverTestOperationV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	records, err := EmbeddedPortableToolLockRecordsV1(resolution.Closures)
	if err != nil {
		t.Fatal(err)
	}
	closure := resolution.Closures[0]
	if len(records.Releases) != 1 || len(records.Artifacts) != 1 {
		t.Fatalf("records = %#v", records)
	}
	release := records.Releases[0]
	if release.Scope != "source-builder:omegaconf" || release.Tool != "java" ||
		release.Manifest.Reference.ID != "tool:java/releases/21/revisions/1/manifest" ||
		release.Manifest.Reference.Digest != closure.Provenance.ManifestDigest ||
		release.Manifest.Record.Schema != ReleaseManifestSchemaV1 ||
		release.Manifest.Record.Value["revision"] != "1" {
		t.Fatalf("release = %#v", release)
	}
	artifact := records.Artifacts[0]
	wantSHA := canonical.Digest("sha256:e4446ff06a276155697597cc0f1b15da004ff083f4964a35271ecee567177370")
	if artifact.Scope != "source-builder:omegaconf" || artifact.Tool != "java" ||
		artifact.Artifact.ID != "tool:java/releases/21/payloads/jdk-linux-amd64" ||
		artifact.Artifact != closure.Records.Payloads[0].Reference ||
		artifact.Descriptor.LogicalPath != "tools/java/21.0.12+8/OpenJDK21U-jdk_x64_linux_hotspot_21.0.12_8.tar.gz" ||
		artifact.Descriptor.Kind != "jdk-archive" || artifact.Descriptor.Size != "207486543" || artifact.Descriptor.SHA256 != wantSHA ||
		artifact.Source.Reference.ID != "tool:java/releases/21/revisions/1/sources/jdk-linux-amd64" ||
		artifact.Source.Record.Schema != ArtifactSourceRecordSchemaV1 ||
		len(artifact.Mirrors) != 1 || !strings.HasPrefix(artifact.Mirrors[0], "https://github.com/adoptium/temurin21-binaries/") {
		t.Fatalf("artifact = %#v", artifact)
	}
	digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, artifact.Source.Record.Value)
	if err != nil || digest != artifact.Source.Reference.Digest {
		t.Fatalf("source record digest = %s, %v; want %s", digest, err, artifact.Source.Reference.Digest)
	}
}

func TestEmbeddedPortableToolLockRecordsV1ResolvesBindingWheelManifestAndSource(t *testing.T) {
	_, resolution, err := ResolveEmbeddedPortableToolPlanV1(
		[]CanonicalRequirementGroupV1{playwrightGroupV1("==1.61.0", "runtime")},
		playwrightTargetV1("debian", "12"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil,
		solverTestDomainsV1(false)[:1],
		solverTestOperationV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	records, err := EmbeddedPortableToolLockRecordsV1(resolution.Closures)
	if err != nil {
		t.Fatal(err)
	}
	if len(records.Artifacts) != 4 {
		t.Fatalf("artifacts = %d, want selected wheel and three payloads", len(records.Artifacts))
	}
	var wheel *EmbeddedPortableToolArtifactSourceV1
	for index := range records.Artifacts {
		if records.Artifacts[index].Artifact.ID == "tool:playwright/releases/1.61.0/bindings/python/artifacts/linux-amd64" {
			wheel = &records.Artifacts[index]
			break
		}
	}
	if wheel == nil {
		t.Fatal("embedded binding wheel was not projected")
	}
	if wheel.Descriptor.LogicalPath != "wheels/playwright-1.61.0-py3-none-manylinux1_x86_64.whl" ||
		wheel.Descriptor.Kind != "wheel" || wheel.Descriptor.Size != "47421381" ||
		wheel.Descriptor.SHA256 != "sha256:54f3b39f6eab832e33458c1dd7da0b5682aedab3b09ae731b5c59fa12fd2024e" ||
		wheel.Source.Reference.ID != "tool:playwright/releases/1.61.0/revisions/1/sources/python-linux-amd64" ||
		len(wheel.Mirrors) != 1 {
		t.Fatalf("binding wheel projection = %#v", wheel)
	}
	if digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, wheel.Source.Record.Value); err != nil || digest != wheel.Source.Reference.Digest {
		t.Fatalf("binding source record digest = %s, %v; want %s", digest, err, wheel.Source.Reference.Digest)
	}
}

func TestEmbeddedPortableToolLockRecordsV1RejectsMutatedBindingArtifactBeforeAcquisition(t *testing.T) {
	_, resolution, err := ResolveEmbeddedPortableToolPlanV1(
		[]CanonicalRequirementGroupV1{playwrightGroupV1("==1.61.0", "runtime")},
		playwrightTargetV1("debian", "12"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil,
		solverTestDomainsV1(false)[:1],
		solverTestOperationV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	resolution.Closures[0].Records.BindingArtifacts[0].Record.Filename = "renamed.whl"
	if _, err := EmbeddedPortableToolLockRecordsV1(resolution.Closures); err == nil || !strings.Contains(err.Error(), "exact reference digest") {
		t.Fatalf("error = %v, want selected binding identity rejection", err)
	}
}

func TestEmbeddedPortableToolLockRecordsV1RejectsDuplicateBindingSourceMappings(t *testing.T) {
	_, resolution, err := ResolveEmbeddedPortableToolPlanV1(
		[]CanonicalRequirementGroupV1{playwrightGroupV1("==1.61.0", "runtime")},
		playwrightTargetV1("debian", "12"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil,
		solverTestDomainsV1(false)[:1],
		solverTestOperationV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := loadCatalogV1(definitionFilesV1, "definitions")
	if err != nil {
		t.Fatal(err)
	}
	closure := &resolution.Closures[0]
	_, manifest, err := catalog.embeddedManifestForClosureV1(closure)
	if err != nil {
		t.Fatal(err)
	}
	binding := closure.Records.BindingArtifacts[0]
	var duplicate ArtifactSourceMappingV1
	found := false
	for _, mapping := range manifest.ArtifactSources {
		if mapping.Artifact == binding.Reference {
			duplicate, found = mapping, true
			break
		}
	}
	if !found {
		t.Fatal("embedded binding wheel source mapping is missing")
	}
	manifest.ArtifactSources = append(manifest.ArtifactSources, duplicate)
	if _, err := catalog.embeddedBindingArtifactSourceV1(closure, manifest, binding); err == nil || !strings.Contains(err.Error(), "duplicate source mappings") {
		t.Fatalf("error = %v, want duplicate source mapping rejection", err)
	}
}

func TestEmbeddedPortableToolLockRecordsV1RejectsMissingMismatchedOrUnauthorizedBindingSource(t *testing.T) {
	_, resolution, err := ResolveEmbeddedPortableToolPlanV1(
		[]CanonicalRequirementGroupV1{playwrightGroupV1("==1.61.0", "runtime")},
		playwrightTargetV1("debian", "12"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil,
		solverTestDomainsV1(false)[:1],
		solverTestOperationV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	closure := &resolution.Closures[0]
	binding := closure.Records.BindingArtifacts[0]
	for _, test := range []struct {
		name   string
		change func(*ReleaseManifestV1, int)
		want   string
	}{
		{"missing", func(manifest *ReleaseManifestV1, index int) {
			manifest.ArtifactSources = append(manifest.ArtifactSources[:index], manifest.ArtifactSources[index+1:]...)
		}, "maps no source"},
		{"mismatched content", func(manifest *ReleaseManifestV1, index int) {
			manifest.ArtifactSources[index].ArtifactSHA256 = canonical.Digest("sha256:" + strings.Repeat("0", 64))
		}, "names content"},
		{"unauthorized source", func(manifest *ReleaseManifestV1, index int) {
			manifest.ArtifactSources[index].Source.Digest = canonical.Digest("sha256:" + strings.Repeat("0", 64))
		}, "does not resolve to its exact record"},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := loadCatalogV1(definitionFilesV1, "definitions")
			if err != nil {
				t.Fatal(err)
			}
			_, manifest, err := catalog.embeddedManifestForClosureV1(closure)
			if err != nil {
				t.Fatal(err)
			}
			index := -1
			for candidate, mapping := range manifest.ArtifactSources {
				if mapping.Artifact == binding.Reference {
					index = candidate
					break
				}
			}
			if index < 0 {
				t.Fatal("embedded binding source mapping is missing")
			}
			test.change(manifest, index)
			if _, err := catalog.embeddedBindingArtifactSourceV1(closure, manifest, binding); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestEmbeddedPortableToolLockRecordsV1RejectsUnknownManifestProvenance(t *testing.T) {
	_, resolution, err := ResolveEmbeddedPortableToolPlanV1(
		[]CanonicalRequirementGroupV1{javaOwnedBuilderGroupV1()},
		javaTargetV1("debian", "12"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}},
		nil,
		[]ProviderDomainSetV1{ownedBuilderDomainsV1("source-builder:omegaconf")},
		solverTestOperationV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	resolution.Closures[0].Provenance.ManifestDigest = canonical.Digest("sha256:" + strings.Repeat("0", 64))
	if _, err := EmbeddedPortableToolLockRecordsV1(resolution.Closures); err == nil || !strings.Contains(err.Error(), "no release manifest with digest") {
		t.Fatalf("error = %v, want unknown manifest rejection", err)
	}
}
