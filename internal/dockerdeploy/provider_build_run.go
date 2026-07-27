package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type ProviderBuildRunInputV1 struct {
	DeploymentDir  string
	Runtime        StagedProviderBuildRuntimeV1
	Automatic      bool
	NoCache        bool
	ValidateLayers bool
	RunOptions     RunOptions
}

type LockedProviderBuildRunInputV1 struct {
	Operation      *deploy.OperationLock
	Store          providerstore.Store
	DeploymentDir  string
	Runtime        StagedProviderBuildRuntimeV1
	Automatic      bool
	NoCache        bool
	ValidateLayers bool
	RunOptions     RunOptions
}

type StagedProviderBuildRuntimeV1 struct {
	Host blueprint.HostOS
	UID  int
	GID  int
}

func CurrentStagedProviderBuildRuntimeV1() (StagedProviderBuildRuntimeV1, error) {
	return stagedProviderBuildRuntimeV1(runtime.GOOS, os.Getuid(), os.Getgid())
}

func stagedProviderBuildRuntimeV1(goos string, uid int, gid int) (StagedProviderBuildRuntimeV1, error) {
	host := blueprint.HostOS("")
	switch goos {
	case "linux":
		host = blueprint.HostLinux
	case "darwin":
		host = blueprint.HostMacOS
	case "windows":
		host = blueprint.HostWindows
	default:
		return StagedProviderBuildRuntimeV1{}, fmt.Errorf("provider build is unsupported on host OS %q", goos)
	}
	return StagedProviderBuildRuntimeV1{Host: host, UID: uid, GID: gid}, nil
}

type providerBuildRunBackend struct {
	acquire        func(context.Context, string) (*deploy.OperationLock, error)
	newStore       func(string) (providerstore.Store, error)
	prepare        func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error)
	execute        func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error)
	cleanupFailure func(context.Context, LockedProviderBuildPreparationV1) error
}

// RunProviderBuildV1 owns one complete deployment-locked provider build from
// reuse selection through provider execution, validation, and publication.
func RunProviderBuildV1(
	ctx context.Context,
	input ProviderBuildRunInputV1,
) (LockedProviderBuildExecutionResultV1, error) {
	return runProviderBuildV1(ctx, input, providerBuildRunBackend{
		acquire:        deploy.AcquireOperationLock,
		newStore:       providerstore.NewStore,
		prepare:        PrepareLockedProviderBuildV1,
		execute:        ExecuteLockedProviderBuildV1,
		cleanupFailure: cleanupFailedProviderBuildV1,
	})
}

func runProviderBuildV1(
	ctx context.Context,
	input ProviderBuildRunInputV1,
	backend providerBuildRunBackend,
) (result LockedProviderBuildExecutionResultV1, err error) {
	if ctx == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run provider build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	if input.DeploymentDir == "" {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run provider build requires a deployment directory")
	}
	if backend.acquire == nil || backend.newStore == nil || backend.prepare == nil || backend.execute == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run provider build requires a complete backend")
	}

	deploymentDir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("resolve provider build deployment directory: %w", err)
	}
	operation, err := backend.acquire(ctx, deploymentDir)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	defer func() {
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()

	store, err := backend.newStore(deploymentDir)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	return runLockedProviderBuildV1(ctx, LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: deploymentDir,
		Runtime: input.Runtime, Automatic: input.Automatic,
		NoCache: input.NoCache, ValidateLayers: input.ValidateLayers, RunOptions: input.RunOptions,
	}, backend)
}

// RunLockedProviderBuildV1 runs the canonical provider build while retaining
// ownership of a caller-held deployment lock. This lets install keep the same
// source lock held through a later transfer without recursively acquiring it.
func RunLockedProviderBuildV1(
	ctx context.Context,
	input LockedProviderBuildRunInputV1,
) (LockedProviderBuildExecutionResultV1, error) {
	return runLockedProviderBuildV1(ctx, input, providerBuildRunBackend{
		prepare:        PrepareLockedProviderBuildV1,
		execute:        ExecuteLockedProviderBuildV1,
		cleanupFailure: cleanupFailedProviderBuildV1,
	})
}

func runLockedProviderBuildV1(
	ctx context.Context,
	input LockedProviderBuildRunInputV1,
	backend providerBuildRunBackend,
) (LockedProviderBuildExecutionResultV1, error) {
	if ctx == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run locked provider build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	if input.Operation == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run locked provider build requires an operation lock")
	}
	if input.DeploymentDir == "" {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run locked provider build requires a deployment directory")
	}
	if backend.prepare == nil || backend.execute == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run locked provider build requires a complete backend")
	}
	if backend.cleanupFailure == nil {
		backend.cleanupFailure = cleanupFailedProviderBuildV1
	}

	deploymentDir, err := filepath.Abs(input.DeploymentDir)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("resolve locked provider build deployment directory: %w", err)
	}
	if err := validatePublicationDeployment(input.Operation, input.Store, deploymentDir); err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run locked provider build deployment: %w", err)
	}
	state, found, err := input.Operation.ReadStateV1()
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run provider build state: %w", err)
	}
	if !found {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("build state is missing; stage or install the deployment first")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run provider build blueprint: %w", err)
	}
	if state.Deployment != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("provider build requires a staged deployment; an installed deployment cannot be used as a build source")
	}
	workspaceRoot := ""
	if state.Staging != nil {
		workspaceRoot = state.Staging.WorkspaceRoot
	}
	reusableSources := []providers.ResolvedSourceInput{}
	reusableWheels := []providerstore.ArtifactDescriptor{}
	observedSources := []PythonWorkspaceSource{}
	cacheExists := false
	if state.Current != nil && !input.NoCache {
		cacheExists, err = input.Store.Exists()
		if err != nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("inspect provider build cache for workspace reuse: %w", err)
		}
	}
	if state.Current != nil && !input.NoCache && cacheExists {
		lock, found, err := input.Operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
		if err != nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("load current build lock for workspace reuse: %w", err)
		}
		if !found {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("current build lock %s is missing", state.Current.BuildLockDigest)
		}
		if err := validateGenerationBuildLock(*state.Current, lock, registry.ValidateRequirementProfileV1); err != nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("load current build lock for workspace reuse: %w", err)
		}
		if lock.Platform == state.Platform {
			lockedSources, err := buildLockSelectedSourcesV1(lock)
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, err
			}
			eligible := make([]providers.ResolvedSourceInput, 0, len(lockedSources))
			distributionSet := map[string]struct{}{}
			for _, source := range lockedSources {
				component, found := document.Environment.Components[source.Component]
				if !found || component.Type != blueprint.ComponentTypePython {
					continue
				}
				eligible = append(eligible, source)
				distributionSet[source.LogicalPackage] = struct{}{}
			}
			distributions := make([]string, 0, len(distributionSet))
			for distribution := range distributionSet {
				distributions = append(distributions, distribution)
			}
			sort.Strings(distributions)
			observedSources, err = ResolveSelectedPythonWorkspaceSources(document, workspaceRoot, distributions)
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("observe current provider build workspace sources: %w", err)
			}
			reusableSources, err = MatchReusablePythonWorkspaceSources(eligible, observedSources)
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, err
			}
			reusableWheels, err = buildLockSelectedSourceWheelsV1(input.Store, lock, reusableSources)
			if err != nil {
				if !errors.Is(err, errCurrentPythonSourceWheelMissing) {
					return LockedProviderBuildExecutionResultV1{}, err
				}
				if input.Automatic {
					return LockedProviderBuildExecutionResultV1{}, fmt.Errorf(
						"current build cache is incomplete because a locked Python source wheel is missing; run reploy build --dir %q to repair it: %w",
						deploymentDir, err,
					)
				}
				// An explicit build is authorization to reconstruct the selected
				// source wheel. Keep the current lock available to the provider
				// resolver so other verified artifacts can still be reused.
				reusableSources = []providers.ResolvedSourceInput{}
				reusableWheels = []providerstore.ArtifactDescriptor{}
			}
		}
	}

	dockerPlan, err := PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir:  deploymentDir,
		Phase:          blueprint.PhaseStaged,
		GeneratedImage: providerBuildPlanImage,
		Host:           input.Runtime.Host,
		UID:            input.Runtime.UID,
		GID:            input.Runtime.GID,
	})
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("plan provider build runtime: %w", err)
	}

	preparation, err := backend.prepare(ctx, LockedProviderBuildPreparationInputV1{
		Operation: input.Operation, Store: input.Store, Environment: document.Environment.ID,
		DeploymentDir: deploymentDir, Sources: reusableSources,
		DockerPlan: dockerPlan, NoCache: input.NoCache,
	})
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	workspaceSources := []PythonWorkspaceSource{}
	if !preparation.Reused {
		workspaceSources, err = CompletePythonWorkspaceSources(document, workspaceRoot, observedSources)
		if err != nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("prepare provider build workspace sources: %w", err)
		}
	}
	options := input.RunOptions
	options.NoCache = input.NoCache
	result, err := backend.execute(ctx, LockedProviderBuildExecutionInputV1{
		Preparation:      preparation,
		SourceWheels:     reusableWheels,
		WorkspaceSources: workspaceSources,
		ValidateLayers:   input.ValidateLayers, RunValidation: nil, RunOptions: options,
	})
	if err != nil {
		cleanupErr := backend.cleanupFailure(context.WithoutCancel(ctx), preparation)
		return LockedProviderBuildExecutionResultV1{}, errors.Join(err, cleanupErr)
	}
	if err := ctx.Err(); err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	if err := PrepareStagedManagedBindPathsV1(deploymentDir, dockerPlan); err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("prepare staged managed paths: %w", err)
	}
	return result, nil
}

// PlanDockerExecution requires an image reference even though runtime-policy
// identity deliberately excludes it. This value is planning-only and is never
// created, published, recorded, or shown to the user.
const providerBuildPlanImage = "reploy-internal-build-plan"
