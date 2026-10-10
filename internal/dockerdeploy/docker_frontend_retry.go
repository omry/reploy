package dockerdeploy

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Frontend resolution precedes Dockerfile execution. Retry only a final solve
// error for our exact pinned frontend, never a warning followed by a RUN failure.
func runDockerBuildWithFrontendRetry(spec CommandSpec, options RunOptions) error {
	if spec.Name != "docker" || len(spec.Args) == 0 || spec.Args[0] != "build" {
		return fmt.Errorf("frontend fetch recovery requires a Docker build command")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	options.Capture = effectiveCommandOutputCapture(options)
	run, err := bindDockerCommandRunnerV1(ctx, spec, options.DockerPreflightTimeout)
	if err != nil {
		return err
	}
	options.Context = ctx
	return runDockerBuildWithFrontendRetryDelay(spec, options, run, waitDockerPullRetry)
}

func runDockerBuildWithFrontendRetryDelay(spec CommandSpec, options RunOptions, run commandRunner, wait func(context.Context, time.Duration) error) error {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	progress := options.Progress
	if progress == nil {
		progress, _ = ctx.Value(dockerPullProgressContextKey{}).(io.Writer)
	}
	options.Capture = effectiveCommandOutputCapture(options)
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var output dockerBuildOutputTail
		attemptOptions := options
		attemptOptions.Context = ctx
		attemptOptions.Stdout = &output
		attemptOptions.Stderr = &output
		if options.Stdout != nil {
			attemptOptions.Stdout = io.MultiWriter(&output, options.Stdout)
		}
		if options.Stderr != nil {
			attemptOptions.Stderr = io.MultiWriter(&output, options.Stderr)
		}
		err := run(spec, attemptOptions)
		if err == nil {
			if attempt > 1 {
				writeProviderBuildProgress(progress, "Dockerfile frontend fetch recovered after %d attempts", attempt)
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		transcript := output.String()
		retryable := transientDockerFrontendFailure(transcript)
		if transcript != "" && options.Capture == nil {
			err = fmt.Errorf("%w\ncommand output:\n%s", err, transcript)
		}
		if !retryable {
			return options.Capture.Wrap(err, transcript)
		}
		if attempt == 3 {
			err = options.Capture.Wrap(err, transcript)
			return fmt.Errorf("Dockerfile frontend fetch failed after %d attempts: %w", attempt, err)
		}
		writeProviderBuildProgress(progress, "retrying Dockerfile frontend fetch (attempt %d/3)", attempt+1)
		if err := wait(ctx, time.Duration(attempt)*500*time.Millisecond); err != nil {
			return err
		}
	}
}

func transientDockerFrontendFailure(output string) bool {
	message := strings.ToLower(strings.TrimSpace(output))
	lines := strings.Split(message, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		terminal := strings.TrimSpace(lines[index])
		solve, found := strings.CutPrefix(terminal, "error: failed to solve: ")
		if !found {
			solve, found = strings.CutPrefix(terminal, "error: failed to build: failed to solve: ")
		}
		if !found {
			if strings.HasPrefix(terminal, "error:") {
				return false
			}
			continue
		}
		references := []string{"docker.io/" + MaterializationDockerfileSyntax, MaterializationDockerfileSyntax}
		for _, reference := range references {
			if remainder, found := strings.CutPrefix(solve, reference+": "); found {
				solve = remainder
				break
			}
		}
		for _, reference := range references {
			if strings.HasPrefix(solve, "failed to resolve source metadata for "+reference+": ") {
				return transientDockerPullFailure(message)
			}
		}
		return false
	}
	return false
}

// stdout and stderr may be copied concurrently when verbose output is streamed.
// Retain only the same diagnostic tail as the ordinary command runner.
type dockerBuildOutputTail struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (output *dockerBuildOutputTail) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	length := len(data)
	if len(output.data)+length > commandOutputErrorLimit {
		output.truncated = true
	}
	if length >= commandOutputErrorLimit {
		output.data = append(output.data[:0], data[length-commandOutputErrorLimit:]...)
	} else {
		if excess := len(output.data) + length - commandOutputErrorLimit; excess > 0 {
			output.data = output.data[excess:]
		}
		output.data = append(output.data, data...)
	}
	return length, nil
}

func (output *dockerBuildOutputTail) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	text := strings.TrimSpace(string(output.data))
	if output.truncated {
		return "[last 4000 bytes]\n" + text
	}
	return text
}
