package dockerdeploy

import (
	"context"
	"fmt"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type DirectProviderInstallInputV1 struct {
	Pack             deploy.PackRef
	ExplicitPlatform string
	Target           string
	Runtime          StagedProviderBuildRuntimeV1
	ControlMode      ControlAdmissionModeV1
	Scope            InstallScope
	Service          string
	PortOverrides    []PortOverride
	Replace          []string
	Clean            bool
	Start            bool
	RunOptions       RunOptions
}

type DirectProviderInstallResultV1 struct {
	Target string
	State  deploy.StateV1
}

type directProviderInstallEntryBackendV1 struct {
	withSource func(context.Context, directProviderInstallSourceInputV1, func(context.Context, string) error) error
	readState  func(context.Context, string) (deploy.StateV1, error)
	install    func(context.Context, ProviderInstallInputV1) (deploy.StateV1, error)
	roots      func(string) (installTargetRootsV1, error)
}

func RunDirectProviderInstallV1(ctx context.Context, input DirectProviderInstallInputV1) (DirectProviderInstallResultV1, error) {
	return runDirectProviderInstallEntryV1(ctx, input, directProviderInstallEntryBackendV1{
		withSource: withDirectProviderInstallSourceV1,
		readState:  readProviderInstallSourceStateV1,
		install:    RunProviderInstallV1,
		roots:      installTargetRoots,
	})
}

func runDirectProviderInstallEntryV1(
	ctx context.Context,
	input DirectProviderInstallInputV1,
	backend directProviderInstallEntryBackendV1,
) (result DirectProviderInstallResultV1, err error) {
	if ctx == nil {
		return result, fmt.Errorf("direct provider install requires a context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if backend.withSource == nil || backend.readState == nil || backend.install == nil || backend.roots == nil {
		return result, fmt.Errorf("direct provider install requires a complete backend")
	}
	err = backend.withSource(ctx, directProviderInstallSourceInputV1{
		Pack: input.Pack, ExplicitPlatform: input.ExplicitPlatform,
	}, func(ctx context.Context, sourceDir string) error {
		state, err := backend.readState(ctx, sourceDir)
		if err != nil {
			return fmt.Errorf("read direct install source: %w", err)
		}
		document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
		if err != nil {
			return fmt.Errorf("decode direct install blueprint: %w", err)
		}
		platform, err := installHostPlatformV1(input.Runtime.Host)
		if err != nil {
			return err
		}
		roots, err := backend.roots(platform.GOOS)
		if err != nil {
			return err
		}
		target, err := blueprint.ResolveInstallTarget(document.Environment.Install.Target, document.Environment.ID, blueprint.InstallTargetContext{
			Host: input.Runtime.Host, Scope: blueprint.InstallScope(input.Scope), Override: input.Target,
			Paths:     blueprint.HostPaths{Home: roots.UserHome, UserData: roots.UserData, LocalData: roots.UserLocalData, SystemData: roots.SystemData},
			Variables: document.Environment.Vars,
		})
		if err != nil {
			return err
		}
		installed, err := backend.install(ctx, ProviderInstallInputV1{
			SourceDeploymentDir: sourceDir, DestinationDeploymentDir: target,
			Runtime: input.Runtime, ControlMode: input.ControlMode, Scope: input.Scope, Service: input.Service,
			PortOverrides: append([]PortOverride(nil), input.PortOverrides...),
			Replace:       append([]string(nil), input.Replace...), Clean: input.Clean, Start: input.Start,
			RunOptions: input.RunOptions,
		})
		if err != nil {
			return err
		}
		result = DirectProviderInstallResultV1{Target: target, State: installed}
		return nil
	})
	return result, err
}

func readProviderInstallSourceStateV1(ctx context.Context, dir string) (state deploy.StateV1, err error) {
	operation, err := deploy.AcquireOperationLock(ctx, dir)
	if err != nil {
		return state, err
	}
	defer func() {
		if unlockErr := operation.Unlock(); err == nil && unlockErr != nil {
			err = unlockErr
		}
	}()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return state, err
	}
	if !found {
		return state, fmt.Errorf("direct install source state is missing")
	}
	return state, nil
}
