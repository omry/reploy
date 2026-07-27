package dockerdeploy

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

const (
	stagingOutputPhase  = "STAGING"
	deployedOutputPhase = "DEPLOYED"
	stagingOutputColor  = "117"
	deployedOutputColor = "208"
)

func DeploymentOutputWriters(dir string, stdout io.Writer, stderr io.Writer) (io.Writer, io.Writer, error) {
	label, color, versioned, err := deploymentOutputPrefixForStateV1(dir)
	if err != nil {
		return nil, nil, err
	}
	if versioned {
		return newDeploymentOutputWriter(stdout, label, color), newDeploymentOutputWriter(stderr, label, color), nil
	}
	return stdout, stderr, nil
}

func DeploymentOutputPrefix(dir string, output io.Writer) (string, error) {
	label, color, versioned, err := deploymentOutputPrefixForStateV1(dir)
	if err != nil {
		return "", err
	}
	if versioned {
		return deploymentOutputPrefixText(output, label, color), nil
	}
	return "", nil
}

func deploymentOutputPrefixForStateV1(dir string) (string, string, bool, error) {
	schema, err := runtimeStateSchema(dir)
	if err != nil {
		return "", "", false, err
	}
	if schema == "" {
		return "", "", false, nil
	}
	if schema != deploy.StateSchemaV1 {
		return "", "", false, fmt.Errorf("deployment state schema %q is unsupported; expected %q", schema, deploy.StateSchemaV1)
	}
	content, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		return "", "", false, fmt.Errorf("read deployment state: %w", err)
	}
	state, err := deploy.DecodeStateV1(content)
	if err != nil {
		return "", "", false, err
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return "", "", false, err
	}
	phase, color := stagingOutputPhase, stagingOutputColor
	if state.Deployment != nil {
		phase, color = deployedOutputPhase, deployedOutputColor
	}
	return deploymentOutputLabel(phase, document.Environment.ID), color, true, nil
}

func deploymentOutputLabel(phase string, appID string) string {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return "[" + phase + "]"
	}
	return "[" + phase + " : " + appID + "]"
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
