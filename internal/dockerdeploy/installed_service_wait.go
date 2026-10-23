package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func waitInstalledServiceWorkloadV1(ctx context.Context, dockerPath, container, service string, options RunOptions, run commandRunner) error {
	var status bytes.Buffer
	waitOptions := options
	waitOptions.Context, waitOptions.Stdin, waitOptions.Stdout = ctx, nil, &status
	if err := run(CommandSpec{Name: dockerPath, Args: []string{"wait", container}}, waitOptions); err != nil {
		return fmt.Errorf("wait for installed workload container: %w", err)
	}
	exitCode, err := strconv.Atoi(strings.TrimSpace(status.String()))
	if err != nil || exitCode < 0 || exitCode > 255 {
		return fmt.Errorf("Docker returned an invalid workload exit status")
	}
	if exitCode == 0 {
		return nil
	}
	failure := fmt.Errorf("installed workload container exited with status %d", exitCode)
	stopping, err := installedServiceManagerStoppingV1(ctx, service, options.Stderr)
	if err != nil {
		return errors.Join(failure, err)
	}
	if !stopping {
		return failure
	}
	if options.Stderr != nil {
		fmt.Fprintf(options.Stderr, "installed workload stopped by systemd; container exit status %d\n", exitCode)
	}
	return nil
}

// A nonzero container exit is not stop intent. Require the system manager to
// be stopping this exact controller. Its ExecStop result still determines
// whether container and network cleanup succeeded.
func installedServiceManagerStoppingV1(ctx context.Context, service string, stderr io.Writer) (bool, error) {
	systemctl, err := providerInstallAbsoluteToolPathV1(exec.LookPath, "systemctl")
	if err != nil {
		return false, fmt.Errorf("confirm installed service stop intent: %w", err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, privateWorkloadEnvironmentTimeoutV1)
	defer cancel()
	var output bytes.Buffer
	if err := runCommandWithoutDockerPreflight(CommandSpec{
		Name: systemctl,
		Args: []string{"--system", "show", "--property=ActiveState,MainPID", "--", service + ".service"},
	}, RunOptions{Context: probeCtx, Stdout: &output, Stderr: stderr}); err != nil {
		return false, fmt.Errorf("confirm installed service stop intent: %w", errors.Join(err, probeCtx.Err()))
	}
	properties := make(map[string]string, 2)
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || (key != "ActiveState" && key != "MainPID") || value == "" {
			return false, fmt.Errorf("systemd returned invalid installed service stop evidence")
		}
		if _, duplicate := properties[key]; duplicate {
			return false, fmt.Errorf("systemd returned duplicate installed service stop evidence")
		}
		properties[key] = value
	}
	pid, err := strconv.Atoi(properties["MainPID"])
	if err != nil || pid < 0 || properties["ActiveState"] == "" {
		return false, fmt.Errorf("systemd returned incomplete installed service stop evidence")
	}
	return properties["ActiveState"] == "deactivating" && pid == os.Getpid(), nil
}
