package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Reploy emits explicit project/file arguments and detached up commands. Keep
// this adapter confined to those commands rather than reinterpret arbitrary CLI.
func podmanHealthcheckComposeAction(args []string) (int, string, error) {
	if len(args) == 0 || args[0] != "compose" {
		return -1, "", nil
	}
	project := ""
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--project-name", "-p", "--project-directory", "--env-file", "-f", "--file":
			if index+1 >= len(args) {
				return -1, "", fmt.Errorf("incomplete Compose option")
			}
			if args[index] == "--project-name" || args[index] == "-p" {
				if project != "" {
					return -1, "", fmt.Errorf("ambiguous Compose project")
				}
				project = args[index+1]
			}
			index++
		case "up":
			return index, project, nil
		default:
			return -1, "", nil
		}
	}
	return -1, "", nil
}

func runPodmanHealthcheckCompose(client *podmanHealthcheckClient, spec CommandSpec, options RunOptions, raw commandRunner, action int) (resultErr error) {
	_, project, err := podmanHealthcheckComposeAction(spec.Args)
	if err != nil || project == "" {
		return fmt.Errorf("Podman Compose compatibility requires one explicit project: %v", err)
	}
	prefix := append([]string{}, spec.Args[:action]...)
	createArgs := append(append([]string{}, prefix...), "create")
	detached := false
	for index := action + 1; index < len(spec.Args); index++ {
		switch spec.Args[index] {
		case "-d", "--detach":
			detached = true
		case "--force-recreate":
			createArgs = append(createArgs, spec.Args[index])
		case "--pull":
			if index+1 >= len(spec.Args) || spec.Args[index+1] != "never" {
				return fmt.Errorf("Podman Compose compatibility requires local images")
			}
			createArgs = append(createArgs, "--pull", "never")
			index++
		default:
			return fmt.Errorf("unsupported Reploy Podman Compose up argument %q", spec.Args[index])
		}
	}
	if !detached {
		return fmt.Errorf("Podman Compose compatibility requires detached startup")
	}
	// Prepare the policy before stopping/removing any existing workload.
	create := spec
	create.Args = createArgs
	translated, cleanup, seccomp, err := preparePodmanSeccompCompose(create, len(prefix))
	if err != nil {
		return err
	}
	defer cleanup()
	create = translated
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	list := func(c context.Context) ([]string, error) {
		var output bytes.Buffer
		var diagnostic dockerBuildOutputTail
		command := spec
		command.Args = append(append([]string{}, prefix...), "ps", "--all", "--quiet")
		var stderr io.Writer = &diagnostic
		if options.Stderr != nil {
			stderr = io.MultiWriter(options.Stderr, &diagnostic)
		}
		if err := raw(command, RunOptions{
			Context: c,
			Stdout:  &output,
			Stderr:  stderr,
			Capture: options.Capture,
		}); err != nil {
			if message := diagnostic.String(); message != "" {
				if options.Capture != nil {
					if CommandOutputCaptureDiagnostic(err, options.Capture) == "" {
						err = options.Capture.Wrap(err, message)
					}
				} else {
					err = fmt.Errorf("%w\ncommand output:\n%s", err, message)
				}
			}
			return nil, err
		}
		ids := strings.Fields(output.String())
		seen := map[string]bool{}
		owned := make([]string, 0, len(ids))
		for _, id := range ids {
			if !validPodmanHealthcheckContainerID(id) || seen[id] {
				return nil, fmt.Errorf("Compose did not return unique exact container IDs")
			}
			seen[id] = true
			probe, cancel := context.WithTimeout(c, effectiveDockerPreflightTimeout(options.DockerPreflightTimeout))
			inspection, err := client.inspect(probe, id)
			cancel()
			if err != nil || inspection.Config.Labels["com.docker.compose.project"] != project {
				return nil, fmt.Errorf("refuse Podman preparation outside Compose project %q: %v", project, err)
			}
			// A shared project label also covers retained one-offs and orphaned
			// services. Only the workload rendered by Reploy belongs to this up.
			if inspection.Config.Labels["com.docker.compose.service"] != composeWorkloadService ||
				!strings.EqualFold(inspection.Config.Labels["com.docker.compose.oneoff"], "False") {
				continue
			}
			owned = append(owned, id)
		}
		return owned, nil
	}
	before, err := list(ctx)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, id := range before {
		existing[id] = true
		probe, cancel := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(options.DockerPreflightTimeout))
		inspection, err := client.inspect(probe, id)
		cancel()
		if err != nil {
			return err
		}
		// Remove the old owned container while its positive interval still lets
		// the affected engine clean up its timer. Never clear that timer in place.
		if podmanNoneHealthcheck(inspection.Config.Healthcheck) && inspection.Config.Healthcheck.Interval != 0 {
			// Preserve the configured stop signal and grace period. A failed stop
			// must not become a forced removal of the existing workload.
			stop := spec
			stop.Args = []string{"container", "stop", id}
			if err := raw(stop, options); err != nil {
				return err
			}
			remove := spec
			remove.Args = []string{"container", "rm", id}
			if err := raw(remove, options); err != nil {
				return err
			}
		}
	}
	defer func() {
		if resultErr == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), effectiveDockerPreflightTimeout(options.DockerPreflightTimeout))
		defer cancel()
		ids, err := list(cleanup)
		resultErr = errors.Join(resultErr, err)
		for _, id := range ids {
			if !existing[id] {
				resultErr = errors.Join(resultErr, removeCreatedPodmanContainer(cleanup, spec, id, raw))
			}
		}
	}()
	if err := raw(create, options); err != nil {
		if seccomp {
			err = podmanSeccompCreateError(client, err)
		}
		return err
	}
	ids, err := list(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !existing[id] {
			probe, cancel := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(options.DockerPreflightTimeout))
			_, err := client.prepare(probe, id)
			cancel()
			if err != nil {
				return fmt.Errorf("prepare disabled Podman healthcheck before Compose start: %w", err)
			}
		}
	}
	start := spec
	start.Args = append(append([]string{}, prefix...), "start")
	return raw(start, options)
}
