package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

type MaterializationBuildPlan struct {
	BaseReference  string
	Platform       blueprint.Platform
	DockerfilePath string
	ContextDir     string
	IIDFile        string
	NoCache        bool
}

func MaterializationBuildCommand(plan MaterializationBuildPlan) (CommandSpec, error) {
	if err := validateDockerBuildBaseReference(plan.BaseReference); err != nil {
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
	args := []string{"build"}
	if plan.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args,
		"--file", plan.DockerfilePath,
		"--platform", plan.Platform.Canonical,
		"--build-arg", "REPLOY_BASE_IMAGE="+plan.BaseReference,
		"--iidfile", plan.IIDFile,
		plan.ContextDir,
	)
	return CommandSpec{Name: "docker", Args: args, Env: []string{"DOCKER_BUILDKIT=1"}}, nil
}

func validateDockerBuildBaseReference(reference string) error {
	if reference == "" || strings.TrimSpace(reference) != reference || strings.ContainsAny(reference, "\r\n\t") {
		return fmt.Errorf("materialization build requires a safe base reference")
	}
	if strings.HasPrefix(reference, temporaryBuildReferencePrefix) {
		name, tag, found := strings.Cut(reference, ":")
		if !found || name == temporaryBuildReferencePrefix || tag == "" || strings.Contains(tag, ":") {
			return fmt.Errorf("materialization build temporary base reference is malformed")
		}
		return nil
	}
	digest := reference
	if repository, value, found := strings.Cut(reference, "@"); found {
		if repository == "" || strings.ContainsAny(repository, " \t\r\n") || strings.Contains(value, "@") {
			return fmt.Errorf("materialization build base reference is malformed")
		}
		digest = value
	}
	if err := canonical.Digest(digest).Validate(); err != nil {
		return fmt.Errorf("materialization build base reference must be a verified temporary reference or contain an immutable sha256 digest: %w", err)
	}
	return nil
}
