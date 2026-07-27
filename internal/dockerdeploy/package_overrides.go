package dockerdeploy

import (
	"fmt"
	"path/filepath"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

func LoadStagedPackageOverridesV1(
	operation *deploy.OperationLock,
	deploymentDir string,
	document blueprint.Document,
) (
	deploy.PackageOverridesV1,
	deploy.ResolvedPackageOverridesV1,
	deploy.PackageOverrideIntentV1,
	error,
) {
	if operation == nil {
		return deploy.PackageOverridesV1{}, deploy.ResolvedPackageOverridesV1{}, deploy.PackageOverrideIntentV1{}, fmt.Errorf("load staged package overrides requires an operation lock")
	}
	if err := operation.RequireHeld(); err != nil {
		return deploy.PackageOverridesV1{}, deploy.ResolvedPackageOverridesV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	absoluteDir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return deploy.PackageOverridesV1{}, deploy.ResolvedPackageOverridesV1{}, deploy.PackageOverrideIntentV1{}, fmt.Errorf("resolve package override deployment directory: %w", err)
	}
	wantLock := filepath.Join(absoluteDir, ".reploy", "operation.lock")
	if filepath.Clean(operation.Path()) != wantLock {
		return deploy.PackageOverridesV1{}, deploy.ResolvedPackageOverridesV1{}, deploy.PackageOverrideIntentV1{}, fmt.Errorf("package override operation lock does not belong to deployment %q", absoluteDir)
	}
	raw, found, err := deploy.ReadPackageOverridesV1(absoluteDir)
	if err != nil {
		return deploy.PackageOverridesV1{}, deploy.ResolvedPackageOverridesV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	if !found {
		raw = deploy.EmptyPackageOverridesV1(document.Environment.ID)
	}
	if raw.Environment.ID != document.Environment.ID {
		return deploy.PackageOverridesV1{}, deploy.ResolvedPackageOverridesV1{}, deploy.PackageOverrideIntentV1{}, fmt.Errorf(
			"package overrides target environment %q, want %q",
			raw.Environment.ID, document.Environment.ID,
		)
	}
	resolved, err := deploy.ResolvePackageOverridesV1(raw, absoluteDir, registry.NormalizePackageOverrideV1)
	if err != nil {
		return deploy.PackageOverridesV1{}, deploy.ResolvedPackageOverridesV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	intent, err := resolved.Intent()
	if err != nil {
		return deploy.PackageOverridesV1{}, deploy.ResolvedPackageOverridesV1{}, deploy.PackageOverrideIntentV1{}, err
	}
	return raw, resolved, intent, nil
}
