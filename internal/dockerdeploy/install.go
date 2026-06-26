package dockerdeploy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/deploy"
)

type InstallOptions struct {
	Dir           string
	Target        string
	Service       string
	PortOverrides []PortOverride
	Start         bool
	DryRun        bool
	Stdout        io.Writer
}

type installPlan struct {
	SourceDir       string
	TargetDir       string
	Service         string
	UnitPath        string
	InstanceID      string
	ComposeProject  string
	ContainerName   string
	NetworkName     string
	Ports           []dockerPortBinding
	Start           bool
	ComposeOverride bool
}

const defaultSystemdUnitDir = "/etc/systemd/system"

var installGeteuid = os.Geteuid
var installLookPath = exec.LookPath
var installRunCommand = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
var installSystemdUnitDir = defaultSystemdUnitDir

func Install(options InstallOptions) error {
	plan, err := newInstallPlan(options)
	if err != nil {
		return err
	}
	doctorCode := Doctor(DoctorOptions{Dir: options.Dir, Preinstall: true})
	if doctorCode != 0 {
		return fmt.Errorf("preinstall doctor failed")
	}
	if options.DryRun {
		printInstallDryRun(options.Stdout, plan)
		return nil
	}
	if installGeteuid() != 0 {
		return fmt.Errorf("install requires root unless --dry-run is set")
	}
	return applyInstallPlan(plan)
}

func newInstallPlan(options InstallOptions) (installPlan, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if strings.TrimSpace(options.Target) == "" {
		return installPlan{}, fmt.Errorf("--to is required")
	}
	target, err := filepath.Abs(options.Target)
	if err != nil {
		return installPlan{}, err
	}
	if !filepath.IsAbs(options.Target) {
		return installPlan{}, fmt.Errorf("--to must be an absolute path: %s", options.Target)
	}
	if strings.ContainsAny(target, " \t\n") {
		return installPlan{}, fmt.Errorf("--to must not contain whitespace: %s", target)
	}
	absoluteDir, err := filepath.Abs(options.Dir)
	if err != nil {
		return installPlan{}, err
	}
	canonicalSourceDir, err := canonicalIdentityPath(absoluteDir)
	if err != nil {
		return installPlan{}, err
	}
	canonicalTargetDir, err := canonicalIdentityPath(target)
	if err != nil {
		return installPlan{}, err
	}
	if installPathsOverlap(canonicalSourceDir, canonicalTargetDir) {
		return installPlan{}, fmt.Errorf("--to must not overlap deployment directory: %s overlaps %s", target, absoluteDir)
	}
	if options.Service == "" {
		service, err := defaultInstallService(options.Dir)
		if err != nil {
			return installPlan{}, err
		}
		options.Service = service
	}
	if options.Service != "" && !validServiceName(options.Service) {
		return installPlan{}, fmt.Errorf("--service contains unsupported characters: %s", options.Service)
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return installPlan{}, err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return installPlan{}, err
	}
	instanceID, err := installedInstanceID(options.Service, target)
	if err != nil {
		return installPlan{}, err
	}
	service := dockerServiceDefaults(pack, instanceID)
	ports, err := dockerPortBindings(pack, service)
	if err != nil {
		return installPlan{}, err
	}
	ports, err = applyPortOverrides(ports, options.PortOverrides)
	if err != nil {
		return installPlan{}, err
	}
	_, overrideErr := os.Stat(filepath.Join(absoluteDir, ComposeOverrideFileName))
	if overrideErr != nil && !os.IsNotExist(overrideErr) {
		return installPlan{}, overrideErr
	}
	return installPlan{
		SourceDir:       absoluteDir,
		TargetDir:       target,
		Service:         options.Service,
		UnitPath:        filepath.Join(installSystemdUnitDir, options.Service+".service"),
		InstanceID:      instanceID,
		ComposeProject:  instanceID,
		ContainerName:   service.ContainerName,
		NetworkName:     service.NetworkName,
		Ports:           ports,
		Start:           options.Start,
		ComposeOverride: overrideErr == nil,
	}, nil
}

func defaultInstallService(dir string) (string, error) {
	state, err := loadState(dir)
	if err != nil {
		return "", err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return "", err
	}
	service := dockerNameSlug(pack.AppID, "reploy")
	if !validServiceName(service) {
		return "", fmt.Errorf("app id cannot be used as a systemd service name: %s", pack.AppID)
	}
	return service, nil
}

func printInstallDryRun(stdout io.Writer, plan installPlan) {
	if stdout == nil {
		return
	}
	fmt.Fprintf(stdout, "would install deployment: %s\n", plan.SourceDir)
	fmt.Fprintf(stdout, "target: %s\n", plan.TargetDir)
	fmt.Fprintf(stdout, "service: %s\n", plan.Service)
	fmt.Fprintf(stdout, "instance id: %s\n", plan.InstanceID)
	fmt.Fprintf(stdout, "compose project: %s\n", plan.ComposeProject)
	fmt.Fprintf(stdout, "container: %s\n", plan.ContainerName)
	fmt.Fprintf(stdout, "network: %s\n", plan.NetworkName)
	for _, port := range plan.Ports {
		fmt.Fprintf(stdout, "port %s: %s:%s -> %s\n", port.Name, port.HostBind, port.HostPort, port.ContainerPort)
	}
	fmt.Fprintf(stdout, "would write systemd unit: %s\n", plan.UnitPath)
	fmt.Fprintln(stdout, "would run: systemctl daemon-reload")
	fmt.Fprintf(stdout, "would run: systemctl enable %s.service\n", plan.Service)
	if plan.Start {
		fmt.Fprintf(stdout, "would run: systemctl restart %s.service\n", plan.Service)
		fmt.Fprintf(stdout, "would run: %s test\n", filepath.Join(plan.TargetDir, "reploy"))
		fmt.Fprintf(stdout, "would run: %s app config check --live\n", filepath.Join(plan.TargetDir, "reploy"))
	} else {
		fmt.Fprintln(stdout, "start: no")
	}
}

func validServiceName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_', r == '.', r == '@', r == '-':
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
	if err != nil || relative == "." {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func copyDeploymentTreeProtected(sourceDir string, targetDir string) error {
	sourceDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return err
	}
	targetDir, err = filepath.Abs(targetDir)
	if err != nil {
		return err
	}
	return filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if installCopySkips(relativePath) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symlink: %s", path)
		}
		targetPath := filepath.Join(targetDir, relativePath)
		if relativePath == "." {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if entry.IsDir() {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to copy special file: %s", path)
		}
		return copyInstallFile(path, targetPath, info.Mode().Perm())
	})
}

func installCopySkips(relativePath string) bool {
	return filepath.ToSlash(relativePath) == RuntimeDirName
}

func copyInstallFile(sourcePath string, targetPath string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	if err := target.Close(); err != nil {
		return err
	}
	return os.Chmod(targetPath, mode)
}

func systemdUnit(plan installPlan, dockerBin string, includeDockerUnit bool) string {
	dockerUnitLines := ""
	if includeDockerUnit {
		dockerUnitLines = "Requires=docker.service\nAfter=docker.service\n"
	}
	composeFiles := "--project-directory " + plan.TargetDir + " -f " + filepath.Join(plan.TargetDir, ComposeFileName)
	if plan.ComposeProject != "" {
		composeFiles = "--project-name " + plan.ComposeProject + " " + composeFiles
	}
	if plan.ComposeOverride {
		composeFiles += " -f " + filepath.Join(plan.TargetDir, ComposeOverrideFileName)
	}
	return fmt.Sprintf(`[Unit]
Description=Reploy Docker service (%s)
%s
[Service]
Type=simple
WorkingDirectory=%s
ExecStartPre=/bin/sh -c 'i=0; while [ "$i" -lt 120 ]; do [ -x "$1" ] && "$1" info >/dev/null 2>&1 && exit 0; i=$((i + 1)); sleep 1; done; echo "error: Docker API did not become ready for Reploy" >&2; exit 1' reploy-docker-ready %s
ExecStart=%s compose --env-file %s %s up
ExecStop=%s compose --env-file %s %s down
Restart=on-failure
RestartSec=10
TimeoutStartSec=180

[Install]
WantedBy=multi-user.target
`, plan.Service, dockerUnitLines, plan.TargetDir, dockerBin, dockerBin, filepath.Join(plan.TargetDir, DockerEnvFileName), composeFiles, dockerBin, filepath.Join(plan.TargetDir, DockerEnvFileName), composeFiles)
}

func writeInstalledState(plan installPlan) error {
	state, err := loadState(plan.TargetDir)
	if err != nil {
		return err
	}
	state, err = withInferredBundleState(plan.TargetDir, state)
	if err != nil {
		return err
	}
	state.Phase = deploy.PhaseInstalled
	state.Install = &deploy.InstallState{
		TargetDir:      plan.TargetDir,
		Service:        plan.Service,
		UnitPath:       plan.UnitPath,
		InstanceID:     plan.InstanceID,
		ComposeProject: plan.ComposeProject,
		ContainerName:  plan.ContainerName,
		NetworkName:    plan.NetworkName,
		Ports:          installPortState(plan.Ports),
	}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(plan.TargetDir, StateFileName), append(content, '\n'), 0o644)
}

func writeInstalledDockerEnv(plan installPlan) error {
	updates := dockerEnvPortUpdates(plan.Ports)
	updates["REPLOY_CONTAINER_NAME"] = plan.ContainerName
	updates["REPLOY_DOCKER_NETWORK_NAME"] = plan.NetworkName
	_, err := upsertDockerEnvValues(plan.TargetDir, updates)
	return err
}

func applyInstallPlan(plan installPlan) error {
	if err := copyDeploymentTreeProtected(plan.SourceDir, plan.TargetDir); err != nil {
		return fmt.Errorf("copy deployment: %w", err)
	}
	runtimeDir := filepath.Join(plan.TargetDir, RuntimeDirName)
	if err := os.RemoveAll(runtimeDir); err != nil {
		return fmt.Errorf("remove install runtime dir: %w", err)
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create install runtime dir: %w", err)
	}
	if err := writeInstalledDockerEnv(plan); err != nil {
		return fmt.Errorf("write installed docker env: %w", err)
	}
	if err := writeInstalledState(plan); err != nil {
		return fmt.Errorf("mark deployment installed: %w", err)
	}
	dockerBin, err := installLookPath("docker")
	if err != nil {
		return fmt.Errorf("docker command not found: %w", err)
	}
	systemctlBin, err := installLookPath("systemctl")
	if err != nil {
		return fmt.Errorf("systemctl command not found: %w", err)
	}
	includeDockerUnit := installRunCommand(systemctlBin, "cat", "docker.service") == nil
	if err := os.MkdirAll(filepath.Dir(plan.UnitPath), 0o755); err != nil {
		return err
	}
	unit := systemdUnit(plan, dockerBin, includeDockerUnit)
	if err := os.WriteFile(plan.UnitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(plan.UnitPath, 0o644); err != nil {
		return err
	}
	if err := installRunCommand(systemctlBin, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := installRunCommand(systemctlBin, "enable", plan.Service+".service"); err != nil {
		return fmt.Errorf("systemctl enable %s.service: %w", plan.Service, err)
	}
	if plan.Start {
		if err := installRunCommand(systemctlBin, "restart", plan.Service+".service"); err != nil {
			return fmt.Errorf("systemctl restart %s.service: %w", plan.Service, err)
		}
		if err := runInstalledPostStartChecks(plan); err != nil {
			return err
		}
	}
	return nil
}

func runInstalledPostStartChecks(plan installPlan) error {
	helper := filepath.Join(plan.TargetDir, "reploy")
	if err := installRunCommand(helper, "test"); err != nil {
		return fmt.Errorf("installed server test: %w", err)
	}
	if err := installRunCommand(helper, "app", "config", "check", "--live"); err != nil {
		return fmt.Errorf("installed live config check: %w", err)
	}
	return nil
}
