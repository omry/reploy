package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

type MaterializationBuildPlan struct {
	BaseIdentity   string
	Platform       blueprint.Platform
	DockerfilePath string
	ContextDir     string
	IIDFile        string
}

func MaterializationBuildCommand(plan MaterializationBuildPlan) (CommandSpec, error) {
	if err := validateImmutableDockerIdentity(plan.BaseIdentity); err != nil {
		return CommandSpec{}, err
	}
	if err := plan.Platform.Validate(); err != nil {
		return CommandSpec{}, fmt.Errorf("materialization build platform: %w", err)
	}
	if plan.Platform.OS != "linux" {
		return CommandSpec{}, fmt.Errorf("materialization Docker build platform must use Linux")
	}
	paths := []struct {
		field string
		value string
	}{
		{field: "Dockerfile", value: plan.DockerfilePath},
		{field: "context", value: plan.ContextDir},
		{field: "IID file", value: plan.IIDFile},
	}
	for _, item := range paths {
		if item.value == "" || !filepath.IsAbs(item.value) || filepath.Clean(item.value) != item.value {
			return CommandSpec{}, fmt.Errorf("materialization build %s path must be absolute and clean", item.field)
		}
	}
	args := []string{
		"build",
		"--file", plan.DockerfilePath,
		"--platform", plan.Platform.Canonical,
		"--build-arg", "REPLOY_BASE_IMAGE=" + plan.BaseIdentity,
		"--iidfile", plan.IIDFile,
		plan.ContextDir,
	}
	return CommandSpec{Name: "docker", Args: args, Env: []string{"DOCKER_BUILDKIT=1"}}, nil
}

func validateImmutableDockerIdentity(identity string) error {
	if identity == "" || strings.TrimSpace(identity) != identity {
		return fmt.Errorf("materialization build requires an immutable base identity")
	}
	digest := identity
	if repository, value, found := strings.Cut(identity, "@"); found {
		if repository == "" || strings.ContainsAny(repository, " \t\r\n") || strings.Contains(value, "@") {
			return fmt.Errorf("materialization build base identity is malformed")
		}
		digest = value
	}
	if err := canonical.Digest(digest).Validate(); err != nil {
		return fmt.Errorf("materialization build base identity must contain an immutable sha256 digest: %w", err)
	}
	return nil
}
