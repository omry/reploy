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
)

type ReploySystemdService struct {
	ServiceName    string
	TargetDir      string
	ComposeProject string
	UnitPath       string
}

var uninstallLookPath = exec.LookPath
var uninstallRunCommand = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
var uninstallRunDockerCommand = func(spec CommandSpec, dockerPreflightTimeout time.Duration) error {
	return runCommand(spec, RunOptions{DockerPreflightTimeout: dockerPreflightTimeout})
}
var uninstallRemove = os.Remove
var uninstallSystemdUnitDir = defaultSystemdUnitDir

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

func formatCommand(name string, args ...string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}
