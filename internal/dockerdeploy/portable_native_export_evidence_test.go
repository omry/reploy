package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

func materializeNativeBuilderForTest(t *testing.T) (*SourceBuilderPortableToolsV1, providerstore.Store) {
	t.Helper()
	return materializeNativeBuilderVersionForTest(t, "5.2.15", "debian", "12")
}

func materializeNativeBuilderVersionForTest(t *testing.T, version, osID, osVersion string) (*SourceBuilderPortableToolsV1, providerstore.Store) {
	t.Helper()
	_, input := planSourceBuilderJavaForTest(t, "demo")
	stubSourceBuilderTarget(t, sourceBuilderJavaTarget(osID, osVersion), nil)
	dir := writeSourceBuilderTestRecipe(t, "demo", "[tool:bash=="+version+"]")
	recipe, err := ReadPythonLocalSourceRecipeV1(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	input.SelectedRecipes = []SourceBuilderSelectedRecipeV1{{Distribution: "demo", Recipe: recipe}}
	plan, err := PlanSourceBuilderPortableToolsV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := MaterializeSourceBuilderPortableToolsV1(context.Background(), input.Store, plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tools.Cleanup(); err != nil {
			t.Error(err)
		}
	})
	return tools, input.Store
}

func nativeEvidenceFixtureForTest(t *testing.T) (*SourceBuilderPortableToolsV1, providerstore.Store, InspectedImageCandidate, providers.RealizedOutput) {
	t.Helper()
	tools, store := materializeNativeBuilderVersionForTest(t, "5.2.37", "debian", "13")
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	image, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	observation := directExecutableObservation("export_000", "/usr/bin/bash")
	evidence, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
		Output: providers.QualifiedOutput{Component: "source-builder", Name: "bash"},
		Facts:  providers.CanonicalProviderData{Schema: aptprovider.ExplicitExportSchemaV1, Value: canonical.Object{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence.Terminal.Owner = &providers.OwnerEvidence{Provider: "apt", Data: providers.CanonicalProviderData{
		Schema: aptprovider.DPKGOwnerDataSchemaV1, Value: canonical.Object{"name": "bash", "version": "5.2.37-2+b10", "architecture": "amd64", "status": "install ok installed"},
	}}
	output := providers.RealizedOutput{SupplierComponent: "source-builder", SupplierNode: "apt", Name: "bash",
		Candidate: providers.ExecutableCandidate{InvocationPath: "/usr/bin/bash"}, Evidence: evidence}
	return tools, store, InspectedImageCandidate{Descriptor: descriptor, Image: image}, output
}

func mergedBashConsumerObservation(id, terminalPath string) probe.ExecutableObservationV1 {
	observation := directExecutableObservation(id, terminalPath)
	observation.InvocationPath = "/bin/bash"
	observation.Links = []probe.LinkObservationV1{{
		Path: "/bin/bash", Target: terminalPath, ResolvedPath: terminalPath,
		Mode: "0777", UID: "0", GID: "0",
	}}
	access := []probe.AccessObservationV1{
		{Path: "/", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
		{Path: "/bin", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
	}
	if terminalPath == "/usr/bin/bash" {
		access = append(access,
			probe.AccessObservationV1{Path: "/usr", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			probe.AccessObservationV1{Path: "/usr/bin", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
		)
	} else if terminalPath == "/opt/alternate/bash" {
		access = append(access,
			probe.AccessObservationV1{Path: "/opt", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
			probe.AccessObservationV1{Path: "/opt/alternate", Kind: "directory", Mode: "0755", UID: "0", GID: "0"},
		)
	}
	access = append(access, probe.AccessObservationV1{Path: terminalPath, Kind: "regular", Mode: "0755", UID: "0", GID: "0"})
	observation.Access = access
	return observation
}

func TestCollectPortableNativeExportEvidenceV1ExactStateAndUnownedConsumer(t *testing.T) {
	for _, fault := range []string{"", "consumer missing", "consumer different", "consumer alternate", "export bytes", "package state", "wrong owner", "cleanup"} {
		t.Run(fault, func(t *testing.T) {
			tools, store, image, output := nativeEvidenceFixtureForTest(t)
			workspace := testPreparedProbeWorkspace(t, image.Descriptor.Platform, t.TempDir())
			previousPrepare, previousBind := prepareImageProbeWorkspace, bindImageValidationCommandRunner
			t.Cleanup(func() { prepareImageProbeWorkspace, bindImageValidationCommandRunner = previousPrepare, previousBind })
			cleaned := false
			prepareImageProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
				return workspace, func() error { cleaned = true; return nil }, nil
			}
			commands := []CommandSpec{}
			bindImageValidationCommandRunner = func(context.Context, CommandSpec, time.Duration) (commandRunner, error) {
				return func(spec CommandSpec, options RunOptions) error {
					commands = append(commands, spec)
					if spec.Args[0] == "rm" && fault == "cleanup" {
						return errors.New("container removal failed")
					}
					if spec.Args[0] != "exec" {
						return nil
					}
					if options.Stdin != nil {
						consumer := mergedBashConsumerObservation("consumer_000", "/usr/bin/bash")
						export := directExecutableObservation("export_000", "/usr/bin/bash")
						if fault == "consumer different" {
							consumer.Terminal.SHA256 = rendererDigest("d")
						}
						if fault == "consumer alternate" {
							consumer = mergedBashConsumerObservation("consumer_000", "/opt/alternate/bash")
						}
						if fault == "export bytes" {
							export.Terminal.SHA256 = rendererDigest("e")
						}
						observations := []probe.ExecutableObservationV1{consumer, export}
						if fault == "consumer missing" {
							observations = []probe.ExecutableObservationV1{export}
						}
						_, err := options.Stdout.Write(mustCanonicalProbeResponse(t, probe.ResponseV1{Schema: probe.ResponseSchemaV1, Observations: observations}))
						return err
					}
					text := "bash: /usr/bin/bash\n"
					if slices.Contains(spec.Args, "--show") {
						text = "bash\t5.2.37-2+b10\tamd64\tinstall ok installed\n"
						if fault == "package state" {
							text = strings.Replace(text, "5.2.37-2+b10", "5.2.15-2+b13", 1)
						}
					}
					if fault == "wrong owner" {
						text = "coreutils: /usr/bin/bash\n"
					}
					_, err := options.Stdout.Write([]byte(text))
					return err
				}, nil
			}
			exports, consumers, err := CollectPortableNativeExportEvidenceV1(context.Background(), store, image, tools.Lock, "source-builder:demo", []providers.RealizedOutput{output}, map[string]string{"bash": "/bin/bash"})
			if fault != "" {
				if err == nil || exports != nil || consumers != nil {
					t.Fatalf("accepted defective evidence: %s %v", fault, err)
				}
				if fault == "cleanup" && cleaned {
					t.Fatal("removed helper workspace after uncertain container cleanup")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !cleaned || len(exports) != 1 || len(consumers) != 1 || exports[0].Terminal.Owner == nil || consumers[0].Terminal.Owner != nil || exports[0].Terminal.Path != "/usr/bin/bash" || consumers[0].InvocationPath != "/bin/bash" || consumers[0].Terminal.Path != "/usr/bin/bash" || exports[0].Terminal.SHA256 != consumers[0].Terminal.SHA256 {
				t.Fatal("missing exact state or invented consumer alias ownership")
			}
			for _, command := range commands {
				if slices.Contains(command.Args, "--search") && !reflect.DeepEqual(command.Args[len(command.Args)-1:], []string{"/usr/bin/bash"}) {
					t.Fatal("queried ownership of the unowned consumer alias")
				}
			}
		})
	}
}

func TestNativeBuilderExportsAreNotCopiedFromTheHost(t *testing.T) {
	tools, _ := materializeNativeBuilderForTest(t)
	if len(tools.copies) != 0 || len(tools.Lock.Acquisitions) != 0 || len(tools.nativeExports) != 1 || len(tools.Exports) != 1 || tools.Exports[0].Path != "/bin/bash" {
		t.Fatal("native builder was treated as an archive payload")
	}
	headers := sourceBuilderExportArchiveHeadersForTest(t, tools.contextDir+"/"+tools.exportsArchive)
	if len(headers) != 2 || headers[1].Linkname != "/bin/bash" {
		t.Fatal("native builder export link is not exact")
	}
}

func TestNativeBuilderPreparationFailsClosedAndCleansOwnedLayers(t *testing.T) {
	for _, fault := range []string{"", "prepare", "evidence", "native cleanup"} {
		t.Run(fault, func(t *testing.T) {
			tools, store := materializeNativeBuilderForTest(t)
			upstream := testProbeImageDescriptor(t, "linux/amd64")
			builder := sourceBuilderTestImageDescriptor(t, "c")
			stub := &sourceBuilderEnvironmentStub{}
			stubSourceBuilderEnvironment(t, stub, builder)
			previousPrepare, previousCollect, previousRemove := prepareSourceBuilderNativePackagesV1, collectSourceBuilderNativeExportsV1, removeSourceBuilderLayerV1
			t.Cleanup(func() {
				prepareSourceBuilderNativePackagesV1, collectSourceBuilderNativeExportsV1, removeSourceBuilderLayerV1 = previousPrepare, previousCollect, previousRemove
			})
			native := testProbeImageDescriptor(t, "linux/amd64")
			native.ConfigDigest = rendererDigest("d")
			native.AuthorReference = string(native.ConfigDigest)
			native.ImmutableReference = string(native.ConfigDigest)
			nativeImage, _ := realizedImageFromDescriptor(native)
			owned := BuiltImageCandidate{ImageID: native.ConfigDigest, TemporaryReference: "native-owned", Workspace: t.TempDir()}
			prepareSourceBuilderNativePackagesV1 = func(_ context.Context, _ providerstore.Store, _ *SourceBuilderPortableToolsV1, actual deploy.ImageDescriptor, _ RunOptions) (*sourceBuilderNativePackagesV1, error) {
				if !reflect.DeepEqual(actual, upstream) {
					t.Fatal("native packages prepared on a different prefix")
				}
				if fault == "prepare" {
					return nil, errors.New("APT resolution failed")
				}
				return &sourceBuilderNativePackagesV1{Image: InspectedImageCandidate{Descriptor: native, Image: nativeImage}, candidate: owned}, nil
			}
			collectSourceBuilderNativeExportsV1 = func(_ context.Context, _ providerstore.Store, actual InspectedImageCandidate, _ providers.PortableToolLockV1, scope string, _ []providers.RealizedOutput, paths map[string]string) ([]providers.ExecutableEvidence, []providers.ExecutableEvidence, error) {
				if !reflect.DeepEqual(actual.Descriptor, builder) || scope != "source-builder:demo" || paths["bash"] != "/opt/reploy/exports/bash" {
					t.Fatal("native output validation was not bound to the final builder")
				}
				if fault == "evidence" {
					return nil, nil, errors.New("wrong native bytes")
				}
				return []providers.ExecutableEvidence{}, []providers.ExecutableEvidence{}, nil
			}
			nativeRemoved := false
			removeSourceBuilderLayerV1 = func(ctx context.Context, candidate BuiltImageCandidate) error {
				if candidate == owned {
					nativeRemoved = true
					if fault == "native cleanup" {
						return errors.New("native layer removal failed")
					}
					return nil
				}
				return previousRemove(ctx, candidate)
			}
			environment, err := PrepareSourceBuilderEnvironmentV1(context.Background(), store, tools, upstream, RunOptions{})
			if fault != "" {
				if err == nil || environment != nil {
					t.Fatal("native failure was accepted", fault)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !nativeRemoved || !reflect.DeepEqual(stub.upstream, native) || environment.NativePackages == nil {
				t.Fatal("native transaction was not isolated and retained as evidence")
			}
			if err := environment.Cleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
