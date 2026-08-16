package toolcatalog

import (
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/omry/reploy/internal/canonical"
)

func TestEmbeddedCatalogResolvesJavaAndPlaywrightTargets(t *testing.T) {
	if names := Names(); len(names) != 2 || names[0] != "java" || names[1] != "playwright" {
		t.Fatalf("catalog names = %#v", names)
	}
	for _, target := range []TargetIdentityV1{
		testTargetIdentityV1("debian", "12"),
		testTargetIdentityV1("debian", "13"),
		testTargetIdentityV1("ubuntu", "25.10"),
		testTargetIdentityV1("ubuntu", "26.04"),
	} {
		resolved, err := Resolve(ToolRequestV1{Name: "java", Version: "21", Revision: "1", Context: "build"}, target)
		if err != nil {
			t.Fatalf("resolve Java for %#v: %v", target, err)
		}
		if resolved.Manifest.Version != "21.0.12+8" || resolved.ReleaseProvenance.Revision != "1" || resolved.Digest == "" ||
			len(resolved.Payloads) != 1 || resolved.Payloads[0].SHA256 != "sha256:e4446ff06a276155697597cc0f1b15da004ff083f4964a35271ecee567177370" ||
			len(resolved.Sources) != 1 || resolved.Binding != nil || len(resolved.Selections) != 0 ||
			resolved.IntegrationFixture.Target != target || resolved.IntegrationFixture.BaseImageDigest == recordTestDigest ||
			resolved.ValidationProfile.Validator != "java-jdk" {
			t.Fatalf("resolved Java = %#v", resolved)
		}
	}
	for _, target := range []TargetIdentityV1{
		testTargetIdentityV1("debian", "12"),
		testTargetIdentityV1("ubuntu", "25.10"),
		testTargetIdentityV1("ubuntu", "26.04"),
	} {
		resolved, err := Resolve(ToolRequestV1{
			Name: "playwright", Version: "1.61.0", Revision: "1", Context: "runtime",
			Binding: "python", Selections: []string{"chromium"},
		}, target)
		if err != nil {
			t.Fatalf("resolve Playwright for %#v: %v", target, err)
		}
		if resolved.Binding == nil || resolved.Binding.Name != "python" || resolved.BindingArtifact == nil ||
			resolved.BindingArtifact.SHA256 != "sha256:54f3b39f6eab832e33458c1dd7da0b5682aedab3b09ae731b5c59fa12fd2024e" ||
			len(resolved.Payloads) != 3 || len(resolved.PackageSets) != 1 || len(resolved.Sources) != 4 || resolved.Digest == "" ||
			resolved.IntegrationFixture.Target != target || resolved.IntegrationFixture.BaseImageDigest == recordTestDigest ||
			resolved.ValidationProfile.Validator != "playwright-python-browser" {
			t.Fatalf("resolved Playwright = %#v", resolved)
		}
		wantsT64 := target.OSReleaseID == "ubuntu"
		hasT64 := containsRecordValueV1(resolved.PackageSets[0].Requirements, "libasound2t64")
		if wantsT64 != hasT64 {
			t.Fatalf("%s %s package roots = %#v", target.OSReleaseID, target.VersionID, resolved.PackageSets[0].Requirements)
		}
	}
}

func TestEmbeddedCatalogContainsEveryDefinitionRecordFamily(t *testing.T) {
	catalog := mustLoadCatalogV1()
	counts := map[string]int{}
	for _, record := range catalog.records {
		counts[record.Schema]++
	}
	for _, schema := range []string{
		ToolRecordSchemaV1,
		ReleaseManifestSchemaV1,
		ReleaseContractSchemaV1,
		TargetRecordSchemaV1,
		BindingContractSchemaV1,
		BindingArtifactSchemaV1,
		PayloadRecordSchemaV1,
		ArtifactSourceRecordSchemaV1,
		NativePackageSetSchemaV1,
		IntegrationFixtureSchemaV1,
		ValidationProfileSchemaV1,
	} {
		if counts[schema] == 0 {
			t.Errorf("embedded catalog has no %s records", schema)
		}
	}
}

func TestEmbeddedArtifactDefinitionsMatchVettedInventory(t *testing.T) {
	catalog := mustLoadCatalogV1()
	payloads := []struct {
		id           string
		size         string
		sha256       canonical.Digest
		entries      string
		unpackedSize string
	}{
		{id: "tool:java/releases/21.0.12+8/payloads/jdk-linux-amd64", size: "207486543", sha256: "sha256:e4446ff06a276155697597cc0f1b15da004ff083f4964a35271ecee567177370", entries: "542", unpackedSize: "361144464"},
		{id: "tool:playwright/releases/1.61.0/payloads/chromium/chromium-linux-amd64", size: "185646494", sha256: "sha256:13113b963ac22fffdad898a677591028e4397c46c1daa9e61811258eed6e35b5", entries: "308", unpackedSize: "396335288"},
		{id: "tool:playwright/releases/1.61.0/payloads/chromium/chromium-headless-shell-linux-amd64", size: "119778157", sha256: "sha256:410c9407d5de3fea80d9398666be06f2aa09154a3fa7b327dc254e336bb4c4b7", entries: "287", unpackedSize: "272987776"},
		{id: "tool:playwright/releases/1.61.0/payloads/chromium/ffmpeg-linux-amd64", size: "2376500", sha256: "sha256:ebc74fc5b94830176a3c2914ae96bd8bc7f6a91f4f33890230f84a172ee61ccc", entries: "2", unpackedSize: "5127582"},
	}
	for _, want := range payloads {
		record, exists := catalog.records[want.id]
		if !exists {
			t.Errorf("missing artifact definition %q", want.id)
			continue
		}
		got := record.Value.(*PayloadRecordV1)
		if got.Size != want.size || got.SHA256 != want.sha256 || got.Entries != want.entries || got.UnpackedSize != want.unpackedSize {
			t.Errorf("artifact %q inventory = size %s, sha256 %s, entries %s, unpacked %s", want.id, got.Size, got.SHA256, got.Entries, got.UnpackedSize)
		}
	}
	wheel := catalog.records["tool:playwright/releases/1.61.0/bindings/python/artifacts/linux-amd64"].Value.(*BindingArtifactRecordV1)
	if wheel.Filename != "playwright-1.61.0-py3-none-manylinux1_x86_64.whl" || wheel.Size != "47421381" || wheel.SHA256 != "sha256:54f3b39f6eab832e33458c1dd7da0b5682aedab3b09ae731b5c59fa12fd2024e" {
		t.Errorf("Playwright wheel inventory = %#v", wheel)
	}
}

func TestEmbeddedCatalogRejectsUnsupportedRequestsBeforeAcquisition(t *testing.T) {
	tests := []struct {
		name    string
		request ToolRequestV1
		target  TargetIdentityV1
		want    string
	}{
		{name: "unknown tool", request: ToolRequestV1{Name: "missing", Context: "build"}, target: testTargetIdentityV1("debian", "12"), want: "not defined"},
		{name: "wrong context", request: ToolRequestV1{Name: "java", Context: "runtime"}, target: testTargetIdentityV1("debian", "12"), want: "does not support context"},
		{name: "unsupported version alias", request: ToolRequestV1{Name: "playwright", Version: "1", Context: "runtime", Binding: "python", Selections: []string{"chromium"}}, target: testTargetIdentityV1("debian", "12"), want: "no release matching"},
		{name: "unsupported OS", request: ToolRequestV1{Name: "playwright", Context: "runtime", Binding: "python", Selections: []string{"chromium"}}, target: testTargetIdentityV1("ubuntu", "24.04"), want: "has no target"},
		{name: "unsupported architecture", request: ToolRequestV1{Name: "java", Context: "build"}, target: testTargetIdentityForArchitectureV1("debian", "12", "arm64"), want: "has no target"},
		{name: "missing binding", request: ToolRequestV1{Name: "playwright", Context: "runtime", Selections: []string{"chromium"}}, target: testTargetIdentityV1("debian", "12"), want: "binding is required"},
		{name: "missing selection", request: ToolRequestV1{Name: "playwright", Context: "runtime", Binding: "python", Selections: []string{}}, target: testTargetIdentityV1("debian", "12"), want: "at least 1"},
		{name: "unsupported selection", request: ToolRequestV1{Name: "playwright", Context: "runtime", Binding: "python", Selections: []string{"webkit"}}, target: testTargetIdentityV1("debian", "12"), want: "not supported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Resolve(test.request, test.target)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestSelectedClosureIgnoresUnselectedBindingAndSelectionAvailability(t *testing.T) {
	catalog := mustLoadCatalogV1()
	request := ToolRequestV1{
		Name: "playwright", Version: "1.61.0", Revision: "1", Context: "runtime",
		Binding: "python", Selections: []string{"chromium"},
	}
	targetIdentity := testTargetIdentityV1("debian", "12")
	resolved, err := catalog.Resolve(request, targetIdentity)
	if err != nil {
		t.Fatal(err)
	}
	contract := cloneReleaseContractV1(&resolved.Contract)
	target := cloneTargetRecordV1(&resolved.Target)
	contract.Binding.Options = append([]string{"node"}, contract.Binding.Options...)
	contract.Selections.Options = append(contract.Selections.Options, "webkit")
	target.Bindings = append(target.Bindings, TargetBindingV1{Name: "node"})
	target.Selections = append(target.Selections, TargetSelectionV1{Name: "webkit"})
	selectedBinding, _ := targetBindingV1(target.Bindings, "python")
	selectedSelection, _ := targetSelectionV1(target.Selections, "chromium")
	digest, err := selectedClosureDigestV1(
		request.Name, resolved.Manifest.Version, request.Context, "python", []string{"chromium"},
		&contract, &target, &selectedBinding, []TargetSelectionV1{selectedSelection}, resolved.SelectedRecords,
	)
	if err != nil {
		t.Fatal(err)
	}
	if digest != resolved.Digest {
		t.Fatalf("selected closure changed after unselected availability was added: %s != %s", digest, resolved.Digest)
	}
}

func TestSelectedClosureIgnoresUnrelatedTargetAndSourceLocatorChanges(t *testing.T) {
	files := embeddedDefinitionMapFSV1(t)
	baseCatalog, err := loadCatalogV1(files, "definitions")
	if err != nil {
		t.Fatal(err)
	}
	request := ToolRequestV1{Name: "java", Version: "21", Revision: "1", Context: "build"}
	target := testTargetIdentityV1("debian", "12")
	base, err := baseCatalog.Resolve(request, target)
	if err != nil {
		t.Fatal(err)
	}

	sourceDigest := rewriteCatalogRecordV1(t, files, "tool:java/releases/21.0.12+8/revisions/1/sources/jdk-linux-amd64", func(value any) {
		source := value.(*ArtifactSourceRecordV1)
		source.Mirrors = []string{"https://mirror.example.com/OpenJDK21U-jdk_x64_linux_hotspot_21.0.12_8.tar.gz"}
	})
	newFixture := cloneIntegrationFixtureV1(catalogRecordValueV1(t, files, "tool:java/releases/21.0.12+8/validation/fixtures/debian-13-amd64").(*IntegrationFixtureRecordV1))
	newFixture.ID = "tool:java/releases/21.0.12+8/validation/fixtures/debian-14-amd64"
	newFixture.Target.VersionID = "14"
	newFixtureDigest := addCatalogRecordV1(t, files, "definitions/java/versions/21.0.12+8/validation/fixtures/debian-14-amd64.json", &newFixture)
	newTarget := cloneTargetRecordV1(catalogRecordValueV1(t, files, "tool:java/releases/21.0.12+8/targets/debian/13/amd64").(*TargetRecordV1))
	newTarget.ID = "tool:java/releases/21.0.12+8/targets/debian/14/amd64"
	newTarget.Target.VersionID = "14"
	newTarget.IntegrationFixture = RecordReferenceV1{ID: newFixture.ID, Digest: newFixtureDigest}
	newTargetDigest := addCatalogRecordV1(t, files, "definitions/java/versions/21.0.12+8/targets/debian/14/amd64.json", &newTarget)
	manifestDigest := rewriteCatalogRecordV1(t, files, "tool:java/releases/21.0.12+8/revisions/1/manifest", func(value any) {
		manifest := value.(*ReleaseManifestV1)
		manifest.ArtifactSources[0].Source.Digest = sourceDigest
		manifest.Targets = append(manifest.Targets, RecordReferenceV1{ID: newTarget.ID, Digest: newTargetDigest})
		sort.Slice(manifest.Targets, func(left int, right int) bool { return manifest.Targets[left].ID < manifest.Targets[right].ID })
	})
	rewriteCatalogRecordV1(t, files, "tool:java", func(value any) {
		value.(*ToolRecordV1).Releases[0].Digest = manifestDigest
	})
	changedCatalog, err := loadCatalogV1(files, "definitions")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := changedCatalog.Resolve(request, target)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Digest != base.Digest {
		t.Fatalf("selected closure changed after unrelated catalog edits: %s != %s", changed.Digest, base.Digest)
	}
	if changed.ReleaseProvenance.ManifestDigest == base.ReleaseProvenance.ManifestDigest {
		t.Fatal("release provenance did not change with manifest revision contents")
	}
}

func TestSelectedClosureChangesWithPayloadMaterialization(t *testing.T) {
	files := embeddedDefinitionMapFSV1(t)
	request := ToolRequestV1{Name: "java", Version: "21", Revision: "1", Context: "build"}
	target := testTargetIdentityV1("debian", "12")
	baseCatalog, err := loadCatalogV1(files, "definitions")
	if err != nil {
		t.Fatal(err)
	}
	base, err := baseCatalog.Resolve(request, target)
	if err != nil {
		t.Fatal(err)
	}

	payloadID := "tool:java/releases/21.0.12+8/payloads/jdk-linux-amd64"
	payloadDigest := rewriteCatalogRecordV1(t, files, payloadID, func(value any) {
		value.(*PayloadRecordV1).LogicalPath = "tools/java/21.0.12+8/relocated.tar.gz"
	})
	targetDigests := map[string]canonical.Digest{}
	for _, targetID := range []string{
		"tool:java/releases/21.0.12+8/targets/debian/12/amd64",
		"tool:java/releases/21.0.12+8/targets/debian/13/amd64",
		"tool:java/releases/21.0.12+8/targets/ubuntu/25.10/amd64",
		"tool:java/releases/21.0.12+8/targets/ubuntu/26.04/amd64",
	} {
		targetDigests[targetID] = rewriteCatalogRecordV1(t, files, targetID, func(value any) {
			value.(*TargetRecordV1).Payloads[0].Digest = payloadDigest
		})
	}
	manifestDigest := rewriteCatalogRecordV1(t, files, "tool:java/releases/21.0.12+8/revisions/1/manifest", func(value any) {
		manifest := value.(*ReleaseManifestV1)
		for index := range manifest.Targets {
			manifest.Targets[index].Digest = targetDigests[manifest.Targets[index].ID]
		}
		manifest.ArtifactSources[0].Artifact.Digest = payloadDigest
	})
	rewriteCatalogRecordV1(t, files, "tool:java", func(value any) {
		value.(*ToolRecordV1).Releases[0].Digest = manifestDigest
	})
	changedCatalog, err := loadCatalogV1(files, "definitions")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := changedCatalog.Resolve(request, target)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Digest == base.Digest {
		t.Fatalf("selected closure did not change after payload materialization changed: %s", changed.Digest)
	}
}

func TestCatalogRejectsMissingDuplicateAndUnmappedRecords(t *testing.T) {
	t.Run("missing reference", func(t *testing.T) {
		files := embeddedDefinitionMapFSV1(t)
		delete(files, catalogRecordFilenameV1(t, files, "tool:java/releases/21.0.12+8/payloads/jdk-linux-amd64"))
		if _, err := loadCatalogV1(files, "definitions"); err == nil || !strings.Contains(err.Error(), "missing record") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("duplicate ID", func(t *testing.T) {
		files := embeddedDefinitionMapFSV1(t)
		filename := catalogRecordFilenameV1(t, files, "tool:java/releases/21.0.12+8/contract")
		files["definitions/java/versions/21.0.12+8/duplicate.json"] = &fstest.MapFile{Data: append([]byte{}, files[filename].Data...)}
		if _, err := loadCatalogV1(files, "definitions"); err == nil || !strings.Contains(err.Error(), "duplicate record ID") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("reference digest mismatch", func(t *testing.T) {
		files := embeddedDefinitionMapFSV1(t)
		rewriteCatalogRecordV1(t, files, "tool:java/releases/21.0.12+8/targets/debian/12/amd64", func(value any) {
			value.(*TargetRecordV1).Payloads[0].Digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		})
		if _, err := loadCatalogV1(files, "definitions"); err == nil || !strings.Contains(err.Error(), "has digest") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("filesystem namespace mismatch", func(t *testing.T) {
		files := embeddedDefinitionMapFSV1(t)
		filename := catalogRecordFilenameV1(t, files, "tool:java/releases/21.0.12+8/payloads/jdk-linux-amd64")
		files["definitions/playwright/misplaced-java-payload.json"] = files[filename]
		delete(files, filename)
		if _, err := loadCatalogV1(files, "definitions"); err == nil || !strings.Contains(err.Error(), "must live below") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("cross-tool reference", func(t *testing.T) {
		files := embeddedDefinitionMapFSV1(t)
		playwrightPayload := catalogRecordV1(t, files, "tool:playwright/releases/1.61.0/payloads/chromium/chromium-linux-amd64")
		targetID := "tool:java/releases/21.0.12+8/targets/debian/12/amd64"
		targetDigest := rewriteCatalogRecordV1(t, files, targetID, func(value any) {
			value.(*TargetRecordV1).Payloads[0] = recordReferenceV1(playwrightPayload)
		})
		manifestDigest := rewriteCatalogRecordV1(t, files, "tool:java/releases/21.0.12+8/revisions/1/manifest", func(value any) {
			manifest := value.(*ReleaseManifestV1)
			for index := range manifest.Targets {
				if manifest.Targets[index].ID == targetID {
					manifest.Targets[index].Digest = targetDigest
				}
			}
		})
		rewriteCatalogRecordV1(t, files, "tool:java", func(value any) {
			value.(*ToolRecordV1).Releases[0].Digest = manifestDigest
		})
		if _, err := loadCatalogV1(files, "definitions"); err == nil || !strings.Contains(err.Error(), "crosses tool namespaces") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("missing source mapping", func(t *testing.T) {
		files := embeddedDefinitionMapFSV1(t)
		delete(files, catalogRecordFilenameV1(t, files, "tool:java/releases/21.0.12+8/revisions/1/sources/jdk-linux-amd64"))
		manifestDigest := rewriteCatalogRecordV1(t, files, "tool:java/releases/21.0.12+8/revisions/1/manifest", func(value any) {
			value.(*ReleaseManifestV1).ArtifactSources = []ArtifactSourceMappingV1{}
		})
		rewriteCatalogRecordV1(t, files, "tool:java", func(value any) {
			value.(*ToolRecordV1).Releases[0].Digest = manifestDigest
		})
		if _, err := loadCatalogV1(files, "definitions"); err == nil || !strings.Contains(err.Error(), "no source mapping") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("duplicate source mapping", func(t *testing.T) {
		files := embeddedDefinitionMapFSV1(t)
		manifestID := "tool:java/releases/21.0.12+8/revisions/1/manifest"
		filename := catalogRecordFilenameV1(t, files, manifestID)
		manifest := catalogRecordValueV1(t, files, manifestID).(*ReleaseManifestV1)
		manifest.ArtifactSources = append(manifest.ArtifactSources, manifest.ArtifactSources[0])
		payload, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		files[filename] = &fstest.MapFile{Data: payload}
		if _, err := loadCatalogV1(files, "definitions"); err == nil || !strings.Contains(err.Error(), "unique and sorted") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("orphaned source mapping", func(t *testing.T) {
		files := embeddedDefinitionMapFSV1(t)
		payload := clonePayloadRecordV1(catalogRecordValueV1(t, files, "tool:java/releases/21.0.12+8/payloads/jdk-linux-amd64").(*PayloadRecordV1))
		payload.ID = "tool:java/releases/21.0.12+8/payloads/orphan-linux-amd64"
		payload.Name = "orphan"
		payload.SHA256 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		payload.Size = "42"
		payloadDigest := addCatalogRecordV1(t, files, "definitions/java/versions/21.0.12+8/payloads/orphan-linux-amd64.json", &payload)
		source := *catalogRecordValueV1(t, files, "tool:java/releases/21.0.12+8/revisions/1/sources/jdk-linux-amd64").(*ArtifactSourceRecordV1)
		source.ID = "tool:java/releases/21.0.12+8/revisions/1/sources/orphan-linux-amd64"
		source.SHA256 = payload.SHA256
		source.Size = payload.Size
		sourceDigest := addCatalogRecordV1(t, files, "definitions/java/versions/21.0.12+8/revisions/1/sources/orphan-linux-amd64.json", &source)
		manifestDigest := rewriteCatalogRecordV1(t, files, "tool:java/releases/21.0.12+8/revisions/1/manifest", func(value any) {
			manifest := value.(*ReleaseManifestV1)
			manifest.ArtifactSources = append(manifest.ArtifactSources, ArtifactSourceMappingV1{
				ArtifactSHA256: payload.SHA256,
				Artifact:       RecordReferenceV1{ID: payload.ID, Digest: payloadDigest},
				Source:         RecordReferenceV1{ID: source.ID, Digest: sourceDigest},
			})
			sort.Slice(manifest.ArtifactSources, func(left int, right int) bool {
				return manifest.ArtifactSources[left].ArtifactSHA256 < manifest.ArtifactSources[right].ArtifactSHA256
			})
		})
		rewriteCatalogRecordV1(t, files, "tool:java", func(value any) {
			value.(*ToolRecordV1).Releases[0].Digest = manifestDigest
		})
		if _, err := loadCatalogV1(files, "definitions"); err == nil || !strings.Contains(err.Error(), "orphaned artifact source mapping") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("shared digest size mismatch", func(t *testing.T) {
		files := embeddedDefinitionMapFSV1(t)
		payload := clonePayloadRecordV1(catalogRecordValueV1(t, files, "tool:java/releases/21.0.12+8/payloads/jdk-linux-amd64").(*PayloadRecordV1))
		payload.ID = "tool:java/releases/21.0.12+8/payloads/jdk-copy-linux-amd64"
		payload.Name = "jdk-copy"
		payload.Size = "1"
		payloadDigest := addCatalogRecordV1(t, files, "definitions/java/versions/21.0.12+8/payloads/jdk-copy-linux-amd64.json", &payload)
		targetID := "tool:java/releases/21.0.12+8/targets/debian/12/amd64"
		targetDigest := rewriteCatalogRecordV1(t, files, targetID, func(value any) {
			target := value.(*TargetRecordV1)
			target.Payloads = append(target.Payloads, RecordReferenceV1{ID: payload.ID, Digest: payloadDigest})
			sort.Slice(target.Payloads, func(left int, right int) bool { return target.Payloads[left].ID < target.Payloads[right].ID })
		})
		manifestDigest := rewriteCatalogRecordV1(t, files, "tool:java/releases/21.0.12+8/revisions/1/manifest", func(value any) {
			manifest := value.(*ReleaseManifestV1)
			for index := range manifest.Targets {
				if manifest.Targets[index].ID == targetID {
					manifest.Targets[index].Digest = targetDigest
				}
			}
		})
		rewriteCatalogRecordV1(t, files, "tool:java", func(value any) {
			value.(*ToolRecordV1).Releases[0].Digest = manifestDigest
		})
		if _, err := loadCatalogV1(files, "definitions"); err == nil || !strings.Contains(err.Error(), "size disagrees") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestCatalogLoaderEnforcesResourceLimitsBeforeValidation(t *testing.T) {
	tests := []struct {
		name   string
		limits catalogLimitsV1
		want   string
	}{
		{name: "records", limits: catalogLimitsV1{Records: 1, Bytes: maxCatalogBytesV1, ReferenceEdges: maxCatalogReferenceEdgesV1}, want: "more than 1 records"},
		{name: "aggregate bytes", limits: catalogLimitsV1{Records: maxCatalogRecordsV1, Bytes: 1, ReferenceEdges: maxCatalogReferenceEdgesV1}, want: "more than 1 bytes"},
		{name: "reference edges", limits: catalogLimitsV1{Records: maxCatalogRecordsV1, Bytes: maxCatalogBytesV1, ReferenceEdges: 1}, want: "more than 1 reference edges"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadCatalogWithLimitsV1(embeddedDefinitionMapFSV1(t), "definitions", test.limits)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	oversized := fstest.MapFS{
		"definitions/java/tool.json": &fstest.MapFile{Data: make([]byte, maxDefinitionFileBytes+1)},
	}
	if _, err := loadCatalogV1(oversized, "definitions"); err == nil || !strings.Contains(err.Error(), "exceeds the") {
		t.Fatalf("oversized record error = %v", err)
	}
}

func TestSelectedContributionUnionDeduplicatesAndRejectsConflicts(t *testing.T) {
	reference := RecordReferenceV1{ID: "tool:demo/releases/1/contract", Digest: recordTestDigest}
	union, err := canonicalReferenceUnionV1([]RecordReferenceV1{reference, reference})
	if err != nil || len(union) != 1 {
		t.Fatalf("union = %#v, %v", union, err)
	}
	conflict := reference
	conflict.Digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	if _, err := canonicalReferenceUnionV1([]RecordReferenceV1{reference, conflict}); err == nil || !strings.Contains(err.Error(), "conflicting references") {
		t.Fatalf("conflicting reference error = %v", err)
	}
	oversized := make([]RecordReferenceV1, maxSelectedContributionsV1+1)
	if _, err := canonicalReferenceUnionV1(oversized); err == nil || !strings.Contains(err.Error(), "contributions") {
		t.Fatalf("oversized contribution error = %v", err)
	}
	catalog := &CatalogV1{records: map[string]loadedRecordV1{}}
	first := &PayloadRecordV1{ID: "first", LogicalPath: "tools/shared.zip", InstallDirectory: "shared"}
	second := &PayloadRecordV1{ID: "second", LogicalPath: "tools/other.zip", InstallDirectory: "shared/nested"}
	catalog.records[first.ID] = loadedRecordV1{ID: first.ID, Value: first}
	catalog.records[second.ID] = loadedRecordV1{ID: second.ID, Value: second}
	if err := catalog.validateSelectedContributions([]RecordReferenceV1{{ID: first.ID}, {ID: second.ID}}); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap error = %v", err)
	}
}

func testTargetIdentityV1(osID string, version string) TargetIdentityV1 {
	return testTargetIdentityForArchitectureV1(osID, version, "amd64")
}

func testTargetIdentityForArchitectureV1(osID string, version string, architecture string) TargetIdentityV1 {
	return TargetIdentityV1{
		Platform: "linux/" + architecture, OSReleaseID: osID, VersionID: version,
		OCIArchitecture: architecture, NativeArchitecture: architecture, PackageManager: "apt",
	}
}

func embeddedDefinitionMapFSV1(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	err := fs.WalkDir(definitionFilesV1, "definitions", func(filename string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		payload, err := fs.ReadFile(definitionFilesV1, filename)
		if err != nil {
			return err
		}
		files[filename] = &fstest.MapFile{Data: append([]byte{}, payload...)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func catalogRecordFilenameV1(t *testing.T, files fstest.MapFS, id string) string {
	t.Helper()
	for filename, file := range files {
		record, err := decodeRecordV1(filename, file.Data)
		if err == nil && record.ID == id {
			return filename
		}
	}
	t.Fatalf("record %q not found", id)
	return ""
}

func catalogRecordValueV1(t *testing.T, files fstest.MapFS, id string) any {
	t.Helper()
	filename := catalogRecordFilenameV1(t, files, id)
	record, err := decodeRecordV1(filename, files[filename].Data)
	if err != nil {
		t.Fatal(err)
	}
	return record.Value
}

func catalogRecordV1(t *testing.T, files fstest.MapFS, id string) loadedRecordV1 {
	t.Helper()
	filename := catalogRecordFilenameV1(t, files, id)
	record, err := decodeRecordV1(filename, files[filename].Data)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func rewriteCatalogRecordV1(t *testing.T, files fstest.MapFS, id string, mutate func(any)) canonical.Digest {
	t.Helper()
	filename := catalogRecordFilenameV1(t, files, id)
	value := catalogRecordValueV1(t, files, id)
	mutate(value)
	return writeCatalogRecordV1(t, files, filename, value)
}

func addCatalogRecordV1(t *testing.T, files fstest.MapFS, filename string, value any) canonical.Digest {
	t.Helper()
	if _, exists := files[filename]; exists {
		t.Fatalf("catalog file %q already exists", filename)
	}
	return writeCatalogRecordV1(t, files, filename, value)
}

func writeCatalogRecordV1(t *testing.T, files fstest.MapFS, filename string, value any) canonical.Digest {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	files[filename] = &fstest.MapFile{Data: payload}
	record, err := decodeRecordV1(filename, payload)
	if err != nil {
		t.Fatal(err)
	}
	return record.Digest
}
