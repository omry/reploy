package dockerdeploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
)

const dockerNativePlatformFormat = "{{.Server.Os}}/{{.Server.Arch}}"

// ProbeDockerNativePlatform reports the target platform of the selected Docker
// daemon. Callers only need this when selection is otherwise ambiguous.
func ProbeDockerNativePlatform(ctx context.Context) (blueprint.Platform, error) {
	return probeDockerNativePlatform(ctx, runDockerOutput)
}

func probeDockerNativePlatform(ctx context.Context, run dockerOutputRunner) (blueprint.Platform, error) {
	if ctx == nil {
		return blueprint.Platform{}, fmt.Errorf("probe Docker native platform requires a context")
	}
	output, err := run(ctx, "version", "--format", dockerNativePlatformFormat)
	if err != nil {
		return blueprint.Platform{}, fmt.Errorf("probe Docker native platform: %w", err)
	}
	value := strings.TrimSpace(output)
	platform, err := blueprint.ParsePlatform(value)
	if err != nil {
		return blueprint.Platform{}, fmt.Errorf("probe Docker native platform returned %q: %w", value, err)
	}
	return platform, nil
}

// SelectDockerTargetPlatform selects the explicit OCI target used by every
// later Docker and provider operation. Native may be nil when an explicit
// target was requested or the blueprint declares exactly one target.
func SelectDockerTargetPlatform(document blueprint.Document, explicit string, native *blueprint.Platform) (blueprint.Platform, error) {
	declared := document.Blueprint.Compatibility.Platforms
	if err := validateDockerCompatibility(declared); err != nil {
		return blueprint.Platform{}, err
	}

	if explicit != "" {
		selected, err := blueprint.ParsePlatform(explicit)
		if err != nil {
			return blueprint.Platform{}, fmt.Errorf("select Docker target platform: %w", err)
		}
		return validateDockerTargetPlatform(document, selected)
	}

	if len(declared) == 1 {
		return validateDockerTargetPlatform(document, declared[0])
	}
	if native == nil {
		return blueprint.Platform{}, fmt.Errorf("select Docker target platform: blueprint declares multiple platforms; query the Docker daemon's native platform or specify --platform")
	}
	if err := native.Validate(); err != nil {
		return blueprint.Platform{}, fmt.Errorf("select Docker native platform: %w", err)
	}

	matches := make([]blueprint.Platform, 0, 1)
	for _, candidate := range declared {
		if dockerPlatformDeclarationCovers(candidate, *native) {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return blueprint.Platform{}, fmt.Errorf("select Docker target platform: blueprint does not support the daemon's native platform %q; specify --platform", native.Canonical)
	case 1:
		return validateDockerTargetPlatform(document, *native)
	default:
		return blueprint.Platform{}, fmt.Errorf("select Docker target platform: daemon native platform %q matches multiple blueprint declarations", native.Canonical)
	}
}

func validateDockerCompatibility(declared []blueprint.Platform) error {
	if len(declared) == 0 {
		return fmt.Errorf("select Docker target platform: resolved blueprint declares no compatible platforms")
	}
	for index, platform := range declared {
		if err := platform.Validate(); err != nil {
			return fmt.Errorf("select Docker target platform: declared compatibility platform: %w", err)
		}
		if index > 0 && declared[index-1].Canonical >= platform.Canonical {
			return fmt.Errorf("select Docker target platform: resolved blueprint compatibility platforms must be unique and sorted")
		}
	}
	return nil
}

func validateDockerTargetPlatform(document blueprint.Document, selected blueprint.Platform) (blueprint.Platform, error) {
	if err := blueprint.ValidateSelectedPlatform(document, selected); err != nil {
		return blueprint.Platform{}, fmt.Errorf("select Docker target platform: %w", err)
	}
	if selected.OS != "linux" {
		return blueprint.Platform{}, fmt.Errorf("select Docker target platform: Docker environment images require a Linux target, got %q", selected.Canonical)
	}
	return selected, nil
}

func dockerPlatformDeclarationCovers(declared blueprint.Platform, selected blueprint.Platform) bool {
	return declared.OS == selected.OS &&
		declared.Architecture == selected.Architecture &&
		(declared.Variant == "" || declared.Variant == selected.Variant)
}
