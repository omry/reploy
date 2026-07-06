package dockerdeploy

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omry/reploy/internal/deploy"
)

type UninstallOptions struct {
	From        string
	ServiceName string
	RemoveDir   bool
	DryRun      bool
	Stdout      io.Writer

	DockerPreflightTimeout time.Duration
}

type uninstallPlan struct {
	TargetDir      string
	TargetExists   bool
	ServiceName    string
	UnitPath       string
	ComposeProject string
	ContainerName  string
	NetworkName    string
	RemoveDir      bool
	Backend        installBackend

	DockerPreflightTimeout time.Duration
}

type ReploySystemdService struct {
	ServiceName    string
	TargetDir      string
	ComposeProject string
	UnitPath       string
}

var uninstallGeteuid = os.Geteuid
var uninstallLookPath = exec.LookPath
var uninstallRunCommand = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
var uninstallRunCommandOutput = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
var uninstallRunDockerCommand = func(spec CommandSpec, dockerPreflightTimeout time.Duration) error {
	return runCommand(spec, RunOptions{DockerPreflightTimeout: dockerPreflightTimeout})
}
var uninstallRunDockerCommandOutput = func(spec CommandSpec, dockerPreflightTimeout time.Duration) ([]byte, error) {
	return commandOutput(spec, RunOptions{DockerPreflightTimeout: dockerPreflightTimeout})
}
var uninstallRemove = os.Remove
var uninstallRemoveAll = os.RemoveAll
var uninstallSystemdUnitDir = defaultSystemdUnitDir

func Uninstall(options UninstallOptions) error {
	plan, err := newUninstallPlan(options)
	if err != nil {
		return err
	}
	if options.DryRun {
		printUninstallDryRun(options.Stdout, plan)
		return nil
	}
	if plan.Backend == installBackendLinuxSystemd && uninstallGeteuid() != 0 {
		return fmt.Errorf("uninstall requires root unless --dry-run is set")
	}
	return applyUninstallPlan(plan, options.Stdout)
}

func UninstallNeedsRoot(options UninstallOptions) bool {
	if options.DryRun {
		return false
	}
	from := strings.TrimSpace(options.From)
	if from == "" {
		if state, err := loadState("."); err == nil && state.Phase == deploy.PhaseInstalled && state.Install != nil {
			from = "."
		}
	}
	if from != "" {
		if state, err := loadState(from); err == nil && state.Install != nil {
			backend := currentHostPlatform().installBackend()
			if scope, scopeErr := ParseInstallScope(state.Install.Scope); scopeErr == nil {
				backend = currentHostPlatform().installBackendForScope(scope)
			}
			return backend == installBackendLinuxSystemd
		}
	}
	return currentHostPlatform().installBackend() == installBackendLinuxSystemd
}

func newUninstallPlan(options UninstallOptions) (uninstallPlan, error) {
	if strings.TrimSpace(options.From) == "" {
		if state, err := loadState("."); err == nil && state.Phase == deploy.PhaseInstalled && state.Install != nil {
			options.From = "."
		}
	}
	if strings.TrimSpace(options.From) == "" {
		if strings.TrimSpace(options.ServiceName) == "" {
			return uninstallPlan{}, fmt.Errorf("--from is required unless --service-name is set or the current directory is an installed deployment")
		}
		if currentHostPlatform().installBackend() == installBackendDockerDesktop {
			return uninstallPlan{}, fmt.Errorf("--from is required for Docker-managed uninstall")
		}
		return serviceOnlyUninstallPlan(options.ServiceName, options.RemoveDir, options.DockerPreflightTimeout)
	}
	from, err := filepath.Abs(options.From)
	if err != nil {
		return uninstallPlan{}, err
	}
	info, err := os.Stat(from)
	if err != nil {
		if os.IsNotExist(err) {
			if strings.TrimSpace(options.ServiceName) == "" {
				return uninstallPlan{}, fmt.Errorf("--service-name is required when --from is missing: %s", from)
			}
			if currentHostPlatform().installBackend() == installBackendDockerDesktop {
				return uninstallPlan{}, fmt.Errorf("Docker-managed uninstall requires an installed deployment state at --from: %s", from)
			}
			plan, err := serviceOnlyUninstallPlan(options.ServiceName, options.RemoveDir, options.DockerPreflightTimeout)
			if err != nil {
				return uninstallPlan{}, err
			}
			plan.TargetDir = from
			return plan, nil
		}
		return uninstallPlan{}, err
	}
	if !info.IsDir() {
		return uninstallPlan{}, fmt.Errorf("--from must be a directory: %s", from)
	}
	state, err := loadState(from)
	if err != nil {
		return uninstallPlan{}, err
	}
	if state.Phase != deploy.PhaseInstalled || state.Install == nil {
		return uninstallPlan{}, fmt.Errorf("--from is not an installed deployment: %s", from)
	}
	install := state.Install
	if strings.TrimSpace(install.Service) == "" {
		return uninstallPlan{}, fmt.Errorf("installed deployment state is missing service name")
	}
	if options.ServiceName != "" && options.ServiceName != install.Service {
		return uninstallPlan{}, fmt.Errorf("--service-name %q does not match installed service %q", options.ServiceName, install.Service)
	}
	backend := currentHostPlatform().installBackend()
	if scope, scopeErr := ParseInstallScope(install.Scope); scopeErr == nil {
		backend = currentHostPlatform().installBackendForScope(scope)
	}
	unitPath := install.UnitPath
	if backend == installBackendLinuxSystemd {
		unitPath = defaultString(unitPath, filepath.Join(uninstallSystemdUnitDir, install.Service+".service"))
	}
	return uninstallPlan{
		TargetDir:              from,
		TargetExists:           true,
		ServiceName:            install.Service,
		UnitPath:               unitPath,
		ComposeProject:         install.ComposeProject,
		ContainerName:          install.ContainerName,
		NetworkName:            install.NetworkName,
		RemoveDir:              options.RemoveDir,
		Backend:                backend,
		DockerPreflightTimeout: options.DockerPreflightTimeout,
	}, nil
}

func serviceOnlyUninstallPlan(serviceName string, removeDir bool, dockerPreflightTimeout time.Duration) (uninstallPlan, error) {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return uninstallPlan{}, fmt.Errorf("--service-name must not be empty")
	}
	if !validServiceName(serviceName) {
		return uninstallPlan{}, fmt.Errorf("--service-name contains unsupported characters: %s", serviceName)
	}
	unitPath := filepath.Join(uninstallSystemdUnitDir, serviceName+".service")
	if _, err := os.Stat(unitPath); err != nil {
		if os.IsNotExist(err) {
			return uninstallPlan{}, fmt.Errorf("service unit not found for --service-name %s at %s; run reploy services list", serviceName, unitPath)
		}
		return uninstallPlan{}, err
	}
	identity, err := readSystemdUnitInstallIdentity(unitPath)
	if err != nil {
		return uninstallPlan{}, err
	}
	return uninstallPlan{
		TargetDir:              identity.TargetDir,
		TargetExists:           false,
		ServiceName:            serviceName,
		UnitPath:               unitPath,
		ComposeProject:         identity.ComposeProject,
		RemoveDir:              removeDir,
		Backend:                installBackendLinuxSystemd,
		DockerPreflightTimeout: dockerPreflightTimeout,
	}, nil
}

func PrintReploySystemdServices(stdout io.Writer) error {
	if stdout == nil {
		return nil
	}
	if currentHostPlatform().installBackend() != installBackendLinuxSystemd {
		return fmt.Errorf("services list is Linux/systemd-only; use uninstall --from for Docker-managed installs")
	}
	services, err := ListReploySystemdServices()
	if err != nil {
		return err
	}
	if len(services) == 0 {
		fmt.Fprintln(stdout, "no reploy services found")
		return nil
	}
	writer := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "SERVICE\tTARGET\tCOMPOSE_PROJECT\tUNIT")
	for _, service := range services {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", valueOrDash(service.ServiceName), valueOrDash(service.TargetDir), valueOrDash(service.ComposeProject), service.UnitPath)
	}
	return writer.Flush()
}

func ListReploySystemdServices() ([]ReploySystemdService, error) {
	entries, err := os.ReadDir(uninstallSystemdUnitDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var services []ReploySystemdService
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".service") {
			continue
		}
		unitPath := filepath.Join(uninstallSystemdUnitDir, entry.Name())
		service, ok, err := readReploySystemdService(unitPath)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if service.ServiceName == "" {
			service.ServiceName = strings.TrimSuffix(entry.Name(), ".service")
		}
		service.UnitPath = unitPath
		services = append(services, service)
	}
	sort.Slice(services, func(i, j int) bool {
		return services[i].ServiceName < services[j].ServiceName
	})
	return services, nil
}

func readReploySystemdService(unitPath string) (ReploySystemdService, bool, error) {
	content, err := os.ReadFile(unitPath)
	if err != nil {
		return ReploySystemdService{}, false, err
	}
	service, managed := parseReploySystemdService(string(content))
	return service, managed, nil
}

func parseReploySystemdService(content string) (ReploySystemdService, bool) {
	var service ReploySystemdService
	managed := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "# Managed-By: reploy":
			managed = true
		case strings.HasPrefix(line, "# Reploy-Service:"):
			managed = true
			service.ServiceName = strings.TrimSpace(strings.TrimPrefix(line, "# Reploy-Service:"))
		case strings.HasPrefix(line, "# Reploy-Target:"):
			managed = true
			service.TargetDir = strings.TrimSpace(strings.TrimPrefix(line, "# Reploy-Target:"))
		case strings.HasPrefix(line, "# Reploy-Compose-Project:"):
			managed = true
			service.ComposeProject = strings.TrimSpace(strings.TrimPrefix(line, "# Reploy-Compose-Project:"))
		case strings.HasPrefix(line, "Description=Reploy Docker service ("):
			managed = true
			if service.ServiceName == "" {
				service.ServiceName = strings.TrimSuffix(strings.TrimPrefix(line, "Description=Reploy Docker service ("), ")")
			}
		case strings.HasPrefix(line, "WorkingDirectory="):
			if service.TargetDir == "" {
				service.TargetDir = strings.TrimSpace(strings.TrimPrefix(line, "WorkingDirectory="))
			}
		case strings.HasPrefix(line, "ExecStart="), strings.HasPrefix(line, "ExecStop="):
			if service.ComposeProject == "" {
				service.ComposeProject = commandLineFlagValue(strings.Fields(line), "--project-name")
			}
		}
	}
	return service, managed
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

type unitInstallIdentity struct {
	TargetDir      string
	ComposeProject string
}

func readSystemdUnitInstallIdentity(unitPath string) (unitInstallIdentity, error) {
	content, err := os.ReadFile(unitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return unitInstallIdentity{}, nil
		}
		return unitInstallIdentity{}, err
	}
	var identity unitInstallIdentity
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "WorkingDirectory="):
			identity.TargetDir = strings.TrimSpace(strings.TrimPrefix(line, "WorkingDirectory="))
		case strings.HasPrefix(line, "ExecStart="), strings.HasPrefix(line, "ExecStop="):
			if project := commandLineFlagValue(strings.Fields(line), "--project-name"); project != "" {
				identity.ComposeProject = project
			}
		}
	}
	return identity, nil
}

func commandLineFlagValue(fields []string, flag string) string {
	for index, field := range fields {
		if field == flag && index+1 < len(fields) {
			return fields[index+1]
		}
		if strings.HasPrefix(field, flag+"=") {
			return strings.TrimPrefix(field, flag+"=")
		}
	}
	return ""
}

func printUninstallDryRun(stdout io.Writer, plan uninstallPlan) {
	if stdout == nil {
		return
	}
	fmt.Fprintf(stdout, "would uninstall service: %s\n", plan.ServiceName)
	if plan.TargetDir != "" {
		if plan.TargetExists {
			fmt.Fprintf(stdout, "target: %s\n", plan.TargetDir)
		} else {
			fmt.Fprintf(stdout, "target: %s (missing)\n", plan.TargetDir)
		}
	} else {
		fmt.Fprintln(stdout, "target: not available")
	}
	if plan.Backend == installBackendLinuxSystemd {
		fmt.Fprintf(stdout, "unit: %s\n", plan.UnitPath)
	} else {
		fmt.Fprintln(stdout, "permanent install backend: Docker-managed Compose")
	}
	if plan.ComposeProject != "" {
		fmt.Fprintf(stdout, "compose project: %s\n", plan.ComposeProject)
	}
	if plan.ContainerName != "" {
		fmt.Fprintf(stdout, "container: %s\n", plan.ContainerName)
	}
	if plan.NetworkName != "" {
		fmt.Fprintf(stdout, "network: %s\n", plan.NetworkName)
	}
	if plan.Backend == installBackendLinuxSystemd {
		fmt.Fprintf(stdout, "would run: systemctl stop %s.service\n", plan.ServiceName)
	}
	if plan.TargetExists {
		spec := composeCommandWithProject(plan.TargetDir, plan.ComposeProject, "down", "--remove-orphans")
		fmt.Fprintf(stdout, "would run: %s\n", formatCommand(spec.Name, spec.Args...))
	} else if plan.ComposeProject != "" {
		fmt.Fprintf(stdout, "would remove Docker containers with label com.docker.compose.project=%s\n", plan.ComposeProject)
		fmt.Fprintf(stdout, "would remove Docker networks with label com.docker.compose.project=%s\n", plan.ComposeProject)
	} else {
		fmt.Fprintln(stdout, "docker cleanup: skipped (no compose project recovered)")
	}
	if plan.Backend == installBackendLinuxSystemd {
		fmt.Fprintf(stdout, "would run: systemctl disable %s.service\n", plan.ServiceName)
		fmt.Fprintf(stdout, "would remove: %s\n", plan.UnitPath)
		fmt.Fprintln(stdout, "would run: systemctl daemon-reload")
	}
	if plan.RemoveDir {
		if plan.TargetExists {
			fmt.Fprintf(stdout, "would remove target directory: %s\n", plan.TargetDir)
		} else {
			fmt.Fprintln(stdout, "target directory removal: skipped (target is missing or unverified)")
		}
	} else {
		fmt.Fprintln(stdout, "target directory: kept")
	}
}

func applyUninstallPlan(plan uninstallPlan, stdout io.Writer) error {
	if isDockerManagedInstallBackend(plan.Backend) {
		return applyDockerDesktopUninstallPlan(plan, stdout)
	}
	if plan.Backend != installBackendLinuxSystemd {
		return currentHostPlatform().unsupportedPersistentInstallError("uninstall")
	}
	systemctlBin, err := uninstallLookPath("systemctl")
	if err != nil {
		return fmt.Errorf("systemctl command not found: %w", err)
	}
	if err := uninstallRunCommand(systemctlBin, "stop", plan.ServiceName+".service"); err != nil {
		uninstallWarn(stdout, "systemctl stop %s.service failed: %v", plan.ServiceName, err)
	}
	if err := runUninstallDockerCleanup(plan, stdout); err != nil {
		uninstallWarn(stdout, "docker cleanup failed: %v", err)
	}
	if err := uninstallRunCommand(systemctlBin, "disable", plan.ServiceName+".service"); err != nil {
		uninstallWarn(stdout, "systemctl disable %s.service failed: %v", plan.ServiceName, err)
	}
	if err := uninstallRemove(plan.UnitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove systemd unit: %w", err)
	}
	if err := uninstallRunCommand(systemctlBin, "daemon-reload"); err != nil {
		uninstallWarn(stdout, "systemctl daemon-reload failed: %v", err)
	}
	if plan.RemoveDir && plan.TargetExists {
		if err := uninstallRemoveAll(plan.TargetDir); err != nil {
			return fmt.Errorf("remove target directory: %w", err)
		}
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "uninstalled service: %s\n", plan.ServiceName)
	}
	return nil
}

func applyDockerDesktopUninstallPlan(plan uninstallPlan, stdout io.Writer) error {
	if err := runUninstallDockerCleanup(plan, stdout); err != nil {
		uninstallWarn(stdout, "docker cleanup failed: %v", err)
	}
	if plan.RemoveDir && plan.TargetExists {
		if err := uninstallRemoveAll(plan.TargetDir); err != nil {
			return fmt.Errorf("remove target directory: %w", err)
		}
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "uninstalled service: %s\n", plan.ServiceName)
	}
	return nil
}

func runUninstallDockerCleanup(plan uninstallPlan, stdout io.Writer) error {
	if plan.TargetExists {
		spec := composeCommandWithProject(plan.TargetDir, plan.ComposeProject, "down", "--remove-orphans")
		if err := uninstallRunDockerCommand(spec, plan.DockerPreflightTimeout); err == nil {
			return nil
		} else if plan.ComposeProject == "" {
			return err
		} else {
			uninstallWarn(stdout, "compose cleanup failed; falling back to Docker labels for project %s: %v", plan.ComposeProject, err)
		}
	}
	if plan.ComposeProject == "" {
		return nil
	}
	return removeDockerComposeProjectByLabel(plan.ComposeProject, plan.DockerPreflightTimeout)
}

func removeDockerComposeProjectByLabel(project string, dockerPreflightTimeout time.Duration) error {
	containerIDs, err := dockerIDsByLabel("ps", "-a", project, dockerPreflightTimeout)
	if err != nil {
		return err
	}
	if len(containerIDs) > 0 {
		args := append([]string{"rm", "-f"}, containerIDs...)
		if err := uninstallRunDockerCommand(CommandSpec{Name: "docker", Args: args}, dockerPreflightTimeout); err != nil {
			return err
		}
	}
	networkIDs, err := dockerIDsByLabel("network", "ls", project, dockerPreflightTimeout)
	if err != nil {
		return err
	}
	if len(networkIDs) > 0 {
		args := append([]string{"network", "rm"}, networkIDs...)
		if err := uninstallRunDockerCommand(CommandSpec{Name: "docker", Args: args}, dockerPreflightTimeout); err != nil {
			return err
		}
	}
	return nil
}

func dockerIDsByLabel(first string, second string, project string, dockerPreflightTimeout time.Duration) ([]string, error) {
	output, err := uninstallRunDockerCommandOutput(CommandSpec{
		Name: "docker",
		Args: []string{first, second, "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.ID}}"},
	}, dockerPreflightTimeout)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(output)), nil
}

func nonEmptyLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func uninstallWarn(stdout io.Writer, format string, args ...any) {
	if stdout == nil {
		return
	}
	fmt.Fprintf(stdout, "warning: "+format+"\n", args...)
}

func formatCommand(name string, args ...string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}
