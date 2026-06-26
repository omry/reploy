package dockerdeploy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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
	Progress      io.Writer
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
	Hooks           deploy.DockerInstallHooksConfig
	Success         deploy.DockerInstallSuccessConfig
	Start           bool
	ComposeOverride bool
	Progress        io.Writer
}

const defaultSystemdUnitDir = "/etc/systemd/system"

var installGeteuid = os.Geteuid
var installLookPath = exec.LookPath
var installRunCommand = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
var installRunCommandOutput = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
var installToolBinaryContent = currentExecutableContent
var installChown = os.Lchown
var installLookupUser = user.Lookup
var installLookupGroup = user.LookupGroup
var installServiceStartTimeout = 30 * time.Second
var installServicePollInterval = time.Second
var installServiceTerminalStateGrace = 5 * time.Second
var installSystemdUnitDir = defaultSystemdUnitDir

type resolvedInstallOwner struct {
	Spec          string
	UID           int
	GID           int
	ContainerUser string
}

func Install(options InstallOptions) error {
	plan, err := newInstallPlan(options)
	if err != nil {
		return err
	}
	doctorCode := Doctor(DoctorOptions{Dir: options.Dir, Preinstall: true, Quiet: true, Stdout: options.Stdout})
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
		Hooks:           pack.Docker.Install.Hooks,
		Success:         pack.Docker.Install.Success,
		Start:           options.Start,
		ComposeOverride: overrideErr == nil,
		Progress:        options.Progress,
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
	if containerUser, err := installContainerUser(plan.SourceDir); err == nil {
		fmt.Fprintf(stdout, "container user: %s\n", containerUser)
	}
	if owner, err := installOwnerForDir(plan.SourceDir); err == nil {
		fmt.Fprintf(stdout, "install owner: %s (%d:%d)\n", owner.Spec, owner.UID, owner.GID)
		fmt.Fprintf(stdout, "installed container user: %s\n", owner.ContainerUser)
	}
	for _, port := range plan.Ports {
		fmt.Fprintf(stdout, "port %s: %s:%s -> %s\n", port.Name, port.HostBind, port.HostPort, port.ContainerPort)
	}
	if sources, err := localBundleSourcesForDir(plan.SourceDir); err == nil && len(sources) > 0 {
		fmt.Fprintf(stdout, "would rebuild local source bundle: %s\n", installBundleSourceNames(sources))
	}
	fmt.Fprintln(stdout, "would set installed deployment ownership")
	fmt.Fprintf(stdout, "would write systemd unit: %s\n", plan.UnitPath)
	fmt.Fprintln(stdout, "would run: systemctl daemon-reload")
	fmt.Fprintf(stdout, "would run: systemctl enable %s.service\n", plan.Service)
	if plan.Start {
		for _, hook := range plan.Hooks.BeforeStart {
			fmt.Fprintf(stdout, "would run before start hook: %s\n", installHookDescription(hook))
		}
		fmt.Fprintf(stdout, "would run: systemctl restart %s.service\n", plan.Service)
		for _, hook := range plan.Hooks.AfterStart {
			fmt.Fprintf(stdout, "would run after start hook: %s\n", installHookDescription(hook))
		}
	} else {
		fmt.Fprintln(stdout, "start: no")
	}
	successVarNames := make([]string, 0, len(plan.Success.Vars))
	for name := range plan.Success.Vars {
		successVarNames = append(successVarNames, name)
	}
	sort.Strings(successVarNames)
	for _, name := range successVarNames {
		variable := plan.Success.Vars[name]
		fmt.Fprintf(stdout, "would resolve success var %s: %s\n", name, installSuccessVarDescription(variable))
	}
	for _, line := range plan.Success.Lines {
		fmt.Fprintf(stdout, "would print success line: %s\n", line)
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
	slashPath := filepath.ToSlash(relativePath)
	return slashPath == RuntimeDirName || slashPath == ToolBinaryFileName
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

func writeInstalledToolBinary(targetDir string) error {
	content, err := installToolBinaryContent()
	if err != nil {
		return err
	}
	targetPath := filepath.Join(targetDir, ToolBinaryFileName)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(targetPath, content, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(targetPath, 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("installed reploy binary must not be a symlink: %s", targetPath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("installed reploy binary must be a regular file: %s", targetPath)
	}
	manifest, err := loadManifestOrNew(targetDir)
	if err != nil {
		return err
	}
	manifest.Files[filepath.ToSlash(ToolBinaryFileName)] = deploy.GeneratedFile{
		Kind:   "template",
		SHA256: deploy.HashBytes(content),
	}
	return deploy.WriteDeploymentManifest(filepath.Join(targetDir, ManifestFileName), manifest)
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
# Managed-By: reploy
# Reploy-Service: %s
# Reploy-Target: %s
# Reploy-Compose-Project: %s
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
`, plan.Service, plan.Service, plan.TargetDir, plan.ComposeProject, dockerUnitLines, plan.TargetDir, dockerBin, dockerBin, filepath.Join(plan.TargetDir, DockerEnvFileName), composeFiles, dockerBin, filepath.Join(plan.TargetDir, DockerEnvFileName), composeFiles)
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
	values, err := readDockerEnv(plan.TargetDir)
	if err != nil {
		return err
	}
	owner, err := resolveInstallOwner(values)
	if err != nil {
		return err
	}
	updates := dockerEnvPortUpdates(plan.Ports)
	updates["REPLOY_CONTAINER_NAME"] = plan.ContainerName
	updates["REPLOY_DOCKER_NETWORK_NAME"] = plan.NetworkName
	updates["REPLOY_CONTAINER_USER"] = owner.ContainerUser
	updates["REPLOY_INSTALL_OWNER"] = owner.Spec
	_, err = upsertDockerEnvValues(plan.TargetDir, updates)
	return err
}

func installContainerUser(dir string) (string, error) {
	values, err := readDockerEnv(dir)
	if err != nil {
		return "", err
	}
	return envValue(values, "REPLOY_CONTAINER_USER", defaultContainerUser()), nil
}

func installOwnerForDir(dir string) (resolvedInstallOwner, error) {
	values, err := readDockerEnv(dir)
	if err != nil {
		return resolvedInstallOwner{}, err
	}
	return resolveInstallOwner(values)
}

func chownInstalledDeployment(targetDir string) error {
	values, err := readDockerEnv(targetDir)
	if err != nil {
		return err
	}
	owner, err := resolveInstallOwner(values)
	if err != nil {
		return err
	}
	return chownInstallPath(targetDir, owner.UID, owner.GID)
}

func chownInstallPath(path string, uid int, gid int) error {
	return filepath.WalkDir(path, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return installChown(currentPath, uid, gid)
	})
}

func resolveInstallOwner(values map[string]string) (resolvedInstallOwner, error) {
	spec := strings.TrimSpace(values["REPLOY_INSTALL_OWNER"])
	if spec == "" {
		return resolvedInstallOwner{}, fmt.Errorf("REPLOY_INSTALL_OWNER is required for install; set it in the blueprint as docker.service.install_owner or in %s", DockerEnvFileName)
	}
	uid, gid, err := parseInstallOwner(spec)
	if err != nil {
		return resolvedInstallOwner{}, err
	}
	return resolvedInstallOwner{
		Spec:          spec,
		UID:           uid,
		GID:           gid,
		ContainerUser: fmt.Sprintf("%d:%d", uid, gid),
	}, nil
}

func parseInstallOwner(value string) (int, int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, fmt.Errorf("REPLOY_INSTALL_OWNER must not be empty")
	}
	userPart, groupPart, hasGroup := strings.Cut(value, ":")
	uid, primaryGID, err := resolveInstallOwnerUser(userPart, value)
	if err != nil {
		return 0, 0, err
	}
	gid := primaryGID
	if hasGroup {
		gid, err = resolveInstallOwnerGroup(groupPart, value)
		if err != nil {
			return 0, 0, err
		}
	}
	if uid == 0 || gid == 0 {
		return 0, 0, fmt.Errorf("REPLOY_INSTALL_OWNER must not resolve to root: %s", value)
	}
	return uid, gid, nil
}

func resolveInstallOwnerUser(value string, original string) (int, int, error) {
	if value == "" {
		return 0, 0, fmt.Errorf("REPLOY_INSTALL_OWNER has empty user: %s", original)
	}
	if id, ok := parseNumericInstallID(value); ok {
		return id, id, nil
	}
	lookedUpUser, err := installLookupUser(value)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve REPLOY_INSTALL_OWNER user %q: %w", value, err)
	}
	uid, ok := parseNumericInstallID(lookedUpUser.Uid)
	if !ok {
		return 0, 0, fmt.Errorf("resolved REPLOY_INSTALL_OWNER user has non-numeric uid: %s=%s", value, lookedUpUser.Uid)
	}
	gid, ok := parseNumericInstallID(lookedUpUser.Gid)
	if !ok {
		return 0, 0, fmt.Errorf("resolved REPLOY_INSTALL_OWNER user has non-numeric gid: %s=%s", value, lookedUpUser.Gid)
	}
	return uid, gid, nil
}

func resolveInstallOwnerGroup(value string, original string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("REPLOY_INSTALL_OWNER has empty group: %s", original)
	}
	if id, ok := parseNumericInstallID(value); ok {
		return id, nil
	}
	lookedUpGroup, err := installLookupGroup(value)
	if err != nil {
		return 0, fmt.Errorf("resolve REPLOY_INSTALL_OWNER group %q: %w", value, err)
	}
	gid, ok := parseNumericInstallID(lookedUpGroup.Gid)
	if !ok {
		return 0, fmt.Errorf("resolved REPLOY_INSTALL_OWNER group has non-numeric gid: %s=%s", value, lookedUpGroup.Gid)
	}
	return gid, nil
}

func parseNumericInstallID(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	id, err := strconv.Atoi(value)
	if err != nil || id < 0 {
		return 0, false
	}
	return id, true
}

func applyInstallPlan(plan installPlan) error {
	if err := copyDeploymentTreeProtected(plan.SourceDir, plan.TargetDir); err != nil {
		return fmt.Errorf("copy deployment: %w", err)
	}
	if err := writeInstalledToolBinary(plan.TargetDir); err != nil {
		return fmt.Errorf("write installed reploy binary: %w", err)
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
	if err := rebuildInstalledBundleIfLocalSources(plan); err != nil {
		return fmt.Errorf("rebuild installed bundle: %w", err)
	}
	if owner, err := installOwnerForDir(plan.TargetDir); err == nil {
		installProgress(plan.Progress, fmt.Sprintf("setting installed ownership to %s (%d:%d)", owner.Spec, owner.UID, owner.GID))
	} else {
		installProgress(plan.Progress, "setting installed ownership")
	}
	if err := chownInstalledDeployment(plan.TargetDir); err != nil {
		return fmt.Errorf("set installed ownership: %w", err)
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
		if err := runInstallHooks(plan, "before start", plan.Hooks.BeforeStart); err != nil {
			return err
		}
		if err := installRunCommand(systemctlBin, "restart", plan.Service+".service"); err != nil {
			return fmt.Errorf("systemctl restart %s.service: %w", plan.Service, err)
		}
		if err := runInstallHooks(plan, "after start", plan.Hooks.AfterStart); err != nil {
			return err
		}
	}
	return nil
}

func runInstallHooks(plan installPlan, phase string, hooks []deploy.DockerInstallHookConfig) error {
	for _, hook := range hooks {
		if err := runInstallHook(plan, hook); err != nil {
			return fmt.Errorf("install hook %s %s: %w", phase, installHookDescription(hook), err)
		}
	}
	return nil
}

func runInstallHook(plan installPlan, hook deploy.DockerInstallHookConfig) error {
	helper := filepath.Join(plan.TargetDir, "reploy")
	if len(hook.App) > 0 {
		args := append([]string{"app"}, hook.App...)
		return runInstallCheckCommand("installed app hook", helper, args...)
	}
	if hook.HealthCheck != nil {
		return runInstallHealthCheckHook(plan, hook.HealthCheck)
	}
	return fmt.Errorf("empty install hook")
}

func runInstallHealthCheckHook(plan installPlan, healthCheck *deploy.DockerInstallHealthCheckConfig) error {
	helper := filepath.Join(plan.TargetDir, "reploy")
	if healthCheck.Wait {
		if err := waitInstalledServiceRunning(plan.TargetDir, installServiceStartTimeout, plan.Progress); err != nil {
			return installedServiceStartError(plan, err)
		}
	}
	return runInstallCheckCommand("installed health check", helper, "test")
}

func installHookDescription(hook deploy.DockerInstallHookConfig) string {
	if len(hook.App) > 0 {
		return "app " + strings.Join(hook.App, " ")
	}
	if hook.HealthCheck != nil {
		if hook.HealthCheck.Wait {
			return "health check --wait"
		}
		return "health check"
	}
	return "empty hook"
}

func installSuccessVarDescription(variable deploy.DockerInstallSuccessVarConfig) string {
	if len(variable.App) > 0 {
		return "app " + strings.Join(variable.App, " ")
	}
	if variable.ServerURL {
		return "server_url"
	}
	return "empty variable"
}

func rebuildInstalledBundleIfLocalSources(plan installPlan) error {
	sources, err := localBundleSourcesForDir(plan.TargetDir)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}
	installProgress(plan.Progress, "rebuilding local source bundle")
	return BundlePrepare(BundlePrepareOptions{Dir: plan.TargetDir})
}

func localBundleSourcesForDir(dir string) ([]bundleBuildSource, error) {
	state, err := loadState(dir)
	if err != nil {
		return nil, err
	}
	state, err = withInferredBundleState(dir, state)
	if err != nil {
		return nil, err
	}
	return localBundleBuildSources(state)
}

func installBundleSourceNames(sources []bundleBuildSource) string {
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.Name)
	}
	return strings.Join(names, ", ")
}

func installedServiceStartError(plan installPlan, startErr error) error {
	helper := filepath.Join(plan.TargetDir, "reploy")
	output, logsErr := installRunCommandOutput(helper, "logs")
	trimmedOutput := strings.TrimSpace(string(output))
	switch {
	case logsErr == nil && trimmedOutput != "":
		return fmt.Errorf("installed service start: %w\ninstalled service logs:\n%s", startErr, trimmedOutput)
	case logsErr == nil:
		return fmt.Errorf("installed service start: %w\ninstalled service logs are empty", startErr)
	case trimmedOutput != "":
		return fmt.Errorf("installed service start: %w\ninstalled service logs failed: %v\n%s", startErr, logsErr, trimmedOutput)
	default:
		return fmt.Errorf("installed service start: %w\ninstalled service logs failed: %v", startErr, logsErr)
	}
}

func runInstallCheckCommand(label string, name string, args ...string) error {
	output, err := installRunCommandOutput(name, args...)
	if err == nil {
		return nil
	}
	return commandErrorWithOutput(label, output, err)
}

func commandErrorWithOutput(label string, output []byte, err error) error {
	trimmedOutput := strings.TrimSpace(string(output))
	if trimmedOutput == "" {
		return fmt.Errorf("%s: %w", label, err)
	}
	return fmt.Errorf("%s: %w\n%s", label, err, trimmedOutput)
}

func waitInstalledServiceRunning(dir string, timeout time.Duration, stdout io.Writer) error {
	installProgress(stdout, "waiting for installed service to start")
	deadline := time.Now().Add(timeout)
	lastState := ""
	var terminalObservedAt time.Time
	for {
		now := time.Now()
		states, err := composeServiceStates(dir)
		if err != nil {
			return err
		}
		stateSummary := installServiceStateSummary(states)
		if stateSummary != lastState {
			installProgress(stdout, "installed service state: "+stateSummary)
			lastState = stateSummary
			terminalObservedAt = time.Time{}
		}
		if serviceStatesContain(states, "running") {
			installProgress(stdout, "installed service is running")
			return nil
		}
		if !installServiceMayStillStart(states) {
			if terminalObservedAt.IsZero() {
				terminalObservedAt = now
			}
			if installServiceTerminalStateGrace <= 0 || now.Sub(terminalObservedAt) >= installServiceTerminalStateGrace {
				if len(states) == 0 {
					return fmt.Errorf("service is not started")
				}
				return fmt.Errorf("service is not running; current state: %s", strings.Join(states, ", "))
			}
		} else {
			terminalObservedAt = time.Time{}
		}
		if !now.Before(deadline) {
			if len(states) == 0 {
				return fmt.Errorf("service did not start before timeout")
			}
			return fmt.Errorf("service did not start before timeout; current state: %s", strings.Join(states, ", "))
		}
		time.Sleep(installServicePollInterval)
	}
}

func installProgress(stdout io.Writer, message string) {
	if stdout == nil {
		return
	}
	fmt.Fprintln(stdout, message)
}

func installServiceStateSummary(states []string) string {
	if len(states) == 0 {
		return "not created yet"
	}
	return strings.Join(states, ", ")
}

func installServiceMayStillStart(states []string) bool {
	if len(states) == 0 {
		return true
	}
	for _, state := range states {
		switch strings.ToLower(strings.TrimSpace(state)) {
		case "created", "restarting", "starting":
		default:
			return false
		}
	}
	return true
}
