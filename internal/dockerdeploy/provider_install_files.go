package dockerdeploy

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type providerInstallFileCandidateV1 struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
}

func providerInstallFilesV1(plan providerInstallationPlanV1, dockerPath string, includeDockerUnit bool) ([]providerInstallFileCandidateV1, error) {
	dockerFiles, err := providerInstallDockerFilesV1(plan)
	if err != nil {
		return nil, err
	}
	systemdFiles, err := providerInstallSystemdFileV1(plan, dockerPath, includeDockerUnit)
	if err != nil {
		return nil, err
	}
	candidates := append(dockerFiles, systemdFiles...)
	sort.Slice(candidates, func(left int, right int) bool { return candidates[left].Path < candidates[right].Path })
	for index, candidate := range candidates {
		if err := validateProviderInstallFileCandidateV1(candidate); err != nil {
			return nil, fmt.Errorf("install file candidate %d: %w", index, err)
		}
		if index > 0 && candidates[index-1].Path == candidate.Path {
			return nil, fmt.Errorf("install file candidates repeat destination %q", candidate.Path)
		}
	}
	return candidates, nil
}

// providerInstallDockerFilesV1 renders the installed Compose and docker.env
// files without touching the filesystem. Other deployment and host-service
// files are planned separately.
func providerInstallDockerFilesV1(plan providerInstallationPlanV1) ([]providerInstallFileCandidateV1, error) {
	if err := deploy.ValidateInstallationStateV1(plan.Installation); err != nil {
		return nil, fmt.Errorf("install Docker files: %w", err)
	}
	if plan.Installation.Status != deploy.InstallationStatusReady {
		return nil, fmt.Errorf("install Docker files require a ready installation plan")
	}
	if len(plan.Rendered.Compose) == 0 {
		return nil, fmt.Errorf("install Docker files require rendered Compose content")
	}
	environment, err := renderProviderInstallEnvironmentV1(plan.Rendered.Environment)
	if err != nil {
		return nil, err
	}
	candidates := []providerInstallFileCandidateV1{
		{
			Path:    filepath.Join(plan.Installation.TargetDir, ComposeFileName),
			Content: append([]byte(nil), plan.Rendered.Compose...),
			Mode:    0o644,
		},
		{
			Path:    filepath.Join(plan.Installation.TargetDir, DockerEnvFileName),
			Content: environment,
			Mode:    0o644,
		},
	}
	sort.Slice(candidates, func(left int, right int) bool { return candidates[left].Path < candidates[right].Path })
	return candidates, nil
}

func renderProviderInstallEnvironmentV1(values map[string]string) ([]byte, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("install Docker files require a nonempty rendered environment map")
	}
	names := make([]string, 0, len(values))
	for name, value := range values {
		if !validProviderInstallEnvironmentNameV1(name) {
			return nil, fmt.Errorf("install environment name %q is invalid", name)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("install environment value %q cannot be represented in docker.env", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{"# Private Reploy runtime inputs."}
	for _, name := range names {
		lines = append(lines, name+"="+values[name])
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func validProviderInstallEnvironmentNameV1(name string) bool {
	if name == "" || name[0] != '_' && (name[0] < 'A' || name[0] > 'Z') {
		return false
	}
	for index := 1; index < len(name); index++ {
		char := name[index]
		if char == '_' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}
