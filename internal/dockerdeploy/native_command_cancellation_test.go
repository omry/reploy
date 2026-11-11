package dockerdeploy

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestNativeCommandCancellationStopsBeforeDetachingAndRemoving(t *testing.T) {
	for _, failure := range []string{"", "stop", "missing", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			attached := make(chan context.Context, 1)
			var attachedContext context.Context
			var order []string
			want := errors.New("owned cleanup failure")
			run := func(spec CommandSpec, options RunOptions) error {
				switch spec.Args[0] {
				case "start":
					attachedContext = options.Context
					attached <- options.Context
					<-options.Context.Done()
					return errors.New("attached CLI cancelled")
				case "container":
					if options.Context.Err() != nil || options.Stdin != nil || options.Stdout != nil || options.Stderr != nil {
						t.Error("stop/cleanup inherited cancellation or application streams")
					}
					if _, ok := options.Context.Deadline(); !ok {
						t.Error("stop/cleanup lacks finite deadline")
					}
					order = append(order, spec.Args[1])
					if spec.Args[1] == "stop" {
						if attachedContext.Err() != nil {
							t.Error("attached CLI was cancelled before graceful stop")
						}
						if !reflect.DeepEqual(spec.Args, []string{"container", "stop", "owned"}) || !reflect.DeepEqual(spec.Env, []string{"DOCKER_HOST=unix:///bound", "DOCKER_CONTEXT="}) {
							t.Error("stop changed the target or endpoint")
						}
						if failure == "stop" {
							return want
						}
						if failure == "missing" {
							return errors.New("No such container: owned")
						}
					} else if failure == "cleanup" {
						return want
					}
				}
				return nil
			}
			cleanup := TemporaryContainerCleanupCommand("owned")
			cleanup.Env = []string{"DOCKER_HOST=unix:///bound", "DOCKER_CONTEXT="}
			done := make(chan error, 1)
			go func() {
				done <- runTemporaryContainerCommand(run, CommandSpec{Name: "docker", Args: []string{"start", "owned"}}, cleanup,
					RunOptions{Context: parent, gracefulCancellation: true, Stdin: strings.NewReader("input"), Stdout: io.Discard, Stderr: io.Discard})
			}()
			ctx := <-attached
			cancel()
			err := <-done
			if ctx.Err() == nil || err == nil || !strings.Contains(err.Error(), "native app command interrupted") ||
				!reflect.DeepEqual(order, []string{"stop", "rm"}) {
				t.Fatalf("cancellation error=%v order=%v attached=%v", err, order, ctx.Err())
			}
			if (failure == "stop" || failure == "cleanup") != errors.Is(err, want) {
				t.Fatalf("stop/cleanup failure lost: %v", err)
			}
			var cleanupFailure *temporaryContainerCleanupFailureV1
			if errors.As(err, &cleanupFailure) != (failure == "cleanup") {
				t.Fatalf("cleanup recovery ownership lost: %v", err)
			}
		})
	}
}

func TestNativeCommandCancellationBoundsUnresponsiveEngine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{})
		run := func(spec CommandSpec, options RunOptions) error {
			if spec.Args[0] == "start" {
				close(started)
				<-options.Context.Done()
				return options.Context.Err()
			}
			<-options.Context.Done()
			return options.Context.Err()
		}
		done := make(chan error, 1)
		go func() {
			done <- runTemporaryContainerCommand(run, CommandSpec{Name: "docker", Args: []string{"start", "owned"}}, TemporaryContainerCleanupCommand("owned"), RunOptions{Context: ctx, gracefulCancellation: true})
		}()
		<-started
		start := time.Now()
		cancel()
		err := <-done
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 30*time.Second+defaultDockerPreflightTimeout {
			t.Fatalf("unbounded cleanup: elapsed=%s error=%v", time.Since(start), err)
		}
	})
}

func TestNativeCommandCompletionAndLaunchFailureRemainDistinctFromCancellation(t *testing.T) {
	for _, runErr := range []error{nil, errors.New("genuine launcher failure")} {
		var order []string
		run := func(spec CommandSpec, _ RunOptions) error {
			order = append(order, strings.Join(spec.Args, " "))
			if spec.Args[0] == "start" {
				return runErr
			}
			return nil
		}
		err := runTemporaryContainerCommand(run, CommandSpec{Name: "docker", Args: []string{"start", "owned"}}, TemporaryContainerCleanupCommand("owned"), RunOptions{Context: t.Context(), gracefulCancellation: true})
		want := []string{"start owned"}
		if runErr != nil {
			want = append(want, "container stop owned", "container rm --force --volumes owned")
		}
		if !errors.Is(err, runErr) || !reflect.DeepEqual(order, want) || err != nil && strings.Contains(err.Error(), "interrupted") {
			t.Fatalf("completion/failure changed: order=%v error=%v", order, err)
		}
	}
}
