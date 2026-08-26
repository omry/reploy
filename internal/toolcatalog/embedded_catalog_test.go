package toolcatalog

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
)

func TestEmbeddedCatalogMatchesCanonicalAuthoringV1(t *testing.T) {
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
		t.Fatalf("load embedded authoring: %v", err)
	}
	if len(result.Records) != 59 || len(result.Sources) != 59 {
		t.Fatalf("authoring emitted %d records from %d sources, want 59 and 59", len(result.Records), len(result.Sources))
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
	if catalog, err := loadCatalogV1(definitionFilesV1, "definitions"); err != nil || !reflect.DeepEqual(catalog.Names(), []string{"asciinema", "java", "playwright"}) {
		t.Fatalf("embedded catalog = names %v, error %v", catalogNamesV1(catalog), err)
	}
}

func TestEmbeddedAsciinemaCatalogSelectsEveryPinnedTargetV1(t *testing.T) {
	catalog := mustLoadEmbeddedCatalogV1()
	vettedTargets := []struct {
		target          TargetIdentityV1
		baseImage       string
		baseImageDigest canonical.Digest
		payload         string
	}{
		{asciinemaTargetV1("debian", "12", "amd64"), "docker.io/library/debian:12-slim", "sha256:362e64223cc0da95422b3b13c045186fc0a81250e765d31c025fbddf257f6143", "asciinema-linux-amd64"},
		{asciinemaTargetV1("debian", "12", "arm64"), "docker.io/library/debian:12-slim", "sha256:362e64223cc0da95422b3b13c045186fc0a81250e765d31c025fbddf257f6143", "asciinema-linux-arm64"},
		{asciinemaTargetV1("debian", "13", "amd64"), "docker.io/library/debian:13-slim", "sha256:38a76d01668772e381ad2826d876627c89e7133e2f8a0f5d567306798b0f2a16", "asciinema-linux-amd64"},
		{asciinemaTargetV1("debian", "13", "arm64"), "docker.io/library/debian:13-slim", "sha256:38a76d01668772e381ad2826d876627c89e7133e2f8a0f5d567306798b0f2a16", "asciinema-linux-arm64"},
		{asciinemaTargetV1("ubuntu", "25.10", "amd64"), "docker.io/library/ubuntu:25.10", "sha256:e0b84ef30bbe766773e6056c60a3e706712e4119508e3da12516f1eddd6f761b", "asciinema-linux-amd64"},
		{asciinemaTargetV1("ubuntu", "25.10", "arm64"), "docker.io/library/ubuntu:25.10", "sha256:e0b84ef30bbe766773e6056c60a3e706712e4119508e3da12516f1eddd6f761b", "asciinema-linux-arm64"},
		{asciinemaTargetV1("ubuntu", "26.04", "amd64"), "docker.io/library/ubuntu:26.04", "sha256:7b202b0e2e0028c6250f5fcf41d04df492d145a1654c6995a6553f0c1f6f1960", "asciinema-linux-amd64"},
		{asciinemaTargetV1("ubuntu", "26.04", "arm64"), "docker.io/library/ubuntu:26.04", "sha256:7b202b0e2e0028c6250f5fcf41d04df492d145a1654c6995a6553f0c1f6f1960", "asciinema-linux-arm64"},
	}
	for _, vetted := range vettedTargets {
		candidates, err := catalog.SelectReleaseCandidatesV1(asciinemaGroupV1("==3.2.1", "build"), vetted.target,
			ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}}, nil)
		if err != nil {
			t.Fatalf("select asciinema for %s %s %s: %v", vetted.target.OSReleaseID, vetted.target.VersionID, vetted.target.OCIArchitecture, err)
		}
		if len(candidates) != 1 {
			t.Fatalf("asciinema candidate count = %d, want 1", len(candidates))
		}
		candidate := candidates[0]
		if candidate.Manifest.Version != "3.2.1" || candidate.Manifest.Revision != "1" ||
			len(candidate.Bindings) != 0 || !reflect.DeepEqual(candidate.Selections, map[string][]string{}) ||
			candidate.Fixture.Target != vetted.target || candidate.Fixture.BaseImage != vetted.baseImage ||
			candidate.Fixture.BaseImageDigest != vetted.baseImageDigest || len(candidate.Profiles) != 1 {
			t.Fatalf("selected asciinema candidate = %#v", candidate)
		}
		ids := referenceIDsV1(candidate.Contributions)
		if len(ids) != 1 || !strings.HasSuffix(ids[0], "/payloads/"+vetted.payload) {
			t.Fatalf("asciinema contributions = %v, want payload %q", ids, vetted.payload)
		}

		result, err := catalog.ResolveSelectedClosuresV1(
			[]ReleaseCandidateSetV1{{Group: asciinemaGroupV1("==3.2.1", "build"), Candidates: candidates}},
			solverTestDomainsV1(false)[1:], solverTestOperationV1())
		if err != nil {
			t.Fatalf("resolve asciinema closure: %v", err)
		}
		closure := result.Closures[0]
		if len(closure.Records.Payloads) != 1 || len(closure.Records.BindingContracts) != 0 ||
			len(closure.Records.BindingArtifacts) != 0 || len(closure.Records.PackageSets) != 0 ||
			len(closure.Profiles) != 1 || closure.Fixture.ID == "" || closure.Identity == "" {
			t.Fatalf("resolved asciinema closure = %#v", closure)
		}
	}
}

func TestEmbeddedAsciinemaCatalogPinsArtifactInventoryV1(t *testing.T) {
	manifest := embeddedRecordV1[*ReleaseManifestV1](t, "tool:asciinema/releases/3.2.1/revisions/1/manifest")
	if len(manifest.Targets) != 8 || len(manifest.ArtifactSources) != 2 || len(manifest.ValidationProfiles) != 1 {
		t.Fatalf("embedded asciinema manifest = %#v", manifest)
	}
	payloads := []struct {
		id, platform, logicalPath, size, sha256 string
	}{
		{"asciinema-linux-amd64", "linux/amd64", "tools/asciinema/3.2.1/asciinema-x86_64-unknown-linux-gnu", "7983848", "sha256:1b405bbda565b33c3c4718de67fedc3535580603c0694b1ff3fb04f363430a20"},
		{"asciinema-linux-arm64", "linux/arm64", "tools/asciinema/3.2.1/asciinema-aarch64-unknown-linux-gnu", "7138888", "sha256:b516a6d896844c0ffbc96e0a55afe4cbcc79216abde0fc64fdda4e39bee421ea"},
	}
	for _, want := range payloads {
		payload := embeddedRecordV1[*PayloadRecordV1](t, "tool:asciinema/releases/3.2.1/payloads/"+want.id)
		if payload.UpstreamVersion != "3.2.1" || payload.Revision != "1" || payload.Platform != want.platform ||
			payload.LogicalPath != want.logicalPath || payload.Kind != "raw-executable" || payload.Size != want.size ||
			payload.UnpackedSize != want.size || payload.SHA256 != canonical.Digest(want.sha256) || payload.Entries != "1" ||
			payload.InstallDirectory != "asciinema-3.2.1" || payload.ArchiveRoot != "." ||
			!reflect.DeepEqual(payload.Executables, []string{"asciinema"}) {
			t.Fatalf("embedded asciinema payload %q = %#v", want.id, payload)
		}
		source := embeddedRecordV1[*ArtifactSourceRecordV1](t, "tool:asciinema/releases/3.2.1/revisions/1/sources/"+want.id)
		if source.SHA256 != payload.SHA256 || len(source.Mirrors) != 1 ||
			!strings.HasPrefix(source.Mirrors[0], "https://github.com/asciinema/asciinema/releases/download/v3.2.1/") ||
			!reflect.DeepEqual(source.Provenance, []string{"https://github.com/asciinema/asciinema/releases/tag/v3.2.1"}) || len(source.Diagnostics) != 0 {
			t.Fatalf("embedded asciinema source %q = %#v", want.id, source)
		}
	}
	profile := embeddedRecordV1[*ValidationProfileRecordV1](t, "tool:asciinema/releases/3.2.1/validation/profiles/default")
	if !reflect.DeepEqual(profile.Probes, []RecordProbeV1{{
		Path: "/opt/reploy/tools/asciinema/asciinema-3.2.1/asciinema", Args: []string{"--version"},
	}}) {
		t.Fatalf("embedded asciinema validation profile = %#v", profile)
	}
}

func TestEmbeddedAsciinemaCatalogRejectsUnsupportedRequestsBeforeAcquisitionV1(t *testing.T) {
	catalog := mustLoadEmbeddedCatalogV1()
	tests := []struct {
		name    string
		group   CanonicalRequirementGroupV1
		target  TargetIdentityV1
		wantSub string
	}{
		{name: "runtime context", group: asciinemaGroupV1("==3.2.1", "runtime"), target: asciinemaTargetV1("debian", "12", "amd64"), wantSub: "context"},
		{name: "wrong version", group: asciinemaGroupV1("==3.1.0", "build"), target: asciinemaTargetV1("debian", "12", "amd64"), wantSub: "empty intersection"},
		{name: "unsupported OS", group: asciinemaGroupV1("==3.2.1", "build"), target: asciinemaTargetV1("ubuntu", "24.04", "amd64"), wantSub: "no target leaf"},
		{name: "inconsistent architecture", group: asciinemaGroupV1("==3.2.1", "build"), target: func() TargetIdentityV1 {
			target := asciinemaTargetV1("debian", "12", "amd64")
			target.OCIArchitecture = "arm64"
			return target
		}(), wantSub: "inconsistent"},
		{name: "unsupported binding", group: func() CanonicalRequirementGroupV1 {
			group := asciinemaGroupV1("==3.2.1", "build")
			group.Binding = CanonicalBindingDemandV1{Explicit: []string{"python"}}
			return group
		}(), target: asciinemaTargetV1("debian", "12", "amd64"), wantSub: "binding"},
		{name: "unsupported selection", group: func() CanonicalRequirementGroupV1 {
			group := asciinemaGroupV1("==3.2.1", "build")
			group.Selections = map[string][]string{"distribution": {"gnu"}}
			return group
		}(), target: asciinemaTargetV1("debian", "12", "amd64"), wantSub: "selection"},
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

func TestEmbeddedAsciinemaV2ComparisonIsDistinctAndUnpublishedV1(t *testing.T) {
	catalog := mustLoadEmbeddedCatalogV1()
	tool := embeddedRecordV1[*ToolRecordV1](t, "tool:asciinema")
	if len(tool.Releases) != 1 || !strings.Contains(tool.Releases[0].ID, "/releases/3.2.1/") {
		t.Fatalf("published asciinema releases = %v, want only 3.2.1", tool.Releases)
	}
	for key := range catalog.records {
		if strings.HasPrefix(key.ID, "tool:asciinema/releases/2") {
			t.Fatalf("non-published asciinema v2 record %q is embedded", key.ID)
		}
	}

	candidates, err := catalog.SelectReleaseCandidatesV1(asciinemaGroupV1("==3.2.1", "build"), asciinemaTargetV1("debian", "12", "amd64"),
		ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	v3, err := catalog.ResolveSelectedClosuresV1(
		[]ReleaseCandidateSetV1{{Group: asciinemaGroupV1("==3.2.1", "build"), Candidates: candidates}},
		solverTestDomainsV1(false)[1:], solverTestOperationV1())
	if err != nil {
		t.Fatal(err)
	}
	v3Closure := v3.Closures[0]
	v2Contract := v3Closure.Contract
	v2Contract.Exports = []ToolExportV1{{Name: "asciinema", Path: "/opt/reploy/tools/asciinema/asciinema-2.4.0/bin/asciinema"}}
	v2Target := v3Closure.Target
	v2PayloadDigest := canonical.Digest("sha256:2222222222222222222222222222222222222222222222222222222222222222")
	v2Target.Payloads = []RecordReferenceV1{{ID: "tool:asciinema/releases/2.4.0/payloads/python-package", Digest: v2PayloadDigest}}
	v2IdentityInput := struct {
		Tool     string                       `json:"tool"`
		Version  string                       `json:"version"`
		Contract SelectedContractProjectionV1 `json:"contract"`
		Target   SelectedTargetProjectionV1   `json:"target"`
		Records  []RecordReferenceV1          `json:"records"`
	}{
		Tool: "asciinema", Version: "2.4.0", Contract: v2Contract, Target: v2Target,
		Records: []RecordReferenceV1{{ID: "tool:asciinema/releases/2.4.0/payloads/python-package", Digest: v2PayloadDigest}},
	}
	v2Identity, err := canonical.Sum("portable-tool-selected-closure", SelectedClosureIdentityV1, v2IdentityInput)
	if err != nil {
		t.Fatal(err)
	}
	if v3Closure.Provenance.Version == v2IdentityInput.Version || v3Closure.Identity == v2Identity ||
		v3Closure.Contract.Exports[0].Path == v2Contract.Exports[0].Path ||
		v3Closure.Target.Payloads[0].ID == v2Target.Payloads[0].ID {
		t.Fatalf("asciinema v2 comparison did not retain distinct coordinates and closure layout")
	}
}

func TestEmbeddedPlaywrightCatalogSelectsCompleteChromiumClosureV1(t *testing.T) {
	catalog := mustLoadEmbeddedCatalogV1()
	vettedTargets := []struct {
		target          TargetIdentityV1
		baseImage       string
		baseImageDigest canonical.Digest
		packageSet      string
	}{
		{playwrightTargetV1("debian", "12"), "docker.io/library/debian:12-slim", "sha256:362e64223cc0da95422b3b13c045186fc0a81250e765d31c025fbddf257f6143", "debian-12-amd64"},
		{playwrightTargetV1("ubuntu", "25.10"), "docker.io/library/ubuntu:25.10", "sha256:e0b84ef30bbe766773e6056c60a3e706712e4119508e3da12516f1eddd6f761b", "ubuntu-t64-amd64"},
		{playwrightTargetV1("ubuntu", "26.04"), "docker.io/library/ubuntu:26.04", "sha256:7b202b0e2e0028c6250f5fcf41d04df492d145a1654c6995a6553f0c1f6f1960", "ubuntu-t64-amd64"},
	}
	wantContributionSuffixes := []string{
		"bindings/python/artifacts/linux-amd64", "bindings/python/contract", "package-sets/",
		"payloads/chromium/chromium-headless-shell-linux-amd64", "payloads/chromium/chromium-linux-amd64",
		"payloads/chromium/ffmpeg-linux-amd64",
	}
	for _, vetted := range vettedTargets {
		candidates, err := catalog.SelectReleaseCandidatesV1(playwrightGroupV1("==1.61.0", "runtime"), vetted.target,
			ClientCapabilitiesV1{ReployVersion: "1.0.0", ResolverPrimitives: []string{"https-sha256"}}, nil)
		if err != nil {
			t.Fatalf("select Playwright for %s %s: %v", vetted.target.OSReleaseID, vetted.target.VersionID, err)
		}
		if len(candidates) != 1 {
			t.Fatalf("Playwright candidate count = %d, want 1", len(candidates))
		}
		candidate := candidates[0]
		if candidate.Manifest.Version != "1.61.0" || candidate.Manifest.Revision != "1" ||
			!reflect.DeepEqual(candidate.Bindings, []string{"python"}) ||
			!reflect.DeepEqual(candidate.Selections, map[string][]string{"browser": {"chromium"}}) ||
			candidate.Fixture.Target != vetted.target || candidate.Fixture.BaseImage != vetted.baseImage ||
			candidate.Fixture.BaseImageDigest != vetted.baseImageDigest || len(candidate.Profiles) != 1 {
			t.Fatalf("selected Playwright candidate = %#v", candidate)
		}
		ids := referenceIDsV1(candidate.Contributions)
		if len(ids) != len(wantContributionSuffixes) {
			t.Fatalf("Playwright contributions = %v, want six coupled records", ids)
		}
		for index, suffix := range wantContributionSuffixes {
			if suffix == "package-sets/" {
				suffix += vetted.packageSet
			}
			if !strings.HasSuffix(ids[index], suffix) {
				t.Fatalf("Playwright contribution[%d] = %q, want suffix %q", index, ids[index], suffix)
			}
		}

		result, err := catalog.ResolveSelectedClosuresV1(
			[]ReleaseCandidateSetV1{{Group: playwrightGroupV1("==1.61.0", "runtime"), Candidates: candidates}},
			solverTestDomainsV1(false)[:1], solverTestOperationV1())
		if err != nil {
			t.Fatalf("resolve Playwright closure: %v", err)
		}
		closure := result.Closures[0]
		if len(closure.Records.BindingContracts) != 1 || len(closure.Records.BindingArtifacts) != 1 ||
			len(closure.Records.Payloads) != 3 || len(closure.Records.PackageSets) != 1 ||
			len(closure.Profiles) != 1 || closure.Fixture.ID == "" || closure.Identity == "" {
			t.Fatalf("resolved Playwright closure = %#v", closure)
		}
	}
}

func TestEmbeddedPlaywrightCatalogPinsArtifactInventoryV1(t *testing.T) {
	contract := embeddedRecordV1[*BindingContractV1](t, "tool:playwright/releases/1.61.0/bindings/python/contract")
	if !reflect.DeepEqual(contract.Requirements, []string{"greenlet>=3.1.1,<4.0.0", "playwright==1.61.0", "pyee>=13,<14"}) ||
		!reflect.DeepEqual(contract.SupportedPython, []string{"3.10", "3.11", "3.12", "3.13", "3.14"}) ||
		!reflect.DeepEqual(contract.SupportedTags, []string{"py3-none-manylinux1_x86_64"}) {
		t.Fatalf("embedded Playwright binding contract = %#v", contract)
	}
	binding := embeddedRecordV1[*BindingArtifactRecordV1](t, "tool:playwright/releases/1.61.0/bindings/python/artifacts/linux-amd64")
	if binding.Filename != "playwright-1.61.0-py3-none-manylinux1_x86_64.whl" || binding.Size != "47421381" ||
		binding.SHA256 != "sha256:54f3b39f6eab832e33458c1dd7da0b5682aedab3b09ae731b5c59fa12fd2024e" ||
		binding.RequiresPython != ">=3.10" || !reflect.DeepEqual(binding.Tags, []string{"py3-none-manylinux1_x86_64"}) {
		t.Fatalf("embedded Playwright binding artifact = %#v", binding)
	}
	wantComponents := []BundledComponentV1{
		{Name: "nodejs", Version: "24.17.0", Path: "playwright/driver/node"},
		{Name: "playwright-core", Version: "1.61.1-beta-1782139630000", Path: "playwright/driver/package"},
	}
	if !reflect.DeepEqual(binding.BundledComponents, wantComponents) {
		t.Fatalf("Playwright bundled components = %#v", binding.BundledComponents)
	}
	payloads := []struct {
		id, revision, upstream, size, sha256, entries, unpacked, root, executable string
	}{
		{"chromium/chromium-headless-shell-linux-amd64", "1228", "149.0.7827.55", "119778157", "sha256:410c9407d5de3fea80d9398666be06f2aa09154a3fa7b327dc254e336bb4c4b7", "287", "272987776", "chrome-headless-shell-linux64", "chrome-headless-shell-linux64/chrome-headless-shell"},
		{"chromium/chromium-linux-amd64", "1228", "149.0.7827.55", "185646494", "sha256:13113b963ac22fffdad898a677591028e4397c46c1daa9e61811258eed6e35b5", "308", "396335288", "chrome-linux64", "chrome-linux64/chrome"},
		{"chromium/ffmpeg-linux-amd64", "1011", "1011", "2376500", "sha256:ebc74fc5b94830176a3c2914ae96bd8bc7f6a91f4f33890230f84a172ee61ccc", "2", "5127582", ".", "ffmpeg-linux"},
	}
	for _, want := range payloads {
		payload := embeddedRecordV1[*PayloadRecordV1](t, "tool:playwright/releases/1.61.0/payloads/"+want.id)
		if payload.Revision != want.revision || payload.UpstreamVersion != want.upstream || payload.Size != want.size ||
			payload.SHA256 != canonical.Digest(want.sha256) || payload.Entries != want.entries ||
			payload.UnpackedSize != want.unpacked || payload.ArchiveRoot != want.root ||
			!reflect.DeepEqual(payload.Executables, []string{want.executable}) {
			t.Fatalf("embedded Playwright payload %q = %#v", want.id, payload)
		}
	}
	sources := []struct {
		id, sha256, mirror, provenance string
	}{
		{"chromium-headless-shell-linux-amd64", "sha256:410c9407d5de3fea80d9398666be06f2aa09154a3fa7b327dc254e336bb4c4b7", "https://storage.googleapis.com/chrome-for-testing-public/149.0.7827.55/linux64/chrome-headless-shell-linux64.zip", "https://raw.githubusercontent.com/microsoft/playwright/v1.61.0/packages/playwright-core/browsers.json"},
		{"chromium-linux-amd64", "sha256:13113b963ac22fffdad898a677591028e4397c46c1daa9e61811258eed6e35b5", "https://storage.googleapis.com/chrome-for-testing-public/149.0.7827.55/linux64/chrome-linux64.zip", "https://raw.githubusercontent.com/microsoft/playwright/v1.61.0/packages/playwright-core/browsers.json"},
		{"ffmpeg-linux-amd64", "sha256:ebc74fc5b94830176a3c2914ae96bd8bc7f6a91f4f33890230f84a172ee61ccc", "https://playwright.download.prss.microsoft.com/dbazure/download/playwright/builds/ffmpeg/1011/ffmpeg-linux.zip", "https://raw.githubusercontent.com/microsoft/playwright/v1.61.0/packages/playwright-core/browsers.json"},
		{"python-linux-amd64", "sha256:54f3b39f6eab832e33458c1dd7da0b5682aedab3b09ae731b5c59fa12fd2024e", "https://files.pythonhosted.org/packages/ab/f8/a35bf179e4ba2522c1893635094a64e407572547bd61528820fc0abc87fe/playwright-1.61.0-py3-none-manylinux1_x86_64.whl", "https://pypi.org/project/playwright/1.61.0/"},
	}
	for _, want := range sources {
		source := embeddedRecordV1[*ArtifactSourceRecordV1](t, "tool:playwright/releases/1.61.0/revisions/1/sources/"+want.id)
		if source.SHA256 != canonical.Digest(want.sha256) || !reflect.DeepEqual(source.Mirrors, []string{want.mirror}) ||
			!reflect.DeepEqual(source.Provenance, []string{want.provenance}) || len(source.Diagnostics) != 0 {
			t.Fatalf("embedded Playwright source %q = %#v", want.id, source)
		}
	}
	commonRequirements := []string{
		"fonts-freefont-ttf", "fonts-ipafont-gothic", "fonts-liberation", "fonts-noto-color-emoji",
		"fonts-tlwg-loma-otf", "fonts-unifont", "fonts-wqy-zenhei",
	}
	debianRequirements := append(append([]string{}, commonRequirements...),
		"libasound2", "libatk-bridge2.0-0", "libatk1.0-0", "libatspi2.0-0", "libcairo2", "libcups2",
		"libdbus-1-3", "libdrm2", "libfontconfig1", "libfreetype6", "libgbm1", "libglib2.0-0", "libnspr4",
		"libnss3", "libpango-1.0-0", "libx11-6", "libxcb1", "libxcomposite1", "libxdamage1", "libxext6",
		"libxfixes3", "libxkbcommon0", "libxrandr2", "xfonts-cyrillic", "xfonts-scalable", "xvfb")
	ubuntuRequirements := append(append([]string{}, commonRequirements...),
		"libasound2t64", "libatk-bridge2.0-0t64", "libatk1.0-0t64", "libatspi2.0-0t64", "libcairo2",
		"libcups2t64", "libdbus-1-3", "libdrm2", "libfontconfig1", "libfreetype6", "libgbm1", "libglib2.0-0t64",
		"libnspr4", "libnss3", "libpango-1.0-0", "libx11-6", "libxcb1", "libxcomposite1", "libxdamage1",
		"libxext6", "libxfixes3", "libxkbcommon0", "libxrandr2", "xfonts-cyrillic", "xfonts-scalable", "xvfb")
	for id, want := range map[string][]string{"debian-12-amd64": debianRequirements, "ubuntu-t64-amd64": ubuntuRequirements} {
		packageSet := embeddedRecordV1[*NativePackageSetV1](t, "tool:playwright/releases/1.61.0/package-sets/"+id)
		if packageSet.Manager != "apt" || !reflect.DeepEqual(packageSet.Requirements, want) ||
			len(packageSet.Repositories) != 0 || len(packageSet.ValidationMetadata) != 0 {
			t.Fatalf("embedded Playwright package set %q = %#v", id, packageSet)
		}
	}
	profile := embeddedRecordV1[*ValidationProfileRecordV1](t, "tool:playwright/releases/1.61.0/validation/profiles/default")
	if !reflect.DeepEqual(profile.Probes, []RecordProbeV1{{Path: "/opt/reploy/tools/playwright/bin/playwright", Args: []string{
		"screenshot", "--browser", "chromium", "about:blank", "/tmp/reploy-playwright-validation.png",
	}}}) {
		t.Fatalf("embedded Playwright validation profile = %#v", profile)
	}
}

func TestEmbeddedPlaywrightCatalogRejectsUnsupportedRequestsBeforeAcquisitionV1(t *testing.T) {
	catalog := mustLoadEmbeddedCatalogV1()
	tests := []struct {
		name    string
		group   CanonicalRequirementGroupV1
		target  TargetIdentityV1
		wantSub string
	}{
		{name: "build context", group: playwrightGroupV1("==1.61.0", "build"), target: playwrightTargetV1("debian", "12"), wantSub: "context"},
		{name: "wrong version", group: playwrightGroupV1("==1.60.0", "runtime"), target: playwrightTargetV1("debian", "12"), wantSub: "empty intersection"},
		{name: "unsupported OS", group: playwrightGroupV1("==1.61.0", "runtime"), target: playwrightTargetV1("ubuntu", "24.04"), wantSub: "no target leaf"},
		{name: "unsupported architecture", group: playwrightGroupV1("==1.61.0", "runtime"), target: func() TargetIdentityV1 {
			target := playwrightTargetV1("debian", "12")
			target.Platform, target.OCIArchitecture, target.NativeArchitecture = "linux/arm64", "arm64", "arm64"
			return target
		}(), wantSub: "no target leaf"},
		{name: "unsupported binding", group: func() CanonicalRequirementGroupV1 {
			group := playwrightGroupV1("==1.61.0", "runtime")
			group.Binding = CanonicalBindingDemandV1{Explicit: []string{"node"}}
			return group
		}(), target: playwrightTargetV1("debian", "12"), wantSub: "binding"},
		{name: "unsupported browser", group: func() CanonicalRequirementGroupV1 {
			group := playwrightGroupV1("==1.61.0", "runtime")
			group.Selections = map[string][]string{"browser": {"firefox"}}
			return group
		}(), target: playwrightTargetV1("debian", "12"), wantSub: "selection"},
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

func asciinemaGroupV1(version string, context string) CanonicalRequirementGroupV1 {
	return CanonicalRequirementGroupV1{
		Scope: "source-builder", Tool: "asciinema", VersionConstraints: []string{version}, Context: context,
		Binding: CanonicalBindingDemandV1{Infer: true}, Selections: map[string][]string{},
	}
}

func asciinemaTargetV1(osID string, version string, architecture string) TargetIdentityV1 {
	return TargetIdentityV1{Platform: "linux/" + architecture, OSReleaseID: osID, VersionID: version,
		OCIArchitecture: architecture, NativeArchitecture: architecture, PackageManager: "apt"}
}

func javaTargetV1(osID string, version string) TargetIdentityV1 {
	return TargetIdentityV1{Platform: "linux/amd64", OSReleaseID: osID, VersionID: version,
		OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt"}
}

func playwrightGroupV1(version string, context string) CanonicalRequirementGroupV1 {
	return CanonicalRequirementGroupV1{
		Scope: "application", Tool: "playwright", VersionConstraints: []string{version}, Context: context,
		Binding: CanonicalBindingDemandV1{Infer: true}, Selections: map[string][]string{"browser": {"chromium"}},
	}
}

func playwrightTargetV1(osID string, version string) TargetIdentityV1 {
	return TargetIdentityV1{Platform: "linux/amd64", OSReleaseID: osID, VersionID: version,
		OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt"}
}

func referenceIDsV1(references []RecordReferenceV1) []string {
	result := make([]string, 0, len(references))
	for _, reference := range references {
		result = append(result, reference.ID)
	}
	sort.Strings(result)
	return result
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
