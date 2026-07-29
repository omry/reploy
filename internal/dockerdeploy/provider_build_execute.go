package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"reflect"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/buildprogress"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type LockedProviderBuildExecutionInputV1 struct {
	Preparation     LockedProviderBuildPreparationV1
	SourceWheels    []providerstore.ArtifactDescriptor
	LocalOverrides  []PythonLocalOverrideV1
	ValidateChoices bool
	RunValidation   FullImageValidationRunner
	Progress        io.Writer
	BuildProgress   buildprogress.Reporter
	RunOptions      RunOptions
}

type LockedProviderBuildExecutionResultV1 struct {
	State               deploy.StateV1
	Lock                deploy.BuildLockV1
	Reused              bool
	Republished         bool
	Validated           bool
	VerificationFailure string
}

type providerBuildExecutionBackend struct {
	executeGraph      func(context.Context, PreparedPythonGraphExecutionInput) (providers.GraphExecutionResult, error)
	prepareValidation func(
		context.Context,
		deploy.ImageDescriptor,
		[]providers.RealizedOutput,
		providers.GraphExecutionResult,
		deploy.RuntimePolicyV1,
	) (ProviderGraphValidationPlan, error)
	complete func(
		context.Context,
		*deploy.OperationLock,
		providerstore.Store,
		ProviderBuildCompletionInput,
	) (ProviderBuildCompletionResult, error)
	publishBuild          func(context.Context, *deploy.OperationLock, providerstore.Store, BuildPublicationInput) (deploy.StateV1, error)
	publishValidated      func(context.Context, *deploy.OperationLock, providerstore.Store, string, string, deploy.BuildLockV1, ValidatedBuildInputsV1) (deploy.ValidatedBuildV1, error)
	verifyReference       func(context.Context, providers.RealizedImageV1, string, string, string) error
	retryValidatedCleanup func(context.Context, *deploy.OperationLock, string, string) (deploy.ValidatedBuildV1, bool, error)
	discardValidated      func(context.Context, *deploy.OperationLock, string, string) error
}

// ExecuteLockedProviderBuildV1 consumes a preparation made under the same held
// operation lock. It either returns the exact reused generation without backend
// work or executes the provider graph, plans complete validation, and publishes
// the resulting generation. Local-source observation occurs during fresh graph
// preparation after package selection; lock acquisition belongs to the caller.
func ExecuteLockedProviderBuildV1(
	ctx context.Context,
	input LockedProviderBuildExecutionInputV1,
) (LockedProviderBuildExecutionResultV1, error) {
	if input.RunValidation == nil {
		runner := ProviderFullImageValidationRunner{Store: input.Preparation.Store}
		input.RunValidation = runner.Run
	}
	return executeLockedProviderBuildV1(ctx, input, providerBuildExecutionBackend{
		executeGraph:          ExecutePreparedPythonGraph,
		prepareValidation:     PrepareProviderGraphValidation,
		complete:              CompleteProviderBuild,
		publishBuild:          PublishBuild,
		publishValidated:      PublishValidatedBuild,
		verifyReference:       VerifyEnvironmentGenerationReference,
		retryValidatedCleanup: RetryValidatedBuildCleanup,
		discardValidated:      DiscardValidatedBuild,
	})
}

func executeLockedProviderBuildV1(
	ctx context.Context,
	input LockedProviderBuildExecutionInputV1,
	backend providerBuildExecutionBackend,
) (LockedProviderBuildExecutionResultV1, error) {
	if ctx == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build requires a context")
	}
	if err := ctx.Err(); err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	preparation := input.Preparation
	if err := preparation.Operation.RequireHeld(); err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build: %w", err)
	}
	if input.SourceWheels == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build source wheels must use an array")
	}
	if input.LocalOverrides == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build local overrides must use an array")
	}
	if preparation.Reused {
		if preparation.PreparedBase != nil || preparation.ReusableLock == nil || preparation.PublicationLock == nil {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("reused provider build preparation is incomplete")
		}
		reused := preparation.Current
		if preparation.ReusedCandidate {
			if preparation.ValidatedCandidate == nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("reused validated build preparation has no candidate")
			}
			reused = &preparation.ValidatedCandidate.Current
		}
		if reused == nil || !reflect.DeepEqual(*preparation.ReusableLock, reused.Lock) {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("reused provider build lock does not match its recorded generation")
		}
		publicationLock := *preparation.PublicationLock
		expectedPublicationLock := reused.Lock
		expectedPublicationLock.BlueprintDigest = publicationLock.BlueprintDigest
		if !reflect.DeepEqual(publicationLock, expectedPublicationLock) {
			return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("reused provider build publication lock changes validated build inputs")
		}
		if input.ValidateChoices {
			if preparation.ReusedCandidate {
				if backend.verifyReference == nil {
					return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("validate cached build requires image verification")
				}
				record := preparation.ValidatedCandidate.Record
				if len(record.PendingCleanup) != 0 {
					if backend.retryValidatedCleanup == nil {
						return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("validate cached build requires cleanup retry support")
					}
					retried, found, err := backend.retryValidatedCleanup(
						context.WithoutCancel(ctx), preparation.Operation,
						preparation.Environment, preparation.DeploymentDir,
					)
					if err != nil {
						return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("retry validated build cleanup: %w", err)
					}
					if !found {
						return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("validated build disappeared during cleanup retry")
					}
					record = retried
				}
				if err := backend.verifyReference(
					ctx, reused.Lock.FinalImage, preparation.ValidatedCandidate.Record.ImageReference,
					preparation.Environment, preparation.DeploymentDir,
				); err != nil {
					return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("verify cached validated build: %w", err)
				}
				writeProviderBuildProgress(input.Progress, "validated choices using the cached image")
				writeValidatedBuildCleanupWarning(input.Progress, record)
			} else {
				if backend.publishValidated == nil {
					return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("validate cached build requires candidate publication")
				}
				record, err := backend.publishValidated(
					ctx, preparation.Operation, preparation.Store, preparation.Environment,
					preparation.DeploymentDir, publicationLock, preparation.ValidatedInputs,
				)
				if err != nil {
					return LockedProviderBuildExecutionResultV1{}, err
				}
				writeProviderBuildProgress(input.Progress, "validated choices using the cached image")
				writeValidatedBuildCleanupWarning(input.Progress, record)
			}
			return LockedProviderBuildExecutionResultV1{
				State: reused.State, Lock: publicationLock, Reused: true, Validated: true,
			}, nil
		}
		if preparation.ReusedCandidate {
			if backend.verifyReference == nil || backend.publishBuild == nil || backend.discardValidated == nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("promote cached validated build requires a complete backend")
			}
			if err := backend.verifyReference(
				ctx, reused.Lock.FinalImage, preparation.ValidatedCandidate.Record.ImageReference,
				preparation.Environment, preparation.DeploymentDir,
			); err != nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("verify cached validated build: %w", err)
			}
			state, err := backend.publishBuild(ctx, preparation.Operation, preparation.Store, BuildPublicationInput{
				Environment: preparation.Environment, DeploymentDir: preparation.DeploymentDir,
				Document: preparation.Loaded.Document, Lock: publicationLock,
			})
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, err
			}
			cleanupErr := backend.discardValidated(
				context.WithoutCancel(ctx), preparation.Operation,
				preparation.Environment, preparation.DeploymentDir,
			)
			writeProviderBuildProgress(input.Progress, "promoted validated cached image")
			if cleanupErr != nil {
				writeProviderBuildProgress(
					input.Progress,
					"warning: build succeeded, but cleanup of superseded cached image references is pending; Reploy will retry automatically: %v",
					cleanupErr,
				)
			}
			return LockedProviderBuildExecutionResultV1{
				State: state, Lock: publicationLock, Reused: true,
			}, nil
		}
		if !reflect.DeepEqual(publicationLock, reused.Lock) {
			if backend.publishBuild == nil {
				return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("republish reused build requires publication support")
			}
			state, err := backend.publishBuild(ctx, preparation.Operation, preparation.Store, BuildPublicationInput{
				Environment: preparation.Environment, DeploymentDir: preparation.DeploymentDir,
				Document: preparation.Loaded.Document, Lock: publicationLock,
			})
			if err != nil {
				return LockedProviderBuildExecutionResultV1{}, err
			}
			writeProviderBuildProgress(input.Progress, "reusing current validated image for updated runtime configuration")
			return LockedProviderBuildExecutionResultV1{
				State: state, Lock: publicationLock, Reused: true, Republished: true,
			}, nil
		}
		writeProviderBuildProgress(input.Progress, "reusing current validated image")
		return LockedProviderBuildExecutionResultV1{
			State: preparation.Current.State, Lock: publicationLock, Reused: true,
		}, nil
	}
	if backend.executeGraph == nil || backend.prepareValidation == nil || backend.complete == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute locked provider build requires a complete backend")
	}
	if preparation.PreparedBase == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("provider build preparation has no realized base")
	}
	if input.RunValidation == nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("provider build requires a final image validation runner")
	}
	preparedBase := *preparation.PreparedBase
	if !reflect.DeepEqual(preparedBase.Plan, preparation.SelectedBase.Plan) ||
		!reflect.DeepEqual(preparedBase.Descriptor, preparation.SelectedBase.Descriptor) ||
		!reflect.DeepEqual(preparedBase.Config, preparation.SelectedBase.Config) {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("provider build realized base does not match its selection")
	}
	if err := providers.ValidateImageConfigPolicy(preparation.FinalImageConfig); err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("provider build final image config: %w", err)
	}

	options := input.RunOptions
	options.Context = ctx
	options.Progress = input.Progress
	graphCtx, endGraph := buildprofile.Start(ctx, "Execute provider graph")
	graphOptions := options
	graphOptions.Context = graphCtx
	graph, err := backend.executeGraph(graphCtx, PreparedPythonGraphExecutionInput{
		Store: preparation.Store, Plan: preparedBase.Plan, BaseDescriptor: preparedBase.Descriptor,
		BaseCatalog: preparedBase.Catalog, Sources: preparation.Loaded.Request.Sources,
		SourceWheels:   append([]providerstore.ArtifactDescriptor{}, input.SourceWheels...),
		LocalOverrides: append([]PythonLocalOverrideV1{}, input.LocalOverrides...),
		CurrentLock:    preparation.ReusableLock, FinalImageConfig: preparation.FinalImageConfig,
		Progress: input.Progress, BuildProgress: input.BuildProgress, RunOptions: graphOptions,
	})
	endGraph(err)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("execute provider graph: %w", err)
	}
	writeProviderBuildProgress(input.Progress, "assembling environment runtime plan")
	buildprogress.Report(input.BuildProgress, buildprogress.Event{
		Phase: buildprogress.PhaseAssemble, Detail: "Assembling environment runtime plan",
	})
	resolvedRequest, relevantPackageOverrides, err := finalizeResolvedRequestV1(
		preparation.Loaded.Document, preparation.Loaded.State.Overlay, preparation.Loaded.PackageOverrides,
		preparation.Loaded.Request, graph,
	)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("finalize provider request: %w", err)
	}
	plans, err := RuntimePlansV1(preparation.Loaded.Document, preparation.DockerPlan)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	policy, err := CompileRuntimePolicyV1(preparation.Loaded.Document, graph, plans)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	validationCtx, endValidationPlan := buildprofile.Start(ctx, "Prepare final image validation")
	validation, err := backend.prepareValidation(
		validationCtx, preparedBase.Descriptor, preparedBase.Catalog, graph, policy,
	)
	endValidationPlan(err)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, fmt.Errorf("prepare provider build validation: %w", err)
	}
	if input.ValidateChoices {
		writeProviderBuildProgress(input.Progress, "validating build and caching result")
	} else {
		writeProviderBuildProgress(input.Progress, "validating build and publishing image")
	}
	publishDetail := "Validating build and publishing image"
	if input.ValidateChoices {
		publishDetail = "Validating build and caching result"
	}
	buildprogress.Report(input.BuildProgress, buildprogress.Event{
		Phase: buildprogress.PhasePublish, Detail: publishDetail,
	})
	completeCtx, endComplete := buildprofile.Start(ctx, "Validate and publish environment")
	completeOptions := options
	completeOptions.Context = completeCtx
	completed, err := backend.complete(completeCtx, preparation.Operation, preparation.Store, ProviderBuildCompletionInput{
		Environment: preparation.Environment, DeploymentDir: preparation.DeploymentDir,
		Document: preparation.Loaded.Document, DockerPlan: preparation.DockerPlan,
		ResolvedRequest: resolvedRequest, Overlay: preparation.Loaded.State.Overlay,
		PackageOverrides: relevantPackageOverrides,
		Base:             preparedBase.Descriptor, BaseCatalog: preparedBase.Catalog,
		Graph: graph, Validation: validation,
		ValidateChoices: input.ValidateChoices, ValidatedInputs: preparation.ValidatedInputs,
		NoCache:       preparation.NoCache,
		RunValidation: input.RunValidation, RunOptions: completeOptions,
	})
	endComplete(err)
	if err != nil {
		return LockedProviderBuildExecutionResultV1{}, err
	}
	if completed.Validated {
		writeValidatedBuildCleanupWarning(input.Progress, completed.ValidatedBuild)
	}
	return LockedProviderBuildExecutionResultV1{
		State: completed.State, Lock: completed.Lock, Validated: completed.Validated,
	}, nil
}

func writeValidatedBuildCleanupWarning(output io.Writer, record deploy.ValidatedBuildV1) {
	count := len(record.PendingCleanup)
	if count == 0 {
		return
	}
	noun := "references are"
	if count == 1 {
		noun = "reference is"
	}
	writeProviderBuildProgress(
		output,
		"warning: validation succeeded, but cleanup of %d superseded cached image %s pending; Reploy will retry automatically",
		count,
		noun,
	)
}
