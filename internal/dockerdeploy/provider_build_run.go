package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/buildprogress"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type ProviderBuildRunInputV1 struct {
	DeploymentDir   string
	Runtime         StagedProviderBuildRuntimeV1
	Automatic       bool
	NoCache         bool
	ValidateLayers  bool
	ValidateChoices bool
	Progress        io.Writer
	BuildProgress   buildprogress.Reporter
	RunOptions      RunOptions
}

type LockedProviderBuildRunInputV1 struct {
	Operation       *deploy.OperationLock
	Store           providerstore.Store
	DeploymentDir   string
	Runtime         StagedProviderBuildRuntimeV1
	Automatic       bool
	NoCache         bool
	ValidateLayers  bool
	ValidateChoices bool
	Progress        io.Writer
	BuildProgress   buildprogress.Reporter
	RunOptions      RunOptions
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
		if uid < 0 {
			uid = 0
		}
		if gid < 0 {
			gid = 0
		}
	default:
		return StagedProviderBuildRuntimeV1{}, fmt.Errorf("provider build is unsupported on host OS %q", goos)
	}
	return StagedProviderBuildRuntimeV1{Host: host, UID: uid, GID: gid}, nil
}

type providerBuildRunBackend struct {
	acquire          func(context.Context, string) (*deploy.OperationLock, error)
	newStore         func(string) (providerstore.Store, error)
	prepare          func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error)
	execute          func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error)
	cleanupFailure   func(context.Context, LockedProviderBuildPreparationV1) error
	discardValidated func(context.Context, *deploy.OperationLock, string, string) error
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
		discardValidated: func(ctx context.Context, operation *deploy.OperationLock, environment, deploymentDir string) error {
			return DiscardValidatedBuild(ctx, operation, environment, deploymentDir)
		},
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
		NoCache: input.NoCache, ValidateLayers: input.ValidateLayers,
		ValidateChoices: input.ValidateChoices,
		Progress:        input.Progress, BuildProgress: input.BuildProgress,
		RunOptions: input.RunOptions,
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
		discardValidated: func(ctx context.Context, operation *deploy.OperationLock, environment, deploymentDir string) error {
			return DiscardValidatedBuild(ctx, operation, environment, deploymentDir)
		},
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
	if backend.discardValidated == nil {
		backend.discardValidated = func(ctx context.Context, operation *deploy.OperationLock, environment, deploymentDir string) error {
			return DiscardValidatedBuild(ctx, operation, environment, deploymentDir)
		}
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
	buildprogress.Report(input.BuildProgress, buildprogress.Event{
		Phase: buildprogress.PhaseInspect, Environment: document.Environment.ID,
		Detail: "Inspecting staged inputs and build cache",
	})
	writeProviderBuildProgress(input.Progress, "preparing environment %s for %s", document.Environment.ID, state.Platform.Canonical)
	rawPackageOverrides, packageOverrides, packageOverrideIntent, err := LoadStagedPackageOverridesV1(
		input.Operation, deploymentDir, document,
	)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("load provider build package overrides: %w", err)
	}
	validatedInputs, err := ValidatedBuildInputs(
		document, state.Overlay, rawPackageOverrides, deploymentDir, state.Platform,
	)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("identify provider build validation inputs: %w", err)
	}
	baseImage, err := deploy.EffectiveBaseImageV1(document, rawPackageOverrides)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("resolve provider build base override: %w", err)
	}
	validatedCandidate := ValidatedBuildCandidateV1{}
	validatedCandidateFound := false
	if !input.NoCache {
		validatedCandidate, validatedCandidateFound, err = LoadValidatedBuildCandidate(
			ctx, input.Operation, input.Store, document, state, rawPackageOverrides, deploymentDir, false, false,
		)
		if err != nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("load validated provider build: %w", err)
		}
	}
	if validatedCandidateFound && !input.NoCache {
		if _, cacheErr := deploy.ReusableBuildLockStoreClosure(
			validatedCandidate.Current.Lock, input.Store,
			registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1,
		); cacheErr != nil {
			writeProviderBuildProgress(input.Progress, "discarding incomplete validated build cache")
			if err := backend.discardValidated(
				context.WithoutCancel(ctx), input.Operation, document.Environment.ID, deploymentDir,
			); err != nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf(
					"discard incomplete validated provider build after %v: %w", cacheErr, err,
				)
			}
			validatedCandidate = ValidatedBuildCandidateV1{}
			validatedCandidateFound = false
		}
	}
	if !validatedCandidateFound && !input.ValidateChoices {
		if err := backend.discardValidated(
			context.WithoutCancel(ctx), input.Operation, document.Environment.ID, deploymentDir,
		); err != nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("discard stale validated provider build: %w", err)
		}
	}
	localOverrides, err := PythonLocalOverridesV1(packageOverrides)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("load local Python package overrides: %w", err)
	}
	reusableSources := []providers.ResolvedSourceInput{}
	reusableWheels := []providerstore.ArtifactDescriptor{}
	priorSources := []providers.ResolvedSourceInput{}
	priorSourceWheels := []providerstore.ArtifactDescriptor{}
	cacheExists := false
	if (state.Current != nil || validatedCandidateFound) && !input.NoCache {
		cacheExists, err = input.Store.Exists()
		if err != nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("inspect provider build cache for workspace reuse: %w", err)
		}
	}
	if (state.Current != nil || validatedCandidateFound) && !input.NoCache && cacheExists {
		var lock deploy.BuildLockV1
		if validatedCandidateFound {
			lock = validatedCandidate.Current.Lock
		} else {
			loaded, found, err := input.Operation.ReadBuildLock(state.Current.BuildLockDigest, registry.ValidateRequirementProfileV1)
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("load current build lock for workspace reuse: %w", err)
			}
			if !found {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("current build lock %s is missing", state.Current.BuildLockDigest)
			}
			if err := validateGenerationBuildLock(*state.Current, loaded, registry.ValidateRequirementProfileV1); err != nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("load current build lock for workspace reuse: %w", err)
			}
			lock = loaded
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
			reusableSources, err = ReusablePythonLocalSourcesV1(
				input.Store, localOverrides, eligible,
			)
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("observe current provider build local sources: %w", err)
			}
			reusableWheels, err = buildLockSelectedSourceWheelsV1(input.Store, lock, reusableSources)
			if err != nil {
				if !errors.Is(err, errCurrentPythonSourceArtifactMissing) {
					return LockedProviderBuildExecutionResultV1{}, err
				}
				if input.Automatic {
					return LockedProviderBuildExecutionResultV1{}, fmt.Errorf(
						"current build cache is incomplete because a locked Python source artifact is missing; run reploy build --dir %q to repair it: %w",
						deploymentDir, err,
					)
				}
				// An explicit build is authorization to reconstruct the selected
				// source wheel. Keep the current lock available to the provider
				// resolver so other verified artifacts can still be reused.
				reusableSources = []providers.ResolvedSourceInput{}
				reusableWheels = []providerstore.ArtifactDescriptor{}
			}
			reusableKeys := make(map[string]struct{}, len(reusableSources))
			for _, source := range reusableSources {
				reusableKeys[source.Component+"\x00"+source.LogicalPackage] = struct{}{}
			}
			priorWheelDigests := map[canonical.Digest]struct{}{}
			for _, source := range eligible {
				if _, exact := reusableKeys[source.Component+"\x00"+source.LogicalPackage]; exact {
					continue
				}
				wheels, candidateErr := buildLockSelectedSourceWheelsV1(
					input.Store, lock, []providers.ResolvedSourceInput{source},
				)
				if candidateErr != nil {
					continue
				}
				priorSources = append(priorSources, source)
				for _, wheel := range wheels {
					if _, found := priorWheelDigests[wheel.SHA256]; found {
						continue
					}
					priorWheelDigests[wheel.SHA256] = struct{}{}
					priorSourceWheels = append(priorSourceWheels, wheel)
				}
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

	buildprogress.Report(input.BuildProgress, buildprogress.Event{
		Phase: buildprogress.PhasePrepare, Detail: "Preparing base image and provider plan",
	})
	preparation, err := backend.prepare(ctx, LockedProviderBuildPreparationInputV1{
		Operation: input.Operation, Store: input.Store, Environment: document.Environment.ID,
		DeploymentDir: deploymentDir, PackageOverrides: packageOverrideIntent, BaseImage: baseImage, Sources: reusableSources,
		DockerPlan: dockerPlan, NoCache: input.NoCache, ValidatedCandidate: func() *ValidatedBuildCandidateV1 {
			if validatedCandidateFound {
				return &validatedCandidate
			}
			return nil
		}(),
		ValidatedInputs: validatedInputs,
	})
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	if !input.ValidateChoices && validatedCandidateFound && !preparation.ReusedCandidate {
		if err := backend.discardValidated(
			context.WithoutCancel(ctx), input.Operation, document.Environment.ID, deploymentDir,
		); err != nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("discard superseded validated provider build: %w", err)
		}
	}
	writeProviderBuildProgress(input.Progress, "preparing component packages and image layers")
	if preparation.Reused {
		buildprogress.Report(input.BuildProgress, buildprogress.Event{
			Phase: buildprogress.PhasePublish, Detail: "Finalizing cached environment image",
		})
	}
	options := input.RunOptions
	options.NoCache = input.NoCache
	options.Progress = input.Progress
	result, err := backend.execute(ctx, LockedProviderBuildExecutionInputV1{
		Preparation:  preparation,
		SourceWheels: reusableWheels,
		PriorSources: priorSources, PriorSourceWheels: priorSourceWheels,
		LocalOverrides: localOverrides,
		ValidateLayers: input.ValidateLayers, RunValidation: nil,
		ValidateChoices: input.ValidateChoices,
		Progress:        input.Progress, BuildProgress: input.BuildProgress, RunOptions: options,
	})
	if err != nil {
		cleanupErr := backend.cleanupFailure(context.WithoutCancel(ctx), preparation)
		return LockedProviderBuildExecutionResultV1{}, errors.Join(err, cleanupErr)
	}
	if err := ctx.Err(); err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	if !input.ValidateChoices {
		if err := PrepareStagedManagedBindPathsV1(deploymentDir, dockerPlan); err != nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("prepare staged managed paths: %w", err)
		}
	}
	buildprogress.Report(input.BuildProgress, buildprogress.Event{
		Phase: buildprogress.PhasePublish, Detail: "Build complete", Completed: 1, Total: 1,
	})
	return result, nil
}

// PlanDockerExecution requires an image reference even though runtime-policy
// identity deliberately excludes it. This value is planning-only and is never
// created, published, recorded, or shown to the user.
const providerBuildPlanImage = "reploy-internal-build-plan"

func writeProviderBuildProgress(output io.Writer, format string, values ...any) {
	if output == nil {
		return
	}
	fmt.Fprintf(output, format+"\n", values...)
}
