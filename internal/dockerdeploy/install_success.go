package dockerdeploy

import (
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

func PrintInstallSuccess(dir string, stdout io.Writer) error {
	if stdout == nil {
		return nil
	}
	lines, err := InstallSuccessLines(dir)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return nil
}

func InstallSuccessLines(dir string) ([]string, error) {
	success, err := installSuccessConfig(dir)
	if err != nil {
		return nil, err
	}
	if len(success.Vars) == 0 && len(success.Lines) == 0 {
		return nil, nil
	}
	values, err := resolveInstallSuccessVars(dir, success.Vars)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(success.Lines))
	for _, line := range success.Lines {
		lines = append(lines, expandInstallSuccessLine(line, values))
	}
	return lines, nil
}

func installSuccessConfig(dir string) (deploy.DockerInstallSuccessConfig, error) {
	state, err := loadState(dir)
	if err != nil {
		return deploy.DockerInstallSuccessConfig{}, err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return deploy.DockerInstallSuccessConfig{}, err
	}
	return pack.Docker.Install.Success, nil
}

func resolveInstallSuccessVars(dir string, vars map[string]deploy.DockerInstallSuccessVarConfig) (map[string]string, error) {
	values := make(map[string]string, len(vars))
	for name, variable := range vars {
		value, err := resolveInstallSuccessVar(dir, variable)
		if err != nil {
			return nil, fmt.Errorf("success variable %s: %w", name, err)
		}
		values[name] = value
	}
	return values, nil
}

func resolveInstallSuccessVar(dir string, variable deploy.DockerInstallSuccessVarConfig) (string, error) {
	if len(variable.App) > 0 {
		helper := filepath.Join(dir, "reploy")
		args := append([]string{"app"}, variable.App...)
		output, err := installRunCommandOutput(helper, args...)
		if err != nil {
			return "", commandErrorWithOutput("installed success app output", output, err)
		}
		value := strings.TrimSpace(string(output))
		if value == "" {
			return "", fmt.Errorf("app output is empty")
		}
		if strings.ContainsAny(value, "\t\r\n") {
			return "", fmt.Errorf("app output must be a single line")
		}
		return value, nil
	}
	if variable.ServerURL {
		return InstallServerURL(dir)
	}
	return "", fmt.Errorf("empty success variable")
}

func InstallServerURL(dir string) (string, error) {
	serverURL, err := ServerURL(dir)
	if err != nil {
		return "", err
	}
	baseURL := *serverURL
	baseURL.Path = ""
	baseURL.RawPath = ""
	baseURL.RawQuery = ""
	baseURL.Fragment = ""
	return (&url.URL{
		Scheme: baseURL.Scheme,
		Host:   baseURL.Host,
	}).String(), nil
}

func expandInstallSuccessLine(line string, values map[string]string) string {
	replacements := make([]string, 0, len(values)*2)
	for name, value := range values {
		replacements = append(replacements, "${"+name+"}", value)
	}
	replacer := strings.NewReplacer(replacements...)
	return replacer.Replace(line)
}
