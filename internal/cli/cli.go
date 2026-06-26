package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	reploy "github.com/omry/reploy"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/dockerdeploy"
)

const defaultPackIndexURL = "https://raw.githubusercontent.com/omry/reploy/main/blueprint-index.json"
const packIndexURLEnv = "REPLOY_BLUEPRINT_INDEX_URL"

func Main(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printShortUsage(stderr)
		return 2
	}

	target := "docker"
	switch args[0] {
	case "--docker":
		args = args[1:]
	case "--aws":
		fmt.Fprintln(stderr, "reploy usage error: deployment target aws is not supported yet")
		printShortUsage(stderr)
		return 2
	}
	if len(args) == 0 {
		printShortUsage(stderr)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		printHelp(stdout)
		return 0
	case "--version", "version":
		fmt.Fprintf(stdout, "reploy %s\n", reploy.Version)
		return 0
	case "blueprint-index":
		return runPackIndex(args[1:], stdout, stderr)
	default:
		if target == "docker" && isDeploymentCommand(args[0]) {
			return runDocker(args, stdout, stderr)
		}
		fmt.Fprintf(stderr, "reploy usage error: unknown command: %s\n", args[0])
		printShortUsage(stderr)
		return 2
	}
}

func isDeploymentCommand(command string) bool {
	switch command {
	case "init", "update", "info", "app", "bundle", "up", "restart", "down", "ps", "status", "logs", "test", "doctor", "install":
		return true
	default:
		return false
	}
}

func runPackIndex(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "reploy blueprint-index usage error: expected command")
		printPackIndexShortUsage(stderr)
		return 2
	}
	if isHelpArg(args[0]) {
		printPackIndexHelp(stdout)
		return 0
	}
	switch args[0] {
	case "refresh", "update":
		options, err := parsePackIndexRefreshOptions(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy blueprint-index usage error: %v\n", err)
			printPackIndexShortUsage(stderr)
			return 2
		}
		index, cachePath, err := refreshPackIndex(options.URL)
		if err != nil {
			fmt.Fprintf(stderr, "reploy blueprint-index refresh error: %v\n", err)
			return 1
		}
		if cachePath == "" {
			fmt.Fprintf(stdout, "loaded blueprint index from %s (%d shorthands)\n", options.URL, len(index.Blueprints))
		} else {
			fmt.Fprintf(stdout, "cached blueprint index from %s at %s (%d shorthands)\n", options.URL, cachePath, len(index.Blueprints))
		}
		return 0
	default:
		fmt.Fprintf(stderr, "reploy blueprint-index usage error: unknown command: %s\n", args[0])
		printPackIndexShortUsage(stderr)
		return 2
	}
}

type packIndexRefreshOptions struct {
	URL string
}

func parsePackIndexRefreshOptions(args []string) (packIndexRefreshOptions, error) {
	options := packIndexRefreshOptions{URL: packIndexURL()}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--url":
			value, ok := optionValue(args, &index)
			if !ok {
				return packIndexRefreshOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.URL = value
		default:
			if strings.HasPrefix(arg, "--url=") {
				options.URL = strings.TrimPrefix(arg, "--url=")
				continue
			}
			return packIndexRefreshOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if strings.TrimSpace(options.URL) == "" {
		return packIndexRefreshOptions{}, fmt.Errorf("--url must not be empty")
	}
	return options, nil
}

func runDocker(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printDockerShortUsage(stderr)
		return 2
	}
	if args[0] == "bundle" {
		return runDockerBundle(args[1:], stdout, stderr)
	}
	if args[0] == "app" {
		return runDockerApp(args[1:], stdout, stderr)
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		printDockerCommandHelp(args[0], stdout)
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		printDockerHelp(stdout)
		return 0
	case "init":
		options, err := parseDockerCommandOptions(args[1:], true)
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			printDockerShortUsage(stderr)
			return 2
		}
		results, err := dockerdeploy.Init(dockerdeploy.InitOptions{
			Dir:          options.Dir,
			Pack:         options.Pack,
			Requirements: options.Requirements,
		})
		if err != nil {
			var existingFileError dockerdeploy.ExistingDeploymentFileError
			if errors.As(err, &existingFileError) {
				fmt.Fprintf(stderr, "reploy init error: deployment directory already exists at %s (found %s); run \"%s\" to update it\n", options.Dir, existingFileError.Path, initUpdateCommandHint(options.Dir))
				return 1
			}
			fmt.Fprintf(stderr, "reploy init error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "created staging directory for %s: %s\n", packDisplayName(options.Pack), options.Dir)
		printUpdateResults(stdout, results)
		return 0
	case "update":
		options, err := parseDockerCommandOptions(args[1:], false)
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			printDockerShortUsage(stderr)
			return 2
		}
		options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
		results, err := dockerdeploy.Update(dockerdeploy.UpdateOptions{
			Dir:   options.Dir,
			Pack:  options.Pack,
			Force: options.Force,
		})
		if err != nil {
			fmt.Fprintf(stderr, "reploy update error: %v\n", err)
			return 1
		}
		printUpdateResults(stdout, results)
		return 0
	case "info":
		options, err := parseDockerCommandOptions(args[1:], false)
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			printDockerShortUsage(stderr)
			return 2
		}
		options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
		info, err := dockerdeploy.Info(dockerdeploy.InfoOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy info error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, info)
		return 0
	case "up", "restart", "down", "ps", "status", "logs":
		return runDockerRuntime(args[0], args[1:], stdout, stderr)
	case "test":
		return runDockerTest(args[1:], stdout, stderr)
	case "doctor":
		return runDockerDoctor(args[1:], stdout, stderr)
	case "install":
		return runDockerInstall(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "reploy usage error: unknown command: %s\n", args[0])
		printDockerShortUsage(stderr)
		return 2
	}
}

func runDockerApp(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 && isHelpArg(args[0]) {
		printAppHelp(stdout)
		return 0
	}
	if len(args) == 0 || args[0] == "--dir" || strings.HasPrefix(args[0], "--dir=") {
		return runDockerAppSummary(args, stdout, stderr)
	}
	options, err := parseDockerAppOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printAppShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
	if err := dockerdeploy.AppCommand(dockerdeploy.AppCommandOptions{
		Dir:         options.Dir,
		CommandArgs: options.CommandArgs,
		Stdout:      stdout,
		Stderr:      stderr,
	}); err != nil {
		fmt.Fprintf(stderr, "reploy app error: %v\n", err)
		return 1
	}
	return 0
}

func runDockerAppSummary(args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseDockerAppSummaryOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printAppShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
	result, err := dockerdeploy.AppCommandList(dockerdeploy.AppCommandListOptions{Dir: options.Dir})
	if err != nil {
		fmt.Fprintf(stderr, "reploy app error: %v\n", err)
		return 1
	}
	if result.AppID != "" {
		fmt.Fprintf(stdout, "app: %s\n", result.AppID)
	}
	fmt.Fprintln(stdout, "app subcommands:")
	for _, command := range result.Commands {
		fmt.Fprintf(stdout, "  %s\n", command)
	}
	return 0
}

type dockerAppOptions struct {
	Dir         string
	DirExplicit bool
	CommandArgs []string
}

type dockerAppSummaryOptions struct {
	Dir         string
	DirExplicit bool
}

func parseDockerAppOptions(args []string) (dockerAppOptions, error) {
	options := dockerAppOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerAppOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			options.CommandArgs = append(options.CommandArgs, arg)
		}
	}
	if options.Dir == "" {
		return dockerAppOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if len(options.CommandArgs) == 0 {
		return dockerAppOptions{}, fmt.Errorf("expected app command")
	}
	return options, nil
}

func parseDockerAppSummaryOptions(args []string) (dockerAppSummaryOptions, error) {
	options := dockerAppSummaryOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerAppSummaryOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			return dockerAppSummaryOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerAppSummaryOptions{}, fmt.Errorf("--dir must not be empty")
	}
	return options, nil
}

func isHelpArg(arg string) bool {
	switch arg {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}

func resolveImplicitDeploymentDir(dir string, explicit bool, output io.Writer) string {
	if explicit || dir != dockerdeploy.DefaultDeploymentDir {
		return dir
	}
	if _, err := os.Stat(dockerdeploy.StateFileName); err != nil {
		return dir
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	if output != nil {
		fmt.Fprintf(output, "reploy: using deployment in current directory: %s\n", cwd)
	}
	return "."
}

func runDockerBundle(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "reploy usage error: expected bundle command")
		printBundleShortUsage(stderr)
		return 2
	}
	action := args[0]
	if isHelpArg(action) {
		printBundleHelp(stdout)
		return 0
	}
	if action == "upgrade" {
		options, err := parseDockerBundleUpgradeOptions(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			printBundleShortUsage(stderr)
			return 2
		}
		options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
		results, err := dockerdeploy.BundleUpgrade(dockerdeploy.BundleUpgradeOptions{
			Dir:      options.Dir,
			Target:   options.Root,
			PyPIOnly: options.PyPIOnly,
			Stdout:   stdout,
			Stderr:   stderr,
		})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle upgrade error: %v\n", err)
			return 1
		}
		printUpdateResults(stdout, results)
		return 0
	}
	if action == "list" && len(args) > 1 && args[1] == "all" {
		options, err := parseDockerBundleOptions(args[2:], dockerBundleParseOptions{})
		if err != nil {
			fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
			printBundleShortUsage(stderr)
			return 2
		}
		options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
		packages, err := dockerdeploy.BundleListAll(dockerdeploy.BundleListOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle list all error: %v\n", err)
			return 1
		}
		for _, resolved := range packages {
			fmt.Fprintf(stdout, "%s\t%s\n", resolved.Kind, resolved.Requirement)
		}
		return 0
	}
	options, err := parseDockerBundleOptions(args[1:], dockerBundleParseOptions{
		RequireRoot:   action != "list" && action != "list-options" && action != "check" && action != "build" && action != "clean",
		AllowDryRun:   action == "check" || action == "build",
		AllowPyPIOnly: action == "build",
		AllowVerbose:  action == "build" || action == "clean",
		AllowMultiple: action == "add" || action == "remove",
		AllowNames:    action == "add",
		AllowForce:    action == "add",
	})
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printBundleShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
	switch action {
	case "list":
		roots, err := dockerdeploy.BundleList(dockerdeploy.BundleListOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle list error: %v\n", err)
			return 1
		}
		for _, root := range roots {
			fmt.Fprintln(stdout, root.Source)
		}
		return 0
	case "list-options":
		bundleOptions, err := dockerdeploy.BundleOptions(dockerdeploy.BundleListOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle list-options error: %v\n", err)
			return 1
		}
		for _, option := range bundleOptions {
			fmt.Fprintf(stdout, "%s\t%s\n", option.Name, option.Description)
		}
		return 0
	case "add":
		beforeRoots, beforeErr := dockerdeploy.BundleList(dockerdeploy.BundleListOptions{Dir: options.Dir})
		results, err := dockerdeploy.BundleAddMany(dockerdeploy.BundleRootsOptions{Dir: options.Dir, Sources: options.Roots, Names: options.Names, Force: options.Force})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle add error: %v\n", err)
			return 1
		}
		printBundleAddSummary(stdout, options, beforeRoots, beforeErr)
		printUpdateResults(stdout, results)
		return 0
	case "add-wheel":
		results, err := dockerdeploy.BundleAddWheel(dockerdeploy.BundleRootOptions{Dir: options.Dir, Source: options.Root})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle add-wheel error: %v\n", err)
			return 1
		}
		printUpdateResults(stdout, results)
		return 0
	case "add-source":
		results, err := dockerdeploy.BundleAddSource(dockerdeploy.BundleRootOptions{Dir: options.Dir, Source: options.Root})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle add-source error: %v\n", err)
			return 1
		}
		printUpdateResults(stdout, results)
		return 0
	case "check":
		if err := dockerdeploy.BundleCheck(dockerdeploy.BundleCheckOptions{
			Dir:    options.Dir,
			DryRun: options.DryRun,
			Stdout: stdout,
			Stderr: stderr,
		}); err != nil {
			fmt.Fprintf(stderr, "reploy bundle check error: %v\n", err)
			return 1
		}
		if !options.DryRun {
			fmt.Fprintln(stdout, "bundle check passed")
		}
		return 0
	case "build":
		stopSpinner := func(bool) {}
		if !options.DryRun && !options.Verbose {
			stopSpinner = startSpinner(stderr, "building installation bundle")
		}
		if err := dockerdeploy.BundlePrepare(dockerdeploy.BundlePrepareOptions{
			Dir:      options.Dir,
			DryRun:   options.DryRun,
			PyPIOnly: options.PyPIOnly,
			Verbose:  options.Verbose,
			Stdout:   stdout,
			Stderr:   stderr,
		}); err != nil {
			stopSpinner(false)
			if options.DryRun || options.Verbose {
				fmt.Fprintf(stderr, "reploy bundle build error: %v\n", err)
			} else {
				fmt.Fprintf(stderr, "reploy bundle build error: %v; rerun with --verbose for command output\n", err)
			}
			return 1
		}
		stopSpinner(true)
		return 0
	case "clean":
		results, err := dockerdeploy.BundleClean(dockerdeploy.BundleCleanOptions{Dir: options.Dir})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle clean error: %v\n", err)
			return 1
		}
		if options.Verbose {
			printUpdateResults(stdout, results)
		}
		return 0
	case "remove":
		results, err := dockerdeploy.BundleRemoveMany(dockerdeploy.BundleRootsOptions{Dir: options.Dir, Sources: options.Roots})
		if err != nil {
			fmt.Fprintf(stderr, "reploy bundle remove error: %v\n", err)
			return 1
		}
		printUpdateResults(stdout, results)
		return 0
	default:
		fmt.Fprintf(stderr, "reploy usage error: unknown bundle command: %s\n", action)
		printBundleShortUsage(stderr)
		return 2
	}
}

type dockerBundleOptions struct {
	Dir         string
	DirExplicit bool
	Root        string
	Roots       []string
	Names       []string
	Force       bool
	DryRun      bool
	PyPIOnly    bool
	Verbose     bool
}

type dockerBundleParseOptions struct {
	RequireRoot   bool
	AllowDryRun   bool
	AllowPyPIOnly bool
	AllowVerbose  bool
	AllowMultiple bool
	AllowNames    bool
	AllowForce    bool
}

func parseDockerBundleUpgradeOptions(args []string) (dockerBundleOptions, error) {
	options := dockerBundleOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--pypi-only":
			options.PyPIOnly = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--") {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			if options.Root != "" {
				return dockerBundleOptions{}, fmt.Errorf("bundle upgrade accepts at most one target")
			}
			options.Root = arg
		}
	}
	if options.Dir == "" {
		return dockerBundleOptions{}, fmt.Errorf("--dir must not be empty")
	}
	return options, nil
}

func parseDockerBundleOptions(args []string, parseOptions dockerBundleParseOptions) (dockerBundleOptions, error) {
	options := dockerBundleOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--force":
			if !parseOptions.AllowForce {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.Force = true
		case "--dry-run":
			if !parseOptions.AllowDryRun {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.DryRun = true
		case "--pypi-only":
			if !parseOptions.AllowPyPIOnly {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.PyPIOnly = true
		case "--verbose":
			if !parseOptions.AllowVerbose {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			options.Verbose = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		case "--name":
			if !parseOptions.AllowNames {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerBundleOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			names := splitBundleRoots(value)
			if len(names) == 0 {
				return dockerBundleOptions{}, fmt.Errorf("bundle option name must not be empty")
			}
			options.Names = append(options.Names, names...)
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--name=") {
				if !parseOptions.AllowNames {
					return dockerBundleOptions{}, fmt.Errorf("unknown option: --name")
				}
				names := splitBundleRoots(strings.TrimPrefix(arg, "--name="))
				if len(names) == 0 {
					return dockerBundleOptions{}, fmt.Errorf("bundle option name must not be empty")
				}
				options.Names = append(options.Names, names...)
				continue
			}
			if strings.HasPrefix(arg, "--") {
				return dockerBundleOptions{}, fmt.Errorf("unknown option: %s", arg)
			}
			roots := splitBundleRoots(arg)
			if len(roots) == 0 {
				return dockerBundleOptions{}, fmt.Errorf("bundle root must not be empty")
			}
			if !parseOptions.AllowMultiple && (options.Root != "" || len(roots) > 1) {
				return dockerBundleOptions{}, fmt.Errorf("expected one bundle root")
			}
			if options.Root == "" {
				options.Root = roots[0]
			}
			options.Roots = append(options.Roots, roots...)
		}
	}
	if options.Dir == "" {
		return dockerBundleOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if parseOptions.RequireRoot && len(options.Roots) == 0 && len(options.Names) == 0 {
		if parseOptions.AllowNames {
			return dockerBundleOptions{}, fmt.Errorf("bundle add expects a package root or --name NAME; examples: reploy bundle add --name imap,smtp; reploy bundle add PACKAGE[==VERSION]")
		}
		return dockerBundleOptions{}, fmt.Errorf("expected bundle root")
	}
	if !parseOptions.RequireRoot && (len(options.Roots) > 0 || len(options.Names) > 0) {
		return dockerBundleOptions{}, fmt.Errorf("bundle list does not accept a root")
	}
	return options, nil
}

func splitBundleRoots(arg string) []string {
	parts := strings.Split(arg, ",")
	roots := []string{}
	for _, part := range parts {
		root := strings.TrimSpace(part)
		if root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

func printUpdateResults(output io.Writer, results []dockerdeploy.UpdateResult) {
	anyAction := false
	for _, result := range results {
		if result.Status == deploy.UpdateStatusUpToDate {
			continue
		}
		anyAction = true
		fmt.Fprintf(output, "%s %s\n", result.Status, result.Path)
	}
	if !anyAction {
		fmt.Fprintln(output, deploy.UpdateStatusUpToDate)
	}
}

func printBundleAddSummary(output io.Writer, options dockerBundleOptions, beforeRoots []deploy.ArtifactRoot, beforeErr error) {
	roots := selectedBundleRoots(options)
	if len(roots) == 0 {
		return
	}
	if beforeErr == nil {
		alreadySelected := map[string]bool{}
		for _, root := range beforeRoots {
			alreadySelected[root.Source] = true
		}
		added := []string{}
		existing := []string{}
		for _, root := range roots {
			if alreadySelected[root] {
				existing = append(existing, root)
			} else {
				added = append(added, root)
			}
		}
		if len(added) > 0 {
			printBundleRootSummary(output, "selected", added)
		}
		if len(existing) > 0 {
			printBundleRootSummary(output, "already selected", existing)
		}
		return
	}
	printBundleRootSummary(output, "selected", roots)
}

func printBundleRootSummary(output io.Writer, verb string, roots []string) {
	if allPythonPackageRoots(roots) {
		fmt.Fprintf(output, "%s Python packages: %s (dependencies included by bundle build)\n", verb, strings.Join(roots, ", "))
		return
	}
	fmt.Fprintf(output, "%s installation roots: %s\n", verb, strings.Join(roots, ", "))
}

func selectedBundleRoots(options dockerBundleOptions) []string {
	roots := append([]string{}, options.Roots...)
	if len(options.Names) == 0 {
		return roots
	}
	bundleOptions, err := dockerdeploy.BundleOptions(dockerdeploy.BundleListOptions{Dir: options.Dir})
	byName := map[string]string{}
	if err == nil {
		for _, option := range bundleOptions {
			byName[option.Name] = option.Identifier
		}
	}
	for _, name := range options.Names {
		if root := byName[name]; root != "" {
			roots = append(roots, root)
		} else {
			roots = append(roots, name)
		}
	}
	return roots
}

func allPythonPackageRoots(roots []string) bool {
	for _, root := range roots {
		if root == "" || strings.HasPrefix(root, "/") || strings.ContainsAny(root, " \t\n") {
			return false
		}
	}
	return true
}

func runDockerDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseDockerDoctorOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
	return dockerdeploy.Doctor(dockerdeploy.DoctorOptions{
		Dir:        options.Dir,
		Preinstall: options.Preinstall,
		Quiet:      options.Quiet,
		Stdout:     stdout,
	})
}

func runDockerInstall(args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseDockerInstallOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
	if err := dockerdeploy.Install(dockerdeploy.InstallOptions{
		Dir:           options.Dir,
		Target:        options.Target,
		Service:       options.Service,
		PortOverrides: options.PortOverrides,
		Start:         options.Start,
		DryRun:        options.DryRun,
		Stdout:        stdout,
	}); err != nil {
		fmt.Fprintf(stderr, "reploy install error: %v\n", err)
		return 1
	}
	return 0
}

type dockerInstallOptions struct {
	Dir           string
	DirExplicit   bool
	Target        string
	Service       string
	PortOverrides []dockerdeploy.PortOverride
	Start         bool
	DryRun        bool
}

func parseDockerInstallOptions(args []string) (dockerInstallOptions, error) {
	options := dockerInstallOptions{Dir: dockerdeploy.DefaultDeploymentDir, Start: true}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--dry-run":
			options.DryRun = true
		case "--start":
			options.Start = true
		case "--no-start":
			options.Start = false
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		case "--to":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Target = value
		case "--service":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Service = value
		case "--port":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerInstallOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			override, err := parseInstallPortOverride(value)
			if err != nil {
				return dockerInstallOptions{}, err
			}
			options.PortOverrides = append(options.PortOverrides, override)
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--to=") {
				options.Target = strings.TrimPrefix(arg, "--to=")
				continue
			}
			if strings.HasPrefix(arg, "--service=") {
				options.Service = strings.TrimPrefix(arg, "--service=")
				continue
			}
			if strings.HasPrefix(arg, "--port=") {
				override, err := parseInstallPortOverride(strings.TrimPrefix(arg, "--port="))
				if err != nil {
					return dockerInstallOptions{}, err
				}
				options.PortOverrides = append(options.PortOverrides, override)
				continue
			}
			return dockerInstallOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerInstallOptions{}, fmt.Errorf("--dir must not be empty")
	}
	return options, nil
}

func parseInstallPortOverride(value string) (dockerdeploy.PortOverride, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return dockerdeploy.PortOverride{}, fmt.Errorf("--port must not be empty")
	}
	name, hostPort, ok := strings.Cut(value, "=")
	if !ok {
		return dockerdeploy.PortOverride{HostPort: value}, nil
	}
	name = strings.TrimSpace(name)
	hostPort = strings.TrimSpace(hostPort)
	if name == "" {
		return dockerdeploy.PortOverride{}, fmt.Errorf("--port name must not be empty")
	}
	if hostPort == "" {
		return dockerdeploy.PortOverride{}, fmt.Errorf("--port host port must not be empty")
	}
	return dockerdeploy.PortOverride{Name: name, HostPort: hostPort}, nil
}

type dockerDoctorOptions struct {
	Dir         string
	DirExplicit bool
	Preinstall  bool
	Quiet       bool
}

func parseDockerDoctorOptions(args []string) (dockerDoctorOptions, error) {
	options := dockerDoctorOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--preinstall":
			options.Preinstall = true
		case "--quiet":
			options.Quiet = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerDoctorOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			return dockerDoctorOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerDoctorOptions{}, fmt.Errorf("--dir must not be empty")
	}
	return options, nil
}

func runDockerTest(args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseDockerTestOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
	if err := dockerdeploy.TestServer(dockerdeploy.TestOptions{
		Dir:     options.Dir,
		Timeout: options.Timeout,
		Stdout:  stdout,
	}); err != nil {
		fmt.Fprintf(stderr, "reploy test error: %v\n", err)
		return 1
	}
	return 0
}

type dockerTestOptions struct {
	Dir         string
	DirExplicit bool
	Timeout     time.Duration
}

func parseDockerTestOptions(args []string) (dockerTestOptions, error) {
	options := dockerTestOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--timeout":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerTestOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			timeout, err := time.ParseDuration(value)
			if err != nil {
				return dockerTestOptions{}, fmt.Errorf("invalid --timeout duration: %s", value)
			}
			options.Timeout = timeout
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerTestOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--timeout=") {
				value := strings.TrimPrefix(arg, "--timeout=")
				timeout, err := time.ParseDuration(value)
				if err != nil {
					return dockerTestOptions{}, fmt.Errorf("invalid --timeout duration: %s", value)
				}
				options.Timeout = timeout
				continue
			}
			return dockerTestOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerTestOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if options.Timeout < 0 {
		return dockerTestOptions{}, fmt.Errorf("--timeout must not be negative")
	}
	return options, nil
}

func runDockerRuntime(action string, args []string, stdout io.Writer, stderr io.Writer) int {
	options, err := parseDockerRuntimeOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "reploy usage error: %v\n", err)
		printDockerShortUsage(stderr)
		return 2
	}
	options.Dir = resolveImplicitDeploymentDir(options.Dir, options.DirExplicit, stderr)
	if options.Follow && action != "logs" {
		fmt.Fprintln(stderr, "reploy usage error: --follow is only supported with logs")
		printDockerShortUsage(stderr)
		return 2
	}
	if err := dockerdeploy.Runtime(dockerdeploy.RuntimeOptions{
		Dir:    options.Dir,
		Action: action,
		Follow: options.Follow,
		Stdout: stdout,
		Stderr: stderr,
	}); err != nil {
		fmt.Fprintf(stderr, "reploy %s error: %v\n", action, err)
		return 1
	}
	return 0
}

type dockerRuntimeOptions struct {
	Dir         string
	DirExplicit bool
	Follow      bool
}

func parseDockerRuntimeOptions(args []string) (dockerRuntimeOptions, error) {
	options := dockerRuntimeOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--follow", "-f":
			options.Follow = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerRuntimeOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			return dockerRuntimeOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerRuntimeOptions{}, fmt.Errorf("--dir must not be empty")
	}
	return options, nil
}

type dockerCommandOptions struct {
	Dir          string
	DirExplicit  bool
	Pack         deploy.PackRef
	Force        bool
	Requirements []string
}

func parseDockerCommandOptions(args []string, requirePack bool) (dockerCommandOptions, error) {
	options := dockerCommandOptions{Dir: dockerdeploy.DefaultDeploymentDir}
	packSet := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--force":
			options.Force = true
		case "--dir":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerCommandOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Dir = value
			options.DirExplicit = true
		case "--blueprint", "--fcd":
			if packSet {
				return dockerCommandOptions{}, fmt.Errorf("--blueprint may only be provided once")
			}
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerCommandOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			ref, err := parsePackRefArgument(value)
			if err != nil {
				return dockerCommandOptions{}, err
			}
			options.Pack = ref
			packSet = true
		case "--requirement":
			value, ok := optionValue(args, &index)
			if !ok {
				return dockerCommandOptions{}, fmt.Errorf("%s requires a value", arg)
			}
			options.Requirements = append(options.Requirements, value)
		default:
			if strings.HasPrefix(arg, "--dir=") {
				options.Dir = strings.TrimPrefix(arg, "--dir=")
				options.DirExplicit = true
				continue
			}
			if strings.HasPrefix(arg, "--blueprint=") || strings.HasPrefix(arg, "--fcd=") {
				if packSet {
					return dockerCommandOptions{}, fmt.Errorf("--blueprint may only be provided once")
				}
				_, value, _ := strings.Cut(arg, "=")
				ref, err := parsePackRefArgument(value)
				if err != nil {
					return dockerCommandOptions{}, err
				}
				options.Pack = ref
				packSet = true
				continue
			}
			if strings.HasPrefix(arg, "--requirement=") {
				options.Requirements = append(options.Requirements, strings.TrimPrefix(arg, "--requirement="))
				continue
			}
			return dockerCommandOptions{}, fmt.Errorf("unknown option: %s", arg)
		}
	}
	if options.Dir == "" {
		return dockerCommandOptions{}, fmt.Errorf("--dir must not be empty")
	}
	if requirePack && options.Force {
		return dockerCommandOptions{}, fmt.Errorf("--force is only supported with update")
	}
	if !requirePack && len(options.Requirements) > 0 {
		return dockerCommandOptions{}, fmt.Errorf("--requirement is only supported with init")
	}
	for _, requirement := range options.Requirements {
		if strings.TrimSpace(requirement) == "" {
			return dockerCommandOptions{}, fmt.Errorf("--requirement must not be empty")
		}
	}
	if requirePack && options.Pack.Raw == "" {
		return dockerCommandOptions{}, fmt.Errorf("--blueprint is required; use a blueprint shorthand from the Reploy blueprint index or an explicit ref such as file:PATH or pypi:PACKAGE")
	}
	return options, nil
}

func packDisplayName(ref deploy.PackRef) string {
	if ref.Scheme == "file" {
		if pack, err := deploy.LoadPack(ref); err == nil && strings.TrimSpace(pack.App.ID) != "" {
			return pack.App.ID
		}
	}
	if subdir := strings.Trim(ref.Subdir, "/"); subdir != "" {
		parts := strings.Split(subdir, "/")
		return parts[len(parts)-1]
	}
	source := ref.Source
	if packageName, _, hasVersion := strings.Cut(source, "=="); hasVersion {
		source = packageName
	}
	source = strings.TrimRight(source, "/")
	if source == "" {
		return ref.Raw
	}
	if strings.Contains(source, "/") {
		parts := strings.Split(source, "/")
		return parts[len(parts)-1]
	}
	return source
}

func initUpdateCommandHint(dir string) string {
	if dir == dockerdeploy.DefaultDeploymentDir {
		return "reploy update"
	}
	return fmt.Sprintf("reploy update --dir %s", dir)
}

func parsePackRefArgument(value string) (deploy.PackRef, error) {
	original := strings.TrimSpace(value)
	expanded := original
	if !hasPackRefScheme(original) {
		indexExpanded, found, err := expandPackShorthand(original)
		if err != nil {
			return deploy.PackRef{}, err
		}
		if !found {
			return deploy.PackRef{}, fmt.Errorf("unknown blueprint shorthand %q in Reploy blueprint index %s; use an explicit ref such as file:PATH or pypi:PACKAGE", packShorthandName(original), packIndexURL())
		}
		expanded = indexExpanded
	}
	ref, err := deploy.ParsePackRef(expanded)
	if err != nil {
		return deploy.PackRef{}, err
	}
	if expanded != original {
		ref.Raw = original
	}
	return ref, nil
}

func hasPackRefScheme(value string) bool {
	body, _, _ := strings.Cut(value, "?")
	return strings.Contains(body, ":")
}

type packIndex struct {
	SchemaVersion int                       `json:"schema_version"`
	Blueprints    map[string]packIndexEntry `json:"blueprints"`
}

type packIndexEntry struct {
	Ref          string `json:"ref"`
	VersionedRef string `json:"versioned_ref"`
}

func expandPackShorthand(value string) (string, bool, error) {
	body, rawQuery, hasQuery := strings.Cut(value, "?")
	name, version, hasVersion := strings.Cut(body, "==")
	if strings.TrimSpace(name) == "" {
		return "", false, fmt.Errorf("blueprint shorthand must not be empty")
	}
	if hasVersion && strings.TrimSpace(version) == "" {
		return "", false, fmt.Errorf("blueprint shorthand %q has an empty version", name)
	}
	index, err := loadPackIndex(packIndexURL())
	if err != nil {
		return "", false, fmt.Errorf("load Reploy blueprint index: %w", err)
	}
	entry, found := index.Blueprints[name]
	if !found {
		return "", false, nil
	}
	template := strings.TrimSpace(entry.Ref)
	if hasVersion {
		template = strings.TrimSpace(entry.VersionedRef)
		if template == "" {
			return "", false, fmt.Errorf("blueprint shorthand %q in Reploy blueprint index does not support version pins", name)
		}
		if !strings.Contains(template, "{version}") {
			return "", false, fmt.Errorf("versioned_ref for blueprint shorthand %q must contain {version}", name)
		}
		template = strings.ReplaceAll(template, "{version}", version)
	} else if template == "" {
		return "", false, fmt.Errorf("blueprint shorthand %q in Reploy blueprint index is missing ref", name)
	}
	if hasQuery {
		if strings.Contains(template, "?") {
			return "", false, fmt.Errorf("blueprint shorthand %q expands to a ref that already has a query string", name)
		}
		template += "?" + rawQuery
	}
	return template, true, nil
}

func packShorthandName(value string) string {
	body, _, _ := strings.Cut(value, "?")
	name, _, _ := strings.Cut(body, "==")
	return name
}

func packIndexURL() string {
	if value := strings.TrimSpace(os.Getenv(packIndexURLEnv)); value != "" {
		return value
	}
	return defaultPackIndexURL
}

func loadPackIndex(indexURL string) (packIndex, error) {
	index, _, refreshErr := refreshPackIndex(indexURL)
	if refreshErr == nil {
		return index, nil
	}
	cached, cacheErr := os.ReadFile(packIndexCachePath(indexURL))
	if cacheErr == nil {
		return parsePackIndex(cached)
	}
	return packIndex{}, refreshErr
}

func refreshPackIndex(indexURL string) (packIndex, string, error) {
	if strings.HasPrefix(indexURL, "file:") {
		index, err := readPackIndexFile(strings.TrimPrefix(indexURL, "file:"))
		return index, "", err
	}
	if !strings.HasPrefix(indexURL, "http://") && !strings.HasPrefix(indexURL, "https://") {
		return packIndex{}, "", fmt.Errorf("unsupported index URL %q", indexURL)
	}
	content, err := fetchPackIndex(indexURL)
	if err != nil {
		return packIndex{}, "", err
	}
	index, err := parsePackIndex(content)
	if err != nil {
		return packIndex{}, "", err
	}
	path := packIndexCachePath(indexURL)
	if err := writePackIndexCachePath(path, content); err != nil {
		return packIndex{}, "", err
	}
	return index, path, nil
}

func readPackIndexFile(path string) (packIndex, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return packIndex{}, err
	}
	return parsePackIndex(content)
}

func fetchPackIndex(indexURL string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get(indexURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", indexURL, response.Status)
	}
	return io.ReadAll(response.Body)
}

func parsePackIndex(content []byte) (packIndex, error) {
	var index packIndex
	if err := json.Unmarshal(content, &index); err != nil {
		return packIndex{}, err
	}
	if index.SchemaVersion != 1 {
		return packIndex{}, fmt.Errorf("unsupported schema_version %d", index.SchemaVersion)
	}
	if index.Blueprints == nil {
		index.Blueprints = map[string]packIndexEntry{}
	}
	return index, nil
}

func writePackIndexCachePath(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func packIndexCachePath(indexURL string) string {
	sum := sha256.Sum256([]byte(indexURL))
	return filepath.Join(reployCLICacheDir(), "blueprint-index", hex.EncodeToString(sum[:])+".json")
}

func reployCLICacheDir() string {
	if value := strings.TrimSpace(os.Getenv("REPLOY_CACHE_DIR")); value != "" {
		return value
	}
	if value, err := os.UserCacheDir(); err == nil && value != "" {
		return filepath.Join(value, "reploy")
	}
	return filepath.Join(os.TempDir(), "reploy-cache")
}

func optionValue(args []string, index *int) (string, bool) {
	if *index+1 >= len(args) || strings.HasPrefix(args[*index+1], "--") {
		return "", false
	}
	*index = *index + 1
	return args[*index], true
}

func startSpinner(output io.Writer, label string) func(bool) {
	if output == nil {
		return func(bool) {}
	}
	done := make(chan bool, 1)
	finished := make(chan struct{})
	go func() {
		frames := []string{"|", "/", "-", "\\"}
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		index := 0
		fmt.Fprintf(output, "\r%s %s", frames[index], label)
		for {
			select {
			case ok := <-done:
				if ok {
					fmt.Fprintf(output, "\r%s... done\n", label)
				} else {
					fmt.Fprintf(output, "\r%s... failed\n", label)
				}
				close(finished)
				return
			case <-ticker.C:
				index = (index + 1) % len(frames)
				fmt.Fprintf(output, "\r%s %s", frames[index], label)
			}
		}
	}()
	return func(ok bool) {
		done <- ok
		<-finished
	}
}

func printShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy COMMAND")
	fmt.Fprintln(output, "Run 'reploy --help' for help.")
}

func printHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker] COMMAND

Commands:
  init         Create a Docker deployment directory
  update       Update generated files in a deployment directory
  info         Show deployment state and bundle contents
  app          Run a blueprint-declared app command inside the deployment runtime
  bundle       Manage installation bundle contents
  up           Start or update the Compose service
  restart      Recreate the Compose service
  down         Stop and remove the Compose service
  ps           Show Compose service status
  status       Show Compose service status
  logs         Show Compose logs with timestamps
  test         Probe the blueprint-configured app health endpoint
  doctor       Check deployment files and generated-file drift
  install      Plan or apply installation into a host service directory
  blueprint-index
               Manage the cached blueprint shorthand index
  version      Print version information

Bundle:
  list         List selected installation artifact roots
    all        List root and transitive built installation artifacts
  list-options List blueprint-declared bundle options
  add          Add installation artifact roots
  add-wheel    Copy a wheel into the bundle and add it as a root
  add-source   Build a source directory wheel into the bundle and add it
  remove       Remove installation artifact roots
  check        Validate built installation artifacts
  build        Build and validate installation bundle artifacts
  clean        Remove built installation artifacts
  upgrade      Upgrade package roots and rebuild installation bundle artifacts

Target options:
  --docker     Use the Docker deployment target, default
  --aws        Reserved for a future AWS deployment target

Deployment options:
  --dir DIR    Deployment directory, default current deployment or reploy-staging
  --blueprint REF
              App blueprint reference, required for init
              Use an indexed shorthand, file:PATH, or pypi:PACKAGE.
              Add ==VERSION to an indexed shorthand to pin a release.
  --requirement REQ
              Exact package pin or absolute container path for requirements.txt
  --name NAME  Bundle option name for bundle add; accepts comma-separated names
  --force      Overwrite generated files during update; with bundle add --name,
              treat unknown names as package roots
  --preinstall Run install-readiness doctor checks
  --quiet      Suppress passing doctor checks
  --to DIR     Install target directory
  --service NAME
               Installed systemd service name, default app id
  --port PORT  Installed host port override for single-port apps
  --port NAME=PORT
              Installed host port override for a named blueprint port; repeat
              for multiple ports
  --dry-run    Print the install plan without changing the host
  --start      Start after install, default
  --no-start   Install without starting the service
  --verbose    Show bundle build command output
  --follow     Follow logs instead of exiting after current output
  --timeout DURATION
              With test, readiness timeout for running services

Options:
  -h, --help   Show help
  --version    Print version information
`, "\n"))
}

func printPackIndexShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy blueprint-index COMMAND")
	fmt.Fprintln(output, "Run 'reploy blueprint-index --help' for blueprint index help.")
}

func printPackIndexHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy blueprint-index COMMAND

Commands:
  refresh      Download, validate, and cache the blueprint shorthand index

Options:
  --url URL    Index URL, default from REPLOY_BLUEPRINT_INDEX_URL or built-in default
  -h, --help   Show blueprint index help
`, "\n"))
}

func printBundleShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy bundle COMMAND")
	fmt.Fprintln(output, "Run 'reploy bundle --help' for bundle help.")
	fmt.Fprintln(output)
	fmt.Fprint(output, bundleCommandSummary())
}

func printBundleHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy bundle COMMAND

`, "\n"))
	fmt.Fprint(output, bundleCommandSummary())
	fmt.Fprint(output, strings.TrimLeft(`

Options:
  --dir DIR    Deployment directory, default current deployment or reploy-staging
  --name NAME  Bundle option name for bundle add; accepts comma-separated names
  --force      With bundle add --name, treat unknown names as package roots
  --dry-run    Print build/check commands without changing the deployment
  --pypi-only  Build or upgrade using only PyPI package roots
  --verbose    Show bundle build command output
  -h, --help   Show bundle help
`, "\n"))
}

func bundleCommandSummary() string {
	return strings.TrimLeft(`
Commands:
  list         List selected installation artifact roots
    all        List root and transitive built installation artifacts
  list-options List blueprint-declared bundle options
  add          Add installation artifact roots
  add-wheel    Copy a wheel into the bundle and add it as a root
  add-source   Build a source directory wheel into the bundle and add it
  remove       Remove installation artifact roots
  check        Validate built installation artifacts
  build        Build and validate installation bundle artifacts
  clean        Remove built installation artifacts
  upgrade      Upgrade package roots and rebuild installation bundle artifacts
`, "\n")
}

func printAppShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy app COMMAND")
	fmt.Fprintln(output, "Run 'reploy app --help' for app command help.")
	fmt.Fprintln(output)
	fmt.Fprint(output, appCommandSummary())
}

func printAppHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy app COMMAND

Run a blueprint-declared app command inside the deployment runtime. App commands use
the application installed in the deployment bundle, not a host executable from
PATH.

`, "\n"))
	fmt.Fprint(output, appCommandSummary())
	fmt.Fprint(output, strings.TrimLeft(`

Options:
  --dir DIR    Deployment directory, default current deployment or reploy-staging
  -h, --help   Show app command help
`, "\n"))
}

func appCommandSummary() string {
	return strings.TrimLeft(`
Show this deployment's app subcommands with:
  reploy app

Run an app subcommand with:
  reploy app COMMAND
`, "\n")
}

func printDockerShortUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: reploy [--docker] COMMAND")
	fmt.Fprintln(output, "Run 'reploy --help' for deployment help.")
}

func printDockerHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker] COMMAND

Commands:
  init         Create a Docker deployment directory
  update       Update generated files in a Docker deployment directory
  info         Show deployment state and bundle contents
  app          Run a blueprint-declared app command inside the deployment runtime
  bundle       Manage installation bundle contents
  up           Start or update the Compose service
  restart      Recreate the Compose service
  down         Stop and remove the Compose service
  ps           Show Compose service status
  status       Show Compose service status
  logs         Show Compose logs with timestamps
  test         Probe the blueprint-configured app health endpoint
  doctor       Check deployment files and generated-file drift
  install      Plan or apply installation into a host service directory

Bundle:
  list         List selected installation artifact roots
    all        List root and transitive built installation artifacts
  list-options List blueprint-declared bundle options
  add          Add installation artifact roots
  add-wheel    Copy a wheel into the bundle and add it as a root
  add-source   Build a source directory wheel into the bundle and add it
  remove       Remove installation artifact roots
  check        Validate built installation artifacts
  build        Build and validate installation bundle artifacts
  clean        Remove built installation artifacts
  upgrade      Upgrade package roots and rebuild installation bundle artifacts

Options:
  --dir DIR    Deployment directory, default current deployment or reploy-staging
  --blueprint REF
              App blueprint reference, required for init
              Use an indexed shorthand, file:PATH, or pypi:PACKAGE.
              Add ==VERSION to an indexed shorthand to pin a release.
  --requirement REQ
              Exact package pin or absolute container path for requirements.txt
  --name NAME  Bundle option name for bundle add; accepts comma-separated names
  --force      Overwrite generated files during update; with bundle add --name,
              treat unknown names as package roots
  --preinstall Run install-readiness doctor checks
  --quiet      Suppress passing doctor checks
  --to DIR     Install target directory
  --service NAME
               Installed systemd service name, default app id
  --port PORT  Installed host port override for single-port apps
  --port NAME=PORT
              Installed host port override for a named blueprint port; repeat
              for multiple ports
  --dry-run    Print the install plan without changing the host
  --start      Start after install, default
  --no-start   Install without starting the service
  --verbose    Show bundle build command output
  --follow     Follow logs instead of exiting after current output
  --timeout DURATION
              With test, readiness timeout for running services
  -h, --help   Show deployment help
`, "\n"))
}

func printDockerCommandHelp(command string, output io.Writer) {
	switch command {
	case "update":
		printDockerUpdateHelp(output)
	default:
		printDockerHelp(output)
	}
}

func printDockerUpdateHelp(output io.Writer) {
	fmt.Fprint(output, strings.TrimLeft(`
Usage: reploy [--docker] update [OPTIONS]

Update generated files in a Docker deployment directory.

Options:
  --dir DIR    Deployment directory to update, default current deployment or reploy-staging
  --blueprint REF
              App blueprint reference to update from; defaults to saved state
              Use an indexed shorthand, file:PATH, or pypi:PACKAGE.
              Add ==VERSION to an indexed shorthand to pin a release.
  --force      Overwrite locally edited generated files
  -h, --help   Show update help
`, "\n"))
}
