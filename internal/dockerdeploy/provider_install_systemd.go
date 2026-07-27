package dockerdeploy

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

func providerInstallSystemdFileV1(plan providerInstallationPlanV1, dockerPath string, includeDockerUnit bool) ([]providerInstallFileCandidateV1, error) {
	if plan.Backend != installBackendLinuxSystemd {
		return []providerInstallFileCandidateV1{}, nil
	}
	if err := deploy.ValidateInstallationStateV1(plan.Installation); err != nil {
		return nil, fmt.Errorf("install systemd file: %w", err)
	}
	if plan.Installation.Status != deploy.InstallationStatusReady {
		return nil, fmt.Errorf("install systemd file requires a ready installation plan")
	}
	if plan.Installation.UnitPath == "" {
		return nil, fmt.Errorf("install systemd file requires a unit path")
	}
	if dockerPath == "" || !filepath.IsAbs(dockerPath) || filepath.Clean(dockerPath) != dockerPath {
		return nil, fmt.Errorf("install systemd file requires an absolute clean Docker path")
	}

	workingDirectory, err := systemdInstallArgumentV1(systemdPath(plan.Installation.TargetDir))
	if err != nil {
		return nil, err
	}
	runtimePath := systemdPath(plan.Installation.TargetDir, embeddedRuntimeFileName())
	composeArguments := []string{
		dockerPath, "compose",
		"--project-name", plan.Installation.ComposeProject,
		"--project-directory", systemdPath(plan.Installation.TargetDir),
		"--env-file", systemdPath(plan.Installation.TargetDir, DockerEnvFileName),
		"-f", systemdPath(plan.Installation.TargetDir, ComposeFileName),
	}
	startArguments := []string{
		runtimePath,
		"_service-container",
		"--dir", systemdPath(plan.Installation.TargetDir),
		"--docker", dockerPath,
		"run",
	}
	stopArguments := append(append([]string{}, composeArguments...), "down")
	stopArguments = append(stopArguments, "--remove-orphans")
	start, err := systemdInstallCommandV1(startArguments)
	if err != nil {
		return nil, err
	}
	stop, err := systemdInstallCommandV1(stopArguments)
	if err != nil {
		return nil, err
	}

	dockerUnit := ""
	if includeDockerUnit {
		dockerUnit = "Requires=docker.service\nAfter=docker.service\n"
	}
	content := fmt.Sprintf(`[Unit]
Description=Reploy Docker service (%s)
# Managed-By: reploy
# Reploy-Service: %s
# Reploy-Target: %s
# Reploy-Compose-Project: %s
%s
[Service]
Type=notify
NotifyAccess=main
WorkingDirectory=%s
ExecStart=%s
ExecStop=%s
Restart=on-failure

[Install]
WantedBy=multi-user.target
`, plan.Installation.Service, plan.Installation.Service, systemdPath(plan.Installation.TargetDir), plan.Installation.ComposeProject, dockerUnit, workingDirectory, start, stop)
	return []providerInstallFileCandidateV1{{
		Path: plan.Installation.UnitPath, Content: []byte(content), Mode: 0o644,
	}}, nil
}

func systemdInstallCommandV1(arguments []string) (string, error) {
	if len(arguments) == 0 {
		return "", fmt.Errorf("systemd install command requires arguments")
	}
	quoted := make([]string, len(arguments))
	for index, argument := range arguments {
		value, err := systemdInstallArgumentV1(argument)
		if err != nil {
			return "", fmt.Errorf("systemd install command argument %d: %w", index, err)
		}
		quoted[index] = value
	}
	return strings.Join(quoted, " "), nil
}

func systemdInstallArgumentV1(value string) (string, error) {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return "", fmt.Errorf("systemd install argument contains a control character")
		}
	}
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`%`, `%%`,
	).Replace(value)
	return `"` + escaped + `"`, nil
}
