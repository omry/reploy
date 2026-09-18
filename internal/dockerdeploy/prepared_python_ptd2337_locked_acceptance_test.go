package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

// TestPreparedPythonGraphPTD2337LockedReplayAcceptance drives the real
// prepared graph executor and Python node preparer through one locked replay.
// The Docker layer build is represented by the materialization acceptance seam
// because this test must run in the ordinary package test suite; the provider
// registry transaction and alias claims remain production code.
func TestPreparedPythonGraphPTD2337LockedReplayAcceptance(t *testing.T) {
	reuse := newPreparedPythonGraphReuseFixture(t)
	locked := newPortableToolPythonLockedTestFixture(t)
	packedProbe := packedProbeExecutable(t)
	previousLocateProbeArchive := locateProbeArchiveExecutable
	locateProbeArchiveExecutable = func() (string, error) { return packedProbe, nil }
	t.Cleanup(func() { locateProbeArchiveExecutable = previousLocateProbeArchive })
	lockedContent, err := os.ReadFile(mustBlobPath(t, locked.store, locked.descriptor))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reuse.store.PublishExpected(context.Background(), locked.descriptor, bytes.NewReader(lockedContent)); err != nil {
		t.Fatal(err)
	}
	for _, node := range reuse.request.Plan.Nodes {
		if node.ID != "base" {
			continue
		}
		reuse.lock.Base.AuthorReference, _ = node.Request.Value["image"].(string)
		reuse.lock.BasePlanDigest, err = providers.ProviderNodePlanDigest(node)
		if err != nil {
			t.Fatal(err)
		}
	}
	reuse.lock.PortableTools = &locked.lock

	previousPrepare := preparePythonGraphExecutionBackend
	previousExecute := executePreparedPythonProviderGraph
	previousAliasAcceptance := buildAndAcceptProviderGraphAliasLayer
	t.Cleanup(func() {
		preparePythonGraphExecutionBackend = previousPrepare
		executePreparedPythonProviderGraph = previousExecute
		buildAndAcceptProviderGraphAliasLayer = previousAliasAcceptance
	})

	var configs map[providers.NodeID]PreparedPythonNodeConfig
	var resolverInputDir string
	var resolverOutputDir string
	preparePythonGraphExecutionBackend = func(
		ctx context.Context,
		store providerstore.Store,
		plan providers.ProviderPlanV1,
		base deploy.ImageDescriptor,
		finalImageConfig providers.ImageConfigPolicy,
		value map[providers.NodeID]PreparedPythonNodeConfig,
		apt map[providers.NodeID]PreparedAPTNodeConfig,
		options RunOptions,
	) (PreparedPythonGraphBackend, func() error, error) {
		configs = value
		backend, cleanup, err := PreparePreparedPythonGraphBackend(
			ctx, store, plan, base, finalImageConfig, value, apt, options,
		)
		if err != nil {
			return backend, cleanup, err
		}
		for id := range value {
			operation, found := backend.Operations[id]
			if !found {
				return backend, cleanup, fmt.Errorf("prepared Python backend omitted node %q", id)
			}
			resolverInputDir = operation.Artifacts.InputHostDir
			resolverOutputDir = operation.Artifacts.OutputHostDir
		}
		return backend, cleanup, nil
	}

	var transaction providers.MaterializationTransaction
	var materializerCalls int
	var finalizerCalls int
	buildAndAcceptProviderGraphAliasLayer = func(
		ctx context.Context, store providerstore.Store, got providers.MaterializationTransaction,
		bundle providers.ResolvedBundle, platform blueprint.Platform, _ MaterializationEvidenceRunner,
		_ map[canonical.Digest]string, options RunOptions, _ materializationLayerBuilder,
		_ materializationLayerInspector, _ materializationCandidateRetainer,
		_ materializationCandidateRemover, finalize acceptedMaterializationLayerFinalizer,
	) (providers.GraphNodeMaterializeResult, error) {
		materializerCalls++
		transaction = got
		if finalize == nil {
			t.Fatal("portable binding materializer did not provide alias finalizer")
		}
		accepted := acceptedMaterializationCandidate(t, got, platform)
		base := BuiltImageCandidate{ImageID: accepted.Image.Image.ConfigDigest}
		previousBuild := buildPortablePythonAliasLayerV1
		previousInspect := inspectPortablePythonAliasLayerV1
		previousProbe := collectPortablePythonAliasEvidenceV1
		previousDestinations := inspectPortablePythonAliasDestinationsV1
		t.Cleanup(func() {
			buildPortablePythonAliasLayerV1 = previousBuild
			inspectPortablePythonAliasLayerV1 = previousInspect
			collectPortablePythonAliasEvidenceV1 = previousProbe
			inspectPortablePythonAliasDestinationsV1 = previousDestinations
		})
		aliasID := canonical.Digest("sha256:" + strings.Repeat("e", 64))
		aliasImage := accepted.Image
		aliasImage.Descriptor.ConfigDigest, aliasImage.Descriptor.ImmutableReference = aliasID, string(aliasID)
		aliasImage.Image, _ = realizedImageFromDescriptor(aliasImage.Descriptor)
		buildPortablePythonAliasLayerV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, string, []byte, RunOptions) (BuiltImageCandidate, error) {
			return BuiltImageCandidate{ImageID: aliasID}, nil
		}
		inspectPortablePythonAliasLayerV1 = func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
			return aliasImage, nil
		}
		collectPortablePythonAliasEvidenceV1 = func(_ context.Context, _ providerstore.Store, _ deploy.ImageDescriptor, checks []FullImageExecutableProbe) ([]providers.ExecutableEvidence, error) {
			path := checks[0].InvocationPath
			planned, planErr := planPortablePythonAliasesV1(&locked.component, got)
			if planErr != nil {
				return nil, planErr
			}
			target := planned[0].Target
			return []providers.ExecutableEvidence{{InvocationPath: path, LinkChain: []providers.LinkEvidence{{Path: path, Target: target, ResolvedPath: target}}, Terminal: providers.FileEvidence{Path: target}}}, nil
		}
		inspectPortablePythonAliasDestinationsV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) ([]string, error) {
			return nil, nil
		}
		finalizer := func(finalizeCtx context.Context, source InspectedImageCandidate, acceptedResult providers.GraphNodeMaterializeResult, finalizeOptions RunOptions) (BuiltImageCandidate, InspectedImageCandidate, error) {
			finalizerCalls++
			return finalize(finalizeCtx, source, acceptedResult, finalizeOptions)
		}
		generated := make([]providers.RealizedGeneratedExecutable, 0, len(got.GeneratedExecutables))
		for _, declaration := range got.GeneratedExecutables {
			generated = append(generated, acceptedGeneratedExecutableForDeclaration(declaration))
		}
		result, err := buildAndAcceptMaterializationLayerWithFinalizer(
			ctx, store, got, bundle, platform,
			func(context.Context, MaterializationEvidenceInput) ([]providers.RealizedGeneratedExecutable, []providers.RealizedOutput, error) {
				return generated, ptd2337RealizedOutputs(bundle), nil
			}, nil, options,
			func(providerstore.Store, MaterializationLayerRequest, RunOptions) (MaterializationLayerCandidate, error) {
				key, digest, err := MaterializationAssemblyKey(got, platform)
				return MaterializationLayerCandidate{Built: base, AssemblyKey: key, AssemblyKeyDigest: digest}, err
			},
			func(context.Context, MaterializationLayerCandidate, MaterializationLayerRequest) (InspectedMaterializationLayerCandidate, error) {
				return accepted, nil
			},
			func(context.Context, BuiltImageCandidate, providers.RealizedImageV1) error { return nil },
			func(context.Context, BuiltImageCandidate) error { return nil },
			func(finalizeCtx context.Context, source InspectedImageCandidate, acceptedResult providers.GraphNodeMaterializeResult, finalizeOptions RunOptions) (BuiltImageCandidate, InspectedImageCandidate, error) {
				return finalizer(finalizeCtx, source, acceptedResult, finalizeOptions)
			},
		)
		if err != nil {
			return providers.GraphNodeMaterializeResult{}, err
		}
		if finalizerCalls != 1 {
			t.Fatalf("portable binding finalizer calls = %d", finalizerCalls)
		}
		return result, nil
	}
	executePreparedPythonProviderGraph = func(ctx context.Context, request providers.GraphExecutionRequest) (providers.GraphExecutionResult, error) {
		return providers.ExecuteProviderGraph(ctx, request)
	}

	stubPTD2337LockedReplayResolver(t, &resolverInputDir, &resolverOutputDir, locked.component.TestedTags)
	base := reuse.lock.Base
	result, err := ExecutePreparedPythonGraph(context.Background(), PreparedPythonGraphExecutionInput{
		Store: reuse.store, Plan: reuse.request.Plan, BaseDescriptor: base,
		BaseCatalog: reuse.request.EarlierCatalog, Sources: reuse.request.SourceCandidates,
		SourceWheels: reuse.sourceWheels, CurrentLock: &reuse.lock,
		DesiredPortableToolPlan: &locked.lock.Plan.PortableToolPlan,
		FinalImageConfig:        pythonConsumerTestImageConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if configs[reuse.request.NodeID].PortableToolLockedPlan == nil || configs[reuse.request.NodeID].PortableToolFreshPlan != nil {
		t.Fatalf("locked graph config = %#v", configs[reuse.request.NodeID])
	}
	if len(result.Materializations) != 1 || len(result.Bundles) != 1 {
		t.Fatalf("locked graph result = %#v", result)
	}
	bundle, err := pythonprovider.DecodeCanonicalBundleDataV1(
		locked.component.Component, result.Bundles[0].Payload.ProviderPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Wheels) != 2 || countLockedWheel(bundle.Wheels, locked.descriptor) != 1 {
		t.Fatalf("locked graph wheels = %#v", bundle.Wheels)
	}
	aliases, err := planPortablePythonAliasesV1(&locked.component, transaction)
	if err != nil {
		t.Fatal(err)
	}
	if materializerCalls != 1 || len(aliases) != len(locked.component.Bindings) || len(aliases) != 1 {
		t.Fatalf("locked graph aliases = %#v", aliases)
	}
	if transaction.RecipeVersion != pythonprovider.MaterializationRecipeVersion || len(transaction.GeneratedExecutables) < 2 {
		t.Fatalf("locked materialization transaction = %#v", transaction)
	}
	if aliases[0].Target != transaction.GeneratedExecutables[0].Path && aliases[0].Target != transaction.GeneratedExecutables[1].Path {
		t.Fatalf("locked alias target %q is absent from transaction declarations %#v", aliases[0].Target, transaction.GeneratedExecutables)
	}
	assertPTD2337LockedAliasPublication(t, reuse.store, aliases[0])
}

func acceptedGeneratedExecutableForDeclaration(declaration providers.GeneratedExecutableDeclaration) providers.RealizedGeneratedExecutable {
	return providers.RealizedGeneratedExecutable{
		Declaration: declaration,
		Evidence: providers.GeneratedExecutableEvidence{
			Schema: providers.GeneratedExecutableEvidenceSchemaV1, InvocationPath: declaration.Path,
			LinkChain: []providers.LinkEvidence{},
			Terminal:  providers.GeneratedFileEvidence{Path: declaration.Path, Kind: "regular", Mode: "0755", Size: "100", SHA256: canonical.Digest("sha256:" + strings.Repeat("9", 64))},
			Access:    providers.PortableAccessEvidence{Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1, Paths: []providers.AccessPathEvidence{{Path: declaration.Path, Kind: "regular", Mode: "0755", Required: "other-read-execute"}}},
			Facts:     providers.CanonicalProviderData{Schema: "python-generated-v1", Value: canonical.Object{}},
		},
	}
}

// assertPTD2337LockedAliasPublication calls the production alias publisher
// after the locked graph transaction. Docker build, image inspection, and the
// final-image probe are replaced independently, while archive creation,
// destination checks, claim normalization, and workspace cleanup remain real.
func assertPTD2337LockedAliasPublication(t *testing.T, store providerstore.Store, alias portablePythonAliasSpecV1) {
	t.Helper()
	source := portablePythonAliasLayerTestSource(t)
	imageID := canonical.Digest("sha256:" + strings.Repeat("d", 64))
	aliasDescriptor := source.Descriptor
	aliasDescriptor.ConfigDigest = imageID
	aliasDescriptor.ImmutableReference = string(imageID)
	aliasImage, err := realizedImageFromDescriptor(aliasDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	aliasSource := source
	aliasSource.Descriptor = aliasDescriptor
	aliasSource.Image = aliasImage
	previousBuild := buildPortablePythonAliasLayerV1
	previousInspect := inspectPortablePythonAliasLayerV1
	previousProbe := collectPortablePythonAliasEvidenceV1
	previousDestinations := inspectPortablePythonAliasDestinationsV1
	t.Cleanup(func() {
		buildPortablePythonAliasLayerV1 = previousBuild
		inspectPortablePythonAliasLayerV1 = previousInspect
		collectPortablePythonAliasEvidenceV1 = previousProbe
		inspectPortablePythonAliasDestinationsV1 = previousDestinations
	})
	inspectPortablePythonAliasDestinationsV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []string) ([]string, error) {
		return nil, nil
	}
	events := []string{}
	var contextDir string
	buildPortablePythonAliasLayerV1 = func(_ context.Context, _ providerstore.Store, upstream deploy.ImageDescriptor, gotContext string, dockerfile []byte, _ RunOptions) (BuiltImageCandidate, error) {
		events = append(events, "build")
		contextDir = gotContext
		if !reflect.DeepEqual(upstream, source.Descriptor) || !strings.Contains(string(dockerfile), "ADD --chown=0:0") {
			t.Fatalf("locked alias build inputs = %s, %s", upstream.ImmutableReference, dockerfile)
		}
		archiveFile, err := os.Open(filepath.Join(gotContext, portablePythonAliasArchiveV1))
		if err != nil {
			t.Fatal(err)
		}
		reader := tar.NewReader(archiveFile)
		header, err := reader.Next()
		_ = archiveFile.Close()
		if err != nil || header.Name != strings.TrimPrefix(alias.Destination, "/") || header.Linkname != alias.Target {
			t.Fatalf("locked alias archive header = %#v, %v", header, err)
		}
		return BuiltImageCandidate{ImageID: imageID}, nil
	}
	inspectPortablePythonAliasLayerV1 = func(_ context.Context, candidate BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
		events = append(events, "inspect")
		if candidate.ImageID != imageID || platform != source.Descriptor.Platform {
			t.Fatalf("locked alias inspection input = %#v, %s", candidate, platform.Canonical)
		}
		return aliasSource, nil
	}
	collectPortablePythonAliasEvidenceV1 = func(_ context.Context, _ providerstore.Store, descriptor deploy.ImageDescriptor, checks []FullImageExecutableProbe) ([]providers.ExecutableEvidence, error) {
		events = append(events, "probe")
		if !reflect.DeepEqual(descriptor, aliasSource.Descriptor) || len(checks) != 1 || checks[0].InvocationPath != alias.Destination {
			t.Fatalf("locked alias final probe input = %#v, %#v", descriptor, checks)
		}
		return []providers.ExecutableEvidence{{
			InvocationPath: alias.Destination,
			LinkChain:      []providers.LinkEvidence{{Path: alias.Destination, Target: alias.Target, ResolvedPath: alias.Target}},
			Terminal:       providers.FileEvidence{Path: alias.Target},
		}}, nil
	}
	candidate, inspected, err := buildAndValidatePortablePythonAliasLayerV1(
		context.Background(), store, source, []portablePythonAliasSpecV1{alias}, source.Descriptor.Platform, RunOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ImageID != imageID || !reflect.DeepEqual(inspected, aliasSource) || !reflect.DeepEqual(events, []string{"build", "inspect", "probe"}) {
		t.Fatalf("locked alias result candidate=%#v inspected=%#v events=%#v", candidate, inspected, events)
	}
	if _, err := os.Stat(filepath.Dir(contextDir)); !os.IsNotExist(err) {
		t.Fatalf("locked alias staging workspace still exists: %v", err)
	}

	// A final-image probe failure must leave no alias workspace behind. The
	// materialization acceptance layer owns candidate removal; this function
	// owns its private staging cleanup.
	collectPortablePythonAliasEvidenceV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor, []FullImageExecutableProbe) ([]providers.ExecutableEvidence, error) {
		events = append(events, "probe-failed")
		return nil, errors.New("locked alias final-image probe failed")
	}
	_, _, err = buildAndValidatePortablePythonAliasLayerV1(
		context.Background(), store, source, []portablePythonAliasSpecV1{alias}, source.Descriptor.Platform, RunOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "locked alias final-image probe failed") {
		t.Fatalf("locked alias probe failure = %v", err)
	}
	if _, statErr := os.Stat(filepath.Dir(contextDir)); !os.IsNotExist(statErr) {
		t.Fatalf("failed locked alias staging workspace still exists: %v", statErr)
	}
}

// TestPreparedPythonGraphPTD2337LockedDescriptorDriftStopsBeforePublication
// checks the locked descriptor boundary separately from graph execution. The
// staged resolver output and workspace are still cleaned after the rejection.
func TestPreparedPythonGraphPTD2337LockedDescriptorDriftStopsBeforePublication(t *testing.T) {
	fixture := newPortableToolPythonLockedTestFixture(t)
	contentPath, err := fixture.store.BlobPath(fixture.descriptor.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(contentPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contentPath, []byte("descriptor drift"), 0o600); err != nil {
		t.Fatal(err)
	}

	prepared, cleanup, err := PreparePythonResolverArtifacts(fixture.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected := pythonprovider.PortableToolVerifiedWheelInputV1{
		Descriptor: fixture.descriptor,
		Inspection: pythonprovider.WheelInspectionV1{
			Artifact: fixture.descriptor, Filename: filepath.Base(fixture.descriptor.LogicalPath),
			Distribution: fixture.binding.Distribution,
		},
	}
	if err := StagePythonPortableVerifiedWheels(prepared, fixture.store, nil, []pythonprovider.PortableToolVerifiedWheelInputV1{selected}); err == nil || !strings.Contains(err.Error(), "inspect selected Python resolver wheel") {
		cleanup()
		t.Fatalf("descriptor drift staging error = %v", err)
	}
	entries, err := os.ReadDir(prepared.InputHostDir)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if len(entries) != 0 {
		cleanup()
		t.Fatalf("descriptor drift left staged input = %#v", entries)
	}
	cleanup()
	if _, err := os.Lstat(prepared.HostDir); !os.IsNotExist(err) {
		t.Fatalf("descriptor drift workspace was not cleaned: %v", err)
	}
}

func countLockedWheel(wheels []pythonprovider.PythonWheelV1, want providerstore.ArtifactDescriptor) int {
	count := 0
	for _, wheel := range wheels {
		if wheel.Distribution == "playwright" && wheel.Artifact == want {
			count++
		}
	}
	return count
}

func ptd2337RealizedOutputs(bundle providers.ResolvedBundle) []providers.RealizedOutput {
	result := make([]providers.RealizedOutput, 0, len(bundle.Payload.Outputs))
	for _, resolved := range bundle.Payload.Outputs {
		output := providers.RealizedOutput{
			SupplierComponent: resolved.SupplierComponent,
			SupplierNode:      resolved.SupplierNode,
			Name:              resolved.Name,
			Candidate:         resolved.Candidate,
		}
		output.Evidence = providers.ExecutableEvidence{
			Schema:         providers.ExecutableEvidenceSchemaV1,
			Output:         providers.QualifiedOutput{Component: output.SupplierComponent, Name: output.Name},
			InvocationPath: output.Candidate.InvocationPath,
			LinkChain:      []providers.LinkEvidence{},
			Terminal: providers.FileEvidence{
				Schema: providers.FileEvidenceSchemaV1, Path: output.Candidate.InvocationPath,
				Kind: "regular", Mode: "0755", Size: "1",
				SHA256: canonical.Digest("sha256:" + strings.Repeat("a", 64)),
			},
			Access: providers.PortableAccessEvidence{
				Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1,
				Paths: []providers.AccessPathEvidence{{
					Path: output.Candidate.InvocationPath, Kind: "regular", Mode: "0755", Required: "other-read-execute",
				}},
			},
			Facts: providers.CanonicalProviderData{
				Schema: "ptd2337-test-output-facts-v1",
				Value:  canonical.Object{"output": output.Name},
			},
		}
		result = append(result, output)
	}
	return result
}

func stubPTD2337LockedReplayResolver(t *testing.T, inputDir, outputDir *string, testedTags []string) {
	t.Helper()
	previous := bindPythonResolverCommandRunner
	t.Cleanup(func() { bindPythonResolverCommandRunner = previous })
	inspection := string(pythonInspectionOutputV2ForTest("3.12.2", testedTags, testedTags))
	bindPythonResolverCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
		return func(spec CommandSpec, options RunOptions) error {
			if len(spec.Args) == 0 {
				return errors.New("empty resolver command")
			}
			if spec.Args[0] != "exec" {
				return nil
			}
			if spec.Args[len(spec.Args)-1] == ProbeContainerExecutable {
				content, err := io.ReadAll(options.Stdin)
				if err != nil {
					return err
				}
				request, err := probe.DecodeRequestV1(content)
				if err != nil {
					return err
				}
				observations := make([]probe.ExecutableObservationV1, 0, len(request.Inspections))
				for _, requested := range request.Inspections {
					observations = append(observations, pythonConsumerObservation(requested.ID, requested.InvocationPath))
				}
				encoded, err := canonical.Marshal(probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: observations})
				if err != nil {
					return err
				}
				_, err = options.Stdout.Write(encoded)
				return err
			}
			for index := 0; index+1 < len(spec.Args); index++ {
				if spec.Args[index] == "-m" && (spec.Args[index+1] == "uv" || spec.Args[index+1] == "pip") {
					if err := writeLockedReplayOutput(t, *inputDir, *outputDir); err != nil {
						return err
					}
					return nil
				}
			}
			_, err := options.Stdout.Write([]byte(inspection))
			return err
		}, nil
	}
}

func writeLockedReplayOutput(t *testing.T, inputDir, outputDir string) error {
	t.Helper()
	// The resolver output is host mounted. Derive the portable wheel from the
	// staged resolver input so this acceptance path proves the production
	// StagePythonPortableVerifiedWheels call supplied the resolver.
	if inputDir == "" {
		return errors.New("resolver input directory was not prepared")
	}
	if outputDir == "" {
		return errors.New("resolver output directory was not prepared")
	}
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return fmt.Errorf("read staged resolver input: %w", err)
	}
	var portableInput string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "playwright-") && strings.HasSuffix(entry.Name(), ".whl") {
			if portableInput != "" {
				return fmt.Errorf("multiple staged portable resolver wheels: %q and %q", portableInput, entry.Name())
			}
			portableInput = filepath.Join(inputDir, entry.Name())
		}
	}
	if portableInput == "" {
		return errors.New("staged portable resolver wheel is missing")
	}
	content, err := os.ReadFile(portableInput)
	if err != nil {
		return fmt.Errorf("read staged portable resolver wheel: %w", err)
	}
	writePythonIntegrationWheel(t, filepath.Join(outputDir, "demo_server-1.0-py3-none-any.whl"))
	return os.WriteFile(filepath.Join(outputDir, "playwright-1.61.0-py3-none-manylinux1_x86_64.whl"), content, 0o600)
}
