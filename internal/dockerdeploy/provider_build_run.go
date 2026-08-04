package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/buildprogress"
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
	Verify          bool
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
	Verify          bool
	ValidateChoices bool
	Progress        io.Writer
	BuildProgress   buildprogress.Reporter
	RunOptions      RunOptions
}

type StagedProviderBuildRuntimeV1 struct {
	Host              blueprint.HostOS
	UID               int
	GID               int
	SupplementaryGIDs []int
}

func CurrentStagedProviderBuildRuntimeV1() (StagedProviderBuildRuntimeV1, error) {
	groups := []int{}
	if runtime.GOOS != "windows" {
		var err error
		groups, err = os.Getgroups()
		if err != nil {
			return StagedProviderBuildRuntimeV1{}, fmt.Errorf("resolve current supplementary groups: %w", err)
		}
	}
	return stagedProviderBuildRuntimeV1(runtime.GOOS, os.Getuid(), os.Getgid(), groups)
}

func stagedProviderBuildRuntimeV1(goos string, uid int, gid int, groups []int) (StagedProviderBuildRuntimeV1, error) {
	host := blueprint.HostOS("")
	switch goos {
	case "linux":
		host = blueprint.HostLinux
	case "darwin":
		host = blueprint.HostMacOS
	case "windows":
		host = blueprint.HostWindows
		groups = []int{}
		if uid < 0 {
			uid = 0
		}
		if gid < 0 {
			gid = 0
		}
	default:
		return StagedProviderBuildRuntimeV1{}, fmt.Errorf("provider build is unsupported on host OS %q", goos)
	}
	groups, err := normalizeSupplementaryGIDsV1(gid, groups)
	if err != nil {
		return StagedProviderBuildRuntimeV1{}, fmt.Errorf("provider build runtime supplementary groups: %w", err)
	}
	return StagedProviderBuildRuntimeV1{Host: host, UID: uid, GID: gid, SupplementaryGIDs: groups}, nil
}

type providerBuildRunBackend struct {
	acquire          func(context.Context, string) (*deploy.OperationLock, error)
	newStore         func(string) (providerstore.Store, error)
	prepare          func(context.Context, LockedProviderBuildPreparationInputV1) (LockedProviderBuildPreparationV1, error)
	execute          func(context.Context, LockedProviderBuildExecutionInputV1) (LockedProviderBuildExecutionResultV1, error)
	planCurrent      func(CurrentRuntimePlanInputV1) (CurrentRuntimePlanV1, error)
	verifyCurrent    func(context.Context, CurrentBuildVerificationInputV1) (CurrentBuildVerificationResultV1, error)
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
		planCurrent:    PlanCurrentRuntimeV1,
		verifyCurrent:  VerifyLoadedCurrentBuildV1,
		cleanupFailure: cleanupFailedProviderBuildV1,
		discardValidated: func(ctx context.Context, operation *deploy.OperationLock, environment, deploymentDir string) error {
			return DiscardValidatedBuild(ctx, operation, environment, deploymentDir, input.Progress)
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
	ctx, endProviderBuild := buildprofile.Start(ctx, "Provider build")
	defer func() { endProviderBuild(err) }()
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
	acquireCtx, endAcquire := buildprofile.Start(ctx, "Acquire deployment operation lock")
	operation, err := backend.acquire(acquireCtx, deploymentDir)
	endAcquire(err)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	defer func() {
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()

	_, endStore := buildprofile.Start(ctx, "Open provider store")
	store, err := backend.newStore(deploymentDir)
	endStore(err)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	return runLockedProviderBuildV1(ctx, LockedProviderBuildRunInputV1{
		Operation: operation, Store: store, DeploymentDir: deploymentDir,
		Runtime: input.Runtime, Automatic: input.Automatic,
		NoCache:         input.NoCache,
		Verify:          input.Verify,
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
		planCurrent:    PlanCurrentRuntimeV1,
		verifyCurrent:  VerifyLoadedCurrentBuildV1,
		cleanupFailure: cleanupFailedProviderBuildV1,
		discardValidated: func(ctx context.Context, operation *deploy.OperationLock, environment, deploymentDir string) error {
			return DiscardValidatedBuild(ctx, operation, environment, deploymentDir, input.Progress)
		},
	})
}

func runLockedProviderBuildV1(
	ctx context.Context,
	input LockedProviderBuildRunInputV1,
	backend providerBuildRunBackend,
) (result LockedProviderBuildExecutionResultV1, resultErr error) {
	if ctx == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("run locked provider build requires a context")
	}
	ctx, endBuild := buildprofile.Start(ctx, "Build staged environment")
	defer func() { endBuild(resultErr) }()
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
	if input.Verify && (backend.planCurrent == nil || backend.verifyCurrent == nil) {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("verified provider build requires current-build verification backends")
	}
	if backend.cleanupFailure == nil {
		backend.cleanupFailure = cleanupFailedProviderBuildV1
	}
	if backend.discardValidated == nil {
		backend.discardValidated = func(ctx context.Context, operation *deploy.OperationLock, environment, deploymentDir string) error {
			return DiscardValidatedBuild(ctx, operation, environment, deploymentDir, input.Progress)
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
	reuseSources := []providers.ResolvedSourceInput{}
	if input.Automatic && !input.NoCache {
		var reuseLock *deploy.BuildLockV1
		if validatedCandidateFound {
			lock := validatedCandidate.Current.Lock
			reuseLock = &lock
		} else if state.Current != nil {
			lock, found, err := input.Operation.ReadBuildLock(
				state.Current.BuildLockDigest,
				registry.ValidateRequirementProfileV1,
			)
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf(
					"load current build lock for automatic reuse: %w",
					err,
				)
			}
			if !found {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf(
					"current build lock %s is missing",
					state.Current.BuildLockDigest,
				)
			}
			if err := validateGenerationBuildLock(
				*state.Current,
				lock,
				registry.ValidateRequirementProfileV1,
			); err != nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf(
					"load current build lock for automatic reuse: %w",
					err,
				)
			}
			reuseLock = &lock
		}
		if reuseLock != nil && reuseLock.Platform == state.Platform {
			reuseSources, err = buildLockSelectedSourcesV1(*reuseLock)
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, err
			}
		}
	}

	dockerPlan, err := PlanDockerExecution(document, DockerPlanContext{
		DeploymentDir:     deploymentDir,
		Phase:             blueprint.PhaseStaged,
		GeneratedImage:    providerBuildPlanImage,
		Host:              input.Runtime.Host,
		UID:               input.Runtime.UID,
		GID:               input.Runtime.GID,
		SupplementaryGIDs: append([]int(nil), input.Runtime.SupplementaryGIDs...),
	})
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("plan provider build runtime: %w", err)
	}

	buildprogress.Report(input.BuildProgress, buildprogress.Event{
		Phase: buildprogress.PhasePrepare, Detail: "Preparing base image and provider plan",
	})
	preparationInput := LockedProviderBuildPreparationInputV1{
		Operation: input.Operation, Store: input.Store, Environment: document.Environment.ID,
		DeploymentDir: deploymentDir, PackageOverrides: packageOverrideIntent, BaseImage: baseImage,
		Sources:    reuseSources,
		DockerPlan: dockerPlan, NoCache: input.NoCache, ValidatedCandidate: func() *ValidatedBuildCandidateV1 {
			if validatedCandidateFound {
				return &validatedCandidate
			}
			return nil
		}(),
		ValidatedInputs: validatedInputs,
	}
	prepareCtx, endPrepare := buildprofile.Start(ctx, "Prepare provider build")
	preparation, err := backend.prepare(prepareCtx, preparationInput)
	endPrepare(err)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	effectiveNoCache := input.NoCache
	verificationFailure := ""
	if input.Verify && preparation.Reused {
		reused, err := providerBuildVerificationCurrentV1(preparation)
		if err != nil {
			return LockedProviderBuildExecutionResultV1{}, err
		}
		currentRuntime, err := backend.planCurrent(CurrentRuntimePlanInputV1{
			DeploymentDir: deploymentDir,
			Current:       reused,
			Runtime:       input.Runtime,
		})
		if err == nil {
			verifyCtx, endVerify := buildprofile.Start(ctx, "Verify reusable build")
			_, err = backend.verifyCurrent(verifyCtx, CurrentBuildVerificationInputV1{
				Store: input.Store, Current: reused, Runtime: currentRuntime,
			})
			endVerify(err)
		}
		if err != nil {
			if ctx.Err() != nil {
				return LockedProviderBuildExecutionResultV1{}, ctx.Err()
			}
			verificationFailure = err.Error()
			writeProviderBuildProgress(
				input.Progress,
				"cached build verification failed; rebuilding instead: %v",
				err,
			)
			effectiveNoCache = true
			preparationInput.NoCache = true
			preparationInput.ValidatedCandidate = nil
			rebuildCtx, endRebuild := buildprofile.Start(ctx, "Prepare rebuild after verification failure")
			preparation, err = backend.prepare(rebuildCtx, preparationInput)
			endRebuild(err)
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf(
					"cached build verification failed (%s), and rebuild preparation failed: %w",
					verificationFailure,
					err,
				)
			}
			if preparation.Reused {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf(
					"rebuild preparation reused a cached build after verification failed",
				)
			}
		} else {
			writeProviderBuildProgress(input.Progress, "verified cached environment image")
		}
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
	options.NoCache = effectiveNoCache
	options.Progress = input.Progress
	executeCtx, endExecute := buildprofile.Start(ctx, "Execute provider build")
	result, err = backend.execute(executeCtx, LockedProviderBuildExecutionInputV1{
		Preparation:     preparation,
		SourceWheels:    []providerstore.ArtifactDescriptor{},
		LocalOverrides:  localOverrides,
		RunValidation:   nil,
		ValidateChoices: input.ValidateChoices,
		Progress:        input.Progress, BuildProgress: input.BuildProgress, RunOptions: options,
	})
	endExecute(err)
	if err != nil {
		cleanupErr := backend.cleanupFailure(context.WithoutCancel(ctx), preparation)
		if verificationFailure != "" {
			err = fmt.Errorf(
				"rebuild after cached build verification failed (%s): %w",
				verificationFailure,
				err,
			)
		}
		return LockedProviderBuildExecutionResultV1{}, errors.Join(err, cleanupErr)
	}
	result.VerificationFailure = verificationFailure
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

func providerBuildVerificationCurrentV1(
	preparation LockedProviderBuildPreparationV1,
) (CurrentBuild, error) {
	if !preparation.Reused {
		return CurrentBuild{}, fmt.Errorf("provider build verification requires a reused build")
	}
	if preparation.ReusedCandidate {
		if preparation.ValidatedCandidate == nil {
			return CurrentBuild{}, fmt.Errorf("verified cached build has no validated candidate")
		}
		return preparation.ValidatedCandidate.Current, nil
	}
	if preparation.Current == nil {
		return CurrentBuild{}, fmt.Errorf("verified cached build has no current generation")
	}
	return *preparation.Current, nil
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
