package dockerdeploy

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
)

func cleanManifestPath(value string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(value))))
}

type installTargetRootsV1 struct {
	UserHome          string
	UserData          string
	UserLocalData     string
	SystemData        string
	ReployInstallRoot string
}

func installTargetRoots(goos string) (installTargetRootsV1, error) {
	home := installTargetHome(goos)
	switch installTargetHostKey(goos) {
	case "windows":
		localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if localAppData == "" {
			return installTargetRootsV1{}, fmt.Errorf("LOCALAPPDATA is required for the default Windows install target; pass --to to choose an install directory")
		}
		userData := strings.TrimSpace(os.Getenv("APPDATA"))
		if userData == "" {
			userData = localAppData
		}
		systemData := strings.TrimSpace(os.Getenv("ProgramData"))
		if systemData == "" {
			systemData = `C:\ProgramData`
		}
		return installTargetRootsV1{
			UserHome: home, UserData: userData, UserLocalData: localAppData, SystemData: systemData,
			ReployInstallRoot: strings.TrimRight(localAppData, `\/`) + `\Reploy\installs`,
		}, nil
	case "macos":
		if strings.TrimSpace(home) == "" {
			return installTargetRootsV1{}, fmt.Errorf("home directory is required for the default macOS install target; pass --to to choose an install directory")
		}
		userData := path.Join(home, "Library", "Application Support")
		return installTargetRootsV1{UserHome: home, UserData: userData, UserLocalData: userData, SystemData: path.Join("/", "Library", "Application Support"), ReployInstallRoot: path.Join(userData, "Reploy", "installs")}, nil
	default:
		if strings.TrimSpace(home) == "" {
			home = "/home"
		}
		return installTargetRootsV1{UserHome: home, UserData: path.Join(home, ".local", "share"), UserLocalData: path.Join(home, ".local", "share"), SystemData: path.Join("/", "var", "lib"), ReployInstallRoot: path.Join("/", "opt")}, nil
	}
}

func installTargetHostKey(goos string) string {
	if goos == "darwin" {
		return "macos"
	}
	return goos
}

func installTargetHome(goos string) string {
	if installTargetHostKey(goos) != "windows" {
		if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
			return home
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

func planEnvironmentInstallPathUpdates(document blueprint.Document, sourceDir string, targetDir string, scope InstallScope, replace []string, clean bool, goos string) ([]PathUpdateAction, []string, error) {
	host := blueprintHostForGOOS(goos)
	stagingMounts, err := planDockerMounts(document, DockerPlanContext{DeploymentDir: sourceDir, Phase: blueprint.PhaseStaged, Host: host})
	if err != nil {
		return nil, nil, err
	}
	installedScope := blueprint.InstallScope(scope)
	installedMounts, err := planDockerMounts(document, DockerPlanContext{DeploymentDir: sourceDir, InstallTarget: targetDir, Phase: blueprint.PhaseInstalled, Scope: &installedScope, Host: host})
	if err != nil {
		return nil, nil, err
	}
	replaceAll := false
	requested := []string{}
	installedByName := mountPlansByName(installedMounts)
	for _, value := range replace {
		value = cleanManifestPath(value)
		if value == "all" {
			replaceAll = true
			continue
		}
		name, err := environmentPathUpdateName(value, installedByName, targetDir)
		if err != nil {
			return nil, nil, err
		}
		requested = append(requested, name)
	}
	actions, err := PlanPathUpdates(DockerExecutionPlan{Mounts: stagingMounts}, DockerExecutionPlan{Mounts: installedMounts}, targetDir, PathUpdateOptions{ReplaceAll: replaceAll, Clean: clean, Replace: requested})
	if err != nil {
		return nil, nil, err
	}
	preserve := []string{}
	for _, action := range actions {
		if action.Kind != PathPreserveManagedBind {
			continue
		}
		relative, err := filepath.Rel(targetDir, action.Target)
		if err != nil {
			return nil, nil, err
		}
		preserve = append(preserve, filepath.ToSlash(relative))
	}
	return actions, preserve, nil
}

func environmentPathUpdateName(value string, installed map[string]MountExecutionPlan, targetDir string) (string, error) {
	if value == "." {
		return "", fmt.Errorf("--replace must not be empty")
	}
	if _, ok := installed[value]; ok {
		return value, nil
	}
	for name, mount := range installed {
		if mount.Mode != blueprint.MountManagedBind {
			continue
		}
		relative, err := filepath.Rel(targetDir, mount.Source)
		if err == nil && cleanManifestPath(filepath.ToSlash(relative)) == value {
			return name, nil
		}
	}
	return "", fmt.Errorf("unknown environment path %q; declared paths: %s", value, strings.Join(sortedMountPlanNames(installed), ", "))
}

func blueprintHostForGOOS(goos string) blueprint.HostOS {
	switch goos {
	case "darwin":
		return blueprint.HostMacOS
	case "windows":
		return blueprint.HostWindows
	default:
		return blueprint.HostLinux
	}
}

func validServiceName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '@', r == '-':
		default:
			return false
		}
	}
	return true
}

func installPathsOverlap(sourceDir string, targetDir string) bool {
	sourceDir = filepath.Clean(sourceDir)
	targetDir = filepath.Clean(targetDir)
	return sourceDir == targetDir || pathContains(sourceDir, targetDir) || pathContains(targetDir, sourceDir)
}

func pathContains(parent string, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func controlScriptName(appID string) string { return dockerNameSlug(appID, "app") + "ctl" }

func systemdPath(elements ...string) string {
	normalized := make([]string, 0, len(elements))
	for _, element := range elements {
		normalized = append(normalized, strings.ReplaceAll(element, `\`, "/"))
	}
	return path.Join(normalized...)
}
