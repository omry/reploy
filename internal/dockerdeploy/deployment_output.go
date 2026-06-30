package dockerdeploy

import (
	"bytes"
	"io"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

const (
	stagingOutputPhase  = "STAGING"
	deployedOutputPhase = "DEPLOYED"
	stagingOutputColor  = "117"
	deployedOutputColor = "208"
)

func deploymentOutputWritersForState(state deploy.DeploymentState, stdout io.Writer, stderr io.Writer) (io.Writer, io.Writer) {
	label, color, ok := deploymentOutputPrefixForState("", state)
	if !ok {
		return stdout, stderr
	}
	return newDeploymentOutputWriter(stdout, label, color), newDeploymentOutputWriter(stderr, label, color)
}

func deploymentOutputWritersForDeployment(dir string, state deploy.DeploymentState, stdout io.Writer, stderr io.Writer) (io.Writer, io.Writer) {
	label, color, ok := deploymentOutputPrefixForState(dir, state)
	if !ok {
		return stdout, stderr
	}
	return newDeploymentOutputWriter(stdout, label, color), newDeploymentOutputWriter(stderr, label, color)
}

func DeploymentOutputWriters(dir string, stdout io.Writer, stderr io.Writer) (io.Writer, io.Writer, error) {
	state, err := loadState(dir)
	if err != nil {
		return nil, nil, err
	}
	stdout, stderr = deploymentOutputWritersForDeployment(dir, state, stdout, stderr)
	return stdout, stderr, nil
}

func DeploymentOutputPrefix(dir string, output io.Writer) (string, error) {
	state, err := loadState(dir)
	if err != nil {
		return "", err
	}
	label, color, ok := deploymentOutputPrefixForState(dir, state)
	if !ok {
		return "", nil
	}
	return deploymentOutputPrefixText(output, label, color), nil
}

func deploymentOutputPrefixForState(dir string, state deploy.DeploymentState) (string, string, bool) {
	phase, color, ok := deploymentOutputPhase(state.Phase)
	if !ok {
		return "", "", false
	}
	return deploymentOutputLabel(phase, deploymentOutputAppID(dir, state)), color, true
}

func deploymentOutputPhase(phase deploy.Phase) (string, string, bool) {
	switch phase {
	case deploy.PhaseStaged:
		return stagingOutputPhase, stagingOutputColor, true
	case deploy.PhaseInstalled:
		return deployedOutputPhase, deployedOutputColor, true
	default:
		return "", "", false
	}
}

func deploymentOutputLabel(phase string, appID string) string {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return "[" + phase + "]"
	}
	return "[" + phase + " : " + appID + "]"
}

func deploymentOutputAppID(dir string, state deploy.DeploymentState) string {
	if strings.TrimSpace(state.AppID) != "" {
		return state.AppID
	}
	if dir == "" {
		return ""
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return ""
	}
	return pack.AppID
}

func newDeploymentOutputWriter(output io.Writer, label string, color string) io.Writer {
	if output == nil {
		return nil
	}
	prefix := deploymentOutputPrefixText(output, label, color) + " "
	return &linePrefixWriter{
		output:      output,
		prefix:      []byte(prefix),
		atLineStart: true,
	}
}

func deploymentOutputPrefixText(output io.Writer, label string, color string) string {
	if outputColorEnabled(output) {
		return "\x1b[38;5;" + color + "m" + label + "\x1b[0m"
	}
	return label
}

type linePrefixWriter struct {
	output      io.Writer
	prefix      []byte
	atLineStart bool
}

func (writer *linePrefixWriter) Write(content []byte) (int, error) {
	remaining := content
	for len(remaining) > 0 {
		if writer.atLineStart {
			if _, err := writer.output.Write(writer.prefix); err != nil {
				return 0, err
			}
			writer.atLineStart = false
		}
		newline := bytes.IndexByte(remaining, '\n')
		if newline == -1 {
			if _, err := writer.output.Write(remaining); err != nil {
				return 0, err
			}
			return len(content), nil
		}
		if _, err := writer.output.Write(remaining[:newline+1]); err != nil {
			return 0, err
		}
		writer.atLineStart = true
		remaining = remaining[newline+1:]
	}
	return len(content), nil
}
