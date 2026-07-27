package dockerdeploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type InfoOptions struct {
	Dir string
}

func Info(options InfoOptions) (string, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	schema, err := runtimeStateSchema(options.Dir)
	if err != nil {
		return "", err
	}
	if schema != deploy.StateSchemaV1 {
		return "", fmt.Errorf("deployment state schema %q is unsupported; expected %q", schema, deploy.StateSchemaV1)
	}
	return infoStateV1(options.Dir)
}

func infoStateV1(dir string) (string, error) {
	content, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		return "", fmt.Errorf("read deployment state: %w", err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		return "", err
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return "", err
	}
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	phase := "staged"
	if state.Deployment != nil {
		phase = "installed"
	}
	lines := []string{
		fmt.Sprintf("deployment: %s", absoluteDir),
		"target: docker",
		fmt.Sprintf("phase: %s", phase),
		fmt.Sprintf("environment: %s", document.Environment.ID),
		fmt.Sprintf("platform: %s", state.Platform.Canonical),
	}
	if state.Current == nil {
		lines = append(lines, "resolved: not built", "materialized image: not built")
	} else {
		lines = append(lines,
			fmt.Sprintf("resolved: build lock %s", state.Current.BuildLockDigest),
			fmt.Sprintf("materialized image: %s", state.Current.Reference),
		)
	}
	lines = append(lines, "request overlay:")
	if len(state.Overlay.SelectedOptions) == 0 && len(state.Overlay.DirectPackages) == 0 {
		lines = append(lines, "  (empty)")
	} else {
		for _, option := range state.Overlay.SelectedOptions {
			lines = append(lines, fmt.Sprintf("  - option %s/%s", option.Component, option.Option))
		}
		for _, request := range state.Overlay.DirectPackages {
			lines = append(lines, fmt.Sprintf("  - package %s [%s]", request.Component, request.Package.Schema))
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}
