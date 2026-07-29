package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

const portableBuildToolComponentV1 = "reploy_build_tools"

type PortableBuildToolEvidenceV1 struct {
	Name         string           `json:"name"`
	Path         string           `json:"path"`
	OutputDigest canonical.Digest `json:"output_digest"`
}

type PythonBuildToolEnvironmentInputV1 struct {
	Store            providerstore.Store
	Upstream         deploy.ImageDescriptor
	Workspace        PreparedProbeWorkspace
	FinalImageConfig providers.ImageConfigPolicy
	Tools            []string
	RunOptions       RunOptions
}

type PythonBuildToolEnvironmentV1 struct {
	Descriptor deploy.ImageDescriptor
	Cleanup    func(context.Context) error
}

var executePortableBuildToolGraphV1 = providers.ExecuteProviderGraph
var resolvePortableBuildToolDescriptorV1 = ResolveProviderPrefixDescriptor
var removePortableBuildToolImageV1 = RemoveBuiltImageCandidate

// PreparePythonBuildToolEnvironmentV1 resolves portable tools through the
// normal locked APT provider, but materializes the result only as a disposable
// Python source-builder prefix. The workload provider graph is unchanged.
func PreparePythonBuildToolEnvironmentV1(
	ctx context.Context,
	input PythonBuildToolEnvironmentInputV1,
) (PythonBuildToolEnvironmentV1, error) {
	if ctx == nil {
		return PythonBuildToolEnvironmentV1{}, fmt.Errorf("prepare Python build-tool environment requires a context")
	}
	if err := ctx.Err(); err != nil {
		return PythonBuildToolEnvironmentV1{}, err
	}
	if err := input.Upstream.Validate(); err != nil {
		return PythonBuildToolEnvironmentV1{}, fmt.Errorf("Python build-tool upstream: %w", err)
	}
	if err := validatePreparedProbeWorkspace(input.Upstream, input.Workspace); err != nil {
		return PythonBuildToolEnvironmentV1{}, err
	}
	if err := providers.ValidateImageConfigPolicy(input.FinalImageConfig); err != nil {
		return PythonBuildToolEnvironmentV1{}, fmt.Errorf("Python build-tool image config: %w", err)
	}
	tools, packages, err := portableBuildToolAPTRequestsV1(input.Tools)
	if err != nil {
		return PythonBuildToolEnvironmentV1{}, err
	}
	if len(tools) == 0 {
		return PythonBuildToolEnvironmentV1{
			Descriptor: input.Upstream,
			Cleanup:    func(context.Context) error { return nil },
		}, nil
	}
	baseRequest, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: string(input.Upstream.ConfigDigest), Exports: map[string]blueprint.BaseExecutableExport{},
	})
	if err != nil {
		return PythonBuildToolEnvironmentV1{}, err
	}
	aptRequest, err := aptprovider.CanonicalProviderRequestV1(aptprovider.APTProviderRequestV1{
		Components: []aptprovider.APTComponentRequestV1{{
			Component: portableBuildToolComponentV1,
			Packages:  packages,
		}},
	})
	if err != nil {
		return PythonBuildToolEnvironmentV1{}, err
	}
	plan, err := registry.Plan(providers.PlanInput{
		Platform: input.Upstream.Platform,
		Components: []providers.ResolvedComponentRequestV1{
			{Component: "base", Provider: blueprint.ComponentTypeBase, Request: baseRequest},
			{Component: portableBuildToolComponentV1, Provider: blueprint.ComponentTypeAPT, Request: aptRequest},
		},
	})
	if err != nil {
		return PythonBuildToolEnvironmentV1{}, fmt.Errorf("plan Python build-tool provider: %w", err)
	}
	validators, err := registry.OwnerValidatorsForNode(providerNodeByIDV1(plan, "apt"))
	if err != nil {
		return PythonBuildToolEnvironmentV1{}, err
	}
	options := input.RunOptions
	options.Context = ctx
	aptOperation := PreparedAPTNodeOperations{
		Store: input.Store, Validators: validators,
		FinalImageConfig: cloneImageConfigPolicy(input.FinalImageConfig),
		ReusableDebs:     []providerstore.ArtifactDescriptor{},
		ExclusiveRoots:   []string{},
		RunOptions:       options,
	}
	evidence := ProviderMaterializationEvidenceRunner{Store: input.Store}
	backend := PreparedPythonGraphBackend{
		BaseDescriptor: input.Upstream,
		Workspace:      input.Workspace,
		APTOperations:  map[providers.NodeID]PreparedAPTNodeOperations{"apt": aptOperation},
		Operations:     map[providers.NodeID]PreparedPythonNodeOperations{},
		Materializer: ProviderGraphMaterializer{
			Store: input.Store, Platform: input.Upstream.Platform,
			RunEvidence: evidence.Run, RetainLayer: skipTransientProviderLayerRetention,
			RunOptions:        options,
			verifiedArtifacts: map[providers.NodeID]map[canonical.Digest]string{"apt": nil},
		},
	}
	baseImage, err := realizedImageFromDescriptor(input.Upstream)
	if err != nil {
		return PythonBuildToolEnvironmentV1{}, err
	}
	result, err := executePortableBuildToolGraphV1(ctx, providers.GraphExecutionRequest{
		Plan: plan, Platform: input.Upstream.Platform,
		SourceCandidates: []providers.ResolvedSourceInput{},
		BaseImage:        baseImage,
		BaseCatalog:      []providers.RealizedOutput{},
		ReusableArtifacts: map[providers.NodeID][]providerstore.StoreObjectRef{
			"apt": {},
		},
		CachedResolutions: map[providers.NodeID]providers.ResolveResult{},
		Validators:        registry.OwnerValidatorsForNode,
		PrepareNode:       backend.PrepareNode,
		MaterializeNode:   backend.MaterializeNode,
	})
	if err != nil {
		return PythonBuildToolEnvironmentV1{}, fmt.Errorf(
			"prepare portable Python build tools %s: %w", strings.Join(tools, ", "), err,
		)
	}
	cleanup := func(cleanupContext context.Context) error {
		var cleanupErrors []error
		for index := len(result.Materializations) - 1; index >= 0; index-- {
			candidate := BuiltImageCandidate{ImageID: result.Materializations[index].Image.ConfigDigest}
			if err := removePortableBuildToolImageV1(cleanupContext, candidate); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		return errors.Join(cleanupErrors...)
	}
	if len(result.PrefixImages) != 2 || len(result.Materializations) != 1 {
		resultErr := fmt.Errorf("portable Python build-tool graph returned an incomplete image prefix")
		if cleanupErr := cleanup(context.WithoutCancel(ctx)); cleanupErr != nil {
			return PythonBuildToolEnvironmentV1{}, errors.Join(
				resultErr, fmt.Errorf("remove temporary Python build-tool images: %w", cleanupErr),
			)
		}
		return PythonBuildToolEnvironmentV1{}, resultErr
	}
	descriptor, err := resolvePortableBuildToolDescriptorV1(
		ctx, input.Upstream, result.PrefixImages[len(result.PrefixImages)-1], input.Upstream.Platform,
	)
	if err != nil {
		cleanupErr := cleanup(context.WithoutCancel(ctx))
		if cleanupErr != nil {
			return PythonBuildToolEnvironmentV1{}, fmt.Errorf(
				"resolve portable Python build-tool image: %v; remove temporary builder: %w", err, cleanupErr,
			)
		}
		return PythonBuildToolEnvironmentV1{}, fmt.Errorf("resolve portable Python build-tool image: %w", err)
	}
	return PythonBuildToolEnvironmentV1{Descriptor: descriptor, Cleanup: cleanup}, nil
}

func portableBuildToolAPTRequestsV1(tools []string) ([]string, []blueprint.APTPackageRequest, error) {
	if tools == nil {
		return nil, nil, fmt.Errorf("portable build tools must use an array")
	}
	normalized := append([]string{}, tools...)
	sort.Strings(normalized)
	packages := make([]blueprint.APTPackageRequest, 0, len(normalized))
	for index, tool := range normalized {
		if index > 0 && normalized[index-1] == tool {
			return nil, nil, fmt.Errorf("portable build tools contain duplicate %q", tool)
		}
		switch tool {
		case "java":
			packages = append(packages, blueprint.APTPackageRequest{Name: "default-jre-headless"})
		default:
			return nil, nil, fmt.Errorf("portable build tool %q is unsupported; v1 supports java", tool)
		}
	}
	return normalized, packages, nil
}

func providerNodeByIDV1(plan providers.ProviderPlanV1, id providers.NodeID) providers.NodeSpec {
	for _, node := range plan.Nodes {
		if node.ID == id {
			return node
		}
	}
	return providers.NodeSpec{}
}

func (session *PythonResolverSession) ValidatePortableBuildToolsV1(
	ctx context.Context,
	tools []string,
) ([]PortableBuildToolEvidenceV1, error) {
	if session == nil || session.closed || session.stopped {
		return nil, fmt.Errorf("Python resolver session is not open")
	}
	normalized, _, err := portableBuildToolAPTRequestsV1(tools)
	if err != nil {
		return nil, err
	}
	evidence := make([]PortableBuildToolEvidenceV1, 0, len(normalized))
	for _, tool := range normalized {
		var executable string
		var args []string
		switch tool {
		case "java":
			executable = "/usr/bin/java"
			args = []string{"-version"}
		default:
			return nil, fmt.Errorf("portable build tool %q is unsupported", tool)
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		commandArgs := []string{
			"exec", "--user", "0:0", "--workdir", "/", session.containerName,
			"/usr/bin/env", "-i", "HOME=/tmp", "LANG=C", "LC_ALL=C",
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			executable,
		}
		commandArgs = append(commandArgs, args...)
		if err := runPythonResolverFollowupCommand(
			CommandSpec{Name: "docker", Args: commandArgs},
			RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr},
		); err != nil {
			return nil, fmt.Errorf(
				"validate portable build tool %q at %s: %w",
				tool, executable,
				pythonResolverCommandError("validate build tool "+tool, session.descriptor.Platform.Canonical, stderr.String(), err),
			)
		}
		output := bytes.TrimSpace(append(stdout.Bytes(), stderr.Bytes()...))
		if len(output) == 0 {
			return nil, fmt.Errorf("portable build tool %q produced no version evidence", tool)
		}
		outputDigest, err := canonical.Sum("portable-build-tool-output", "portable-build-tool-output-v1", string(output))
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, PortableBuildToolEvidenceV1{
			Name: tool, Path: executable, OutputDigest: outputDigest,
		})
	}
	session.buildTools = append([]PortableBuildToolEvidenceV1{}, evidence...)
	return evidence, nil
}

func (session *PythonResolverSession) HasPortableBuildToolV1(tool string) bool {
	for _, evidence := range session.buildTools {
		if evidence.Name == tool {
			return true
		}
	}
	return false
}
