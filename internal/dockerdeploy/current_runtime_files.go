package dockerdeploy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/omry/reploy/internal/deploy"
)

// PublishCurrentRuntimeInputsV1 renders and atomically replaces the Compose
// and environment files needed by the selected current plan. Byte-identical
// regular files with the expected mode are left untouched.
func PublishCurrentRuntimeInputsV1(
	operation *deploy.OperationLock,
	deploymentDir string,
	plan CurrentRuntimePlanV1,
) (changed bool, err error) {
	dir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return false, fmt.Errorf("resolve current runtime input directory: %w", err)
	}
	if err := validateCurrentRuntimeInputOperationV1(operation, dir); err != nil {
		return false, err
	}
	candidates, err := currentRuntimeInputCandidatesV1(dir, plan)
	if err != nil {
		return false, err
	}
	candidates, err = changedCurrentRuntimeCandidatesV1(candidates)
	if err != nil {
		return false, err
	}
	if len(candidates) == 0 {
		return false, nil
	}
	prepared, err := prepareProviderInstallFileCandidatesV1(candidates)
	if err != nil {
		return false, fmt.Errorf("prepare current runtime inputs: %w", err)
	}
	defer func() { err = errors.Join(err, prepared.Cleanup()) }()
	if err := prepared.Publish(); err != nil {
		return false, fmt.Errorf("publish current runtime inputs: %w", err)
	}
	return true, nil
}

// RequireCurrentRuntimeInputsV1 checks the rendered runtime files without
// creating, repairing, or replacing either file.
func RequireCurrentRuntimeInputsV1(operation *deploy.OperationLock, deploymentDir string, plan CurrentRuntimePlanV1) error {
	dir, err := filepath.Abs(deploymentDir)
	if err != nil {
		return fmt.Errorf("resolve current runtime input directory: %w", err)
	}
	if err := validateCurrentRuntimeInputOperationV1(operation, dir); err != nil {
		return err
	}
	candidates, err := currentRuntimeInputCandidatesV1(dir, plan)
	if err != nil {
		return err
	}
	changed, err := changedCurrentRuntimeCandidatesV1(candidates)
	if err != nil {
		return err
	}
	if len(changed) != 0 {
		return fmt.Errorf("runtime inputs are missing or stale; run `reploy up`")
	}
	return nil
}

func validateCurrentRuntimeInputOperationV1(operation *deploy.OperationLock, dir string) error {
	if operation == nil {
		return fmt.Errorf("current runtime inputs require an operation lock")
	}
	if err := operation.RequireHeld(); err != nil {
		return err
	}
	expectedLock := filepath.Join(dir, ".reploy", "operation.lock")
	if operation.Path() != expectedLock {
		return fmt.Errorf("runtime input operation lock does not belong to deployment %q", dir)
	}
	return nil
}

func currentRuntimeInputCandidatesV1(dir string, plan CurrentRuntimePlanV1) ([]providerInstallFileCandidateV1, error) {
	rendered, err := RenderDockerInputs(plan.Docker, plan.Document.Environment.ControlScript)
	if err != nil {
		return nil, fmt.Errorf("render current runtime inputs: %w", err)
	}
	environment, err := renderProviderInstallEnvironmentV1(rendered.Environment)
	if err != nil {
		return nil, fmt.Errorf("render current runtime environment: %w", err)
	}
	candidates := []providerInstallFileCandidateV1{
		{Path: filepath.Join(dir, DockerEnvFileName), Content: environment, Mode: 0o644},
		{Path: filepath.Join(dir, ComposeFileName), Content: append([]byte(nil), rendered.Compose...), Mode: 0o644},
	}
	sort.Slice(candidates, func(left int, right int) bool { return candidates[left].Path < candidates[right].Path })
	return candidates, nil
}

func changedCurrentRuntimeCandidatesV1(candidates []providerInstallFileCandidateV1) ([]providerInstallFileCandidateV1, error) {
	changed := make([]providerInstallFileCandidateV1, 0, len(candidates))
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate.Path)
		if os.IsNotExist(err) {
			changed = append(changed, candidate)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect current runtime input %q: %w", candidate.Path, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("current runtime input must be a regular file: %s", candidate.Path)
		}
		content, err := os.ReadFile(candidate.Path)
		if err != nil {
			return nil, fmt.Errorf("read current runtime input %q: %w", candidate.Path, err)
		}
		if !bytes.Equal(content, candidate.Content) || !providerInstallFileModeMatches(info.Mode(), candidate.Mode) {
			changed = append(changed, candidate)
		}
	}
	return changed, nil
}
