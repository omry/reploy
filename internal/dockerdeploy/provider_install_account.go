package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

type providerInstallAccountBackendV1 struct {
	resolve              func(map[string]string) (resolvedInstallOwner, error)
	creationReadiness    func(map[string]string, error) (string, error)
	bulkDiskRequirements func(providerstore.Store, CurrentBuild, string) ([]providerInstallDiskRequirementV1, error)
	preflight            func([]providerInstallDiskRequirementV1) error
	create               func(map[string]string) error
}

func prepareProviderInstallAccountV1(
	ctx context.Context,
	runAs blueprint.RunAs,
	sourceStore providerstore.Store,
	sourceBuild CurrentBuild,
	input providerInstallRunInputV1,
) (providerInstallRunInputV1, error) {
	return prepareProviderInstallAccountWithV1(ctx, runAs, sourceStore, sourceBuild, input, providerInstallAccountBackendV1{
		resolve:              resolveInstallOwner,
		creationReadiness:    installOwnerCreationSpecForResolveError,
		bulkDiskRequirements: providerInstallAccountBulkDiskRequirementsV1,
		preflight:            preflightProviderInstallDiskSpaceV1,
		create:               createMissingInstallOwner,
	})
}

func prepareProviderInstallAccountWithV1(
	ctx context.Context,
	runAs blueprint.RunAs,
	sourceStore providerstore.Store,
	sourceBuild CurrentBuild,
	input providerInstallRunInputV1,
	backend providerInstallAccountBackendV1,
) (providerInstallRunInputV1, error) {
	if ctx == nil {
		return providerInstallRunInputV1{}, fmt.Errorf("prepare provider install account requires a context")
	}
	if err := ctx.Err(); err != nil {
		return providerInstallRunInputV1{}, err
	}
	scope, err := ParseInstallScope(string(input.Install.Scope))
	if err != nil {
		return providerInstallRunInputV1{}, err
	}
	input.Install.SystemUser = ""
	input.Install.SystemGroup = ""
	input.Install.SystemUID = 0
	input.Install.SystemGID = 0
	if scope != InstallScopeSystem {
		return input, nil
	}
	if backend.resolve == nil || backend.creationReadiness == nil || backend.bulkDiskRequirements == nil || backend.preflight == nil || backend.create == nil {
		return providerInstallRunInputV1{}, fmt.Errorf("prepare provider install account requires a complete backend")
	}

	values, err := providerInstallAccountValuesV1(runAs)
	if err != nil {
		return providerInstallRunInputV1{}, err
	}
	owner, resolveErr := backend.resolve(values)
	if resolveErr == nil {
		return providerInstallInputWithAccountV1(input, runAs, owner), nil
	}
	if _, err := backend.creationReadiness(values, resolveErr); err != nil {
		return providerInstallRunInputV1{}, fmt.Errorf("resolve system install account: %w", err)
	}
	requirements, err := backend.bulkDiskRequirements(sourceStore, sourceBuild, input.DestinationDeploymentDir)
	if err != nil {
		return providerInstallRunInputV1{}, fmt.Errorf("check disk space before creating system install account: %w", err)
	}
	if err := backend.preflight(requirements); err != nil {
		return providerInstallRunInputV1{}, fmt.Errorf("check disk space before creating system install account: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return providerInstallRunInputV1{}, err
	}
	if err := backend.create(values); err != nil {
		return providerInstallRunInputV1{}, fmt.Errorf("create system install account: %w", err)
	}
	owner, err = backend.resolve(values)
	if err != nil {
		return providerInstallRunInputV1{}, fmt.Errorf("resolve system install account after creation: %w", err)
	}
	return providerInstallInputWithAccountV1(input, runAs, owner), nil
}

func providerInstallAccountValuesV1(runAs blueprint.RunAs) (map[string]string, error) {
	userName := strings.TrimSpace(runAs.User)
	groupName := strings.TrimSpace(runAs.Group)
	if userName == "" || groupName == "" {
		return nil, fmt.Errorf("environment.install.system.run_as must name both user and group for a system install")
	}
	return map[string]string{
		reployInstallOwnerEnv:       userName + ":" + groupName,
		reployInstallOwnerOnMissing: strings.TrimSpace(runAs.OnMissing),
	}, nil
}

func providerInstallInputWithAccountV1(input providerInstallRunInputV1, runAs blueprint.RunAs, owner resolvedInstallOwner) providerInstallRunInputV1 {
	input.Install.SystemUser = strings.TrimSpace(runAs.User)
	input.Install.SystemGroup = strings.TrimSpace(runAs.Group)
	input.Install.SystemUID = owner.UID
	input.Install.SystemGID = owner.GID
	return input
}

// providerInstallAccountBulkDiskRequirementsV1 covers the large, already-known
// store transfer before on_missing:create changes the host account database.
// The complete exact preflight still runs after account resolution and install
// planning, before any destination files are prepared or published.
func providerInstallAccountBulkDiskRequirementsV1(
	sourceStore providerstore.Store,
	sourceBuild CurrentBuild,
	destinationDir string,
) ([]providerInstallDiskRequirementV1, error) {
	destinationDir, err := filepath.Abs(destinationDir)
	if err != nil {
		return nil, fmt.Errorf("resolve install destination: %w", err)
	}
	_, closureBytes, err := deploy.InspectBuildLockStoreClosure(
		sourceBuild.Lock,
		sourceStore,
		registry.ValidateRequirementProfileV1,
		registry.ValidateResolvedBundlePayloadV1,
	)
	if err != nil {
		return nil, err
	}
	return []providerInstallDiskRequirementV1{{
		Path:  filepath.Join(destinationDir, ".reploy", providerstore.StoreDirName),
		Bytes: closureBytes,
	}}, nil
}
