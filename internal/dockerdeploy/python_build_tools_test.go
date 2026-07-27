package dockerdeploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPortableBuildToolAPTRequestsV1MapsJava(t *testing.T) {
	tools, packages, err := portableBuildToolAPTRequestsV1([]string{"java"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tools, []string{"java"}) ||
		!reflect.DeepEqual(packages, []blueprint.APTPackageRequest{{Name: "default-jre-headless"}}) {
		t.Fatalf("tools = %#v, packages = %#v", tools, packages)
	}
}

func TestPortableBuildToolAPTRequestsV1RejectsUnknownAndDuplicateTools(t *testing.T) {
	for _, test := range []struct {
		tools []string
		want  string
	}{
		{tools: nil, want: "must use an array"},
		{tools: []string{"make"}, want: "supports java"},
		{tools: []string{"java", "java"}, want: "duplicate"},
	} {
		if _, _, err := portableBuildToolAPTRequestsV1(test.tools); err == nil ||
			!strings.Contains(err.Error(), test.want) {
			t.Fatalf("tools %#v error = %v, want %q", test.tools, err, test.want)
		}
	}
}

func TestPreparePythonBuildToolEnvironmentV1ReturnsExactImageCleanup(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	baseImage, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	previousExecute := executePortableBuildToolGraphV1
	previousResolve := resolvePortableBuildToolDescriptorV1
	previousRemove := removePortableBuildToolImageV1
	t.Cleanup(func() {
		executePortableBuildToolGraphV1 = previousExecute
		resolvePortableBuildToolDescriptorV1 = previousResolve
		removePortableBuildToolImageV1 = previousRemove
	})
	executePortableBuildToolGraphV1 = func(context.Context, providers.GraphExecutionRequest) (providers.GraphExecutionResult, error) {
		return providers.GraphExecutionResult{
			PrefixImages: []providers.RealizedImageV1{baseImage, baseImage},
			Materializations: []providers.GraphNodeMaterializeResult{{
				Image: baseImage,
			}},
		}, nil
	}
	resolvePortableBuildToolDescriptorV1 = func(
		_ context.Context,
		_ deploy.ImageDescriptor,
		_ providers.RealizedImageV1,
		_ blueprint.Platform,
	) (deploy.ImageDescriptor, error) {
		return descriptor, nil
	}
	removed := []BuiltImageCandidate{}
	removePortableBuildToolImageV1 = func(_ context.Context, candidate BuiltImageCandidate) error {
		removed = append(removed, candidate)
		return nil
	}
	environment, err := PreparePythonBuildToolEnvironmentV1(context.Background(), PythonBuildToolEnvironmentInputV1{
		Store: store, Upstream: descriptor, Workspace: workspace,
		FinalImageConfig: pythonConsumerTestImageConfig(),
		Tools:            []string{"java"},
		RunOptions:       RunOptions{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(environment.Descriptor, descriptor) || environment.Cleanup == nil || len(removed) != 0 {
		t.Fatalf("environment = %#v, removed = %#v", environment.Descriptor, removed)
	}
	if err := environment.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []BuiltImageCandidate{{ImageID: baseImage.ConfigDigest}}) {
		t.Fatalf("removed = %#v", removed)
	}
}

func TestPreparePythonBuildToolEnvironmentV1CleansMalformedGraphResult(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	baseImage, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	previousExecute := executePortableBuildToolGraphV1
	previousRemove := removePortableBuildToolImageV1
	t.Cleanup(func() {
		executePortableBuildToolGraphV1 = previousExecute
		removePortableBuildToolImageV1 = previousRemove
	})
	executePortableBuildToolGraphV1 = func(context.Context, providers.GraphExecutionRequest) (providers.GraphExecutionResult, error) {
		return providers.GraphExecutionResult{
			PrefixImages: []providers.RealizedImageV1{baseImage},
			Materializations: []providers.GraphNodeMaterializeResult{{
				Image: baseImage,
			}},
		}, nil
	}
	removed := []BuiltImageCandidate{}
	removePortableBuildToolImageV1 = func(_ context.Context, candidate BuiltImageCandidate) error {
		removed = append(removed, candidate)
		return nil
	}
	_, err = PreparePythonBuildToolEnvironmentV1(context.Background(), PythonBuildToolEnvironmentInputV1{
		Store: store, Upstream: descriptor, Workspace: workspace,
		FinalImageConfig: pythonConsumerTestImageConfig(),
		Tools:            []string{"java"},
		RunOptions:       RunOptions{},
	})
	if err == nil || !strings.Contains(err.Error(), "incomplete image prefix") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(removed, []BuiltImageCandidate{{ImageID: baseImage.ConfigDigest}}) {
		t.Fatalf("removed = %#v", removed)
	}
}
