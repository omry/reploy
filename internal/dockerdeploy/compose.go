package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

type CommandSpec struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

type RunOptions struct {
	Context context.Context
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

func runCommand(spec CommandSpec, options RunOptions) error {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.CommandContext(ctx, spec.Name, spec.Args...)
	command.Dir = spec.Dir
	if len(spec.Env) > 0 {
		command.Env = append(os.Environ(), spec.Env...)
	}
	command.Stdin = options.Stdin
	command.Stdout = options.Stdout
	command.Stderr = options.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", spec.Name, err)
	}
	return nil
}

type commandRunner func(CommandSpec, RunOptions) error

func runInterruptibleCommand(run commandRunner, spec CommandSpec, options RunOptions) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runOptions := options
	runOptions.Context = ctx

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	done := make(chan error, 1)
	go func() {
		done <- run(spec, runOptions)
	}()

	select {
	case err := <-done:
		return err
	case sig := <-signals:
		cancel()
		if err := <-done; err != nil {
			return fmt.Errorf("interrupted by %s: %w", sig, err)
		}
		return fmt.Errorf("interrupted by %s", sig)
	}
}

func deploymentComposeProjectName(dir string) (string, error) {
	if state, err := loadState(dir); err == nil {
		if state.Install != nil && state.Install.ComposeProject != "" {
			return state.Install.ComposeProject, nil
		}
	}
	values, err := readDockerEnv(dir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", DockerEnvFileName, err)
	}
	if projectName := envValue(values, "REPLOY_CONTAINER_NAME", ""); projectName != "" {
		return projectName, nil
	}
	if projectName := envValue(values, "REPLOY_DOCKER_NETWORK_NAME", ""); projectName != "" {
		return projectName, nil
	}
	return "", nil
}

func composeCommand(dir string, args ...string) CommandSpec {
	return composeCommandWithProject(dir, "", args...)
}

func composeCommandWithProject(dir string, projectName string, args ...string) CommandSpec {
	if absoluteDir, err := filepath.Abs(dir); err == nil {
		dir = absoluteDir
	}
	composeArgs := []string{"compose"}
	if projectName != "" {
		composeArgs = append(composeArgs, "--project-name", projectName)
	}
	composeArgs = append(
		composeArgs,
		"--project-directory",
		dir,
		"--env-file",
		filepath.Join(dir, DockerEnvFileName),
		"-f",
		filepath.Join(dir, ComposeFileName),
	)
	return CommandSpec{
		Name: "docker",
		Args: append(composeArgs, args...),
		Dir:  dir,
	}
}
