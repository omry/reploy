package dockerdeploy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPreparedPythonNodeOperationsResolvesAndIngestsWheelsInSession(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	request := preparedPythonResolveRequest(t, descriptor)
	requirement, err := pythonprovider.CanonicalPackageRequestV1("demo-server==1.0")
	if err != nil {
		t.Fatal(err)
	}
	requestWithUnused, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component:    "application",
		Interpreter:  blueprint.CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
		Overrides: []pythonprovider.PythonPackageOverrideV1{
			{Distribution: "demo-server", Kind: "local"},
			{Distribution: "unused", Kind: "local"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request.Plan.Nodes = append([]providers.NodeSpec{}, request.Plan.Nodes...)
	for index := range request.Plan.Nodes {
		if request.Plan.Nodes[index].ID != request.NodeID {
			continue
		}
		request.Plan.Nodes[index].Request = requestWithUnused
		request.Plan.Nodes[index].Requirements.ProviderData = providers.CanonicalProviderData{
			Schema: requestWithUnused.Schema, Value: requestWithUnused.Value,
		}
	}
	interpreterObservation := pythonConsumerObservation("interpreter", "/usr/bin/python3")
	interpreterResponse := probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{interpreterObservation}}
	artifacts := testPreparedPythonResolverArtifacts(t)
	commands := stubPythonInterpreterSelectionCommands(t, mustCanonicalProbeResponse(t, interpreterResponse), []string{"3.13.2\n"}, func() error {
		writePythonIntegrationWheel(t, filepath.Join(artifacts.OutputHostDir, "demo_server-1.0-py3-none-any.whl"))
		return nil
	})
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	session.observations[pythonCarrierRequirementID] = pythonConsumerObservation(pythonCarrierRequirementID, pythonCarrierPath)
	session.observations[pythonLauncherRequirementID] = pythonConsumerObservation(pythonLauncherRequirementID, pythonLauncherPath)
	storeRoot := t.TempDir()
	store, err := providerstore.NewStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	operations := PreparedPythonNodeOperations{
		Store: store,
		Validators: providers.ProviderOwnerValidators{
			Profile: pythonprovider.ValidateRequirementProfileV1,
			Bundle:  pythonprovider.ValidateResolvedBundlePayloadV1,
		},
		FinalImageConfig: pythonConsumerTestImageConfig(),
		Artifacts:        artifacts,
		LocalOverrides: []PythonLocalOverrideV1{{
			Distribution: "unused", HostDir: filepath.Join(t.TempDir(), "missing"),
		}},
		verifiedArtifacts: map[canonical.Digest]string{rendererDigest("f"): "/stale/cache/path"},
	}
	resolution, _, err := operations.resolveFresh(context.Background(), session, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Bundle.Payload.Artifacts) != 2 {
		t.Fatalf("resolution artifacts = %#v", resolution.Bundle.Payload.Artifacts)
	}
	for _, artifact := range resolution.Bundle.Payload.Artifacts {
		if artifact.Kind == "wheel" {
			storePath, pathErr := store.BlobPath(artifact.SHA256)
			if pathErr != nil {
				t.Fatal(pathErr)
			}
			if operations.verifiedArtifacts[artifact.SHA256] != storePath {
				t.Fatalf("verified wheel paths = %#v", operations.verifiedArtifacts)
			}
		}
	}
	if _, found := operations.verifiedArtifacts[rendererDigest("f")]; found {
		t.Fatalf("fresh resolution retained stale verified path: %#v", operations.verifiedArtifacts)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*commands) != 7 {
		t.Fatalf("commands = %#v", *commands)
	}
	if !containsInOrder((*commands)[4].Args, []string{
		"/usr/bin/env", "-i", "HOME=/tmp", "LANG=C", "LC_ALL=C",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TMPDIR=/tmp",
		"/usr/bin/python3", "-m", "pip", "--disable-pip-version-check", "wheel",
	}) {
		t.Fatalf("wheel resolver command = %#v", (*commands)[4].Args)
	}
	if !containsInOrder((*commands)[4].Args, []string{
		"--find-links", pythonResolverInputContainerDir,
		"--wheel-dir", pythonResolverOutputContainerDir,
		"demo-server==1.0",
	}) {
		t.Fatalf("wheel resolver inputs = %#v", (*commands)[4].Args)
	}
	if !reflect.DeepEqual((*commands)[5].Args, []string{"kill", "--signal", "KILL", session.containerName}) || (*commands)[6].Args[0] != "rm" {
		t.Fatalf("resolver shutdown commands = %#v", (*commands)[5:])
	}
}

func TestPreparedPythonNodeOperationsExcludesCorruptReusableWheelBeforePip(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	request := preparedPythonResolveRequest(t, descriptor)
	interpreterObservation := pythonConsumerObservation("interpreter", "/usr/bin/python3")
	interpreterResponse := probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{interpreterObservation}}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wheel, err := store.Publish(context.Background(), "wheels/reusable-1-py3-none-any.whl", "wheel", strings.NewReader("expected"))
	if err != nil {
		t.Fatal(err)
	}
	artifacts, cleanup, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{wheel})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	commands := stubPythonInterpreterSelectionCommands(t, mustCanonicalProbeResponse(t, interpreterResponse), []string{"3.13.2\n"}, func() error {
		writePythonIntegrationWheel(t, filepath.Join(artifacts.OutputHostDir, "demo_server-1.0-py3-none-any.whl"))
		return nil
	})
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	session.observations[pythonCarrierRequirementID] = pythonConsumerObservation(pythonCarrierRequirementID, pythonCarrierPath)
	session.observations[pythonLauncherRequirementID] = pythonConsumerObservation(pythonLauncherRequirementID, pythonLauncherPath)
	path := mustBlobPath(t, store, wheel)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("altered!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	reference, err := wheel.StoreObjectRef()
	if err != nil {
		t.Fatal(err)
	}
	request.ReusableArtifacts = []providerstore.StoreObjectRef{reference}
	operations := PreparedPythonNodeOperations{
		Store: store, ReusableWheels: []providerstore.ArtifactDescriptor{wheel}, Artifacts: artifacts,
		LocalOverrides: []PythonLocalOverrideV1{},
		Validators: providers.ProviderOwnerValidators{
			Profile: pythonprovider.ValidateRequirementProfileV1, Bundle: pythonprovider.ValidateResolvedBundlePayloadV1,
		},
		FinalImageConfig: pythonConsumerTestImageConfig(),
	}
	if _, _, err = operations.resolveFresh(context.Background(), session, request); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(artifacts.InputHostDir, filepath.Base(wheel.LogicalPath))); !os.IsNotExist(err) {
		t.Fatalf("corrupt reusable wheel remained visible to pip: %v", err)
	}
	pipCalls := 0
	for _, command := range *commands {
		if containsInOrder(command.Args, []string{"--wheel-dir", pythonResolverOutputContainerDir}) {
			pipCalls++
		}
	}
	if pipCalls != 1 {
		t.Fatalf("pip calls = %d, commands = %#v", pipCalls, *commands)
	}
}

func TestPreparedPythonNodeOperationsBuildsOnlySelectedLocalSource(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	request := preparedPythonResolveRequest(t, descriptor)
	interpreterObservation := pythonConsumerObservation("interpreter", "/usr/bin/python3")
	interpreterResponse := probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: []probe.ExecutableObservationV1{interpreterObservation}}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts, cleanup, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	localSources := sourceWheelTestLocalSources(t, "demo-server")
	if err := os.WriteFile(
		filepath.Join(localSources[0].HostDir, LocalSourceRecipeFilename),
		[]byte("schema: 1\nproject: demo-server\ntype: python\nbuild: pep517\nrequires: []\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	localSources[0].Manifest, localSources[0].SourceInputDigest, err = ObservePythonSourceManifest(localSources[0].HostDir)
	if err != nil {
		t.Fatal(err)
	}
	workCalls := 0
	commands := stubPythonInterpreterSelectionCommands(t, mustCanonicalProbeResponse(t, interpreterResponse), []string{"3.13.2\n"}, func() error {
		workCalls++
		if workCalls == 1 {
			writeDockerdeployTestSourceDistribution(
				t, filepath.Join(artifacts.OutputHostDir, "demo_server-1.0.tar.gz"),
				"demo_server-1.0", "demo-server", "1.0",
			)
			return nil
		}
		output := filepath.Join(artifacts.OutputHostDir, "demo_server-1.0-py3-none-any.whl")
		if workCalls == 2 {
			writePythonIntegrationWheel(t, output)
			return nil
		}
		input := filepath.Join(artifacts.InputHostDir, "demo_server-1.0-py3-none-any.whl")
		content, err := os.ReadFile(input)
		if err != nil {
			return err
		}
		return os.WriteFile(output, content, 0o600)
	})
	session, err := OpenPythonResolverSession(context.Background(), descriptor, workspace, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	session.observations[pythonCarrierRequirementID] = pythonConsumerObservation(pythonCarrierRequirementID, pythonCarrierPath)
	session.observations[pythonLauncherRequirementID] = pythonConsumerObservation(pythonLauncherRequirementID, pythonLauncherPath)
	var progress strings.Builder
	operations := PreparedPythonNodeOperations{
		Store: store, Artifacts: artifacts,
		LocalOverrides: []PythonLocalOverrideV1{
			{Distribution: "demo-server", HostDir: localSources[0].HostDir},
			{Distribution: "unused", HostDir: filepath.Join(t.TempDir(), "missing")},
		},
		Validators: providers.ProviderOwnerValidators{
			Profile: pythonprovider.ValidateRequirementProfileV1, Bundle: pythonprovider.ValidateResolvedBundlePayloadV1,
		},
		FinalImageConfig: pythonConsumerTestImageConfig(),
		Progress:         &progress,
	}
	resolution, _, err := operations.resolveFresh(context.Background(), session, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if workCalls != 3 {
		t.Fatalf("sdist, wheel, and resolver calls = %d, commands = %#v", workCalls, *commands)
	}
	pipCalls := 0
	for _, command := range *commands {
		if containsInOrder(command.Args, []string{"--wheel-dir", pythonResolverOutputContainerDir}) {
			pipCalls++
		}
	}
	if pipCalls != 1 {
		t.Fatalf("pip calls = %d, commands = %#v", pipCalls, *commands)
	}
	if len(resolution.SelectedSources) != 1 || resolution.SelectedSources[0].LogicalPackage != "demo-server" ||
		resolution.SelectedSources[0].SourceInputDigest != localSources[0].SourceInputDigest {
		t.Fatalf("selected sources = %#v", resolution.SelectedSources)
	}
	if resolution.SelectedSources[0].BuilderProfile != pythonprovider.SourceBuilderProfileV2 ||
		resolution.SelectedSources[0].BuildSettings.Value["build_type"] != pythonprovider.SourceBuildTypePEP517 {
		t.Fatalf("selected source recipe identity = %#v", resolution.SelectedSources[0])
	}
	if len(resolution.Bundle.Payload.SelectedSources) != 1 ||
		resolution.Bundle.Payload.SelectedSources[0].OutputArtifactDigest != resolution.SelectedSources[0].OutputArtifactDigest {
		t.Fatalf("bundle selected sources = %#v", resolution.Bundle.Payload.SelectedSources)
	}
	if overrides := resolution.Bundle.Payload.Request.Value["overrides"].([]any); len(overrides) != 1 {
		t.Fatalf("closure-relevant bundle overrides = %#v", overrides)
	}
	for _, want := range []string{
		"observing local Python sources demo-server for component application",
		"building local Python source artifacts demo-server for component application",
	} {
		if !strings.Contains(progress.String(), want) {
			t.Fatalf("progress missing %q:\n%s", want, progress.String())
		}
	}
}

func TestPreparedPythonNodeOperationsRequestsToolsFromSelectedRecipeBeforeBuild(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifacts, cleanup, err := PreparePythonResolverArtifacts(store, []providerstore.ArtifactDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	sourceDir := t.TempDir()
	for name, content := range map[string]string{
		"setup.py":                "from setuptools import setup\n",
		"pyproject.toml":          "[tool.ruff]\n",
		LocalSourceRecipeFilename: "schema: 1\nproject: omegaconf\ntype: python\nbuild: setuptools-legacy\nrequires: [tool:java]\n",
	} {
		if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	session := &PythonResolverSession{artifacts: artifacts, buildTools: []PortableBuildToolEvidenceV1{}}
	operations := PreparedPythonNodeOperations{Store: store, Artifacts: artifacts}
	_, _, err = operations.materializeLocalOverrides(
		context.Background(), session,
		providers.ValidatedExecutableInput{}, providers.ExecutableRequirement{},
		providers.ExecutableEvidence{}, rendererDigest("a"), "application",
		[]PythonLocalOverrideV1{{Distribution: "omegaconf", HostDir: sourceDir}},
		[]providers.ResolvedSourceInput{}, []providerstore.ArtifactDescriptor{},
	)
	var required *pythonBuildToolsRequiredError
	if !errors.As(err, &required) || !reflect.DeepEqual(required.Tools, []string{"java"}) {
		t.Fatalf("tool requirement error = %v", err)
	}
	entries, readErr := os.ReadDir(artifacts.OutputHostDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("project build ran before Java was prepared: %#v", entries)
	}
}

func writeDockerdeployTestSourceDistribution(
	t *testing.T,
	filename string,
	root string,
	distribution string,
	version string,
) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	entries := []struct {
		name    string
		kind    byte
		content string
	}{
		{name: root + "/", kind: tar.TypeDir},
		{name: root + "/pyproject.toml", kind: tar.TypeReg, content: "[build-system]\n"},
		{name: root + "/PKG-INFO", kind: tar.TypeReg, content: "Name: " + distribution + "\nVersion: " + version + "\n\n"},
		{name: root + "/demo_server.py", kind: tar.TypeReg, content: "def main():\n    pass\n"},
	}
	for _, entry := range entries {
		mode := int64(0o644)
		size := int64(len(entry.content))
		if entry.kind == tar.TypeDir {
			mode = 0o755
			size = 0
		}
		if err := archive.WriteHeader(&tar.Header{
			Name: entry.name, Typeflag: entry.kind, Mode: mode, Size: size, Format: tar.FormatPAX,
		}); err != nil {
			t.Fatal(err)
		}
		if entry.kind == tar.TypeReg {
			if _, err := archive.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMergePythonSourceCandidatesReplacesMatchingCurrentCandidate(t *testing.T) {
	existing := []providers.ResolvedSourceInput{
		testPythonResolvedSource("application", "demo", "1.0", reuseTestDigest("1"), reuseTestDigest("2")),
		testPythonResolvedSource("other", "kept", "1.0", reuseTestDigest("3"), reuseTestDigest("4")),
	}
	built := []providers.ResolvedSourceInput{
		testPythonResolvedSource("application", "demo", "2.0", reuseTestDigest("5"), reuseTestDigest("6")),
	}
	merged, err := mergePythonSourceCandidates(existing, built)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 || !reflect.DeepEqual(merged[0], built[0]) || !reflect.DeepEqual(merged[1], existing[1]) {
		t.Fatalf("merged sources = %#v", merged)
	}
}

func preparedPythonResolveRequest(t *testing.T, descriptor deploy.ImageDescriptor) providers.ResolveNodeRequest {
	t.Helper()
	packageRequest, err := pythonprovider.CanonicalPackageRequestV1("demo-server==1.0")
	if err != nil {
		t.Fatal(err)
	}
	pythonRequest, err := pythonprovider.CanonicalProviderRequestV1(pythonprovider.PythonProviderRequestV1{
		Component:    "application",
		Interpreter:  blueprint.CommandRequirement{Command: "python", Version: ">=3.11", Supplier: "base"},
		Requirements: []providers.CanonicalPackageRequest{packageRequest},
		Overrides:    []pythonprovider.PythonPackageOverrideV1{{Distribution: "demo-server", Kind: "local"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pythonNodes, err := (pythonprovider.ComponentProvider{}).Plan(providers.PlanInput{
		Components: []providers.ResolvedComponentRequestV1{{
			Component: "application", Provider: blueprint.ComponentTypePython, Request: pythonRequest,
		}},
		Platform: descriptor.Platform,
	})
	if err != nil {
		t.Fatal(err)
	}
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: "debian:bookworm-slim",
		Exports: map[string]blueprint.BaseExecutableExport{
			"python": {Executable: "/usr/bin/python3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := providers.BaseNodeSpec(baseRequest)
	if err != nil {
		t.Fatal(err)
	}
	plan := providers.ProviderPlanV1{
		Schema: providers.ProviderPlanSchemaV1,
		Nodes:  []providers.NodeSpec{base, pythonNodes[0]},
		Edges: []providers.ProviderEdgeV1{{
			Supplier: "base", Consumer: pythonNodes[0].ID, RequirementID: "interpreter",
			Output: providers.QualifiedOutput{Component: "base", Name: "python"},
		}},
	}
	if err := providers.ValidateProviderPlanV1(plan); err != nil {
		t.Fatal(err)
	}
	provenance := base.OutputDeclarations[0].Provenance
	baseObservation := pythonConsumerObservation("python", "/usr/bin/python3")
	baseEvidence, err := ExecutableEvidenceFromProbe(baseObservation, ProbeExecutableBinding{
		Output: providers.QualifiedOutput{Component: "base", Name: "python"}, Facts: provenance,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := pythonNodePreparationRequest(t, descriptor)
	request.Plan = plan
	request.NodeID = pythonNodes[0].ID
	request.EarlierCatalog = []providers.RealizedOutput{{
		SupplierNode: "base", SupplierComponent: "base", Name: "python",
		Candidate: providers.ExecutableCandidate{InvocationPath: "/usr/bin/python3", Provenance: provenance},
		Evidence:  baseEvidence,
	}}
	request.SourceCandidates = []providers.ResolvedSourceInput{}
	request.ReusableArtifacts = []providerstore.StoreObjectRef{}
	return request
}
