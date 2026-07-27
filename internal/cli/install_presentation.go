package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/dockerdeploy"
)

func writeInstallResult(output io.Writer, result dockerdeploy.ProviderInstallResultV1, successLines []string) {
	prefix := deployedResultPrefix(result.Environment, result.Service)
	if result.Updated {
		fmt.Fprintf(output, "%s update completed successfully\n", prefix)
		writeInstallUpdateActions(output, prefix, result)
	} else {
		fmt.Fprintf(output, "%s installed successfully\n", prefix)
	}
	fmt.Fprintf(output, "%s location: %s\n", prefix, result.TargetDir)
	if result.ControlScript != "" {
		fmt.Fprintf(output, "%s control: %s\n", prefix, filepath.Join(result.TargetDir, result.ControlScript))
	}
	status := "stopped"
	if result.Started {
		status = "running"
	}
	fmt.Fprintf(output, "%s status: %s\n", prefix, status)
	if result.State.Deployment != nil {
		for _, port := range result.State.Deployment.Installation.Ports {
			fmt.Fprintf(output, "%s endpoint %s: %s:%s\n", prefix, port.Name, port.HostBind, port.HostPort)
		}
	}
	for _, line := range successLines {
		line = strings.TrimSpace(line)
		if line != "" {
			fmt.Fprintf(output, "%s %s\n", prefix, line)
		}
	}
}

func writeInstallUpdateActions(output io.Writer, prefix string, result dockerdeploy.ProviderInstallResultV1) {
	for _, action := range result.PathUpdates {
		switch action.Kind {
		case dockerdeploy.PathPreserveManagedBind, dockerdeploy.PathPreserveVolume:
			fmt.Fprintf(output, "%s preserved: %s (%s)\n", prefix, action.Name, action.Target)
		case dockerdeploy.PathReplaceManagedBind, dockerdeploy.PathReplaceVolume:
			fmt.Fprintf(output, "%s replaced: %s (%s)\n", prefix, action.Name, action.Target)
		case dockerdeploy.PathValidateUnmanaged:
			fmt.Fprintf(output, "%s retained: %s (%s)\n", prefix, action.Name, action.Target)
		}
	}
	fmt.Fprintf(output, "%s replaced: service instance\n", prefix)
	fmt.Fprintf(output, "%s replaced: deployment files\n", prefix)
	if result.ImageReused {
		fmt.Fprintf(output, "%s reused: environment image\n", prefix)
	} else {
		fmt.Fprintf(output, "%s replaced: environment image\n", prefix)
	}
}

func writeUninstallResult(output io.Writer, result dockerdeploy.ProviderUninstallResultV1) {
	if result.AlreadyAbsent {
		fmt.Fprintf(output, "No installation found at %s; it may already have been removed.\n", result.DeploymentDir)
		return
	}
	prefix := deployedResultPrefix(result.Environment, result.Service)
	fmt.Fprintf(output, "%s uninstalled successfully\n", prefix)
	fmt.Fprintf(output, "%s removed: service %s\n", prefix, result.Service)
	fmt.Fprintf(output, "%s removed: runtime resources\n", prefix)
	if result.RemovedDirectory {
		fmt.Fprintf(output, "%s removed: installation directory %s\n", prefix, result.DeploymentDir)
	} else if result.RetainedDirectory {
		fmt.Fprintf(output, "%s retained: installation directory %s\n", prefix, result.DeploymentDir)
	}
}

func deployedResultPrefix(environment string, service string) string {
	identity := strings.TrimSpace(environment)
	if identity == "" {
		identity = strings.TrimSpace(service)
	}
	if identity == "" {
		identity = "deployment"
	}
	return "[DEPLOYED : " + identity + "]"
}

func installFailureDiagnostic(err error, childOutput string) string {
	if err == nil {
		return "operation failed"
	}
	details := make([]string, 0, 8)
	for _, rawLine := range strings.Split(stripBuildTerminalControls(childOutput), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || dockerLifecycleChatter(line) || strings.Contains(err.Error(), line) {
			continue
		}
		details = append(details, line)
		if len(details) > 8 {
			details = details[len(details)-8:]
		}
	}
	if len(details) == 0 {
		return err.Error()
	}
	return err.Error() + "\noperation output:\n" + strings.Join(details, "\n")
}

func dockerLifecycleChatter(line string) bool {
	for _, resource := range []string{"Network ", "Container "} {
		if !strings.HasPrefix(line, resource) {
			continue
		}
		for _, action := range []string{
			" Creating", " Created", " Starting", " Started",
			" Stopping", " Stopped", " Removing", " Removed",
		} {
			if strings.HasSuffix(line, action) {
				return true
			}
		}
	}
	return false
}
