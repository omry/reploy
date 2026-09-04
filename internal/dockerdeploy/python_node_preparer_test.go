package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPythonNodePreparerAcceptsCacheInOneResolverSession(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	probeRequest, probeResponse := pythonResolverProbeExchange()
	commands, _ := stubPythonResolverCommands(t, mustCanonicalProbeResponse(t, probeResponse), nil, nil)
	cached := providers.ResolveResult{}
	validationCalls := 0
	freshCalls := 0
	preparer := PythonNodePreparer{
		Descriptor: descriptor,
		Workspace:  workspace,
		Artifacts:  testPreparedPythonResolverArtifacts(t),
		ValidateCached: func(ctx context.Context, session *PythonResolverSession, _ providers.ResolveNodeRequest, got providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			validationCalls++
			if !reflect.DeepEqual(got, cached) {
				return providers.GraphConsumerValidation{}, errors.New("wrong cached resolution")
			}
			_, err := session.Probe(ctx, probeRequest)
			return providers.GraphConsumerValidation{}, err
		},
		ResolveFresh: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			freshCalls++
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, nil
		},
	}
	result, err := preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{
		Resolve: pythonNodePreparationRequest(t, descriptor), CachedResolution: &cached,
	})
	if err != nil {
		t.Fatal(err)
	}
	if validationCalls != 1 || freshCalls != 0 || result.Refreshed || !reflect.DeepEqual(result.Resolution, cached) {
		t.Fatalf("validation = %d, fresh = %d, result = %#v", validationCalls, freshCalls, result)
	}
	assertOnePythonPreparationContainer(t, *commands, workspace.HostDir, 4)
}

func TestPythonNodePreparerReresolvesCachedMismatchOnceInSameSession(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	probeRequest, probeResponse := pythonResolverProbeExchange()
	commands, _ := stubPythonResolverCommands(t, mustCanonicalProbeResponse(t, probeResponse), nil, nil)
	cached := providers.ResolveResult{}
	fresh := providers.ResolveResult{Evidence: providers.ValidationEvidence{Schema: "fresh"}}
	validationCalls := 0
	freshCalls := 0
	preparer := PythonNodePreparer{
		Descriptor: descriptor,
		Workspace:  workspace,
		Artifacts:  testPreparedPythonResolverArtifacts(t),
		ValidateCached: func(ctx context.Context, session *PythonResolverSession, _ providers.ResolveNodeRequest, _ providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			validationCalls++
			if _, err := session.Probe(ctx, probeRequest); err != nil {
				return providers.GraphConsumerValidation{}, err
			}
			return providers.GraphConsumerValidation{}, errors.New("cached interpreter changed")
		},
		ResolveFresh: func(_ context.Context, session *PythonResolverSession, _ providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			freshCalls++
			if len(session.observations) != len(probeResponse.Observations) {
				return providers.ResolveResult{}, providers.GraphConsumerValidation{}, errors.New("fresh resolution did not reuse cached-validation session")
			}
			return fresh, providers.GraphConsumerValidation{}, nil
		},
	}
	result, err := preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{
		Resolve: pythonNodePreparationRequest(t, descriptor), CachedResolution: &cached,
	})
	if err != nil {
		t.Fatal(err)
	}
	if validationCalls != 1 || freshCalls != 1 || !result.Refreshed || !reflect.DeepEqual(result.Resolution, fresh) {
		t.Fatalf("validation = %d, fresh = %d, result = %#v", validationCalls, freshCalls, result)
	}
	assertOnePythonPreparationContainer(t, *commands, workspace.HostDir, 4)
}

func TestPythonNodePreparerResolvesFreshWithoutCache(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	commands, _ := stubPythonResolverCommands(t, nil, nil, nil)
	source := providers.ResolvedSourceInput{Schema: "resolved-source-input-v1", Component: "application", LogicalPackage: "demo"}
	fresh := providers.ResolveResult{Evidence: providers.ValidationEvidence{Schema: "fresh"}, SelectedSources: []providers.ResolvedSourceInput{source}}
	validationCalls := 0
	freshCalls := 0
	preparer := PythonNodePreparer{
		Descriptor: descriptor,
		Workspace:  workspace,
		Artifacts:  testPreparedPythonResolverArtifacts(t),
		ValidateCached: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest, providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			validationCalls++
			return providers.GraphConsumerValidation{}, nil
		},
		ResolveFresh: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			freshCalls++
			return fresh, providers.GraphConsumerValidation{}, nil
		},
	}
	result, err := preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{
		Resolve: pythonNodePreparationRequest(t, descriptor),
	})
	if err != nil {
		t.Fatal(err)
	}
	if validationCalls != 0 || freshCalls != 1 || result.Refreshed || !reflect.DeepEqual(result.Resolution, fresh) || !reflect.DeepEqual(result.SourceCandidates, fresh.SelectedSources) {
		t.Fatalf("validation = %d, fresh = %d, result = %#v", validationCalls, freshCalls, result)
	}
	assertOnePythonPreparationContainer(t, *commands, workspace.HostDir, 3)
}

func TestPythonNodePreparerDoesNotRetryFreshFailure(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	commands, _ := stubPythonResolverCommands(t, nil, nil, nil)
	cached := providers.ResolveResult{}
	freshCalls := 0
	preparer := PythonNodePreparer{
		Descriptor: descriptor,
		Workspace:  workspace,
		Artifacts:  testPreparedPythonResolverArtifacts(t),
		ValidateCached: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest, providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			return providers.GraphConsumerValidation{}, errors.New("cached interpreter changed")
		},
		ResolveFresh: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			freshCalls++
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, errors.New("resolver failed")
		},
	}
	_, err := preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{
		Resolve: pythonNodePreparationRequest(t, descriptor), CachedResolution: &cached,
	})
	if err == nil || !strings.Contains(err.Error(), "cached interpreter changed") || !strings.Contains(err.Error(), "resolver failed") {
		t.Fatalf("error = %v", err)
	}
	if freshCalls != 1 {
		t.Fatalf("fresh calls = %d", freshCalls)
	}
	assertOnePythonPreparationContainer(t, *commands, workspace.HostDir, 3)
}

func TestPythonNodePreparerRejectsDifferentUpstreamBeforeDocker(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	commands, _ := stubPythonResolverCommands(t, nil, nil, nil)
	request := pythonNodePreparationRequest(t, descriptor)
	request.Upstream.ConfigDigest = canonical.Digest("sha256:" + strings.Repeat("e", 64))
	preparer := PythonNodePreparer{
		Descriptor: descriptor,
		Workspace:  workspace,
		Artifacts:  testPreparedPythonResolverArtifacts(t),
		ValidateCached: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest, providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			return providers.GraphConsumerValidation{}, nil
		},
		ResolveFresh: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, nil
		},
	}
	if _, err := preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{Resolve: request}); err == nil || !strings.Contains(err.Error(), "exact upstream") {
		t.Fatalf("error = %v", err)
	}
	if len(*commands) != 0 {
		t.Fatalf("mismatched upstream reached Docker: %#v", *commands)
	}
}

func TestPythonNodePreparerRejectsReusableWheelOutsideResolverInputsBeforeDocker(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	workspace := testPreparedProbeWorkspace(t, descriptor.Platform, t.TempDir())
	commands, _ := stubPythonResolverCommands(t, nil, nil, nil)
	preparer := PythonNodePreparer{
		Descriptor: descriptor, Workspace: workspace, Artifacts: testPreparedPythonResolverArtifacts(t),
		ReusableWheels: []providerstore.ArtifactDescriptor{{
			LogicalPath: "wheels/demo.whl", Kind: "wheel", Size: "1", SHA256: rendererDigest("a"),
		}},
		ValidateCached: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest, providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			return providers.GraphConsumerValidation{}, nil
		},
		ResolveFresh: func(context.Context, *PythonResolverSession, providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, nil
		},
	}
	_, err := preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{Resolve: pythonNodePreparationRequest(t, descriptor)})
	if err == nil || !strings.Contains(err.Error(), "absent from the node resolver inputs") {
		t.Fatalf("error = %v", err)
	}
	if len(*commands) != 0 {
		t.Fatalf("unbound reusable wheel reached Docker: %#v", *commands)
	}
}

func pythonNodePreparationRequest(t *testing.T, descriptor deploy.ImageDescriptor) providers.ResolveNodeRequest {
	t.Helper()
	rootFSSubject, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	imageDigest := descriptor.ManifestDigest
	if imageDigest == "" {
		imageDigest = descriptor.ConfigDigest
	}
	return providers.ResolveNodeRequest{
		Platform: descriptor.Platform,
		Upstream: providers.RealizedImageV1{
			Digest: imageDigest, ConfigDigest: descriptor.ConfigDigest, RootFSSubject: rootFSSubject,
		},
	}
}

func assertOnePythonPreparationContainer(t *testing.T, commands []CommandSpec, workspace string, wantCommands int) {
	t.Helper()
	if len(commands) != wantCommands {
		t.Fatalf("commands = %#v", commands)
	}
	name := pythonResolverContainerName(workspace)
	createCount := 0
	removeCount := 0
	for _, command := range commands {
		if len(command.Args) == 0 {
			t.Fatalf("empty command = %#v", command)
		}
		targetsContainer := false
		for _, argument := range command.Args {
			if argument == name {
				targetsContainer = true
				break
			}
		}
		if !targetsContainer {
			t.Fatalf("command does not target resolver container %q: %#v", name, command.Args)
		}
		switch command.Args[0] {
		case "create":
			createCount++
		case "rm":
			removeCount++
		}
	}
	if createCount != 1 || removeCount != 1 {
		t.Fatalf("create = %d, remove = %d, commands = %#v", createCount, removeCount, commands)
	}
}
