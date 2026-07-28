package dockerdeploy

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type dockerComposeProjectRemovalBackendV1 struct {
	output func(CommandSpec, RunOptions) ([]byte, error)
	run    commandRunner
}

func removeDockerComposeProjectByLabelV1(
	ctx context.Context,
	project string,
	timeout time.Duration,
) error {
	return removeDockerComposeProjectByLabelWithV1(
		ctx,
		project,
		timeout,
		dockerComposeProjectRemovalBackendV1{
			output: commandOutput,
			run:    runCommand,
		},
	)
}

func removeDockerComposeProjectByLabelWithV1(
	ctx context.Context,
	project string,
	timeout time.Duration,
	backend dockerComposeProjectRemovalBackendV1,
) error {
	if backend.output == nil || backend.run == nil {
		return fmt.Errorf("remove Docker Compose project requires a complete backend")
	}
	options := RunOptions{Context: ctx, DockerPreflightTimeout: timeout}
	containerIDs, err := dockerComposeProjectIDsByLabelV1(
		options,
		backend.output,
		"ps",
		"-a",
		project,
	)
	if err != nil {
		return err
	}
	if len(containerIDs) != 0 {
		if err := backend.run(
			CommandSpec{
				Name: "docker",
				Args: append([]string{"rm", "-f"}, containerIDs...),
			},
			options,
		); err != nil {
			return err
		}
	}
	networkIDs, err := dockerComposeProjectIDsByLabelV1(
		options,
		backend.output,
		"network",
		"ls",
		project,
	)
	if err != nil {
		return err
	}
	if len(networkIDs) != 0 {
		if err := backend.run(
			CommandSpec{
				Name: "docker",
				Args: append([]string{"network", "rm"}, networkIDs...),
			},
			options,
		); err != nil {
			return err
		}
	}
	return nil
}

func dockerComposeProjectIDsByLabelV1(
	options RunOptions,
	outputCommand func(CommandSpec, RunOptions) ([]byte, error),
	first string,
	second string,
	project string,
) ([]string, error) {
	output, err := outputCommand(
		CommandSpec{
			Name: "docker",
			Args: []string{
				first,
				second,
				"--filter",
				"label=com.docker.compose.project=" + project,
				"--format",
				"{{.ID}}",
			},
		},
		options,
	)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, line := range strings.Split(string(output), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
