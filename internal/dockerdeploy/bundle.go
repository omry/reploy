package dockerdeploy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/python"
)

type BundleListOptions struct {
	Dir string
}

type BundleOption struct {
	Name        string
	Identifier  string
	Group       string
	Description string
}

type BundleResolvedPackage struct {
	Kind        string
	Requirement string
}

type BundleRootOptions struct {
	Dir                    string
	Source                 string
	DockerPreflightTimeout time.Duration
}

type BundleRootsOptions struct {
	Dir     string
	Sources []string
	Names   []string
}

type BundleCheckOptions struct {
	Dir                    string
	DryRun                 bool
	Verbose                bool
	Stdout                 io.Writer
	Stderr                 io.Writer
	DockerPreflightTimeout time.Duration
}

type BundlePrepareOptions struct {
	Dir                    string
	DryRun                 bool
	PyPIOnly               bool
	NoWarmRuntime          bool
	Verbose                bool
	Stdout                 io.Writer
	Stderr                 io.Writer
	DockerPreflightTimeout time.Duration
}

func bundleDockerRunOptions(stdout io.Writer, stderr io.Writer, dockerPreflightTimeout time.Duration) RunOptions {
	return RunOptions{
		Stdout:                 stdout,
		Stderr:                 stderr,
		DockerPreflightTimeout: dockerPreflightTimeout,
	}
}

type bundleTimingStep struct {
	Name     string
	Duration time.Duration
}

type bundleTimer struct {
	enabled bool
	output  io.Writer
	start   time.Time
	steps   []bundleTimingStep
}

func newBundleTimer(enabled bool, output io.Writer) *bundleTimer {
	return &bundleTimer{enabled: enabled, output: output, start: time.Now()}
}

func (timer *bundleTimer) Measure(name string, run func() error) error {
	if !timer.enabled {
		return run()
	}
	if timer.output != nil {
		fmt.Fprintf(timer.output, "bundle build: %s...\n", name)
	}
	start := time.Now()
	err := run()
	timer.steps = append(timer.steps, bundleTimingStep{Name: name, Duration: time.Since(start)})
	return err
}

func (timer *bundleTimer) Print(output io.Writer) {
	if !timer.enabled || output == nil {
		return
	}
	total := time.Since(timer.start)
	fmt.Fprintln(output, "bundle build timing:")
	for _, step := range timer.steps {
		fmt.Fprintf(output, "  %s: %s\n", step.Name, formatDuration(step.Duration))
	}
	fmt.Fprintf(output, "  total: %s\n", formatDuration(total))
}

func formatDuration(duration time.Duration) string {
	if duration < time.Second {
		return duration.Round(time.Millisecond).String()
	}
	return duration.Round(100 * time.Millisecond).String()
}

type BundleEnsureOptions struct {
	Dir                    string
	DryRun                 bool
	Verbose                bool
	Stdout                 io.Writer
	Stderr                 io.Writer
	DockerPreflightTimeout time.Duration
}

type BundleCleanOptions struct {
	Dir string
}

type BundleUpgradeOptions struct {
	Dir                    string
	Target                 string
	PyPIOnly               bool
	Stdout                 io.Writer
	Stderr                 io.Writer
	DockerPreflightTimeout time.Duration
}

type bundleBuildSource struct {
	Name              string
	HostDir           string
	ContainerDir      string
	BuildDir          string
	BuildRequirements []string
}

var runBundleCommand = runCommand

func BundleList(options BundleListOptions) ([]deploy.ArtifactRoot, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return nil, err
	}
	state, err = withInferredBundleState(options.Dir, state)
	if err != nil {
		return nil, err
	}
	if err := validateBundleRequirementsProjection(options.Dir, state); err != nil {
		return nil, err
	}
	return state.Bundle.Roots, nil
}

func BundleListAll(options BundleListOptions) ([]BundleResolvedPackage, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return nil, err
	}
	state, err = withInferredBundleState(options.Dir, state)
	if err != nil {
		return nil, err
	}
	bundleDir, err := deploymentBundleDir(options.Dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		return nil, err
	}
	rootRequirements := python.RootRequirements(state.Bundle.Roots)
	resolved := []BundleResolvedPackage{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".whl") {
			continue
		}
		requirement, ok := python.WheelFilenameRequirement(entry.Name())
		if !ok {
			continue
		}
		kind := "transitive"
		if rootRequirements[requirement] || rootRequirements[strings.Split(requirement, "==")[0]] {
			kind = "root"
		}
		resolved = append(resolved, BundleResolvedPackage{Kind: kind, Requirement: requirement})
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("installation bundle is empty: %s; run reploy bundle build to build it", bundleDir)
	}
	sortBundleResolvedPackages(resolved)
	return resolved, nil
}

func BundleAdd(options BundleRootOptions) ([]UpdateResult, error) {
	return BundleAddMany(BundleRootsOptions{Dir: options.Dir, Sources: []string{options.Source}})
}

func BundleAddMany(options BundleRootsOptions) ([]UpdateResult, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if len(options.Sources) == 0 && len(options.Names) == 0 {
		return nil, fmt.Errorf("expected bundle root")
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return nil, err
	}
	state, err = withInferredBundleState(options.Dir, state)
	if err != nil {
		return nil, err
	}
	roots := make([]deploy.ArtifactRoot, 0, len(options.Names)+len(options.Sources))
	for _, name := range options.Names {
		root, err := resolveBundleOptionRoot(options.Dir, name)
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	for _, source := range options.Sources {
		root, err := classifyBundleRoot(source)
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	for _, root := range roots {
		selected := false
		for _, existing := range state.Bundle.Roots {
			if existing == root {
				selected = true
				break
			}
		}
		if !selected {
			state.Bundle.Roots = append(state.Bundle.Roots, root)
		}
	}
	return syncBundleState(options.Dir, state)
}

func BundleRemove(options BundleRootOptions) ([]UpdateResult, error) {
	return BundleRemoveMany(BundleRootsOptions{Dir: options.Dir, Sources: []string{options.Source}})
}

func BundleRemoveMany(options BundleRootsOptions) ([]UpdateResult, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	if len(options.Sources) == 0 && len(options.Names) == 0 {
		return nil, fmt.Errorf("expected bundle root")
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return nil, err
	}
	state, err = withInferredBundleState(options.Dir, state)
	if err != nil {
		return nil, err
	}
	sources := map[string]bool{}
	for _, name := range options.Names {
		root, err := resolveBundleOptionRoot(options.Dir, name)
		if err != nil {
			return nil, err
		}
		if !bundleRootSourceSelected(state.Bundle.Roots, root.Source) {
			return nil, fmt.Errorf("bundle root is not selected: %s", root.Source)
		}
		sources[root.Source] = true
	}
	for _, source := range options.Sources {
		source = strings.TrimSpace(source)
		if source == "" {
			return nil, fmt.Errorf("bundle root must not be empty")
		}
		if !bundleRootSourceSelected(state.Bundle.Roots, source) {
			return nil, fmt.Errorf("bundle root is not selected: %s", source)
		}
		sources[source] = true
	}
	roots := make([]deploy.ArtifactRoot, 0, len(state.Bundle.Roots))
	for _, existing := range state.Bundle.Roots {
		if sources[existing.Source] || sources[bundleRootPackageName(existing)] {
			continue
		}
		roots = append(roots, existing)
	}
	state.Bundle.Roots = roots
	return syncBundleState(options.Dir, state)
}

func bundleRootSourceSelected(roots []deploy.ArtifactRoot, source string) bool {
	for _, existing := range roots {
		if existing.Source == source || bundleRootPackageName(existing) == source {
			return true
		}
	}
	return false
}

func BundleAddWheel(options BundleRootOptions) ([]UpdateResult, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	sourcePath, err := filepath.Abs(strings.TrimSpace(options.Source))
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(sourcePath, ".whl") {
		return nil, fmt.Errorf("bundle wheel root must be a .whl file: %s", options.Source)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("bundle wheel root is a directory: %s", options.Source)
	}
	bundleDir, err := deploymentBundleDir(options.Dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return nil, err
	}
	wheelName := filepath.Base(sourcePath)
	targetPath := filepath.Join(bundleDir, wheelName)
	wheelStatus, err := copyFileIfDifferent(sourcePath, targetPath)
	if err != nil {
		return nil, err
	}
	results, err := BundleAdd(BundleRootOptions{Dir: options.Dir, Source: "/bundle/" + wheelName})
	if err != nil {
		return nil, err
	}
	results = append([]UpdateResult{{Path: targetPath, Status: wheelStatus}}, results...)
	return results, nil
}

func BundleAddSource(options BundleRootOptions) ([]UpdateResult, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	sourcePath, err := filepath.Abs(strings.TrimSpace(options.Source))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bundle source root is not a directory: %s", options.Source)
	}
	if _, err := os.Stat(filepath.Join(sourcePath, "pyproject.toml")); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("bundle source root must contain pyproject.toml: %s", options.Source)
		}
		return nil, err
	}
	bundleDir, err := deploymentBundleDir(options.Dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "reploy-source-wheel-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	spec, err := BundleSourceWheelCommand(options.Dir, sourcePath, tmpDir)
	if err != nil {
		return nil, err
	}
	if err := runInterruptibleCommand(runBundleCommand, spec, bundleDockerRunOptions(nil, nil, options.DockerPreflightTimeout)); err != nil {
		return nil, err
	}
	wheelPath, err := singleWheelInDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("build source wheel: %w", err)
	}
	wheelName := filepath.Base(wheelPath)
	targetPath := filepath.Join(bundleDir, wheelName)
	wheelStatus, err := copyFileIfDifferent(wheelPath, targetPath)
	if err != nil {
		return nil, err
	}
	results, err := BundleAdd(BundleRootOptions{Dir: options.Dir, Source: "/bundle/" + wheelName})
	if err != nil {
		return nil, err
	}
	results = append([]UpdateResult{{Path: targetPath, Status: wheelStatus}}, results...)
	return results, nil
}

func BundleCheck(options BundleCheckOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	stdout := options.Stdout
	stderr := options.Stderr
	quiet := !options.Verbose && !options.DryRun
	if quiet {
		stdout = nil
		stderr = nil
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return err
	}
	state, err = withInferredBundleState(options.Dir, state)
	if err != nil {
		return err
	}
	requirements, err := requirementsContentFromRoots(state.Bundle.Roots)
	if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "reploy-bundle-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	requirementsPath := filepath.Join(tmpDir, "requirements.check.txt")
	if err := os.WriteFile(requirementsPath, requirements, 0o644); err != nil {
		return err
	}
	spec, bundleDir, err := bundleCheckCommand(options.Dir, requirementsPath)
	if err != nil {
		return err
	}
	if options.DryRun {
		if options.Stdout != nil {
			fmt.Fprintf(options.Stdout, "would validate installation bundle: %s\n", bundleDir)
			fmt.Fprintln(options.Stdout, commandLine(spec))
		}
		return nil
	}
	if err := requireBundleCheckInputs(options.Dir, bundleDir); err != nil {
		return err
	}
	return runInterruptibleCommand(runBundleCommand, spec, bundleDockerRunOptions(stdout, stderr, options.DockerPreflightTimeout))
}

func BundlePrepare(options BundlePrepareOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	stdout := options.Stdout
	stderr := options.Stderr
	quiet := !options.Verbose && !options.DryRun
	if quiet {
		stdout = nil
		stderr = nil
	}
	timer := newBundleTimer(options.Verbose && options.Stdout != nil, options.Stdout)
	if options.PyPIOnly && !options.DryRun {
		if err := timer.Measure("resolve package roots", func() error {
			state, err := loadState(options.Dir)
			if err != nil {
				return err
			}
			state, err = withInferredBundleState(options.Dir, state)
			if err != nil {
				return err
			}
			_, _, err = resolveBundlePackageRoots(options.Dir, state, "", true, stdout, stderr, options.DockerPreflightTimeout)
			return err
		}); err != nil {
			return err
		}
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return err
	}
	state, err = withInferredBundleState(options.Dir, state)
	if err != nil {
		return err
	}
	if err := python.RejectPersistentSourceRoots(state.Bundle.Roots, "bundle build"); err != nil {
		return err
	}
	bundleDir, err := deploymentBundleDir(options.Dir)
	if err != nil {
		return err
	}
	spec, err := bundlePrepareCommand(bundlePrepareCommandOptions{
		Dir:           options.Dir,
		WheelhouseDir: bundleDir,
		PyPIOnly:      options.PyPIOnly,
		State:         state,
	})
	if err != nil {
		return err
	}
	if options.DryRun {
		if options.Stdout != nil {
			fmt.Fprintf(options.Stdout, "would build installation bundle: %s\n", bundleDir)
			fmt.Fprintln(options.Stdout, commandLine(spec))
		}
		return nil
	}
	var tmpDir string
	if err := timer.Measure("prepare workspace", func() error {
		if err := requireBundlePrepareInputs(options.Dir, bundleDir); err != nil {
			return err
		}
		if err := os.MkdirAll(bundleDir, 0o755); err != nil {
			return err
		}
		var err error
		tmpDir, err = os.MkdirTemp("", "reploy-wheelhouse-*")
		return err
	}); err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	if !options.PyPIOnly {
		if err := timer.Measure("copy existing bundle", func() error {
			return copyWheelhouse(bundleDir, tmpDir)
		}); err != nil {
			return err
		}
	}
	requirementsPath := ""
	findLinksDir := ""
	buildSources := []bundleBuildSource{}
	if !options.PyPIOnly {
		if err := timer.Measure("prepare local sources", func() error {
			buildSources, err = localBundleBuildSources(state)
			if err != nil {
				return err
			}
			if len(buildSources) > 0 {
				requirementsPath = filepath.Join(tmpDir, "requirements.local.txt")
				requirements, err := localBuildRequirements(state.Bundle.Roots, buildSources)
				if err != nil {
					return err
				}
				if err := os.WriteFile(requirementsPath, requirements, 0o644); err != nil {
					return err
				}
				findLinksDir = tmpDir
			}
			return nil
		}); err != nil {
			return err
		}
	}
	spec, err = bundlePrepareCommand(bundlePrepareCommandOptions{
		Dir:              options.Dir,
		WheelhouseDir:    tmpDir,
		PyPIOnly:         options.PyPIOnly,
		State:            state,
		RequirementsPath: requirementsPath,
		FindLinksDir:     findLinksDir,
		Sources:          buildSources,
	})
	if err != nil {
		return err
	}
	runStdout := stdout
	runStderr := stderr
	if err := timer.Measure("build wheelhouse", func() error {
		return runInterruptibleCommand(runBundleCommand, spec, bundleDockerRunOptions(runStdout, runStderr, options.DockerPreflightTimeout))
	}); err != nil {
		return err
	}
	if err := timer.Measure("replace bundle", func() error {
		return replaceWheelhouse(tmpDir, bundleDir)
	}); err != nil {
		return err
	}
	if options.Verbose && options.Stdout != nil {
		fmt.Fprintf(options.Stdout, "built installation bundle: %s\n", bundleDir)
	}
	if err := timer.Measure("validate bundle", func() error {
		return BundleCheck(BundleCheckOptions{Dir: options.Dir, Verbose: options.Verbose, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: options.DockerPreflightTimeout})
	}); err != nil {
		return err
	}
	if !options.NoWarmRuntime {
		if err := timer.Measure("warm Python runtime", func() error {
			return BundleWarmRuntime(BundleWarmRuntimeOptions{Dir: options.Dir, Verbose: options.Verbose, Stdout: stdout, Stderr: stderr, DockerPreflightTimeout: options.DockerPreflightTimeout})
		}); err != nil {
			return err
		}
	}
	timer.Print(options.Stdout)
	return markBundlePrepared(options.Dir)
}

type BundleWarmRuntimeOptions struct {
	Dir                    string
	Verbose                bool
	Stdout                 io.Writer
	Stderr                 io.Writer
	DockerPreflightTimeout time.Duration
}

func BundleWarmRuntime(options BundleWarmRuntimeOptions) error {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return err
	}
	state, err = withInferredBundleState(options.Dir, state)
	if err != nil {
		return err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return err
	}
	if err := ensureOneOffCommandDirs(options.Dir, pack); err != nil {
		return err
	}
	if _, err := materializeRuntimeCompose(options.Dir); err != nil {
		return fmt.Errorf("materialize runtime compose: %w", err)
	}
	projectName, err := deploymentComposeProjectName(options.Dir)
	if err != nil {
		return err
	}
	spec := BundleWarmRuntimeCommand(options.Dir, projectName)
	stdout := options.Stdout
	stderr := options.Stderr
	if !options.Verbose {
		stdout = nil
		stderr = nil
	}
	return runInterruptibleCommand(runBundleCommand, spec, bundleDockerRunOptions(stdout, stderr, options.DockerPreflightTimeout))
}

func BundleWarmRuntimeCommand(dir string, projectName string) CommandSpec {
	args := []string{
		"run",
		"--rm",
		"--no-deps",
		"-e",
		"REPLOY_CONTAINER_COMMAND=__reploy_runtime_warmup",
		"app",
	}
	return quietComposeCommand(composeCommandWithProject(dir, projectName, args...))
}

func EnsureBundlePrepared(options BundleEnsureOptions) (bool, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	prepared, err := bundlePrepared(options.Dir)
	if err != nil {
		return false, err
	}
	if prepared {
		return false, nil
	}
	return true, BundlePrepare(BundlePrepareOptions{
		Dir:                    options.Dir,
		DryRun:                 options.DryRun,
		Verbose:                options.Verbose,
		Stdout:                 options.Stdout,
		Stderr:                 options.Stderr,
		DockerPreflightTimeout: options.DockerPreflightTimeout,
	})
}

func BundleClean(options BundleCleanOptions) ([]UpdateResult, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return nil, err
	}
	state, err = withInferredBundleState(options.Dir, state)
	if err != nil {
		return nil, err
	}
	bundleDir, err := deploymentBundleDir(options.Dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(bundleDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	results := []UpdateResult{}
	if err == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("bundle path is not a directory: %s", bundleDir)
		}
		if err := os.RemoveAll(bundleDir); err != nil {
			return nil, err
		}
		results = append(results, UpdateResult{Path: bundleDir, Status: deploy.UpdateStatusRemoved})
	}
	runtimeCacheDir, err := deploymentRuntimePythonVenvDir(options.Dir)
	if err != nil {
		return nil, err
	}
	cacheInfo, cacheErr := os.Stat(runtimeCacheDir)
	if cacheErr != nil && !os.IsNotExist(cacheErr) {
		return nil, cacheErr
	}
	if cacheErr == nil {
		if !cacheInfo.IsDir() {
			return nil, fmt.Errorf("runtime cache path is not a directory: %s", runtimeCacheDir)
		}
		if err := os.RemoveAll(runtimeCacheDir); err != nil {
			return nil, err
		}
		results = append(results, UpdateResult{Path: runtimeCacheDir, Status: deploy.UpdateStatusRemoved, Ownership: "generated", Reason: "removed warmed Python runtime cache"})
	}
	if len(results) == 0 && state.Bundle.PreparedFingerprint == "" {
		return nil, nil
	}
	status, err := markBundleUnprepared(options.Dir)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(options.Dir, StateFileName), Status: status, Ownership: "state", Reason: "marked installation bundle stale"})
	return results, nil
}

func BundleUpgrade(options BundleUpgradeOptions) ([]UpdateResult, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	state, err := loadState(options.Dir)
	if err != nil {
		return nil, err
	}
	state, err = withInferredBundleState(options.Dir, state)
	if err != nil {
		return nil, err
	}
	_, results, err := resolveBundlePackageRoots(options.Dir, state, options.Target, options.PyPIOnly, options.Stdout, options.Stderr, options.DockerPreflightTimeout)
	if err != nil {
		return nil, err
	}
	if err := BundlePrepare(BundlePrepareOptions{
		Dir:                    options.Dir,
		PyPIOnly:               options.PyPIOnly,
		Stdout:                 options.Stdout,
		Stderr:                 options.Stderr,
		DockerPreflightTimeout: options.DockerPreflightTimeout,
	}); err != nil {
		return nil, err
	}
	return results, nil
}

func resolveBundlePackageRoots(dir string, state deploy.DeploymentState, target string, pypiOnly bool, stdout io.Writer, stderr io.Writer, dockerPreflightTimeout time.Duration) (deploy.DeploymentState, []UpdateResult, error) {
	input, roots, err := python.BundleUpgradeInput(state.Bundle.Roots, target)
	if err != nil {
		return deploy.DeploymentState{}, nil, err
	}
	tmpDir, err := os.MkdirTemp("", "reploy-bundle-resolve-*")
	if err != nil {
		return deploy.DeploymentState{}, nil, err
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "requirements.in"), []byte(strings.Join(input, "\n")+"\n"), 0o644); err != nil {
		return deploy.DeploymentState{}, nil, err
	}
	spec, err := BundleUpgradeResolveCommand(dir, tmpDir, pypiOnly)
	if err != nil {
		return deploy.DeploymentState{}, nil, err
	}
	if err := runInterruptibleCommand(runBundleCommand, spec, bundleDockerRunOptions(stdout, stderr, dockerPreflightTimeout)); err != nil {
		return deploy.DeploymentState{}, nil, err
	}
	resolvedRoots, err := python.ResolvedUpgradeRoots(filepath.Join(tmpDir, "report.json"), roots)
	if err != nil {
		return deploy.DeploymentState{}, nil, err
	}
	state.Bundle.Roots = resolvedRoots
	results, err := syncBundleState(dir, state)
	if err != nil {
		return deploy.DeploymentState{}, nil, err
	}
	return state, results, nil
}

func BundleCheckCommand(dir string) (CommandSpec, string, error) {
	return bundleCheckCommand(dir, "")
}

func bundleCheckCommand(dir string, requirementsPath string) (CommandSpec, string, error) {
	if dir == "" {
		dir = DefaultDeploymentDir
	}
	state, err := loadState(dir)
	if err != nil {
		return CommandSpec{}, "", err
	}
	state, err = withInferredBundleState(dir, state)
	if err != nil {
		return CommandSpec{}, "", err
	}
	if err := python.RejectPersistentSourceRoots(state.Bundle.Roots, "bundle check"); err != nil {
		return CommandSpec{}, "", err
	}
	values, err := readDockerEnv(dir)
	if err != nil {
		return CommandSpec{}, "", err
	}
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return CommandSpec{}, "", err
	}
	if requirementsPath == "" {
		requirementsPath = filepath.Join(absoluteDir, RequirementsFileName)
	}
	absoluteRequirementsPath, err := filepath.Abs(requirementsPath)
	if err != nil {
		return CommandSpec{}, "", err
	}
	bundleDir, err := deploymentBundleDir(absoluteDir)
	if err != nil {
		return CommandSpec{}, "", err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return CommandSpec{}, "", err
	}
	sources, err := selectedPackLocalSources(pack, state.Bundle.Roots, localSourceContainerRoot())
	if err != nil {
		return CommandSpec{}, "", err
	}
	image := envValue(values, "REPLOY_IMAGE", "python:3.11-slim")
	args := []string{
		"run",
		"--rm",
		"--user",
		defaultContainerUser(),
		"-v",
		absoluteRequirementsPath + ":/requirements.txt:ro",
		"-v",
		bundleDir + ":/bundle:ro",
	}
	for _, source := range sources {
		args = append(args, "-v", source.HostDir+":"+source.ContainerDir+":ro")
	}
	args = append(args, image)
	args = append(args, python.InstallCheckArgv()...)
	return CommandSpec{Name: "docker", Args: args, Dir: absoluteDir}, bundleDir, nil
}

type bundlePrepareCommandOptions struct {
	Dir              string
	WheelhouseDir    string
	PyPIOnly         bool
	State            deploy.DeploymentState
	RequirementsPath string
	FindLinksDir     string
	Sources          []bundleBuildSource
}

func BundlePrepareCommand(dir string, wheelhouseDir string, pypiOnly bool) (CommandSpec, error) {
	return bundlePrepareCommand(bundlePrepareCommandOptions{
		Dir:           dir,
		WheelhouseDir: wheelhouseDir,
		PyPIOnly:      pypiOnly,
	})
}

func bundlePrepareCommand(options bundlePrepareCommandOptions) (CommandSpec, error) {
	dir := options.Dir
	if dir == "" {
		dir = DefaultDeploymentDir
	}
	state := options.State
	if state.SchemaVersion == 0 {
		var err error
		state, err = loadState(dir)
		if err != nil {
			return CommandSpec{}, err
		}
		state, err = withInferredBundleState(dir, state)
		if err != nil {
			return CommandSpec{}, err
		}
		if err := python.RejectPersistentSourceRoots(state.Bundle.Roots, "bundle build"); err != nil {
			return CommandSpec{}, err
		}
	}
	values, err := readDockerEnv(dir)
	if err != nil {
		return CommandSpec{}, err
	}
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return CommandSpec{}, err
	}
	requirementsPath := options.RequirementsPath
	if requirementsPath == "" {
		requirementsPath = filepath.Join(absoluteDir, RequirementsFileName)
	}
	absoluteRequirementsPath, err := filepath.Abs(requirementsPath)
	if err != nil {
		return CommandSpec{}, err
	}
	bundleDir, err := deploymentBundleDir(absoluteDir)
	if err != nil {
		return CommandSpec{}, err
	}
	findLinksDir := options.FindLinksDir
	if findLinksDir == "" {
		findLinksDir = bundleDir
	}
	absoluteFindLinksDir, err := filepath.Abs(findLinksDir)
	if err != nil {
		return CommandSpec{}, err
	}
	absoluteWheelhouseDir, err := filepath.Abs(options.WheelhouseDir)
	if err != nil {
		return CommandSpec{}, err
	}
	image := envValue(values, "REPLOY_IMAGE", "python:3.11-slim")
	args := []string{
		"run",
		"--rm",
	}
	args = append(args,
		"--user",
		defaultContainerUser(),
		"-v",
		absoluteRequirementsPath+":/requirements.txt:ro",
	)
	if !options.PyPIOnly {
		args = append(args, "-v", absoluteFindLinksDir+":/bundle:ro")
	}
	for _, source := range options.Sources {
		args = append(args, "-v", source.HostDir+":"+source.ContainerDir+":ro")
	}
	args = append(args,
		"-v",
		absoluteWheelhouseDir+":/wheelhouse",
		image,
	)
	if len(options.Sources) > 0 {
		args = append(args, "sh", "-c", localSourcePrepareScript(options.Sources, options.PyPIOnly))
	} else {
		args = append(args, python.PrepareWheelhouseArgv(options.PyPIOnly)...)
	}
	return CommandSpec{Name: "docker", Args: args, Dir: absoluteDir}, nil
}

func BundleSourceWheelCommand(dir string, sourceDir string, wheelhouseDir string) (CommandSpec, error) {
	if dir == "" {
		dir = DefaultDeploymentDir
	}
	values, err := readDockerEnv(dir)
	if err != nil {
		return CommandSpec{}, err
	}
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return CommandSpec{}, err
	}
	absoluteSourceDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return CommandSpec{}, err
	}
	absoluteWheelhouseDir, err := filepath.Abs(wheelhouseDir)
	if err != nil {
		return CommandSpec{}, err
	}
	image := envValue(values, "REPLOY_IMAGE", "python:3.11-slim")
	args := []string{
		"run",
		"--rm",
		"--user",
		defaultContainerUser(),
		"-v",
		absoluteSourceDir + ":/source:ro",
		"-v",
		absoluteWheelhouseDir + ":/wheelhouse",
		image,
	}
	args = append(args, python.SourceWheelArgv()...)
	return CommandSpec{Name: "docker", Args: args, Dir: absoluteDir}, nil
}

func BundleUpgradeResolveCommand(dir string, workDir string, pypiOnly bool) (CommandSpec, error) {
	if dir == "" {
		dir = DefaultDeploymentDir
	}
	values, err := readDockerEnv(dir)
	if err != nil {
		return CommandSpec{}, err
	}
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return CommandSpec{}, err
	}
	absoluteWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return CommandSpec{}, err
	}
	bundleDir, err := deploymentBundleDir(absoluteDir)
	if err != nil {
		return CommandSpec{}, err
	}
	image := envValue(values, "REPLOY_IMAGE", "python:3.11-slim")
	args := []string{
		"run",
		"--rm",
		"--user",
		defaultContainerUser(),
		"-v",
		absoluteWorkDir + ":/work",
	}
	if !pypiOnly {
		args = append(args, "-v", bundleDir+":/bundle:ro")
	}
	args = append(args,
		image,
	)
	args = append(args, python.UpgradeResolveArgv(pypiOnly)...)
	return CommandSpec{Name: "docker", Args: args, Dir: absoluteDir}, nil
}

func localBundleBuildSources(state deploy.DeploymentState) ([]bundleBuildSource, error) {
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return nil, err
	}
	return selectedPackLocalSources(pack, state.Bundle.Roots, "/source")
}

func selectedPackLocalSources(pack deploy.AppPack, roots []deploy.ArtifactRoot, containerRoot string) ([]bundleBuildSource, error) {
	if (pack.Ref.Scheme != "file" && pack.Ref.Scheme != "source" && pack.Ref.Scheme != "git") || len(pack.App.Provider.LocalSources) == 0 {
		return nil, nil
	}
	containerRoot = strings.TrimRight(containerRoot, "/")
	byName := map[string]string{}
	for name, source := range pack.App.Provider.LocalSources {
		byName[python.NormalizeRequirementName(name)] = source
	}
	sources := []bundleBuildSource{}
	seen := map[string]bool{}
	for _, root := range roots {
		name := python.RootPackageName(root)
		if name == "" {
			continue
		}
		normalized := python.NormalizeRequirementName(name)
		relativeSource, ok := byName[normalized]
		if !ok || seen[normalized] {
			continue
		}
		hostDir, err := filepath.Abs(filepath.Clean(filepath.Join(pack.Dir, relativeSource)))
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(hostDir)
		if err != nil {
			return nil, fmt.Errorf("local source for %s is not available: %s: %w", name, hostDir, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("local source for %s is not a directory: %s", name, hostDir)
		}
		buildRequirements, err := localSourceBuildRequirements(hostDir)
		if err != nil {
			return nil, fmt.Errorf("local source build requirements for %s: %w", name, err)
		}
		sources = append(sources, bundleBuildSource{
			Name:              normalized,
			HostDir:           hostDir,
			ContainerDir:      containerRoot + "/" + normalized,
			BuildDir:          "/wheelhouse/.source/" + normalized,
			BuildRequirements: buildRequirements,
		})
		seen[normalized] = true
	}
	sort.Slice(sources, func(i int, j int) bool {
		return sources[i].Name < sources[j].Name
	})
	return sources, nil
}

func localBuildRequirements(roots []deploy.ArtifactRoot, sources []bundleBuildSource) ([]byte, error) {
	byName := map[string]string{}
	for _, source := range sources {
		byName[source.Name] = source.BuildDir
	}
	lines := localBuildRequirementLines(sources)
	for _, root := range roots {
		if root.Provider != python.ProviderName {
			return nil, fmt.Errorf("cannot project %s bundle root into requirements.txt", root.Provider)
		}
		line := root.Source
		if name := python.RootPackageName(root); name != "" {
			if sourcePath, ok := byName[python.NormalizeRequirementName(name)]; ok {
				line = sourcePath
			}
		}
		lines = append(lines, line)
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func localBuildRequirementLines(sources []bundleBuildSource) []string {
	lines := []string{}
	seen := map[string]bool{}
	for _, source := range sources {
		for _, requirement := range source.BuildRequirements {
			if seen[requirement] {
				continue
			}
			lines = append(lines, requirement)
			seen[requirement] = true
		}
	}
	return lines
}

func localSourceBuildRequirements(sourceDir string) ([]string, error) {
	content, err := os.ReadFile(filepath.Join(sourceDir, "pyproject.toml"))
	if err != nil {
		return nil, err
	}
	inBuildSystem := false
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inBuildSystem = trimmed == "[build-system]"
			continue
		}
		if !inBuildSystem || !strings.HasPrefix(trimmed, "requires") {
			continue
		}
		_, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		return parseInlineStringArray(value), nil
	}
	return nil, nil
}

func parseInlineStringArray(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	requirements := []string{}
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		field = strings.Trim(field, `"'`)
		if field != "" {
			requirements = append(requirements, field)
		}
	}
	return requirements
}

func localSourcePrepareScript(sources []bundleBuildSource, pypiOnly bool) string {
	commands := []string{
		"set -eu",
		"rm -rf /wheelhouse/.source",
		"mkdir -p /wheelhouse/.source",
	}
	for _, source := range sources {
		commands = append(commands, "cp -a "+shellQuote(source.ContainerDir)+" "+shellQuote(source.BuildDir))
	}
	commands = append(commands, shellCommand(python.PrepareWheelhouseArgv(pypiOnly)))
	return strings.Join(commands, "\n")
}

func shellCommand(args []string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func requireBundleCheckInputs(dir string, bundleDir string) error {
	if _, err := os.Stat(filepath.Join(dir, RequirementsFileName)); err != nil {
		return err
	}
	info, err := os.Stat(bundleDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("bundle path is not a directory: %s", bundleDir)
	}
	return nil
}

func requireBundlePrepareInputs(dir string, bundleDir string) error {
	if _, err := os.Stat(filepath.Join(dir, RequirementsFileName)); err != nil {
		return err
	}
	info, err := os.Stat(bundleDir)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("bundle path is not a directory: %s", bundleDir)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func copyWheelhouse(sourceDir string, targetDir string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".whl") {
			continue
		}
		if _, err := copyFileIfDifferent(filepath.Join(sourceDir, entry.Name()), filepath.Join(targetDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func replaceWheelhouse(sourceDir string, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".whl") {
			continue
		}
		if err := os.Remove(filepath.Join(targetDir, entry.Name())); err != nil {
			return err
		}
	}
	return copyWheelhouse(sourceDir, targetDir)
}

func singleWheelInDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	wheels := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".whl") {
			continue
		}
		wheels = append(wheels, filepath.Join(dir, entry.Name()))
	}
	if len(wheels) != 1 {
		return "", fmt.Errorf("expected one wheel, got %d", len(wheels))
	}
	return wheels[0], nil
}

func sortBundleResolvedPackages(packages []BundleResolvedPackage) {
	sort.Slice(packages, func(i int, j int) bool {
		if packages[i].Kind != packages[j].Kind {
			return packages[i].Kind < packages[j].Kind
		}
		return packages[i].Requirement < packages[j].Requirement
	})
}

func BundleOptions(options BundleListOptions) ([]BundleOption, error) {
	if options.Dir == "" {
		options.Dir = DefaultDeploymentDir
	}
	_, bundleOptions, err := loadBundleOptionsWithPack(options.Dir)
	return bundleOptions, err
}

func syncBundleState(dir string, state deploy.DeploymentState) ([]UpdateResult, error) {
	state.Bundle.PreparedFingerprint = ""
	stateContent, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	stateContent = append(stateContent, '\n')
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return nil, err
	}
	requirements, err := runtimeRequirementsContent(pack, state.Bundle.Roots)
	if err != nil {
		return nil, err
	}
	results := []UpdateResult{}
	stateStatus, err := deploy.WriteFileIfChanged(filepath.Join(dir, StateFileName), stateContent, 0o644)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(dir, StateFileName), Status: stateStatus, Ownership: "state", Reason: "recorded selected bundle roots"})
	requirementsStatus, err := deploy.WriteFileIfChanged(filepath.Join(dir, RequirementsFileName), requirements, 0o644)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(dir, RequirementsFileName), Status: requirementsStatus, Ownership: "local", Reason: "projected selected bundle roots for Docker runtime"})
	manifest, err := loadManifestOrNew(dir)
	if err != nil {
		return nil, err
	}
	dockerIdentity, err := deploymentDockerIdentity(pack, state, dir)
	if err != nil {
		return nil, err
	}
	composeResult, err := writeRuntimeCompose(dir, pack, state.Bundle.Roots, dockerIdentity)
	if err != nil {
		return nil, err
	}
	results = append(results, composeResult)
	manifestStatus, err := deploy.WriteDeploymentManifestIfChanged(filepath.Join(dir, ManifestFileName), manifest)
	if err != nil {
		return nil, err
	}
	results = append(results, UpdateResult{Path: filepath.Join(dir, ManifestFileName), Status: manifestStatus, Ownership: "state", Reason: "recorded generated file hashes"})
	return results, nil
}

func bundlePrepared(dir string) (bool, error) {
	state, err := loadState(dir)
	if err != nil {
		return false, err
	}
	state, err = withInferredBundleState(dir, state)
	if err != nil {
		return false, err
	}
	if state.Bundle.PreparedFingerprint == "" || state.Bundle.PreparedFingerprint != bundlePreparedFingerprint(state) {
		return false, nil
	}
	bundleDir, err := deploymentBundleDir(dir)
	if err != nil {
		return false, err
	}
	return bundleDirHasWheels(bundleDir)
}

func bundleDirHasWheels(bundleDir string) (bool, error) {
	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".whl") {
			return true, nil
		}
	}
	return false, nil
}

func markBundlePrepared(dir string) error {
	state, err := loadState(dir)
	if err != nil {
		return err
	}
	state, err = withInferredBundleState(dir, state)
	if err != nil {
		return err
	}
	state.Bundle.PreparedFingerprint = bundlePreparedFingerprint(state)
	_, err = writeDeploymentStateIfChanged(dir, state)
	return err
}

func markBundleUnprepared(dir string) (deploy.UpdateStatus, error) {
	state, err := loadState(dir)
	if err != nil {
		return "", err
	}
	state.Bundle.PreparedFingerprint = ""
	return writeDeploymentStateIfChanged(dir, state)
}

func writeDeploymentStateIfChanged(dir string, state deploy.DeploymentState) (deploy.UpdateStatus, error) {
	content, err := marshalState(state)
	if err != nil {
		return "", err
	}
	return deploy.WriteFileIfChanged(filepath.Join(dir, StateFileName), content, 0o644)
}

func bundlePreparedFingerprint(state deploy.DeploymentState) string {
	input := struct {
		Blueprint             deploy.PackRef               `json:"blueprint"`
		RequestedBlueprintRef string                       `json:"requested_blueprint_ref,omitempty"`
		ResolvedArtifact      *deploy.ResolvedPackArtifact `json:"resolved_artifact,omitempty"`
		Roots                 []deploy.ArtifactRoot        `json:"roots,omitempty"`
	}{
		Blueprint:             state.Blueprint,
		RequestedBlueprintRef: state.RequestedBlueprintRef,
		ResolvedArtifact:      state.ResolvedArtifact,
		Roots:                 state.Bundle.Roots,
	}
	content, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum[:])
}

func validateBundleRequirementsProjection(dir string, state deploy.DeploymentState) error {
	requirements, err := runtimeRequirementsContentForState(dir, state)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, RequirementsFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("bundle requirements projection is missing: %s; run %s", path, stageUpdateCommand(dir))
		}
		return err
	}
	if string(content) != string(requirements) {
		return fmt.Errorf("bundle requirements projection is out of date: %s; run %s", path, stageUpdateCommand(dir))
	}
	return nil
}

func stageUpdateCommand(dir string) string {
	return "reploy stage --update --dir " + shellQuote(dir)
}

func deploymentBundleDir(dir string) (string, error) {
	values, err := readDockerEnv(dir)
	if err != nil {
		return "", err
	}
	bundlePath := envValue(values, "REPLOY_BUNDLE_DIR", "./bundle")
	if filepath.IsAbs(bundlePath) {
		return bundlePath, nil
	}
	return filepath.Join(dir, bundlePath), nil
}

func deploymentRuntimeDir(dir string) (string, error) {
	values, err := readDockerEnv(dir)
	if err != nil {
		return "", err
	}
	runtimePath := envValue(values, "REPLOY_RUNTIME_DIR", "./"+RuntimeDirName)
	if filepath.IsAbs(runtimePath) {
		return runtimePath, nil
	}
	return filepath.Join(dir, runtimePath), nil
}

func deploymentRuntimePythonVenvDir(dir string) (string, error) {
	runtimeDir, err := deploymentRuntimeDir(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(runtimeDir, "python-venv"), nil
}

func copyFileIfDifferent(sourcePath string, targetPath string) (deploy.UpdateStatus, error) {
	sourceHash, err := deploy.HashFile(sourcePath)
	if err != nil {
		return "", err
	}
	targetHash, err := deploy.HashFile(targetPath)
	if err == nil && targetHash == sourceHash {
		return deploy.UpdateStatusUpToDate, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return "", err
	}
	if err := target.Close(); err != nil {
		return "", err
	}
	return deploy.UpdateStatusUpdated, nil
}

func commandLine(spec CommandSpec) string {
	parts := append([]string{spec.Name}, spec.Args...)
	for index, part := range parts {
		parts[index] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n'\"\\$`;&|<>*?()[]{}!") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func resolveBundleOptionRoot(dir string, name string) (deploy.ArtifactRoot, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return deploy.ArtifactRoot{}, fmt.Errorf("bundle option name must not be empty")
	}
	pack, options, err := loadBundleOptionsWithPack(dir)
	if err != nil {
		return deploy.ArtifactRoot{}, err
	}
	for _, option := range options {
		if option.Name != name {
			continue
		}
		return providerIdentifierRoot(pack.App.Provider.Type, option.Identifier)
	}
	return deploy.ArtifactRoot{}, unknownBundleOptionError(name, options)
}

func unknownBundleOptionError(name string, options []BundleOption) error {
	names := make([]string, 0, len(options))
	var suggestion string
	for _, option := range options {
		names = append(names, option.Name)
		if suggestion == "" && editDistanceAtMostOne(strings.ToLower(name), strings.ToLower(option.Name)) {
			suggestion = option.Name
		}
	}
	sort.Strings(names)
	if suggestion != "" {
		return fmt.Errorf("unknown bundle option %q\ndid you mean %q?\nuse --extra to add %q as an explicit bundle root", name, suggestion, name)
	}
	if len(names) == 0 {
		return fmt.Errorf("unknown bundle option %q\nthis blueprint does not declare bundle options\nuse --extra to add it as an explicit bundle root", name)
	}
	return fmt.Errorf("unknown bundle option %q\nuse one of:\n  %s\nuse --extra to add it as an explicit bundle root", name, strings.Join(names, "\n  "))
}

func editDistanceAtMostOne(left string, right string) bool {
	if left == right {
		return false
	}
	if len(left) > len(right)+1 || len(right) > len(left)+1 {
		return false
	}
	mismatches := 0
	for len(left) > 0 && len(right) > 0 {
		if left[0] == right[0] {
			left = left[1:]
			right = right[1:]
			continue
		}
		mismatches++
		if mismatches > 1 {
			return false
		}
		switch {
		case len(left) > len(right):
			left = left[1:]
		case len(right) > len(left):
			right = right[1:]
		default:
			left = left[1:]
			right = right[1:]
		}
	}
	return mismatches+len(left)+len(right) <= 1
}

func loadBundleOptionsWithPack(dir string) (deploy.AppPack, []BundleOption, error) {
	state, err := loadState(dir)
	if err != nil {
		return deploy.AppPack{}, nil, err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return deploy.AppPack{}, nil, err
	}
	return pack, bundleOptionsFromPack(pack), nil
}

func bundleOptionsFromPack(pack deploy.AppPack) []BundleOption {
	names := make([]string, 0, len(pack.Bundle.Options))
	for name := range pack.Bundle.Options {
		names = append(names, name)
	}
	sort.Strings(names)
	options := make([]BundleOption, 0, len(names))
	for _, name := range names {
		option := pack.Bundle.Options[name]
		options = append(options, BundleOption{
			Name:        name,
			Identifier:  option.Identifier,
			Group:       option.Group,
			Description: option.Description,
		})
	}
	return options
}

func providerIdentifierRoot(providerType string, identifier string) (deploy.ArtifactRoot, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return deploy.ArtifactRoot{}, fmt.Errorf("bundle option identifier must not be empty")
	}
	switch providerType {
	case python.ProviderName:
		return python.PackageRoot(identifier), nil
	case "":
		return deploy.ArtifactRoot{}, fmt.Errorf("blueprint app.provider.type is required for bundle option identifiers")
	default:
		return deploy.ArtifactRoot{}, fmt.Errorf("provider %q does not support bundle option identifiers", providerType)
	}
}

func bundleRootPackageName(root deploy.ArtifactRoot) string {
	return python.RootPackageName(root)
}

func classifyBundleRoot(source string) (deploy.ArtifactRoot, error) {
	return python.ClassifyRoot(source)
}

func bundleRootsFromRequirements(content []byte, validate bool) ([]deploy.ArtifactRoot, error) {
	roots := []deploy.ArtifactRoot{}
	for _, line := range strings.Split(string(content), "\n") {
		root := strings.TrimSpace(line)
		if root == "" || strings.HasPrefix(root, "#") {
			continue
		}
		var artifactRoot deploy.ArtifactRoot
		var err error
		if validate {
			artifactRoot, err = classifyBundleRoot(root)
		} else {
			artifactRoot = classifyPackBundleRoot(root)
		}
		if err != nil {
			return nil, err
		}
		roots = append(roots, artifactRoot)
	}
	return roots, nil
}

func classifyPackBundleRoot(source string) deploy.ArtifactRoot {
	return python.ClassifyPackRoot(source)
}

func requirementsContentFromRoots(roots []deploy.ArtifactRoot) ([]byte, error) {
	lines := make([]string, 0, len(roots))
	for _, root := range roots {
		if root.Provider != python.ProviderName {
			return nil, fmt.Errorf("cannot project %s bundle root into requirements.txt", root.Provider)
		}
		if strings.TrimSpace(root.Source) == "" {
			return nil, fmt.Errorf("bundle root source must not be empty")
		}
		lines = append(lines, root.Source)
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func runtimeRequirementsContentForState(dir string, state deploy.DeploymentState) ([]byte, error) {
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return nil, err
	}
	return runtimeRequirementsContent(pack, state.Bundle.Roots)
}

func runtimeRequirementsContent(pack deploy.AppPack, roots []deploy.ArtifactRoot) ([]byte, error) {
	sourceByName := map[string]string{}
	sources, err := selectedPackLocalSources(pack, roots, localSourceContainerRoot())
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		sourceByName[source.Name] = source.ContainerDir
	}
	lines := localBuildRequirementLines(sources)
	for _, root := range roots {
		if root.Provider != python.ProviderName {
			return nil, fmt.Errorf("cannot project %s bundle root into requirements.txt", root.Provider)
		}
		if strings.TrimSpace(root.Source) == "" {
			return nil, fmt.Errorf("bundle root source must not be empty")
		}
		lines = append(lines, root.Source)
		if name := python.RootPackageName(root); name != "" {
			if sourcePath, ok := sourceByName[python.NormalizeRequirementName(name)]; ok {
				lines = append(lines, sourcePath)
			}
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func inferBundleState(dir string, pack deploy.AppPack) (deploy.BundleState, error) {
	requirementsPath := filepath.Join(dir, RequirementsFileName)
	content, err := os.ReadFile(requirementsPath)
	if err == nil {
		roots, err := bundleRootsFromRequirements(content, false)
		if err != nil {
			return deploy.BundleState{}, err
		}
		return deploy.BundleState{Roots: roots}, nil
	}
	if !os.IsNotExist(err) {
		return deploy.BundleState{}, err
	}
	roots, err := initBundleRoots(pack, nil)
	if err != nil {
		return deploy.BundleState{}, err
	}
	return deploy.BundleState{Roots: roots}, nil
}

func withInferredBundleState(dir string, state deploy.DeploymentState) (deploy.DeploymentState, error) {
	if len(state.Bundle.Roots) > 0 {
		return state, nil
	}
	content, err := os.ReadFile(filepath.Join(dir, RequirementsFileName))
	if err == nil {
		roots, err := bundleRootsFromRequirements(content, false)
		if err != nil {
			return deploy.DeploymentState{}, err
		}
		state.Bundle.Roots = roots
		return state, nil
	}
	if !os.IsNotExist(err) {
		return deploy.DeploymentState{}, err
	}
	pack, err := deploy.LoadResolvedPack(state.Blueprint, state.RequestedBlueprintRef, state.ResolvedArtifact)
	if err != nil {
		return deploy.DeploymentState{}, err
	}
	bundle, err := inferBundleState(dir, pack)
	if err != nil {
		return deploy.DeploymentState{}, err
	}
	state.Bundle = bundle
	return state, nil
}
