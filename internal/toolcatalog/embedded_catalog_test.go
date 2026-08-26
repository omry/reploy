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
	if len(result.Records) != 35 || len(result.Sources) != 35 {
		t.Fatalf("authoring emitted %d records from %d sources, want 35 and 35", len(result.Records), len(result.Sources))
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
	if catalog, err := loadCatalogV1(definitionFilesV1, "definitions"); err != nil || !reflect.DeepEqual(catalog.Names(), []string{"java", "playwright"}) {
		t.Fatalf("embedded catalog = names %v, error %v", catalogNamesV1(catalog), err)
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
		"libxfixes3", "libxkbcommon0", "libxrandr2", "xfonts-scalable", "xvfb")
	ubuntuRequirements := append(append([]string{}, commonRequirements...),
		"libasound2t64", "libatk-bridge2.0-0t64", "libatk1.0-0t64", "libatspi2.0-0t64", "libcairo2",
		"libcups2t64", "libdbus-1-3", "libdrm2", "libfontconfig1", "libfreetype6", "libgbm1", "libglib2.0-0t64",
		"libnspr4", "libnss3", "libpango-1.0-0", "libx11-6", "libxcb1", "libxcomposite1", "libxdamage1",
		"libxext6", "libxfixes3", "libxkbcommon0", "libxrandr2", "xfonts-scalable", "xvfb")
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
