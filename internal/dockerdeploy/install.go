package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
)

type InstallOptions struct {
	Dir                    string
	Target                 string
	Scope                  InstallScope
	Service                string
	PortOverrides          []PortOverride
	Replace                []string
	Clean                  bool
	InPlace                bool
	Start                  bool
	DryRun                 bool
	Stdout                 io.Writer
	Progress               io.Writer
	DockerPreflightTimeout time.Duration
}

type DirectInstallOptions struct {
	Pack                   deploy.PackRef
	Target                 string
	Scope                  InstallScope
	Service                string
	PortOverrides          []PortOverride
	Replace                []string
	Clean                  bool
	InPlace                bool
	Start                  bool
	DryRun                 bool
	Stdout                 io.Writer
	Progress               io.Writer
	DockerPreflightTimeout time.Duration
}

type installPlan struct {
	SourceDir              string
	TargetDir              string
	Scope                  InstallScope
	AppID                  string
	Service                string
	ControlScript          string
	UnitPath               string
	InstanceID             string
	ComposeProject         string
	ContainerName          string
	NetworkName            string
	Ports                  []dockerPortBinding
	Health                 deploy.DockerHealthConfig
	Terminal               deploy.AppTerminalConfig
	ConfigDir              string
	ConfigContainerDir     string
	ManagedFiles           []string
	DeployedCommands       []deploy.DockerCommandConfig
	Hooks                  deploy.DockerInstallHooksConfig
	Success                deploy.DockerInstallSuccessConfig
	Backend                installBackend
	PreservePaths          []string
	PathUpdates            []PathUpdateAction
	PathUpdateImage        string
	Warnings               []string
	ExistingImages         *deploy.GeneratedImagesState
	EnvironmentModel       bool
	AfterInstallSteps      int
	BeforeStartSteps       int
	AfterStartSteps        int
	Replace                []string
	Clean                  bool
	Start                  bool
	ComposeOverride        bool
	InPlace                bool
	Progress               io.Writer
	DockerPreflightTimeout time.Duration
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
var installChown = os.Lchown
var installLookupUser = user.Lookup
var installLookupGroup = user.LookupGroup
var installServiceStartTimeout = 30 * time.Second
var installServicePollInterval = time.Second
var installServiceTerminalStateGrace = 5 * time.Second
var installSystemdUnitDir = defaultSystemdUnitDir
var runInstallAppCommand = func(dir string, args []string, stdout io.Writer, stderr io.Writer, dockerPreflightTimeout time.Duration) error {
	return AppCommand(AppCommandOptions{Dir: dir, CommandArgs: args, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: dockerPreflightTimeout})
}
var runInstallHealthCheck = func(dir string, stdout io.Writer, stderr io.Writer, restartingDiagnostics string, dockerPreflightTimeout time.Duration) error {
	return TestServer(TestOptions{Dir: dir, Stdout: stdout, RestartingDiagnostics: restartingDiagnostics, DockerPreflightTimeout: dockerPreflightTimeout})
}
var runInstallPathUpdateCommand = runCommand
var runInstallPathUpdateOutput dockerOutputRunner = executeDockerOutput
var runInstallAfterInstall = runEnvironmentAfterInstall
var runInstallEnvironmentStart = runEnvironmentInstallStart

type resolvedInstallOwner struct {
	Spec          string
	UID           int
	GID           int
	ContainerUser string
}

const (
	installOwnerOnMissingCreate = "create"
	installOwnerOnMissingFail   = "fail"
)

type InstallScope string

const (
	InstallScopeUser   InstallScope = "user"
	InstallScopeSystem InstallScope = "system"
)

func ParseInstallScope(value string) (InstallScope, error) {
	switch InstallScope(strings.TrimSpace(value)) {
	case InstallScopeUser:
		return InstallScopeUser, nil
	case InstallScopeSystem:
		return InstallScopeSystem, nil
	case "":
		return "", fmt.Errorf("--scope is required and must be user or system")
	default:
		return "", fmt.Errorf("--scope must be user or system: %s", value)
	}
}

func validateInstallScopeForBackend(scope InstallScope, backend installBackend, platform hostPlatform) error {
	switch scope {
	case InstallScopeUser:
		switch backend {
		case installBackendDockerDesktop, installBackendDockerManaged:
			return nil
		case installBackendLinuxSystemd:
			return fmt.Errorf("--scope user requires a Docker-managed backend on Linux")
		default:
			return platform.unsupportedPersistentInstallError("install")
		}
	case InstallScopeSystem:
		switch backend {
		case installBackendLinuxSystemd:
			return nil
		case installBackendDockerDesktop:
			return fmt.Errorf("--scope system is not supported on %s with Docker Desktop; no native system service backend is available", deploy.InstallTargetHostKey(platform.GOOS))
		default:
			return platform.unsupportedPersistentInstallError("install")
		}
	default:
		_, err := ParseInstallScope(string(scope))
		return err
	}
}

func Install(options InstallOptions) error {
	plan, err := newInstallPlan(options)
	if err != nil {
		return err
	}
	if !options.DryRun {
		if err := installRequireCurrentBundle(plan); err != nil {
			return err
		}
	}
	printInstallWarnings(options.Stdout, plan.Warnings)
	doctorCode := Doctor(DoctorOptions{Dir: options.Dir, Preinstall: true, Scope: plan.Scope, Quiet: true, SuppressWarnings: true, Stdout: options.Stdout, DockerPreflightTimeout: options.DockerPreflightTimeout})
	if doctorCode != 0 {
		return fmt.Errorf("preinstall doctor failed")
	}
	if options.DryRun {
		printInstallDryRun(options.Stdout, plan)
		return printInstallEnvironmentInspection(options.Dir, options.Stdout)
	}
	if err := requireInstallPrivileges(plan.Backend, options.DryRun); err != nil {
		return err
	}
	return applyInstallPlan(plan)
}

func printInstallEnvironmentInspection(dir string, stdout io.Writer) error {
	if stdout == nil {
		return nil
	}
	state, err := loadState(dir)
	if err != nil {
		return err
	}
	if !state.EnvironmentModel {
		return nil
	}
	inspection, err := Info(InfoOptions{Dir: dir})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "environment inspection:")
	_, err = io.WriteString(stdout, inspection)
	return err
}

func requireInstallPrivileges(backend installBackend, dryRun bool) error {
	if dryRun {
		return nil
	}
	if backend == installBackendLinuxSystemd && installGeteuid() != 0 {
		return fmt.Errorf("install requires root unless --dry-run is set")
	}
	return nil
}

func installShouldPrepareSourceBundle(dir string) (bool, error) {
	sources, err := localBundleSourcesForDir(dir)
	if err != nil {
		return false, err
	}
	return len(sources) == 0, nil
}

func installRequireCurrentBundle(plan installPlan) error {
	prepare, err := installShouldPrepareSourceBundle(plan.SourceDir)
	if err != nil {
		return fmt.Errorf("inspect installation bundle: %w", err)
	}
	if !prepare {
		return nil
	}
	prepared, err := bundlePrepared(plan.SourceDir)
	if err != nil {
		return err
	}
	if prepared {
		return nil
	}
	return fmt.Errorf("staging bundle is outdated; run `reploy bundle build`, retest the staging environment, then install again")
}

func DirectInstall(options DirectInstallOptions) (string, error) {
	pack, err := deploy.LoadPack(options.Pack)
	if err != nil {
		return "", err
	}
	scope, err := ParseInstallScope(string(options.Scope))
	if err != nil {
		return "", err
	}
	platform := currentHostPlatform()
	backend := platform.installBackendForScope(scope)
	if err := validateInstallScopeForBackend(scope, backend, platform); err != nil {
		return "", err
	}
	target := options.Target
	if strings.TrimSpace(target) == "" {
		target, err = defaultInstallTarget(pack, scope)
		if err != nil {
			return "", err
		}
	}
	if err := requireInstallPrivileges(backend, options.DryRun); err != nil {
		return "", err
	}
	options.Pack = pack.Ref
	if options.InPlace {
		if options.DryRun {
			return target, directInstallViaTemporaryStaging(target, options)
		}
		if _, err := Init(InitOptions{Dir: target, Pack: pack.Ref}); err != nil {
			return "", err
		}
		if _, err := EnsureBundlePrepared(BundleEnsureOptions{Dir: target, Stdout: options.Stdout, DockerPreflightTimeout: options.DockerPreflightTimeout}); err != nil {
			return "", fmt.Errorf("prepare direct install bundle: %w", err)
		}
		return target, Install(InstallOptions{
			Dir:                    target,
			Target:                 target,
			Scope:                  scope,
			Service:                options.Service,
			PortOverrides:          options.PortOverrides,
			Replace:                options.Replace,
			Clean:                  options.Clean,
			InPlace:                true,
			Start:                  options.Start,
			DryRun:                 options.DryRun,
			Stdout:                 options.Stdout,
			Progress:               options.Progress,
			DockerPreflightTimeout: options.DockerPreflightTimeout,
		})
	}
	return target, directInstallViaTemporaryStaging(target, options)
}

func directInstallViaTemporaryStaging(target string, options DirectInstallOptions) error {
	tempDir, err := os.MkdirTemp("", "reploy-direct-install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	stagingDir := filepath.Join(tempDir, "staging")
	if _, err := Init(InitOptions{Dir: stagingDir, Pack: options.Pack}); err != nil {
		return err
	}
	if !options.DryRun {
		if _, err := EnsureBundlePrepared(BundleEnsureOptions{Dir: stagingDir, Stdout: options.Stdout, DockerPreflightTimeout: options.DockerPreflightTimeout}); err != nil {
			return fmt.Errorf("prepare direct install bundle: %w", err)
		}
	}
	return Install(InstallOptions{
		Dir:                    stagingDir,
		Target:                 target,
		Scope:                  options.Scope,
		Service:                options.Service,
		PortOverrides:          options.PortOverrides,
		Replace:                options.Replace,
		Clean:                  options.Clean,
		Start:                  options.Start,
		DryRun:                 options.DryRun,
		Stdout:                 options.Stdout,
		Progress:               options.Progress,
		DockerPreflightTimeout: options.DockerPreflightTimeout,
	})
}

func newInstallPlan(options InstallOptions) (installPlan, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	scope, err := ParseInstallScope(string(options.Scope))
	if err != nil {
		return installPlan{}, err
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
	if strings.ContainsAny(target, "\t\r\n") {
		return installPlan{}, fmt.Errorf("--to must not contain tabs or newlines: %s", target)
	}
	if strings.Contains(target, " ") && currentHostPlatform().installBackend() == installBackendLinuxSystemd {
		return installPlan{}, fmt.Errorf("--to must not contain spaces for Linux/systemd installs: %s", target)
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
	if installPathsOverlap(canonicalSourceDir, canonicalTargetDir) && !options.InPlace {
		return installPlan{}, fmt.Errorf("--to must not overlap deployment directory: %s overlaps %s", target, absoluteDir)
	}
	if options.InPlace && canonicalSourceDir != canonicalTargetDir {
		return installPlan{}, fmt.Errorf("--in-place requires deployment directory and target to be the same path")
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
	ports, err := installPortBindings(pack.Install.Ports.Deployed)
	if err != nil {
		return installPlan{}, err
	}
	if len(ports) == 0 {
		return installPlan{}, fmt.Errorf("install.ports.deployed must declare at least one port")
	}
	applyPrimaryPortDefaults(&service, ports)
	ports, err = applyPortOverrides(ports, options.PortOverrides)
	if err != nil {
		return installPlan{}, err
	}
	preservePaths := []string{}
	if pack.Environment == nil {
		preservePaths, err = installPreservePaths(pack.Install.ManagedPaths, options.Replace, options.Clean)
		if err != nil {
			return installPlan{}, err
		}
	}
	deployedCommands := pack.Docker.DeployedCommands()
	if err := validateDeployedControlCommands(deployedCommands); err != nil {
		return installPlan{}, err
	}
	_, overrideErr := os.Stat(filepath.Join(absoluteDir, ComposeOverrideFileName))
	if overrideErr != nil && !os.IsNotExist(overrideErr) {
		return installPlan{}, overrideErr
	}
	configLayout := configMountLayoutForPack(pack)
	platform := currentHostPlatform()
	backend := platform.installBackendForScope(scope)
	if backend == installBackendUnsupported {
		return installPlan{}, platform.unsupportedPersistentInstallError("install")
	}
	if err := validateInstallScopeForBackend(scope, backend, platform); err != nil {
		return installPlan{}, err
	}
	pathUpdates := []PathUpdateAction{}
	pathUpdateImage := ""
	if pack.Environment != nil {
		pathUpdates, preservePaths, err = planEnvironmentInstallPathUpdates(*pack.Environment, absoluteDir, target, scope, options.Replace, options.Clean, platform.GOOS)
		if err != nil {
			return installPlan{}, err
		}
		if state.Images != nil && state.Images.Staging != nil {
			pathUpdateImage = state.Images.Staging.Reference
		}
		for _, action := range pathUpdates {
			if (action.Kind == PathPreserveVolume || action.Kind == PathReplaceVolume) && pathUpdateImage == "" {
				return installPlan{}, fmt.Errorf("environment volume transfer requires a prepared staging image")
			}
		}
	}
	warnings := []string{}
	if pack.Environment != nil && scope == InstallScopeUser {
		userScope := blueprint.InstallScopeUser
		userPlan, err := planRuntimeUser(*pack.Environment, DockerPlanContext{
			Phase: blueprint.PhaseInstalled, Scope: &userScope, Host: blueprintHostForGOOS(platform.GOOS),
			UID: os.Getuid(), GID: os.Getgid(),
		})
		if err != nil {
			return installPlan{}, err
		}
		warnings = append(warnings, userPlan.Warnings...)
	}
	existingImages, err := installedTargetImages(target)
	if err != nil {
		return installPlan{}, err
	}
	unitPath := ""
	if backend == installBackendLinuxSystemd {
		unitPath = systemdPath(installSystemdUnitDir, options.Service+".service")
	}
	return installPlan{
		SourceDir:              absoluteDir,
		TargetDir:              target,
		Scope:                  scope,
		AppID:                  pack.AppID,
		Service:                options.Service,
		ControlScript:          controlScriptNameForPack(pack),
		UnitPath:               unitPath,
		InstanceID:             instanceID,
		ComposeProject:         instanceID,
		ContainerName:          service.ContainerName,
		NetworkName:            service.NetworkName,
		Ports:                  ports,
		Health:                 pack.Docker.Health,
		Terminal:               pack.App.Terminal,
		ConfigDir:              pack.Docker.DeploymentDirs.Config,
		ConfigContainerDir:     configLayout.ContainerConfigDir,
		ManagedFiles:           append([]string(nil), configLayout.FileMounts...),
		DeployedCommands:       deployedCommands,
		Hooks:                  pack.Docker.Install.Hooks,
		Success:                pack.Docker.Install.Success,
		Backend:                backend,
		PreservePaths:          preservePaths,
		PathUpdates:            pathUpdates,
		PathUpdateImage:        pathUpdateImage,
		Warnings:               warnings,
		ExistingImages:         existingImages,
		EnvironmentModel:       pack.Environment != nil,
		AfterInstallSteps:      environmentAfterInstallSteps(pack),
		BeforeStartSteps:       environmentBeforeStartSteps(pack),
		AfterStartSteps:        environmentAfterStartSteps(pack),
		Replace:                append([]string(nil), options.Replace...),
		Clean:                  options.Clean,
		Start:                  options.Start,
		ComposeOverride:        overrideErr == nil,
		InPlace:                options.InPlace,
		Progress:               options.Progress,
		DockerPreflightTimeout: options.DockerPreflightTimeout,
	}, nil
}

func environmentAfterInstallSteps(pack deploy.AppPack) int {
	if pack.Environment == nil {
		return 0
	}
	return len(pack.Environment.Environment.Install.AfterInstall)
}

func environmentBeforeStartSteps(pack deploy.AppPack) int {
	if pack.Environment == nil || pack.Environment.Environment.Workload == nil {
		return 0
	}
	return len(pack.Environment.Environment.Workload.Runtime.BeforeStart)
}

func environmentAfterStartSteps(pack deploy.AppPack) int {
	if pack.Environment == nil || pack.Environment.Environment.Workload == nil {
		return 0
	}
	return len(pack.Environment.Environment.Workload.Runtime.AfterStart)
}

func installedTargetImages(target string) (*deploy.GeneratedImagesState, error) {
	if _, err := os.Stat(filepath.Join(target, StateFileName)); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	state, err := loadState(target)
	if err != nil {
		return nil, fmt.Errorf("read installed target image state: %w", err)
	}
	if state.Phase != deploy.PhaseInstalled || state.Images == nil {
		return nil, nil
	}
	content, err := json.Marshal(state.Images)
	if err != nil {
		return nil, err
	}
	var cloned deploy.GeneratedImagesState
	if err := json.Unmarshal(content, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
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

func defaultInstallTarget(pack deploy.AppPack, scope InstallScope) (string, error) {
	platform := currentHostPlatform()
	backend := platform.installBackendForScope(scope)
	if err := validateInstallScopeForBackend(scope, backend, platform); err != nil {
		return "", err
	}
	roots, err := installTargetRoots(platform.GOOS)
	if err != nil {
		return "", err
	}
	if pack.Environment != nil {
		return blueprint.ResolveInstallTarget(
			pack.Environment.Environment.Install.Target,
			pack.Environment.Environment.ID,
			blueprint.InstallTargetContext{
				Host: blueprintHostForGOOS(platform.GOOS), Scope: blueprint.InstallScope(scope),
				Paths: blueprint.HostPaths{
					Home: roots.UserHome, UserData: roots.UserData, LocalData: roots.UserLocalData, SystemData: roots.SystemData,
				},
				Variables: pack.Environment.Environment.Vars,
			},
		)
	}
	target, _, ok, err := deploy.ResolveInstallTargetDefault(pack.Install.Target, pack.AppID, platform.GOOS, string(scope), roots)
	if err != nil {
		return "", err
	}
	if ok {
		if deploy.InstallTargetHostKey(platform.GOOS) == "windows" {
			target = strings.ReplaceAll(target, "/", `\`)
		}
		return target, nil
	}

	switch scope {
	case InstallScopeUser:
		switch deploy.InstallTargetHostKey(platform.GOOS) {
		case "windows":
			return windowsDockerManagedDefaultInstallTarget(os.Getenv("LOCALAPPDATA"), pack.AppID)
		case "macos":
			return path.Join(roots.ReployInstallRoot, pack.AppID), nil
		default:
			return path.Join(roots.UserData, "Reploy", "installs", pack.AppID), nil
		}
	case InstallScopeSystem:
		target, _, _, err := deploy.ResolveInstallTargetDefault(
			deploy.InstallTargetConfig{DefaultPath: "/opt/{{ app.id }}"},
			pack.AppID,
			platform.GOOS,
			string(scope),
			roots,
		)
		return target, err
	default:
		return "", fmt.Errorf("--scope must be user or system: %s", scope)
	}
}

func installTargetRoots(goos string) (deploy.InstallTargetRoots, error) {
	home := installTargetHome(goos)
	switch deploy.InstallTargetHostKey(goos) {
	case "windows":
		localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if localAppData == "" {
			return deploy.InstallTargetRoots{}, fmt.Errorf("LOCALAPPDATA is required for the default Windows install target; pass --to to choose an install directory")
		}
		userData := strings.TrimSpace(os.Getenv("APPDATA"))
		if userData == "" {
			userData = localAppData
		}
		systemData := strings.TrimSpace(os.Getenv("ProgramData"))
		if systemData == "" {
			systemData = `C:\ProgramData`
		}
		return deploy.InstallTargetRoots{
			UserHome:          home,
			UserData:          userData,
			UserLocalData:     localAppData,
			SystemData:        systemData,
			ReployInstallRoot: strings.TrimRight(localAppData, `\/`) + `\Reploy\installs`,
		}, nil
	case "macos":
		if strings.TrimSpace(home) == "" {
			return deploy.InstallTargetRoots{}, fmt.Errorf("home directory is required for the default macOS install target; pass --to to choose an install directory")
		}
		userData := path.Join(home, "Library", "Application Support")
		return deploy.InstallTargetRoots{
			UserHome:          home,
			UserData:          userData,
			UserLocalData:     userData,
			SystemData:        path.Join("/", "Library", "Application Support"),
			ReployInstallRoot: path.Join(userData, "Reploy", "installs"),
		}, nil
	default:
		if strings.TrimSpace(home) == "" {
			home = "/home"
		}
		return deploy.InstallTargetRoots{
			UserHome:          home,
			UserData:          path.Join(home, ".local", "share"),
			UserLocalData:     path.Join(home, ".local", "share"),
			SystemData:        path.Join("/", "var", "lib"),
			ReployInstallRoot: path.Join("/", "opt"),
		}, nil
	}
}

func windowsDockerManagedDefaultInstallTarget(localAppData string, appID string) (string, error) {
	root := strings.TrimSpace(localAppData)
	if root == "" {
		return "", fmt.Errorf("LOCALAPPDATA is required for the default Windows install target; pass --to to choose an install directory")
	}
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return "", fmt.Errorf("app id is required for the default Windows install target")
	}
	if appID == "." || appID == ".." || strings.ContainsAny(appID, `<>:"/\|?*`) {
		return "", fmt.Errorf("app id cannot be used in the default Windows install target: %s", appID)
	}
	return strings.TrimRight(root, `\/`) + `\Reploy\installs\` + appID, nil
}

func installTargetHome(goos string) string {
	if deploy.InstallTargetHostKey(goos) != "windows" {
		if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
			return home
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

func installOwnerSpec(owner deploy.InstallOwnerConfig) string {
	user := strings.TrimSpace(owner.User)
	group := strings.TrimSpace(owner.Group)
	if user == "" || group == "" {
		return ""
	}
	return user + ":" + group
}

func printInstallDryRun(stdout io.Writer, plan installPlan) {
	if stdout == nil {
		return
	}
	fmt.Fprintf(stdout, "would install deployment: %s\n", plan.SourceDir)
	fmt.Fprintf(stdout, "target: %s\n", plan.TargetDir)
	fmt.Fprintf(stdout, "scope: %s\n", plan.Scope)
	fmt.Fprintf(stdout, "service: %s\n", plan.Service)
	fmt.Fprintf(stdout, "instance id: %s\n", plan.InstanceID)
	fmt.Fprintf(stdout, "compose project: %s\n", plan.ComposeProject)
	fmt.Fprintf(stdout, "container: %s\n", plan.ContainerName)
	fmt.Fprintf(stdout, "network: %s\n", plan.NetworkName)
	if isDockerManagedInstallBackend(plan.Backend) {
		fmt.Fprintln(stdout, "permanent install backend: Docker-managed Compose")
	}
	if containerUser, err := installContainerUser(plan.SourceDir); err == nil {
		fmt.Fprintf(stdout, "container user: %s\n", containerUser)
	}
	if plan.Backend == installBackendLinuxSystemd {
		if owner, err := installOwnerForDir(plan.SourceDir); err == nil {
			fmt.Fprintf(stdout, "install owner: %s (%d:%d)\n", owner.Spec, owner.UID, owner.GID)
			fmt.Fprintf(stdout, "installed container user: %s\n", owner.ContainerUser)
		} else if spec, err := installOwnerCreationSpecForDir(plan.SourceDir, err); err == nil {
			fmt.Fprintf(stdout, "install owner: %s (will create system user/group)\n", spec)
		} else {
			fmt.Fprintf(stdout, "install owner: unresolved (%v)\n", err)
		}
	}
	for _, port := range plan.Ports {
		fmt.Fprintf(stdout, "port %s: %s:%s -> %s\n", port.Name, port.HostBind, port.HostPort, port.ContainerPort)
	}
	if sources, err := localBundleSourcesForDir(plan.SourceDir); err == nil && len(sources) > 0 {
		fmt.Fprintf(stdout, "would rebuild local source bundle: %s\n", installBundleSourceNames(sources))
	}
	for _, path := range plan.PreservePaths {
		fmt.Fprintf(stdout, "would preserve installed managed path: %s\n", path)
	}
	for _, path := range plan.Replace {
		fmt.Fprintf(stdout, "would replace installed managed path: %s\n", path)
	}
	for _, action := range plan.PathUpdates {
		switch action.Kind {
		case PathPreserveVolume:
			fmt.Fprintf(stdout, "would preserve installed named volume: %s\n", action.Name)
		case PathReplaceVolume:
			fmt.Fprintf(stdout, "would replace installed named volume from staging: %s\n", action.Name)
		}
	}
	if plan.Clean {
		fmt.Fprintln(stdout, "would clean app-owned managed paths")
	}
	fmt.Fprintf(stdout, "would write control script: %s\n", filepath.Join(plan.TargetDir, plan.ControlScript))
	if shouldWriteWindowsPowerShellControlScript(plan) {
		fmt.Fprintf(stdout, "would write PowerShell control script: %s\n", filepath.Join(plan.TargetDir, powerShellControlScriptName(plan.AppID)))
	}
	if plan.Backend == installBackendLinuxSystemd {
		fmt.Fprintln(stdout, "would set installed deployment ownership")
		fmt.Fprintf(stdout, "would write systemd unit: %s\n", plan.UnitPath)
		fmt.Fprintln(stdout, "would run: systemctl daemon-reload")
		fmt.Fprintf(stdout, "would run: systemctl enable %s.service\n", plan.Service)
	}
	if plan.EnvironmentModel && plan.AfterInstallSteps > 0 {
		fmt.Fprintf(stdout, "would run environment after_install lifecycle: %d step(s)\n", plan.AfterInstallSteps)
	}
	if plan.Start {
		if plan.EnvironmentModel {
			if plan.BeforeStartSteps > 0 {
				fmt.Fprintf(stdout, "would run environment before_start lifecycle: %d step(s)\n", plan.BeforeStartSteps)
			}
		} else {
			for _, hook := range plan.Hooks.BeforeStart {
				fmt.Fprintf(stdout, "would run before start hook: %s\n", installHookDescription(hook))
			}
		}
		if plan.Backend == installBackendLinuxSystemd {
			fmt.Fprintf(stdout, "would run: systemctl restart %s.service\n", plan.Service)
		} else {
			spec := composeCommandWithProject(plan.TargetDir, plan.ComposeProject, "up", "-d")
			fmt.Fprintf(stdout, "would run: %s\n", formatCommand(spec.Name, spec.Args...))
		}
		if plan.EnvironmentModel {
			if plan.AfterStartSteps > 0 {
				fmt.Fprintf(stdout, "would satisfy readiness and run environment after_start lifecycle: %d step(s)\n", plan.AfterStartSteps)
			}
		} else {
			for _, hook := range plan.Hooks.AfterStart {
				fmt.Fprintf(stdout, "would run after start hook: %s\n", installHookDescription(hook))
			}
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

func printInstallWarnings(output io.Writer, warnings []string) {
	if output == nil {
		return
	}
	for _, warning := range warnings {
		fmt.Fprintln(output, "warning: "+warning)
	}
}

func installPreservePaths(managedPaths deploy.InstallManagedPathsConfig, replace []string, clean bool) ([]string, error) {
	if clean {
		return nil, nil
	}
	replaceAll := false
	replaced := map[string]bool{}
	declared := map[string]bool{}
	for _, name := range managedPathNames(managedPaths) {
		declared[name] = true
	}
	for _, name := range replace {
		name = cleanManifestPath(name)
		switch {
		case name == ".":
			return nil, fmt.Errorf("--replace must not be empty")
		case name == "all":
			replaceAll = true
		default:
			if !declared[name] {
				return nil, fmt.Errorf("unknown managed install path %q; declared managed paths: %s", name, strings.Join(managedPathNames(managedPaths), ", "))
			}
			replaced[name] = true
		}
	}
	if replaceAll {
		return nil, nil
	}
	paths := []string{}
	entries := append([]deploy.InstallManagedPathConfig{}, managedPaths.Files...)
	entries = append(entries, managedPaths.Dirs...)
	sort.Slice(entries, func(i int, j int) bool {
		return cleanManifestPath(entries[i].Path) < cleanManifestPath(entries[j].Path)
	})
	for _, entry := range entries {
		path := cleanManifestPath(entry.Path)
		if entry.Update != "preserve" || replaced[path] {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
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
	actions, err := PlanPathUpdates(
		DockerExecutionPlan{Mounts: stagingMounts},
		DockerExecutionPlan{Mounts: installedMounts},
		targetDir,
		PathUpdateOptions{ReplaceAll: replaceAll, Clean: clean, Replace: requested},
	)
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
	names := sortedMountPlanNames(installed)
	return "", fmt.Errorf("unknown environment path %q; declared paths: %s", value, strings.Join(names, ", "))
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

func copyDeploymentTreeProtected(sourceDir string, targetDir string, preservePaths []string, controlScript string, generatedSkipPaths ...string) error {
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
		targetPath := filepath.Join(targetDir, relativePath)
		if installCopySkips(relativePath, controlScript, generatedSkipPaths) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if installCopyPreserves(relativePath, targetPath, preservePaths) {
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
		if relativePath == "." {
			if err := rejectInstallTargetSymlink(targetPath); err != nil {
				return err
			}
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if entry.IsDir() {
			if err := rejectInstallTargetSymlink(targetPath); err != nil {
				return err
			}
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to copy special file: %s", path)
		}
		return copyInstallFile(path, targetPath, info.Mode().Perm())
	})
}

func installCopyPreserves(relativePath string, targetPath string, preservePaths []string) bool {
	if relativePath == "." {
		return false
	}
	slashPath := filepath.ToSlash(filepath.Clean(relativePath))
	if slashPath == ".reploy" || strings.HasPrefix(slashPath, ".reploy/") {
		return false
	}
	for _, preservePath := range preservePaths {
		preservePath = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(preservePath)), "/")
		if slashPath != preservePath && !strings.HasPrefix(slashPath, preservePath+"/") {
			continue
		}
		if _, err := os.Lstat(targetPath); err == nil {
			return true
		}
		return false
	}
	return false
}

func installCopySkips(relativePath string, controlScript string, generatedSkipPaths []string) bool {
	slashPath := filepath.ToSlash(relativePath)
	if slashPath == "reploy" || slashPath == RuntimeDirName || slashPath == ToolBinaryFileName || slashPath == embeddedRuntimeFileName() || (controlScript != "" && slashPath == filepath.ToSlash(controlScript)) {
		return true
	}
	for _, skipPath := range generatedSkipPaths {
		skipPath = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(skipPath)), "/")
		if skipPath == "" || skipPath == "." {
			continue
		}
		if slashPath == skipPath || strings.HasPrefix(slashPath, skipPath+"/") {
			return true
		}
	}
	return false
}

func copyInstallFile(sourcePath string, targetPath string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	if err := rejectInstallTargetSymlink(targetPath); err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := openInstallTargetNoFollow(targetPath, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	if err := target.Chmod(mode); err != nil {
		target.Close()
		return err
	}
	if err := target.Close(); err != nil {
		return err
	}
	return nil
}

func rejectInstallTargetSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to overwrite target symlink: %s", path)
	}
	return nil
}

func writeInstallFileNoFollow(path string, content []byte, mode os.FileMode) error {
	if err := rejectInstallTargetSymlink(path); err != nil {
		return err
	}
	target, err := openInstallTargetNoFollow(path, mode)
	if err != nil {
		return err
	}
	if _, err := target.Write(content); err != nil {
		target.Close()
		return err
	}
	if err := target.Chmod(mode); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}

func controlScriptName(appID string) string {
	return dockerNameSlug(appID, "app") + "ctl"
}

func controlScriptNameForPack(pack deploy.AppPack) string {
	if pack.Environment != nil {
		return pack.Environment.Environment.ControlScript
	}
	return controlScriptName(pack.AppID)
}

func validateDeployedControlCommands(commands []deploy.DockerCommandConfig) error {
	seen := map[string]bool{}
	for _, command := range commands {
		if !command.AppCommand {
			return fmt.Errorf("deployed command %s must also set app_command: true", command.Name)
		}
		if len(command.Trigger) == 0 {
			return fmt.Errorf("deployed command %s must declare a trigger", command.Name)
		}
		first := command.Trigger[0]
		if controlScriptBuiltins()[first] {
			return fmt.Errorf("deployed command %q conflicts with built-in control command %q", strings.Join(command.Trigger, " "), first)
		}
		trigger := strings.Join(command.Trigger, " ")
		if seen[trigger] {
			return fmt.Errorf("duplicate deployed command trigger: %s", trigger)
		}
		seen[trigger] = true
	}
	return nil
}

func controlScriptBuiltins() map[string]bool {
	return map[string]bool{
		"up": true, "start": true,
		"down": true, "stop": true,
		"restart": true,
		"status":  true,
		"logs":    true,
		"enable":  true,
		"disable": true,
		"health":  true,
		"help":    true,
		"stage":   true,
		"update":  true,
		"install": true,
		"bundle":  true,
	}
}

func writeInstalledControlScripts(plan installPlan) error {
	if err := removeInstalledReployEntrypoints(plan.TargetDir); err != nil {
		return err
	}
	if err := writeInstalledControlScript(plan); err != nil {
		return err
	}
	if shouldWriteWindowsPowerShellControlScript(plan) {
		if err := writeInstalledPowerShellControlScript(plan); err != nil {
			return err
		}
	}
	return nil
}

func shouldWriteWindowsPowerShellControlScript(plan installPlan) bool {
	return plan.Backend == installBackendDockerDesktop && currentHostPlatform().GOOS == "windows"
}

func writeInstalledControlScript(plan installPlan) error {
	content := []byte(controlScriptContent(plan))
	relativePath := plan.ControlScript
	targetPath := filepath.Join(plan.TargetDir, relativePath)
	if err := writeInstallFileNoFollow(targetPath, content, 0o755); err != nil {
		return err
	}
	manifest, err := loadManifestOrNew(plan.TargetDir)
	if err != nil {
		return err
	}
	manifest.Files[filepath.ToSlash(relativePath)] = deploy.GeneratedFile{
		Kind:   "template",
		SHA256: deploy.HashBytes(content),
	}
	return deploy.WriteDeploymentManifest(filepath.Join(plan.TargetDir, ManifestFileName), manifest)
}

func writeInstalledPowerShellControlScript(plan installPlan) error {
	content := []byte(powerShellDockerDesktopControlScriptContent(plan))
	relativePath := powerShellControlScriptName(plan.AppID)
	targetPath := filepath.Join(plan.TargetDir, relativePath)
	if err := writeInstallFileNoFollow(targetPath, content, 0o644); err != nil {
		return err
	}
	manifest, err := loadManifestOrNew(plan.TargetDir)
	if err != nil {
		return err
	}
	manifest.Files[filepath.ToSlash(relativePath)] = deploy.GeneratedFile{
		Kind:   "template",
		SHA256: deploy.HashBytes(content),
	}
	return deploy.WriteDeploymentManifest(filepath.Join(plan.TargetDir, ManifestFileName), manifest)
}

func removeInstalledReployEntrypoints(targetDir string) error {
	manifest, manifestErr := loadManifestOrNew(targetDir)
	for _, relativePath := range []string{ToolBinaryFileName, "reploy"} {
		path := filepath.Join(targetDir, relativePath)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if manifestErr == nil {
			delete(manifest.Files, filepath.ToSlash(relativePath))
		}
	}
	if manifestErr != nil {
		return nil
	}
	return deploy.WriteDeploymentManifest(filepath.Join(targetDir, ManifestFileName), manifest)
}

func systemdUnit(plan installPlan, dockerBin string, includeDockerUnit bool) string {
	dockerUnitLines := ""
	if includeDockerUnit {
		dockerUnitLines = "Requires=docker.service\nAfter=docker.service\n"
	}
	managedFilePreflights := systemdManagedFilePreflights(plan)
	composeFiles := "--project-directory " + systemdPath(plan.TargetDir) + " -f " + systemdPath(plan.TargetDir, ComposeFileName)
	if plan.ComposeProject != "" {
		composeFiles = "--project-name " + plan.ComposeProject + " " + composeFiles
	}
	if plan.ComposeOverride {
		composeFiles += " -f " + systemdPath(plan.TargetDir, ComposeOverrideFileName)
	}
	targetDir := systemdPath(plan.TargetDir)
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
%sExecStartPre=/bin/sh -c 'i=0; while [ "$i" -lt 120 ]; do [ -x "$1" ] && "$1" info >/dev/null 2>&1 && exit 0; i=$((i + 1)); sleep 1; done; echo "error: Docker API did not become ready for Reploy" >&2; exit 1' reploy-docker-ready %s
ExecStart=%s compose --env-file %s %s up
ExecStop=%s compose --env-file %s %s down
Restart=on-failure
RestartSec=10
TimeoutStartSec=180

[Install]
WantedBy=multi-user.target
`, plan.Service, plan.Service, targetDir, plan.ComposeProject, dockerUnitLines, targetDir, managedFilePreflights, dockerBin, dockerBin, systemdPath(plan.TargetDir, DockerEnvFileName), composeFiles, dockerBin, systemdPath(plan.TargetDir, DockerEnvFileName), composeFiles)
}

func systemdManagedFilePreflights(plan installPlan) string {
	if len(plan.ManagedFiles) == 0 {
		return ""
	}
	lines := make([]string, 0, len(plan.ManagedFiles))
	for _, relativePath := range plan.ManagedFiles {
		path := systemdPath(plan.TargetDir, relativePath)
		lines = append(lines, "ExecStartPre=/bin/sh -c '[ -f \"$1\" ] || { echo \"managed file is missing: $1\" >&2; exit 1; }' reploy-managed-file "+strconv.Quote(path))
	}
	return strings.Join(lines, "\n") + "\n"
}

func systemdPath(elements ...string) string {
	normalized := make([]string, 0, len(elements))
	for _, element := range elements {
		normalized = append(normalized, strings.ReplaceAll(element, `\`, "/"))
	}
	return path.Join(normalized...)
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
	if state.AppID == "" {
		state.AppID = plan.AppID
	}
	runtimeState, err := embeddedRuntimeStateForDir(plan.TargetDir)
	if err != nil {
		return err
	}
	state.Runtime = runtimeState
	if plan.ExistingImages != nil {
		if state.Images == nil {
			state.Images = &deploy.GeneratedImagesState{}
		}
		state.Images.Deployed = plan.ExistingImages.Deployed
		state.Images.Previous = plan.ExistingImages.Previous
	}
	state.Install = &deploy.InstallState{
		TargetDir:      plan.TargetDir,
		Scope:          string(plan.Scope),
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
	updates := dockerEnvPortUpdates(plan.Ports)
	sourceContainerName := envValue(values, "REPLOY_CONTAINER_NAME", "")
	sourceNetworkName := envValue(values, "REPLOY_DOCKER_NETWORK_NAME", "")
	updates["REPLOY_CONTAINER_NAME"] = plan.ContainerName
	updates[reployDeploymentScopeEnv] = reployDeploymentScopeDeployed
	updates["REPLOY_DOCKER_NETWORK_NAME"] = plan.NetworkName
	if shouldUpdateInstalledRuntimeDir(values["REPLOY_RUNTIME_DIR"], sourceContainerName, sourceNetworkName) {
		updates["REPLOY_RUNTIME_DIR"] = dockerRuntimeVolumeName(plan.ComposeProject)
	}
	if plan.Backend == installBackendLinuxSystemd {
		owner, err := resolveInstallOwner(values)
		if err != nil {
			return err
		}
		updates["REPLOY_CONTAINER_USER"] = owner.ContainerUser
		updates["REPLOY_INSTALL_OWNER"] = owner.Spec
	}
	if isDockerManagedInstallBackend(plan.Backend) {
		updates["REPLOY_RESTART"] = "unless-stopped"
	}
	_, err = upsertDockerEnvValues(plan.TargetDir, updates)
	return err
}

func shouldUpdateInstalledRuntimeDir(value string, sourceIdentities ...string) bool {
	if shouldUpdateGeneratedRuntimeDir(value) {
		return true
	}
	value = strings.TrimSpace(value)
	for _, identity := range sourceIdentities {
		if value == dockerRuntimeVolumeName(identity) {
			return true
		}
	}
	return false
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

func installOwnerCreationSpecForDir(dir string, resolveErr error) (string, error) {
	values, err := readDockerEnv(dir)
	if err != nil {
		return "", err
	}
	return installOwnerCreationSpecForResolveError(values, resolveErr)
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

func chownInstalledRuntimeDir(targetDir string, runtimeRelativePath string) error {
	values, err := readDockerEnv(targetDir)
	if err != nil {
		return err
	}
	owner, err := resolveInstallOwner(values)
	if err != nil {
		return err
	}
	return chownInstallPath(filepath.Join(targetDir, runtimeRelativePath), owner.UID, owner.GID)
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
	spec := strings.TrimSpace(values[reployInstallOwnerEnv])
	if spec == "" {
		return resolvedInstallOwner{}, fmt.Errorf("REPLOY_INSTALL_OWNER is required for system install; set it in the blueprint as install.system.run_as or in %s", DockerEnvFileName)
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

func ensureInstallOwnerForDir(dir string) error {
	values, err := readDockerEnv(dir)
	if err != nil {
		return err
	}
	if installOwnerOnMissingPolicy(values) == installOwnerOnMissingCreate {
		if _, _, err := installOwnerNamedParts(values); err != nil {
			return err
		}
	}
	if _, err := resolveInstallOwner(values); err == nil {
		return nil
	} else if installOwnerOnMissingPolicy(values) != installOwnerOnMissingCreate {
		return err
	} else if _, createErr := installOwnerCreationSpecForResolveError(values, err); createErr != nil {
		return createErr
	}
	if err := createMissingInstallOwner(values); err != nil {
		return err
	}
	_, err = resolveInstallOwner(values)
	return err
}

func installOwnerOnMissingPolicy(values map[string]string) string {
	switch strings.TrimSpace(values[reployInstallOwnerOnMissing]) {
	case installOwnerOnMissingCreate:
		return installOwnerOnMissingCreate
	default:
		return installOwnerOnMissingFail
	}
}

func installOwnerCreationSpec(values map[string]string) (string, error) {
	userPart, groupPart, err := installOwnerNamedParts(values)
	if err != nil {
		return "", err
	}
	return userPart + ":" + groupPart, nil
}

func installOwnerCreationSpecForResolveError(values map[string]string, resolveErr error) (string, error) {
	if installOwnerOnMissingPolicy(values) != installOwnerOnMissingCreate {
		return "", resolveErr
	}
	if !isUnknownInstallOwnerLookupError(resolveErr) {
		return "", resolveErr
	}
	return installOwnerCreationReadiness(values)
}

func installOwnerCreationReadiness(values map[string]string) (string, error) {
	userPart, groupPart, err := installOwnerNamedParts(values)
	if err != nil {
		return "", err
	}
	if _, err := installLookupUser(userPart); err != nil && !isUnknownUserError(err) {
		return "", fmt.Errorf("lookup install owner user %q: %w", userPart, err)
	}
	if _, err := installLookupGroup(groupPart); err != nil && !isUnknownGroupError(err) {
		return "", fmt.Errorf("lookup install owner group %q: %w", groupPart, err)
	}
	return userPart + ":" + groupPart, nil
}

func installOwnerNamedParts(values map[string]string) (string, string, error) {
	if installOwnerOnMissingPolicy(values) != installOwnerOnMissingCreate {
		return "", "", fmt.Errorf("%s is not %s", reployInstallOwnerOnMissing, installOwnerOnMissingCreate)
	}
	spec := strings.TrimSpace(values[reployInstallOwnerEnv])
	if spec == "" {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER is required for install")
	}
	userPart, groupPart, hasGroup := strings.Cut(spec, ":")
	userPart = strings.TrimSpace(userPart)
	groupPart = strings.TrimSpace(groupPart)
	if !hasGroup {
		groupPart = userPart
	}
	if userPart == "" || groupPart == "" {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER must name both user and group for account creation: %s", spec)
	}
	if strings.Contains(groupPart, ":") {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER must not contain more than one separator for account creation: %s", spec)
	}
	if _, ok := parseNumericInstallID(userPart); ok {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER user must be named for account creation: %s", spec)
	}
	if _, ok := parseNumericInstallID(groupPart); ok {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER group must be named for account creation: %s", spec)
	}
	if userPart == "root" || groupPart == "root" {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER must not create root-owned deployments: %s", spec)
	}
	if !deploy.IsInstallSystemAccountName(userPart) {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER user must be a safe system account name for account creation: %s", spec)
	}
	if !deploy.IsInstallSystemAccountName(groupPart) {
		return "", "", fmt.Errorf("REPLOY_INSTALL_OWNER group must be a safe system account name for account creation: %s", spec)
	}
	return userPart, groupPart, nil
}

func createMissingInstallOwner(values map[string]string) error {
	userPart, groupPart, err := installOwnerNamedParts(values)
	if err != nil {
		return err
	}
	if _, err := installLookupGroup(groupPart); err != nil {
		if !isUnknownGroupError(err) {
			return fmt.Errorf("lookup install owner group %q: %w", groupPart, err)
		}
		if err := runInstallAccountCommand("groupadd", "--system", groupPart); err != nil {
			return err
		}
	}
	if _, err := installLookupUser(userPart); err != nil {
		if !isUnknownUserError(err) {
			return fmt.Errorf("lookup install owner user %q: %w", userPart, err)
		}
		if err := runInstallAccountCommand("useradd", "--system", "--gid", groupPart, "--home-dir", "/nonexistent", "--no-create-home", "--shell", "/usr/sbin/nologin", userPart); err != nil {
			return err
		}
	}
	return nil
}

func isUnknownInstallOwnerLookupError(err error) bool {
	return isUnknownUserError(err) || isUnknownGroupError(err)
}

func isUnknownUserError(err error) bool {
	var unknown user.UnknownUserError
	return errors.As(err, &unknown)
}

func isUnknownGroupError(err error) bool {
	var unknown user.UnknownGroupError
	return errors.As(err, &unknown)
}

func runInstallAccountCommand(name string, args ...string) error {
	output, err := installRunCommandOutput(name, args...)
	if err == nil {
		return nil
	}
	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, trimmed)
	}
	return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
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
	switch plan.Backend {
	case installBackendLinuxSystemd:
		return applyLinuxSystemdInstallPlan(plan)
	case installBackendDockerDesktop, installBackendDockerManaged:
		return applyDockerDesktopInstallPlan(plan)
	default:
		return currentHostPlatform().unsupportedPersistentInstallError("install")
	}
}

func prepareInstalledDeployment(plan installPlan) error {
	runtimeRelativePath, err := installRuntimeRelativePath(plan.SourceDir)
	if err != nil {
		return err
	}
	if err := prepareEnvironmentPathUpdates(plan); err != nil {
		return fmt.Errorf("prepare environment paths: %w", err)
	}
	if !plan.InPlace {
		installProgress(plan.Progress, "copying staged deployment")
		if err := copyDeploymentTreeProtected(plan.SourceDir, plan.TargetDir, plan.PreservePaths, plan.ControlScript, runtimeRelativePath); err != nil {
			return fmt.Errorf("copy deployment: %w", err)
		}
	} else {
		installProgress(plan.Progress, "using staging directory in place")
	}
	installProgress(plan.Progress, "writing installed control scripts")
	if err := writeInstalledControlScripts(plan); err != nil {
		return fmt.Errorf("write installed control script: %w", err)
	}
	installProgress(plan.Progress, "writing embedded Reploy runtime")
	if _, err := writeInstalledEmbeddedRuntime(plan); err != nil {
		return fmt.Errorf("write embedded Reploy runtime: %w", err)
	}
	installProgress(plan.Progress, "preparing installed runtime directory")
	runtimeDir := filepath.Join(plan.TargetDir, runtimeRelativePath)
	if err := os.RemoveAll(runtimeDir); err != nil {
		return fmt.Errorf("remove install runtime dir: %w", err)
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return fmt.Errorf("create install runtime dir: %w", err)
	}
	if plan.Backend == installBackendLinuxSystemd {
		if err := ensureInstallOwnerForDir(plan.TargetDir); err != nil {
			return fmt.Errorf("ensure install owner: %w", err)
		}
	}
	if err := writeInstalledDockerEnv(plan); err != nil {
		return fmt.Errorf("write installed docker env: %w", err)
	}
	installProgress(plan.Progress, "writing installed deployment state")
	if err := writeInstalledState(plan); err != nil {
		return fmt.Errorf("mark deployment installed: %w", err)
	}
	installedState, err := loadState(plan.TargetDir)
	if err != nil {
		return err
	}
	installedPack, err := deploy.LoadResolvedPack(installedState.Blueprint, installedState.RequestedBlueprintRef, installedState.ResolvedArtifact)
	if err != nil {
		return err
	}
	if installedPack.Environment != nil {
		installProgress(plan.Progress, "materializing installed environment image")
		installedState, err = BuildEnvironmentImage(context.Background(), plan.TargetDir, installedPack, installedState, RunOptions{DockerPreflightTimeout: plan.DockerPreflightTimeout})
		if err != nil {
			return fmt.Errorf("materialize installed environment image: %w", err)
		}
		if _, err := WriteResolvedRuntimeInputs(plan.TargetDir, installedPack, installedState); err != nil {
			return fmt.Errorf("materialize resolved runtime inputs: %w", err)
		}
		if _, err := writeUpdatedStateIfChanged(plan.TargetDir, installedPack, installedState.Bundle, installedState); err != nil {
			return fmt.Errorf("record installed environment image: %w", err)
		}
	} else {
		installProgress(plan.Progress, "materializing runtime compose")
		if _, err := materializeRuntimeCompose(plan.TargetDir); err != nil {
			return fmt.Errorf("materialize runtime compose: %w", err)
		}
	}
	if plan.Backend == installBackendLinuxSystemd {
		if err := chownInstalledRuntimeDir(plan.TargetDir, runtimeRelativePath); err != nil {
			return fmt.Errorf("set install runtime ownership: %w", err)
		}
	}
	if installedPack.Environment == nil {
		if err := rebuildInstalledBundleIfLocalSources(plan); err != nil {
			return fmt.Errorf("rebuild installed bundle: %w", err)
		}
	}
	if plan.Backend == installBackendLinuxSystemd {
		if owner, err := installOwnerForDir(plan.TargetDir); err == nil {
			installProgress(plan.Progress, fmt.Sprintf("setting installed ownership to %s (%d:%d)", owner.Spec, owner.UID, owner.GID))
		} else {
			installProgress(plan.Progress, "setting installed ownership")
		}
		if err := chownInstalledDeployment(plan.TargetDir); err != nil {
			return fmt.Errorf("set installed ownership: %w", err)
		}
	}
	installProgress(plan.Progress, "installed deployment prepared")
	return nil
}

func prepareEnvironmentPathUpdates(plan installPlan) error {
	for _, action := range plan.PathUpdates {
		switch action.Kind {
		case PathReplaceManagedBind:
			if err := os.RemoveAll(action.Target); err != nil {
				return fmt.Errorf("replace managed path %q: %w", action.Name, err)
			}
		case PathValidateUnmanaged:
			if _, err := os.Stat(action.Target); err != nil {
				return fmt.Errorf("validate unmanaged path %q: %w", action.Name, err)
			}
		case PathPreserveVolume:
			exists, err := installVolumeExists(action.Target)
			if err != nil {
				return fmt.Errorf("inspect preserved volume %q: %w", action.Name, err)
			}
			if exists {
				continue
			}
			if err := replaceInstalledVolume(action, plan.PathUpdateImage, false, plan.DockerPreflightTimeout); err != nil {
				return err
			}
		case PathReplaceVolume:
			if err := replaceInstalledVolume(action, plan.PathUpdateImage, true, plan.DockerPreflightTimeout); err != nil {
				return err
			}
		}
	}
	return nil
}

func installVolumeExists(name string) (bool, error) {
	_, err := runInstallPathUpdateOutput(context.Background(), "volume", "inspect", name)
	if err == nil {
		return true, nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "no such volume") || strings.Contains(message, "not found") {
		return false, nil
	}
	return false, err
}

func replaceInstalledVolume(action PathUpdateAction, image string, removeExisting bool, timeout time.Duration) error {
	options := RunOptions{DockerPreflightTimeout: timeout}
	sourceExists, err := installVolumeExists(action.Source)
	if err != nil {
		return fmt.Errorf("inspect staging volume %q: %w", action.Name, err)
	}
	if !sourceExists {
		return fmt.Errorf("staging volume %q is unavailable: %s", action.Name, action.Source)
	}
	if removeExisting {
		if err := runInstallPathUpdateCommand(DockerVolumeRemoveCommand(action.Target), options); err != nil {
			return fmt.Errorf("remove installed volume %q: %w", action.Name, err)
		}
	}
	if err := runInstallPathUpdateCommand(DockerVolumeCreateCommand(action.Target), options); err != nil {
		return fmt.Errorf("create installed volume %q: %w", action.Name, err)
	}
	if err := runInstallPathUpdateCommand(VolumeCopyCommand(action.Source, action.Target, image), options); err != nil {
		_ = runInstallPathUpdateCommand(DockerVolumeRemoveCommand(action.Target), options)
		return fmt.Errorf("copy installed volume %q: %w", action.Name, err)
	}
	return nil
}

func VolumeCopyCommand(source string, target string, image string) CommandSpec {
	return CommandSpec{Name: "docker", Args: []string{
		"run", "--rm",
		"--mount", "type=volume,source=" + source + ",target=/reploy-source,readonly",
		"--mount", "type=volume,source=" + target + ",target=/reploy-target",
		"--entrypoint", "/bin/sh", image,
		"-c", "cp -a /reploy-source/. /reploy-target/",
	}}
}

func applyLinuxSystemdInstallPlan(plan installPlan) error {
	if err := prepareInstalledDeployment(plan); err != nil {
		return err
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
	if err := runInstallAfterInstall(plan); err != nil {
		return err
	}
	if plan.Start {
		installProgress(plan.Progress, "checking managed files")
		if err := ensureManagedFiles(plan.TargetDir, plan.ManagedFiles); err != nil {
			return err
		}
		handled, err := runInstallEnvironmentStart(plan, func(context.Context) error {
			installProgress(plan.Progress, "restarting systemd service")
			if err := installRunCommand(systemctlBin, "restart", plan.Service+".service"); err != nil {
				return fmt.Errorf("systemctl restart %s.service: %w", plan.Service, err)
			}
			return waitInstalledServiceRunning(plan.TargetDir, installServiceStartTimeout, plan.Progress, plan.DockerPreflightTimeout)
		})
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
		if err := runInstallHooks(plan, "before start", plan.Hooks.BeforeStart); err != nil {
			return err
		}
		installProgress(plan.Progress, "restarting systemd service")
		if err := installRunCommand(systemctlBin, "restart", plan.Service+".service"); err != nil {
			return fmt.Errorf("systemctl restart %s.service: %w", plan.Service, err)
		}
		if err := runInstallHooks(plan, "after start", plan.Hooks.AfterStart); err != nil {
			return err
		}
	}
	return nil
}

func installRuntimeRelativePath(dir string) (string, error) {
	values, err := readDockerEnv(dir)
	if err != nil {
		return "", err
	}
	runtimeDir := envValue(values, "REPLOY_RUNTIME_DIR", "./"+RuntimeDirName)
	if isDockerNamedVolumeReference(runtimeDir) {
		return RuntimeDirName, nil
	}
	if filepath.IsAbs(runtimeDir) {
		return "", fmt.Errorf("runtime path must be relative for install: REPLOY_RUNTIME_DIR=%s", runtimeDir)
	}
	clean := filepath.Clean(runtimeDir)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("runtime path must stay under deployment directory: REPLOY_RUNTIME_DIR=%s", runtimeDir)
	}
	return clean, nil
}

func applyDockerDesktopInstallPlan(plan installPlan) error {
	if err := prepareInstalledDeployment(plan); err != nil {
		return err
	}
	if err := runInstallAfterInstall(plan); err != nil {
		return err
	}
	if plan.Start {
		installProgress(plan.Progress, "checking managed files")
		if err := ensureManagedFiles(plan.TargetDir, plan.ManagedFiles); err != nil {
			return err
		}
		handled, err := runInstallEnvironmentStart(plan, func(context.Context) error {
			installProgress(plan.Progress, "starting Docker-managed app")
			if err := ensureRuntimeNamedVolumeWritable(plan.TargetDir, plan.ComposeProject, plan.DockerPreflightTimeout); err != nil {
				return err
			}
			spec := composeCommandWithProject(plan.TargetDir, plan.ComposeProject, "up", "-d")
			if err := runCommand(spec, RunOptions{DockerPreflightTimeout: plan.DockerPreflightTimeout}); err != nil {
				return fmt.Errorf("docker compose up: %w", err)
			}
			return waitInstalledServiceRunning(plan.TargetDir, installServiceStartTimeout, plan.Progress, plan.DockerPreflightTimeout)
		})
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
		if err := runInstallHooks(plan, "before start", plan.Hooks.BeforeStart); err != nil {
			return err
		}
		installProgress(plan.Progress, "starting Docker-managed app")
		if err := ensureRuntimeNamedVolumeWritable(plan.TargetDir, plan.ComposeProject, plan.DockerPreflightTimeout); err != nil {
			return err
		}
		spec := composeCommandWithProject(plan.TargetDir, plan.ComposeProject, "up", "-d")
		if err := runCommand(spec, RunOptions{DockerPreflightTimeout: plan.DockerPreflightTimeout}); err != nil {
			return fmt.Errorf("docker compose up: %w", err)
		}
		if err := waitInstalledServiceRunning(plan.TargetDir, installServiceStartTimeout, plan.Progress, plan.DockerPreflightTimeout); err != nil {
			return err
		}
		if err := runInstallHooks(plan, "after start", plan.Hooks.AfterStart); err != nil {
			return err
		}
	}
	return nil
}

func runEnvironmentInstallStart(plan installPlan, start func(context.Context) error) (bool, error) {
	state, err := loadState(plan.TargetDir)
	if err != nil {
		return false, err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return false, err
	}
	if pack.Environment == nil {
		return false, nil
	}
	resolvedPlan, err := ResolvedDockerExecutionPlan(plan.TargetDir, pack, state)
	if err != nil {
		return true, err
	}
	lifecycle, err := PlanStartLifecycle(*pack.Environment, resolvedPlan, state.Materialization.Executables)
	if err != nil {
		return true, err
	}
	executor := environmentLifecycleExecutor(
		RuntimeOptions{Dir: plan.TargetDir, DockerPreflightTimeout: plan.DockerPreflightTimeout}, resolvedPlan, nil, nil,
	)
	executor.Start = start
	if err := ExecuteLifecycle(context.Background(), lifecycle, executor); err != nil {
		return true, err
	}
	return true, nil
}

func runEnvironmentAfterInstall(plan installPlan) error {
	state, err := loadState(plan.TargetDir)
	if err != nil {
		return err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return err
	}
	if pack.Environment == nil {
		return nil
	}
	resolvedPlan, err := ResolvedDockerExecutionPlan(plan.TargetDir, pack, state)
	if err != nil {
		return err
	}
	lifecycle, err := PlanInstallLifecycle(*pack.Environment, resolvedPlan, state.Materialization.Executables, false)
	if err != nil {
		return err
	}
	afterInstall := LifecyclePlan{}
	for _, operation := range lifecycle.Operations {
		if operation.Kind != LifecycleMaterialize && operation.Kind != LifecycleSuccess {
			afterInstall.Operations = append(afterInstall.Operations, operation)
		}
	}
	if len(afterInstall.Operations) == 0 {
		return nil
	}
	installProgress(plan.Progress, "running environment after_install")
	return ExecuteLifecycle(
		context.Background(), afterInstall,
		environmentLifecycleExecutor(RuntimeOptions{Dir: plan.TargetDir, DockerPreflightTimeout: plan.DockerPreflightTimeout}, resolvedPlan, nil, nil),
	)
}

func runInstallHooks(plan installPlan, phase string, hooks []deploy.DockerInstallHookConfig) error {
	for _, hook := range hooks {
		installProgress(plan.Progress, fmt.Sprintf("running %s hook: %s", phase, installHookDescription(hook)))
		if err := runInstallHook(plan, hook); err != nil {
			return fmt.Errorf("install hook %s %s: %w", phase, installHookDescription(hook), err)
		}
	}
	return nil
}

func runInstallHook(plan installPlan, hook deploy.DockerInstallHookConfig) error {
	if len(hook.App) > 0 {
		var stderr bytes.Buffer
		if err := runInstallAppCommand(plan.TargetDir, hook.App, nil, &stderr, plan.DockerPreflightTimeout); err != nil {
			return commandErrorWithOutput("installed app hook", stderr.Bytes(), err)
		}
		return nil
	}
	if hook.HealthCheck != nil {
		return runInstallHealthCheckHook(plan, hook.HealthCheck)
	}
	return fmt.Errorf("empty install hook")
}

func runInstallHealthCheckHook(plan installPlan, healthCheck *deploy.DockerInstallHealthCheckConfig) error {
	if healthCheck.Wait {
		if err := waitInstalledServiceRunning(plan.TargetDir, installServiceStartTimeout, plan.Progress, plan.DockerPreflightTimeout); err != nil {
			return installedServiceStartError(plan, err)
		}
	}
	return runInstallHealthCheck(plan.TargetDir, nil, nil, installedServiceRestartingDiagnostics(plan), plan.DockerPreflightTimeout)
}

func installedServiceRestartingDiagnostics(plan installPlan) string {
	controlScript := filepath.Join(plan.TargetDir, plan.ControlScript)
	logsCommand := commandLine(CommandSpec{Name: controlScript, Args: []string{"logs"}})
	lines := []string{
		"next steps:",
		"  run " + logsCommand,
	}
	driftedPaths, err := preservedInstallPathDrift(plan)
	if err != nil || len(driftedPaths) == 0 {
		return strings.Join(lines, "\n")
	}
	lines = append(lines,
		"  preserved installed paths differ from staging: "+strings.Join(driftedPaths, ", "),
	)
	if examplePath, ok := installReplaceExamplePath(driftedPaths); ok {
		lines = append(lines, "  replace only the paths you intend to refresh, for example --replace "+shellQuote(examplePath))
	} else {
		lines = append(lines, "  replace only the paths you intend to refresh")
	}
	return strings.Join(lines, "\n")
}

func preservedInstallPathDrift(plan installPlan) ([]string, error) {
	drifted := []string{}
	for _, relativePath := range plan.PreservePaths {
		relativePath = filepath.ToSlash(filepath.Clean(relativePath))
		differs, err := installPathsDiffer(
			filepath.Join(plan.SourceDir, filepath.FromSlash(relativePath)),
			filepath.Join(plan.TargetDir, filepath.FromSlash(relativePath)),
		)
		if err != nil {
			return nil, err
		}
		if differs {
			drifted = append(drifted, relativePath)
		}
	}
	sort.Strings(drifted)
	return drifted, nil
}

func installReplaceExamplePath(paths []string) (string, bool) {
	for _, candidate := range []string{"config", "conf"} {
		for _, path := range paths {
			if path == candidate || strings.Contains(path, "/"+candidate+"/") || strings.HasPrefix(path, candidate+"/") {
				return path, true
			}
		}
	}
	for _, path := range paths {
		base := strings.ToLower(filepath.Base(path))
		if base == "env" || strings.HasSuffix(base, ".env") {
			continue
		}
		return path, true
	}
	return "", false
}

func installPathsDiffer(sourcePath string, targetPath string) (bool, error) {
	sourceSnapshot, err := installPathSnapshot(sourcePath)
	if err != nil {
		return false, err
	}
	targetSnapshot, err := installPathSnapshot(targetPath)
	if err != nil {
		return false, err
	}
	if len(sourceSnapshot) != len(targetSnapshot) {
		return true, nil
	}
	for path, sourceEntry := range sourceSnapshot {
		targetEntry, ok := targetSnapshot[path]
		if !ok || targetEntry != sourceEntry {
			return true, nil
		}
	}
	return false, nil
}

type installPathSnapshotEntry struct {
	Kind   string
	SHA256 string
}

func installPathSnapshot(root string) (map[string]installPathSnapshotEntry, error) {
	entries := map[string]installPathSnapshotEntry{}
	if _, err := os.Lstat(root); err != nil {
		if os.IsNotExist(err) {
			return entries, nil
		}
		return nil, err
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			entries[relativePath] = installPathSnapshotEntry{Kind: "symlink"}
			if entry.IsDir() {
				return filepath.SkipDir
			}
		case info.IsDir():
			entries[relativePath] = installPathSnapshotEntry{Kind: "dir"}
		case info.Mode().IsRegular():
			hash, err := deploy.HashFile(path)
			if err != nil {
				return err
			}
			entries[relativePath] = installPathSnapshotEntry{Kind: "file", SHA256: hash}
		default:
			entries[relativePath] = installPathSnapshotEntry{Kind: info.Mode().Type().String()}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
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
	return BundlePrepare(BundlePrepareOptions{Dir: plan.TargetDir, DockerPreflightTimeout: plan.DockerPreflightTimeout})
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
	controlScript := filepath.Join(plan.TargetDir, plan.ControlScript)
	output, logsErr := installRunCommandOutput(controlScript, "logs")
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

func waitInstalledServiceRunning(dir string, timeout time.Duration, stdout io.Writer, dockerPreflightTimeout time.Duration) error {
	installProgress(stdout, "waiting for installed service to start")
	deadline := time.Now().Add(timeout)
	lastState := ""
	var terminalObservedAt time.Time
	for {
		now := time.Now()
		states, err := composeServiceStates(dir, dockerPreflightTimeout)
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
		switch serviceStateName(state) {
		case "created", "restarting", "starting":
		default:
			return false
		}
	}
	return true
}
