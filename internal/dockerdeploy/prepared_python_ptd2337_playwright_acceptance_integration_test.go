package dockerdeploy

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// TestPortableToolPythonPTD2337TwoNeutralSyntheticWheels keeps the two-neutral
// fixture useful when Docker is unavailable. It constructs both selected wheel
// byte streams and sends the exact published descriptors through the same
// metadata and entry-point inspection used by the production acquisition path.
func TestPortableToolPythonPTD2337TwoNeutralSyntheticWheels(t *testing.T) {
	ctx := context.Background()
	fresh, _ := portableToolPythonFreshTwoNeutralFixtureV1(t)
	if len(fresh.Projection.Components) != 1 || len(fresh.Projection.Components[0].Bindings) != 2 {
		t.Fatalf("two-neutral projection = %#v", fresh.Projection)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range fresh.Projection.Components[0].Bindings {
		module := distributionUnderscoreV1(binding.Distribution)
		content := preparedPTD2337NeutralBindingWheelV1(
			t, module, binding.Distribution, binding.CLI.Name,
			"fixture "+binding.Distribution,
		)
		descriptor, err := store.Publish(ctx, "wheels/"+binding.Wheel.Filename, "wheel", bytes.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		inspection, err := pythonprovider.InspectWheelReaderV1(
			ctx, bytes.NewReader(content), int64(len(content)), binding.Wheel.Filename, descriptor,
		)
		if err != nil {
			t.Fatalf("inspect synthetic %s wheel: %v", binding.Distribution, err)
		}
		if inspection.Distribution != binding.Distribution || inspection.Version != binding.Wheel.EcosystemVersion {
			t.Fatalf("synthetic %s wheel identity = %#v", binding.Distribution, inspection)
		}
		if len(inspection.ConsoleScripts) != 1 || inspection.ConsoleScripts[0].Name != binding.CLI.Name {
			t.Fatalf("synthetic %s wheel entry points = %#v", binding.Distribution, inspection.ConsoleScripts)
		}
		if descriptor.Size != strconv.Itoa(len(content)) || descriptor.LogicalPath != "wheels/"+binding.Wheel.Filename {
			t.Fatalf("synthetic %s descriptor = %#v", binding.Distribution, descriptor)
		}
	}
}

// TestPreparedPythonGraphDockerIntegrationPTD2337PlaywrightBinding proves the
// application-scoped Python binding path against the selected embedded
// Playwright contract. The wheel bytes are a small synthetic replacement for
// the selected immutable artifact, while the plan, contract, artifact source,
// and CLI export remain the embedded selection.
func TestPreparedPythonGraphDockerIntegrationPTD2337PlaywrightBinding(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	base := os.Getenv("REPLOY_PTD2337_PYTHON_BASE")
	if base == "" {
		base = "python:3.12-slim"
	}
	imagePlatformOutput, err := runDockerOutput(ctx, "image", "inspect", "--platform", platform.Canonical, "--format", "{{.Os}}/{{.Architecture}}", base)
	if err != nil {
		t.Skipf("local %s image for %s is required for PTD-23.3.7 Playwright acceptance: %v", base, platform.Canonical, err)
	}
	if imagePlatform := strings.TrimSpace(imagePlatformOutput); imagePlatform != platform.Canonical {
		t.Skipf("local %s image platform %q does not match selected platform %s", base, imagePlatform, platform.Canonical)
	}
	baseIDOutput, err := runDockerOutput(ctx, "image", "inspect", "--platform", platform.Canonical, "--format", "{{index .RepoDigests 0}}", base)
	if err != nil {
		t.Skipf("local %s image repository digest is required for PTD-23.3.7 Playwright acceptance: %v", base, err)
	}
	base = strings.TrimSpace(baseIDOutput)
	if base == "" || base == "<no value>" {
		t.Skipf("local %s image has no repository digest for PTD-23.3.7 Playwright acceptance", base)
	}
	packedProbe := packIntegrationProbe(t, platform)
	previousLocate := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return packedProbe, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previousLocate })

	storeRoot := t.TempDir()
	store, err := providerstore.NewStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	fresh := portableToolPythonFreshPlaywrightFixtureV1(t, "application:application")
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1(fresh.Closures)
	if err != nil {
		t.Fatal(err)
	}
	component := fresh.Projection.Components[0]
	if len(component.Bindings) != 1 || len(fresh.Plan.Tools) != 1 || len(fresh.Plan.Tools[0].Exports) != 1 {
		t.Fatalf("embedded Playwright selection = %#v, projection = %#v", fresh.Plan, fresh.Projection)
	}
	binding := component.Bindings[0]
	wheelContent := ptd2337SyntheticPlaywrightWheelV1(t)
	wheel, err := store.Publish(ctx, "wheels/"+binding.Wheel.Filename, "wheel", bytes.NewReader(wheelContent))
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := pythonprovider.InspectWheelReaderV1(ctx, bytes.NewReader(wheelContent), int64(len(wheelContent)), binding.Wheel.Filename, wheel)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Distribution != binding.Distribution || inspection.Version != binding.Wheel.EcosystemVersion || len(inspection.ConsoleScripts) != 1 || inspection.ConsoleScripts[0].Name != binding.CLI.Name {
		t.Fatalf("synthetic selected Playwright wheel inspection = %#v", inspection)
	}

	// Rebind every catalog identity that authorizes the synthetic bytes. The
	// production producer must see the same artifact, source, manifest, plan,
	// closure, and lock identities that a real selected wheel would carry.
	selectedArtifact := &fresh.Plan.Tools[0].Responsibilities.BindingArtifacts[0]
	oldArtifactReference := selectedArtifact.Reference
	selectedArtifact.Record.Value["size"] = wheel.Size
	selectedArtifact.Record.Value["sha256"] = string(wheel.SHA256)
	selectedArtifact.Reference = portableToolPythonFreshReferenceV1(
		t, selectedArtifact.Record.Value["id"].(string), selectedArtifact.Record,
	)
	fresh.Plan.Tools[0].Responsibilities.BindingArtifacts[0] = *selectedArtifact
	newArtifactReference := selectedArtifact.Reference
	closure := &fresh.Closures[0]
	for index := range closure.Records.BindingArtifacts {
		if closure.Records.BindingArtifacts[index].Reference != oldArtifactReference {
			continue
		}
		closure.Records.BindingArtifacts[index].Reference = newArtifactReference
		closure.Records.BindingArtifacts[index].Record.Size = wheel.Size
		closure.Records.BindingArtifacts[index].Record.SHA256 = wheel.SHA256
	}
	for index := range closure.Target.Bindings {
		for artifactIndex := range closure.Target.Bindings[index].Artifacts {
			if closure.Target.Bindings[index].Artifacts[artifactIndex] == oldArtifactReference {
				closure.Target.Bindings[index].Artifacts[artifactIndex] = newArtifactReference
			}
		}
	}
	var selectedSource *toolcatalog.EmbeddedPortableToolArtifactSourceV1
	for index := range records.Artifacts {
		if records.Artifacts[index].Artifact == oldArtifactReference {
			selectedSource = &records.Artifacts[index]
			break
		}
	}
	if selectedSource == nil {
		t.Fatal("embedded Playwright binding artifact source is missing")
	}
	selectedSource.Artifact = newArtifactReference
	selectedSource.Descriptor = wheel
	selectedSource.Mirrors = []string{"https://example.invalid/reploy-ptd2337-playwright-wheel.whl"}
	selectedSource.Source.Record.Value["sha256"] = string(wheel.SHA256)
	selectedSource.Source.Record.Value["mirrors"] = []any{selectedSource.Mirrors[0]}
	selectedSource.Source.Reference = portableToolPythonFreshReferenceV1(
		t, selectedSource.Source.Record.Value["id"].(string), selectedSource.Source.Record,
	)
	for index := range records.Releases {
		manifestValue := records.Releases[index].Manifest.Record.Value
		artifactSources, ok := manifestValue["artifact_sources"].([]any)
		if !ok {
			t.Fatalf("embedded Playwright manifest artifact_sources = %#v", manifestValue["artifact_sources"])
		}
		mapped := false
		for sourceIndex := range artifactSources {
			mapping, ok := artifactSources[sourceIndex].(map[string]any)
			if !ok {
				t.Fatalf("embedded Playwright artifact source mapping = %#v", artifactSources[sourceIndex])
			}
			artifactValue, ok := mapping["artifact"].(map[string]any)
			if !ok || artifactValue["id"] != oldArtifactReference.ID {
				continue
			}
			artifactValue["digest"] = string(newArtifactReference.Digest)
			mapping["artifact_sha256"] = string(wheel.SHA256)
			sourceValue, ok := mapping["source"].(map[string]any)
			if !ok {
				t.Fatalf("embedded Playwright source mapping source = %#v", mapping["source"])
			}
			sourceValue["digest"] = string(selectedSource.Source.Reference.Digest)
			mapped = true
		}
		if !mapped {
			t.Fatalf("embedded Playwright release manifest has no mapping for %s", oldArtifactReference.ID)
		}
		sort.Slice(artifactSources, func(left, right int) bool {
			leftMapping, leftOK := artifactSources[left].(map[string]any)
			rightMapping, rightOK := artifactSources[right].(map[string]any)
			if !leftOK || !rightOK {
				return false
			}
			leftDigest, leftOK := leftMapping["artifact_sha256"].(string)
			rightDigest, rightOK := rightMapping["artifact_sha256"].(string)
			if !leftOK || !rightOK {
				return false
			}
			return leftDigest < rightDigest
		})
		manifestValue["artifact_sources"] = artifactSources
		manifestReference := portableToolPythonFreshReferenceV1(
			t, records.Releases[index].Manifest.Record.Value["id"].(string), records.Releases[index].Manifest.Record,
		)
		records.Releases[index].Manifest.Reference = manifestReference
		fresh.Plan.Tools[0].Provenance.ManifestDigest = manifestReference.Digest
		closure.Provenance.ManifestDigest = manifestReference.Digest
	}
	for index := range records.Artifacts {
		if records.Artifacts[index].Artifact != oldArtifactReference {
			continue
		}
		records.Artifacts[index].Artifact = newArtifactReference
		records.Artifacts[index].Descriptor = wheel
		records.Artifacts[index].Source = selectedSource.Source
		records.Artifacts[index].Mirrors = append([]string{}, selectedSource.Mirrors...)
	}
	closure.Identity = ptd2337PortableToolClosureIdentityV1(t, *closure)
	fresh.Plan.Tools[0].SelectedClosureDigest = closure.Identity
	// Keep only the selected binding source in this seam. If the production
	// path attempted browser payload acquisition, it would have no source to
	// join and the test would fail before the Python resolver runs.
	records.Artifacts = []toolcatalog.EmbeddedPortableToolArtifactSourceV1{*selectedSource}
	projectedComponents, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(fresh.Plan, []providers.ResolvedComponentRequestV1{})
	if err != nil {
		t.Fatal(err)
	}
	fresh.Projection = projection
	component = projection.Components[0]
	binding = component.Bindings[0]
	if binding.Wheel.Size != wheel.Size || binding.Wheel.SHA256 != wheel.SHA256 {
		t.Fatalf("selected Playwright wheel identity = %#v, published = %#v", binding.Wheel, wheel)
	}

	previousRecords := portableToolPythonFreshLockRecordsV1
	t.Cleanup(func() {
		portableToolPythonFreshLockRecordsV1 = previousRecords
	})
	portableToolPythonFreshLockRecordsV1 = func([]toolcatalog.SelectedClosureV1) (toolcatalog.EmbeddedPortableToolLockRecordSetV1, error) {
		return records, nil
	}
	cacheHit, err := store.AcquireArtifact(ctx, providerstore.AcquisitionRequest{
		Artifact: wheel,
		Source: providerstore.ArtifactSource{
			ID: selectedSource.Source.Reference.ID, SHA256: wheel.SHA256,
			Mirrors: append([]string{}, selectedSource.Mirrors...),
		},
		Policy: providerstore.DefaultAcquisitionPolicy(),
	})
	if err != nil {
		t.Fatalf("verified Playwright wheel cache hit: %v", err)
	}
	if cacheHit.Provenance.Outcome != providerstore.AcquisitionOutcomeCacheHit || len(cacheHit.Provenance.Attempts) != 0 {
		t.Fatalf("verified Playwright wheel acquisition = %#v, want cache hit without network attempts", cacheHit.Provenance)
	}

	components, projection, err := pythonprovider.ProjectPortableToolPythonBindingsV1(
		fresh.Plan, []providers.ResolvedComponentRequestV1{},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the complete projected request. The selected Playwright contract
	// includes greenlet and pyee, and the Docker acceptance must resolve the
	// same application closure that production receives.
	if len(components) != 1 || len(projectedComponents) != 1 {
		t.Fatalf("projected Playwright request changed unexpectedly: components=%#v initial=%#v", components, projectedComponents)
	}
	expectedRequest, err := providers.CanonicalProviderRequestBytes(projectedComponents[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	actualRequest, err := providers.CanonicalProviderRequestBytes(components[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualRequest, expectedRequest) {
		t.Fatalf("projected Playwright request changed unexpectedly: got=%s want=%s", actualRequest, expectedRequest)
	}
	distributions, err := pythonprovider.ProviderRequestDistributionsV1(components[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(distributions, ",") != "greenlet,playwright,pyee" {
		t.Fatalf("projected Playwright request distributions = %v, want greenlet,playwright,pyee", distributions)
	}
	dependencyOverrides := ptd2337SyntheticDependencyOverridesV1(t)
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: base,
		Exports: map[string]blueprint.BaseExecutableExport{
			"python": {Executable: "/usr/local/bin/python3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	components = append(components, providers.ResolvedComponentRequestV1{
		Component: "base", Provider: blueprint.ComponentTypeBase, Request: baseRequest,
	})
	request := providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: reuseTestDigest("b"),
		Platform: platform, Components: components, Sources: []providers.ResolvedSourceInput{},
	}
	if err := providers.ValidateResolvedRequestV1(request, registry.ValidateResolvedRequestOwnersV1); err != nil {
		t.Fatal(err)
	}
	preparedBase, err := PrepareProviderBase(ctx, store, request)
	if err != nil {
		t.Fatal(err)
	}
	finalImageConfig, err := ProviderFinalImageConfigV1(preparedBase.Config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecutePreparedPythonGraph(ctx, PreparedPythonGraphExecutionInput{
		Store: store, Plan: preparedBase.Plan, BaseDescriptor: preparedBase.Descriptor,
		BaseCatalog: preparedBase.Catalog, Sources: request.Sources,
		SourceWheels: []providerstore.ArtifactDescriptor{},
		PortablePython: &PortableToolPythonFreshPlanV1{
			Plan: fresh.Plan, Projection: projection, Closures: fresh.Closures,
		},
		DesiredPortableToolPlan: &fresh.Plan,
		LocalOverrides:          dependencyOverrides,
		FinalImageConfig:        finalImageConfig,
		RunOptions:              RunOptions{Stdout: os.Stdout, Stderr: os.Stderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Materializations) != 1 || len(result.Bundles) != 1 {
		t.Fatalf("Playwright graph result = %#v", result)
	}
	image := result.Materializations[0].Image.Digest
	t.Cleanup(func() { _, _ = runDockerOutput(context.Background(), "image", "rm", "--force", string(image)) })
	bundle, err := pythonprovider.DecodeCanonicalBundleDataV1(component.Component, result.Bundles[0].Payload.ProviderPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Wheels) != 4 {
		t.Fatalf("Playwright bundle wheel closure = %#v, want selected wheel plus three synthetic dependencies", bundle.Wheels)
	}
	bundleDistributions := make(map[string]bool, len(bundle.Wheels))
	for _, bundleWheel := range bundle.Wheels {
		bundleDistributions[bundleWheel.Distribution] = true
	}
	for _, distribution := range []string{"greenlet", "playwright", "pyee", "typing-extensions"} {
		if !bundleDistributions[distribution] {
			t.Fatalf("Playwright bundle is missing %s: %#v", distribution, bundle.Wheels)
		}
	}
	for _, bundleWheel := range bundle.Wheels {
		if bundleWheel.Distribution == binding.Distribution && bundleWheel.Artifact != wheel {
			t.Fatalf("Playwright bundle selected wheel = %#v, want published wheel %v", bundleWheel, wheel)
		}
	}
	if len(bundle.Outputs) != 1 || bundle.Outputs[0].Name != binding.CLI.Name {
		t.Fatalf("Playwright generated outputs = %#v", bundle.Outputs)
	}
	generatedPath := bundle.Outputs[0].Path
	if !strings.HasPrefix(generatedPath, pythonprovider.InstallRoot+"/") || strings.Contains(generatedPath, storeRoot) {
		t.Fatalf("Playwright generated target = %q contains an invalid or host staging prefix", generatedPath)
	}
	materializationScript, err := store.OpenVerifiedArtifact(bundle.Script)
	if err != nil {
		t.Fatal(err)
	}
	scriptBytes, readErr := io.ReadAll(materializationScript)
	closeErr := materializationScript.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read materialization script: read=%v close=%v", readErr, closeErr)
	}
	script := strings.ToLower(string(scriptBytes))
	declared := fresh.Plan.Tools[0].Exports[0]
	contract := fresh.Closures[0].Records.BindingContracts[0].Record
	forbidden := []string{
		"playwright", "node", "nodejs", "playwright-core", "package",
		"npm", "npx", "install-deps",
		strings.ToLower(binding.Distribution), strings.ToLower(binding.CLI.Name),
		strings.ToLower(contract.Package), strings.ToLower(contract.CLI.Name),
	}
	for _, bundled := range contract.BundledComponents {
		forbidden = append(forbidden, strings.ToLower(bundled.Name))
	}
	for _, name := range forbidden {
		if name != "" && strings.Contains(script, name) {
			t.Fatalf("materialization script contains forbidden Playwright dispatch name %q", name)
		}
	}
	if declared.Name != binding.CLI.Name || declared.Path != binding.CLI.Path {
		t.Fatalf("selected Playwright export = %#v, binding CLI = %#v", declared, binding.CLI)
	}
	catalogCount := 0
	for _, output := range result.Catalog {
		if output.SupplierComponent != component.Component || output.Name != declared.Name {
			continue
		}
		catalogCount++
		if output.Candidate.InvocationPath != generatedPath {
			t.Fatalf("Playwright generated catalog path = %q, want %q", output.Candidate.InvocationPath, generatedPath)
		}
	}
	if catalogCount != 1 {
		t.Fatalf("Playwright generated catalog output count = %d, want exactly one", catalogCount)
	}
	dispatchMarker := runDockerIntegration(t, ctx, "run", "--rm", "--platform", platform.Canonical, "--entrypoint", "/bin/sh", string(image), "-c", "if test -e \"$1\"; then printf '%s\\n' 'unexpected synthetic Playwright dispatch'; exit 1; fi; printf '%s\\n' 'no synthetic Playwright dispatch'", "reploy-dispatch-probe", "/tmp/reploy-ptd2337-playwright-dispatch")
	if strings.TrimSpace(dispatchMarker) != "no synthetic Playwright dispatch" {
		t.Fatalf("Playwright build-time dispatch marker = %q", dispatchMarker)
	}
	resolvedTarget := runDockerIntegration(t, ctx, "run", "--rm", "--platform", platform.Canonical, "--entrypoint", "/bin/sh", string(image), "-c", "readlink \"$1\"", "reploy-alias-probe", declared.Path)
	if strings.TrimSpace(resolvedTarget) != generatedPath {
		t.Fatalf("declared Playwright export %q resolves to %q, want generated script %q", declared.Path, resolvedTarget, generatedPath)
	}
	output := runDockerIntegration(t, ctx, "run", "--rm", "--platform", platform.Canonical, "-e", "REPLOY_PTD2337_RUNTIME_ALIAS=1", "--entrypoint", declared.Path, string(image))
	if strings.TrimSpace(output) != "playwright synthetic cli" {
		t.Fatalf("Playwright alias output = %q", output)
	}
	unexpectedAlias := runDockerIntegration(t, ctx, "run", "--rm", "--platform", platform.Canonical, "--entrypoint", "/bin/sh", string(image), "-c", "set +e; output=$(\"$1\" install 2>&1); status=$?; test \"$status\" -ne 0; printf '%s\\n' \"$output\"", "reploy-installer-probe", declared.Path)
	if !strings.Contains(strings.ToLower(unexpectedAlias), "unexpected synthetic playwright arguments") {
		t.Fatalf("Playwright installer dispatch sentinel = %q", unexpectedAlias)
	}
	unexpectedModule := runDockerIntegration(t, ctx, "run", "--rm", "--platform", platform.Canonical, "--entrypoint", "/bin/sh", string(image), "-c", "set +e; output=$(\"$1\" -m playwright 2>&1); status=$?; test \"$status\" -ne 0; printf '%s\\n' \"$output\"", "reploy-module-probe", "/usr/local/bin/python3")
	if !strings.Contains(strings.ToLower(unexpectedModule), "unexpected synthetic playwright module dispatch") {
		t.Fatalf("Playwright module dispatch sentinel = %q", unexpectedModule)
	}
}

// ptd2337SyntheticDependencyOverridesV1 supplies local source projects for
// Playwright's two direct dependencies and pyee's typing-extensions
// transitive dependency. The resolver may use its controlled index during the
// first pass to discover transitive requirements, then substitutes the local
// source project on a subsequent pass. The projected request stays intact.
func ptd2337SyntheticDependencyOverridesV1(t *testing.T) []PythonLocalOverrideV1 {
	t.Helper()
	overrides := make([]PythonLocalOverrideV1, 0, 3)
	for _, dependency := range []struct {
		distribution string
		version      string
	}{
		{distribution: "greenlet", version: "3.1.1"},
		{distribution: "pyee", version: "13.0.0"},
		{distribution: "typing-extensions", version: "4.12.2"},
	} {
		directory := t.TempDir()
		installRequires := ""
		if dependency.distribution == "pyee" {
			installRequires = ", install_requires=['typing-extensions']"
		}
		setup := "from setuptools import setup\nsetup(name=" + strconv.Quote(dependency.distribution) + ", version=" + strconv.Quote(dependency.version) + installRequires + ")\n"
		if err := os.WriteFile(filepath.Join(directory, "setup.py"), []byte(setup), 0o644); err != nil {
			t.Fatal(err)
		}
		overrides = append(overrides, PythonLocalOverrideV1{
			Distribution: dependency.distribution,
			HostDir:      directory,
		})
	}
	return overrides
}

func ptd2337SyntheticPlaywrightWheelV1(t *testing.T) []byte {
	t.Helper()
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	files := map[string]string{
		"playwright/__init__.py": "",
		"playwright/__main__.py": "import os\nimport sys\n\n_DISPATCH_MARKER = '/tmp/reploy-ptd2337-playwright-dispatch'\n\ndef _record_dispatch():\n    if os.environ.get('REPLOY_PTD2337_RUNTIME_ALIAS') != '1':\n        open(_DISPATCH_MARKER, 'a').close()\n\ndef main():\n    _record_dispatch()\n    if len(sys.argv) != 1:\n        raise SystemExit('unexpected synthetic Playwright arguments')\n    print('playwright synthetic cli')\n\nif __name__ == '__main__':\n    _record_dispatch()\n    raise SystemExit('unexpected synthetic Playwright module dispatch')\n",
		// These members satisfy both bundled components declared by the
		// embedded Playwright contract. The integration path must authenticate
		// their presence without invoking either upstream browser installer.
		"playwright/driver/node":                 "synthetic node payload\n",
		"playwright/driver/package/":             "",
		"playwright/driver/package/LICENSE":      "synthetic Playwright core license\n",
		"playwright/driver/package/package.json": "{\"name\":\"playwright-core\",\"version\":\"1.61.1-beta-1782139630000\"}\n",
		"playwright-1.61.0.dist-info/METADATA":   "Metadata-Version: 2.1\nName: playwright\nVersion: 1.61.0\nRequires-Python: >=3.10\n\n",
		// The catalog wheel keeps a manylinux filename but declares a generic
		// pure-Python internal tag. Preserve that distinction so this path also
		// exercises the generic purelib tag allowance.
		"playwright-1.61.0.dist-info/WHEEL":            "Wheel-Version: 1.0\nGenerator: reploy-integration\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		"playwright-1.61.0.dist-info/entry_points.txt": "[console_scripts]\nplaywright = playwright.__main__:main\n",
		"playwright-1.61.0.dist-info/RECORD":           "",
	}
	for name, body := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

func ptd2337PortableToolClosureIdentityV1(
	t *testing.T,
	closure toolcatalog.SelectedClosureV1,
) canonical.Digest {
	t.Helper()
	seen := map[string]struct{}{}
	references := make([]toolcatalog.RecordReferenceV1, 0,
		len(closure.Records.BindingContracts)+len(closure.Records.BindingArtifacts)+
			len(closure.Records.Payloads)+len(closure.Records.PackageSets),
	)
	add := func(reference toolcatalog.RecordReferenceV1) {
		key := reference.ID + "\x00" + string(reference.Digest)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		references = append(references, reference)
	}
	for _, record := range closure.Records.BindingContracts {
		add(record.Reference)
	}
	for _, record := range closure.Records.BindingArtifacts {
		add(record.Reference)
	}
	for _, record := range closure.Records.Payloads {
		add(record.Reference)
	}
	for _, record := range closure.Records.PackageSets {
		add(record.Reference)
	}
	sort.Slice(references, func(left, right int) bool {
		if references[left].ID != references[right].ID {
			return references[left].ID < references[right].ID
		}
		return references[left].Digest < references[right].Digest
	})
	identityInput := struct {
		Tool     string                                   `json:"tool"`
		Version  string                                   `json:"version"`
		Contract toolcatalog.SelectedContractProjectionV1 `json:"contract"`
		Target   toolcatalog.SelectedTargetProjectionV1   `json:"target"`
		Records  []toolcatalog.RecordReferenceV1          `json:"records"`
	}{
		Tool: closure.Provenance.Tool, Version: closure.Provenance.Version,
		Contract: closure.Contract, Target: closure.Target, Records: references,
	}
	digest, err := canonical.Sum("portable-tool-selected-closure", toolcatalog.SelectedClosureIdentityV1, identityInput)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
