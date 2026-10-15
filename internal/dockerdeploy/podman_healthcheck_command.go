package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

func runPodmanHealthcheckCompatibility(spec CommandSpec, options RunOptions, raw commandRunner) (bool, error) {
	if !dockerCommandExecutableV1(spec.Name) || len(spec.Args) == 0 {
		return false, nil
	}
	args := spec.Args
	if args[0] == "container" && len(args) > 1 {
		args = args[1:]
	}
	composeIndex, _, _ := podmanHealthcheckComposeAction(spec.Args)
	if args[0] != "create" && args[0] != "run" && composeIndex < 0 {
		return false, nil
	}
	endpoint, ok := commandSpecEnvironmentValueV1(spec, "DOCKER_HOST")
	if !ok || !pinnedDockerEndpointV1(spec) {
		return true, fmt.Errorf("Podman healthcheck compatibility requires a pinned local endpoint")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	probe, cancel := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(options.DockerPreflightTimeout))
	client, err := probePodmanHealthcheckClient(probe, endpoint)
	cancel()
	if err != nil || client == nil {
		return err != nil, err
	}
	defer client.http.CloseIdleConnections()
	if composeIndex >= 0 {
		return true, runPodmanHealthcheckCompose(client, spec, options, raw, composeIndex)
	}
	translated, err := preparePodmanSeccompCommand(spec)
	if err != nil {
		return true, err
	}
	if spec.builtinSeccompIndex > 0 {
		args = translated.Args
		spec = translated
	}
	create := spec
	create.Args = append([]string{"create"}, args[1:]...)
	var idOutput, createStderr dockerBuildOutputTail
	creation := options
	creation.Stdin = nil
	creation.Stdout = &idOutput
	if creation.Stderr == nil {
		creation.Stderr = &createStderr
	}
	if err := raw(create, creation); err != nil {
		if spec.builtinSeccompIndex > 0 {
			err = podmanSeccompCreateError(client, err)
		}
		if options.Stdout == nil && options.Stderr == nil && options.Capture == nil && createStderr.String() != "" {
			err = fmt.Errorf("%w\ncommand output:\n%s", err, createStderr.String())
		}
		if id := strings.TrimSpace(idOutput.String()); validPodmanHealthcheckContainerID(id) {
			err = errors.Join(err, removeCreatedPodmanContainer(ctx, spec, id, raw))
		}
		return true, err
	}
	id := strings.TrimSpace(idOutput.String())
	if !validPodmanHealthcheckContainerID(id) {
		return true, fmt.Errorf("Podman create did not return one exact container ID")
	}
	prepare, stop := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(options.DockerPreflightTimeout))
	inspection, err := client.prepare(prepare, id)
	stop()
	if err != nil {
		return true, errors.Join(fmt.Errorf("prepare disabled Podman healthcheck before start: %w", err), removeCreatedPodmanContainer(ctx, spec, id, raw))
	}
	if args[0] == "create" {
		if options.Stdout != nil {
			if _, err := io.WriteString(options.Stdout, id+"\n"); err != nil {
				return true, errors.Join(err, removeCreatedPodmanContainer(ctx, spec, id, raw))
			}
		}
		return true, nil
	}
	start := spec
	start.Args = []string{"start", "--attach"}
	if inspection.Config.OpenStdin {
		start.Args = append(start.Args, "--interactive")
	}
	start.Args = append(start.Args, id)
	err = raw(start, options)
	if err != nil {
		err = errors.Join(err, removeCreatedPodmanContainer(ctx, spec, id, raw))
	}
	return true, err
}
