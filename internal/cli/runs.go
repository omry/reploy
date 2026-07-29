package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/dockerdeploy"
)

var dockerListLiveRuns = dockerdeploy.ListLiveRunsWithNoticeV1
var dockerStopLiveRun = dockerdeploy.StopLiveRunV1

type dockerRunsOptions struct {
	Dir         string
	DirExplicit bool
	RunID       string
}

func runDockerRuns(args []string, stdout io.Writer, stderr io.Writer, globalOptions globalDeploymentOptions) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "reploy runs usage error: expected command")
		printRunsShortUsage(stderr)
		return 2
	}
	if isHelpArg(args[0]) {
		printRunsHelp(stdout)
		return 0
	}
	action := args[0]
	if action != "list" && action != "stop" {
		fmt.Fprintf(stderr, "reploy runs usage error: unknown command: %s\n", action)
		printRunsShortUsage(stderr)
		return 2
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		printRunsHelp(stdout)
		return 0
	}
	options, err := parseDockerRunsOptions(action, args[1:])
	if err != nil {
		fmt.Fprintf(stderr, "reploy runs %s usage error: %v\n", action, err)
		printRunsShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, io.Discard)

	switch action {
	case "list":
		runs, err := dockerListLiveRuns(context.Background(), options.Dir, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "reploy runs list error: %v\n", err)
			return 1
		}
		printLiveRuns(stdout, runs)
		return 0
	case "stop":
		result, err := dockerStopLiveRun(context.Background(), options.Dir, options.RunID, globalOptions.DockerTimeout)
		dockerdeploy.WriteLiveRunRecoveryNoticeV1(stderr, result.Recovery)
		if err != nil {
			fmt.Fprintf(stderr, "reploy runs stop error: %v\n", err)
			return 1
		}
		if !result.Found {
			fmt.Fprintf(stdout, "No active run found for %s; it may have already finished.\n", options.RunID)
			return 0
		}
		verb := "Stopped"
		if result.Run.Status == deploy.LiveRunStatusWaitingV1 {
			verb = "Canceled"
		}
		fmt.Fprintf(stdout, "%s %s %s %s.\n", verb, result.Run.Status, result.Run.Kind, result.Run.ID)
		return 0
	default:
		panic("validated runs action was not handled")
	}
}

func parseDockerRunsOptions(action string, args []string) (dockerRunsOptions, error) {
	options := dockerRunsOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerRunsOptions{}, fmt.Errorf("--dir requires a value")
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return dockerRunsOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			if action != "stop" {
				return dockerRunsOptions{}, fmt.Errorf("list does not accept arguments")
			}
			if options.RunID != "" {
				return dockerRunsOptions{}, fmt.Errorf("RUN_ID may only be provided once")
			}
			options.RunID = arg
		}
	}
	if options.Dir == "" {
		return dockerRunsOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if action == "stop" {
		if options.RunID == "" {
			return dockerRunsOptions{}, fmt.Errorf("RUN_ID is required")
		}
		if err := deploy.ValidateLiveRunIDV1(options.RunID); err != nil {
			return dockerRunsOptions{}, err
		}
	}
	return options, nil
}

func printLiveRuns(output io.Writer, runs []deploy.LiveRunV1) {
	if len(runs) == 0 {
		fmt.Fprintln(output, "No active or waiting runs.")
		return
	}
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tSTATUS\tKIND\tNAME")
	for _, run := range runs {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", run.ID, run.Status, run.Kind, run.Name)
	}
	_ = writer.Flush()
}

func printRunsShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy [--docker-timeout DURATION] runs list [--dir DIR]")
	fmt.Fprintln(output, "       reploy [--docker-timeout DURATION] runs stop RUN_ID [--dir DIR]")
	fmt.Fprintln(output, "Run 'reploy runs --help' for live-run help.")
}

func printRunsHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker-timeout DURATION] runs list [--dir DIR]
       reploy [--docker-timeout DURATION] runs stop RUN_ID [--dir DIR]

List or stop outstanding app commands and shell sessions. Only active and
waiting runs are retained; completed runs do not appear.

Commands:
  list         List active and waiting runs in queue order
  stop RUN_ID  Stop an active run or remove a waiting run from the queue

Options:
  --dir DIR    Deployment directory, default current deployment or reploy-staging
  -h, --help   Show live-run help
`, "\n"))
}
