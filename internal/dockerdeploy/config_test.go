package dockerdeploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestConfigCheckCommand(t *testing.T) {
	dir := filepath.Join("tmp", "deployment")
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	spec := ConfigCheckCommand(dir, "config_check", nil)
	want := []string{
		"compose",
		"--project-directory",
		absoluteDir,
		"--env-file",
		filepath.Join(absoluteDir, DockerEnvFileName),
		"-f",
		filepath.Join(absoluteDir, ComposeFileName),
		"run",
		"--rm",
		"--no-deps",
		"-e",
		"REPLOY_CONTAINER_COMMAND=config_check",
		"-e",
		"REPLOY_FORWARDED_ARGC=0",
		"app",
	}
	if spec.Name != "docker" {
		t.Fatalf("name = %q", spec.Name)
	}
	if !reflect.DeepEqual(spec.Args, want) {
		t.Fatalf("args = %#v\nwant %#v", spec.Args, want)
	}
	if spec.Dir != absoluteDir {
		t.Fatalf("dir = %q", spec.Dir)
	}
}

func TestConfigCheckCommandForwardsArgsThroughEnv(t *testing.T) {
	spec := ConfigCheckCommand("deployment", "config_check", []string{"--profile=full", "--profile", "quick"})
	for _, pair := range [][2]string{
		{"-e", "REPLOY_FORWARDED_ARGC=3"},
		{"-e", "REPLOY_FORWARDED_ARG_0=--profile=full"},
		{"-e", "REPLOY_FORWARDED_ARG_1=--profile"},
		{"-e", "REPLOY_FORWARDED_ARG_2=quick"},
	} {
		if !containsAdjacent(spec.Args, pair[0], pair[1]) {
			t.Fatalf("args did not include env %s: %#v", pair[1], spec.Args)
		}
	}
}

func TestConfigCheckCommandUsesTemporaryProject(t *testing.T) {
	spec := ConfigCheckCommandForProject("deployment", "config_check", nil, "reploy-config-check-test")
	if !containsAdjacent(spec.Args, "--project-name", "reploy-config-check-test") {
		t.Fatalf("args did not include project name: %#v", spec.Args)
	}
	if !containsInOrder(spec.Args, []string{"run", "--rm", "--no-deps"}) {
		t.Fatalf("args did not include config check run sequence: %#v", spec.Args)
	}
	if !containsEnvValue(spec.Env, "COMPOSE_PROGRESS=quiet") || !containsEnvValue(spec.Env, "COMPOSE_ANSI=never") {
		t.Fatalf("env did not quiet temporary compose output: %#v", spec.Env)
	}
}

func TestAppCommandUsesWritableConfigAndNoRuntimeOverrides(t *testing.T) {
	spec := AppCommandForProject("deployment", "bootstrap_plugin", []string{"imap", "account", "primary"}, "reploy-app-command-test", "deployment/conf", "/conf")
	if !containsAdjacent(spec.Args, "--project-name", "reploy-app-command-test") {
		t.Fatalf("args did not include project name: %#v", spec.Args)
	}
	for _, pair := range [][2]string{
		{"-e", "REPLOY_CONTAINER_COMMAND=bootstrap_plugin"},
		{"-e", "REPLOY_INCLUDE_RUNTIME_OVERRIDES=0"},
		{"-e", "REPLOY_FORWARDED_ARGC=3"},
		{"-e", "REPLOY_FORWARDED_ARG_0=imap"},
		{"-e", "REPLOY_FORWARDED_ARG_1=account"},
		{"-e", "REPLOY_FORWARDED_ARG_2=primary"},
		{"-e", "REPLOY_CONFIG_CONTAINER_DIR=/conf"},
		{"-e", "REPLOY_CONFIG_DISPLAY_DIR=deployment/conf"},
		{"-e", "REPLOY_APP_COMMAND_PREFIX=reploy app"},
	} {
		if !containsAdjacent(spec.Args, pair[0], pair[1]) {
			t.Fatalf("args did not include env %s: %#v", pair[1], spec.Args)
		}
	}
	for _, expected := range []string{
		"COMPOSE_PROGRESS=quiet",
		"REPLOY_INCLUDE_RUNTIME_OVERRIDES=0",
		"REPLOY_CONFIG_MOUNT=rw",
	} {
		if !containsEnvValue(spec.Env, expected) {
			t.Fatalf("env missing %s: %#v", expected, spec.Env)
		}
	}
	if containsEnvValue(spec.Env, "COMPOSE_ANSI=never") {
		t.Fatalf("app command env should preserve app ANSI output: %#v", spec.Env)
	}
}

func TestTemporaryComposeCleanupCommand(t *testing.T) {
	spec := TemporaryComposeCleanupCommand("deployment", "reploy-config-check-test")
	if !containsAdjacent(spec.Args, "--project-name", "reploy-config-check-test") {
		t.Fatalf("args did not include project name: %#v", spec.Args)
	}
	if !containsInOrder(spec.Args, []string{"down", "--remove-orphans", "--volumes", "--timeout", "0"}) {
		t.Fatalf("args did not include cleanup sequence: %#v", spec.Args)
	}
	if !containsEnvValue(spec.Env, "COMPOSE_PROGRESS=quiet") || !containsEnvValue(spec.Env, "COMPOSE_ANSI=never") {
		t.Fatalf("env did not quiet cleanup output: %#v", spec.Env)
	}
}

func TestAppCommandRunsOneOffOnDeploymentProject(t *testing.T) {
	deployDir := makeAppCommandDeployment(t)
	var stdout strings.Builder
	var specs []CommandSpec
	var runOptions []RunOptions
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		runOptions = append(runOptions, options)
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"bootstrap", "plugin", "imap", "account", "primary"}, Stdout: &stdout})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("ran %d commands, want one-off run", len(specs))
	}
	if runOptions[0].Context == nil {
		t.Fatalf("one-off run was not cancellable")
	}
	if runOptions[0].Stdin != nil {
		t.Fatalf("captured app command should not attach stdin: %#v", runOptions[0].Stdin)
	}
	projectName := mustDeploymentComposeProjectName(t, deployDir)
	if !containsAdjacent(specs[0].Args, "--project-name", projectName) {
		t.Fatalf("app command should use deployment project %q, got args: %#v", projectName, specs[0].Args)
	}
	if !containsInOrder(specs[0].Args, []string{"-e", "REPLOY_CONTAINER_COMMAND=bootstrap_plugin"}) {
		t.Fatalf("first command did not select bootstrap_plugin: %#v", specs[0].Args)
	}
	if !containsInOrder(specs[0].Args, []string{"-e", "REPLOY_FORWARDED_ARG_0=imap"}) {
		t.Fatalf("first command did not forward app args: %#v", specs[0].Args)
	}
	if !containsAdjacent(specs[0].Args, "-e", "REPLOY_INCLUDE_RUNTIME_OVERRIDES=0") {
		t.Fatalf("first command did not disable runtime overrides inside container: %#v", specs[0].Args)
	}
	if !containsAdjacent(specs[0].Args, "-e", "REPLOY_CONFIG_CONTAINER_DIR=/conf") {
		t.Fatalf("first command did not set config container dir: %#v", specs[0].Args)
	}
	if !containsAdjacent(specs[0].Args, "-e", "REPLOY_APP_COMMAND_PREFIX=reploy app") {
		t.Fatalf("first command did not set app command prefix: %#v", specs[0].Args)
	}
	expectedDisplayDir := filepath.Join(deployDir, "conf")
	if !containsAdjacent(specs[0].Args, "-e", "REPLOY_CONFIG_DISPLAY_DIR="+expectedDisplayDir) {
		t.Fatalf("first command did not set config display dir %q: %#v", expectedDisplayDir, specs[0].Args)
	}
	for _, expected := range []string{"COMPOSE_PROGRESS=quiet", "REPLOY_INCLUDE_RUNTIME_OVERRIDES=0"} {
		if !containsEnvValue(specs[0].Env, expected) {
			t.Fatalf("first command env missing %s: %#v", expected, specs[0].Env)
		}
	}
	if containsEnvValue(specs[0].Env, "COMPOSE_ANSI=never") {
		t.Fatalf("app command env should preserve app ANSI output: %#v", specs[0].Env)
	}
}

func TestAppCommandUsesManagedFileRootWhenSingleFileArtifactIsMounted(t *testing.T) {
	deployDir := makeSingleFileConfigAppCommandDeployment(t)
	if err := os.WriteFile(filepath.Join(deployDir, ".arbiter.env"), []byte("ARB=my-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var specs []CommandSpec
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"bootstrap", "plugin", "imap"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("ran %d commands, want one-off run", len(specs))
	}
	if !containsAdjacent(specs[0].Args, "-e", "REPLOY_CONFIG_CONTAINER_DIR=/conf") {
		t.Fatalf("app command did not set nested config container dir: %#v", specs[0].Args)
	}
}

func TestAppCommandCreatesMissingManagedFilePlaceholder(t *testing.T) {
	deployDir := makeSingleFileConfigAppCommandDeployment(t)
	called := false
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		called = true
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"bootstrap", "plugin", "imap"}})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("app command did not invoke Docker after creating managed file placeholder")
	}
	path := filepath.Join(deployDir, ".arbiter.env")
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("app command did not create .arbiter.env placeholder: %v", statErr)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf(".arbiter.env placeholder is not a regular file: %s", info.Mode())
	}
	if info.Size() != 0 {
		t.Fatalf(".arbiter.env placeholder should start empty, size=%d", info.Size())
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf(".arbiter.env placeholder mode = %s, want 0600", mode)
	}
}

func TestAppCommandDoesNotCreateManagedFilePlaceholderForUnknownCommand(t *testing.T) {
	deployDir := makeSingleFileConfigAppCommandDeployment(t)
	called := false
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		called = true
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"not-a-command"}})
	if err == nil {
		t.Fatal("expected unknown app command error")
	}
	if called {
		t.Fatal("app command invoked Docker for unknown command")
	}
	if !strings.Contains(err.Error(), "no app command matches") {
		t.Fatalf("error did not explain unknown command: %v", err)
	}
	if info, statErr := os.Stat(filepath.Join(deployDir, ".arbiter.env")); !os.IsNotExist(statErr) || info != nil {
		t.Fatalf("unknown command should not create .arbiter.env: info=%v err=%v", info, statErr)
	}
}

func TestAppCommandRecreatesRequiredWritableDirs(t *testing.T) {
	deployDir := makeAppCommandDeployment(t)
	if err := os.RemoveAll(filepath.Join(deployDir, "conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(deployDir, RuntimeDirName)); err != nil {
		t.Fatal(err)
	}
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"bootstrap", "plugin", "imap"}})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(deployDir, "conf")); err != nil || !info.IsDir() {
		t.Fatalf("app command did not recreate config dir: info=%v err=%v", info, err)
	}
	if info, err := os.Stat(filepath.Join(deployDir, RuntimeDirName)); err != nil || !info.IsDir() {
		t.Fatalf("app command did not recreate runtime dir: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(deployDir, "conf", ".env")); !os.IsNotExist(err) {
		t.Fatalf("app command created app env file: %v", err)
	}
}

func TestAppCommandRepairsUnwritableRuntimeDir(t *testing.T) {
	deployDir := makeAppCommandDeployment(t)
	runtimeDir := filepath.Join(deployDir, RuntimeDirName)
	if err := os.Chmod(runtimeDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(runtimeDir, 0o755)
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"bootstrap", "plugin", "imap"}})
	if err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(runtimeDir, "probe")
	if err := os.WriteFile(probe, []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("runtime dir was not repaired: %v", err)
	}
}

func TestAppCommandListShowsPackDeclaredAppCommands(t *testing.T) {
	deployDir := makeAppCommandDeployment(t)
	result, err := AppCommandList(AppCommandListOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	if result.AppID != "demo" {
		t.Fatalf("app id = %q", result.AppID)
	}
	if strings.Join(result.Commands, "\n") != "bootstrap plugin\nconfig check" {
		t.Fatalf("commands = %#v", result.Commands)
	}
}

func TestAppCommandRunsConfigCheck(t *testing.T) {
	deployDir := makeAppCommandDeployment(t)
	var specs []CommandSpec
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"config", "check", "--live"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("ran %d commands, want one-off run", len(specs))
	}
	if !containsInOrder(specs[0].Args, []string{"-e", "REPLOY_CONTAINER_COMMAND=config_check"}) {
		t.Fatalf("first command did not select config_check: %#v", specs[0].Args)
	}
	if !containsInOrder(specs[0].Args, []string{"-e", "REPLOY_FORWARDED_ARG_0=--live"}) {
		t.Fatalf("first command did not forward --live: %#v", specs[0].Args)
	}
}

func TestAppCommandPrefixesStagedOutput(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	deployDir := makeAppCommandDeployment(t)
	var stdout strings.Builder
	var stderr strings.Builder
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		if _, err := options.Stdout.Write([]byte("app out\n")); err != nil {
			return err
		}
		if _, err := options.Stderr.Write([]byte("app err\n")); err != nil {
			return err
		}
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"config", "check"}, Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "[STAGING : demo] app out\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.String() != "[STAGING : demo] app err\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAppCommandPassesPackDeclaredColorEnv(t *testing.T) {
	deployDir := makeAppCommandDeployment(t)
	t.Setenv("REPLOY_COLOR", "always")
	unsetEnv(t, "DEMO_COLOR")
	var specs []CommandSpec
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"config", "check"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("ran %d commands, want one-off run", len(specs))
	}
	if !containsAdjacent(specs[0].Args, "-e", "DEMO_COLOR=always") {
		t.Fatalf("first command did not pass blueprint-declared color env: %#v", specs[0].Args)
	}
}

func TestTerminalLooksColorCapableOnWindowsWithoutTerm(t *testing.T) {
	oldGOOS := colorRuntimeGOOS
	t.Cleanup(func() {
		colorRuntimeGOOS = oldGOOS
	})
	colorRuntimeGOOS = "windows"
	t.Setenv("TERM", "")
	if !terminalLooksColorCapable() {
		t.Fatal("Windows terminal should be color-capable without TERM")
	}
	t.Setenv("TERM", "dumb")
	if terminalLooksColorCapable() {
		t.Fatal("TERM=dumb should disable color even on Windows")
	}
}

func TestAppCommandHonorsExplicitPackDeclaredColorEnv(t *testing.T) {
	deployDir := makeAppCommandDeployment(t)
	t.Setenv("REPLOY_COLOR", "always")
	t.Setenv("DEMO_COLOR", "never")
	var specs []CommandSpec
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"config", "check"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("ran %d commands, want one-off run", len(specs))
	}
	if !containsAdjacent(specs[0].Args, "-e", "DEMO_COLOR=never") {
		t.Fatalf("first command did not preserve explicit blueprint-declared color env: %#v", specs[0].Args)
	}
}

func TestAppCommandPassesTerminalColumns(t *testing.T) {
	deployDir := makeAppCommandDeployment(t)
	t.Setenv("COLUMNS", "72")
	var specs []CommandSpec
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		return nil
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"config", "check"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("ran %d commands, want one-off run", len(specs))
	}
	if !containsAdjacent(specs[0].Args, "-e", "COLUMNS=72") {
		t.Fatalf("first command did not pass terminal columns: %#v", specs[0].Args)
	}
}

func TestAppCommandReportsAppCommandFailure(t *testing.T) {
	deployDir := makeAppCommandDeployment(t)
	restore := stubAppCommandRunner(func(spec CommandSpec, options RunOptions) error {
		return fmt.Errorf("docker failed: exit status 2")
	})
	defer restore()

	err := AppCommand(AppCommandOptions{Dir: deployDir, CommandArgs: []string{"config", "check"}})
	if err == nil {
		t.Fatal("expected app command error")
	}
	if err.Error() != "app command failed: exit status 2" {
		t.Fatalf("error = %q", err)
	}
}

func TestConfigCheckRunsOneOffOnDeploymentProject(t *testing.T) {
	deployDir := makeConfigCheckDeployment(t)
	var specs []CommandSpec
	restore := stubConfigCheckRunner(func(spec CommandSpec, options RunOptions) error {
		specs = append(specs, spec)
		return nil
	})
	defer restore()

	err := ConfigCheck(ConfigCheckOptions{Dir: deployDir, CommandArgs: []string{"config", "check", "--live"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("ran %d commands, want one-off run", len(specs))
	}
	projectName := mustDeploymentComposeProjectName(t, deployDir)
	if !containsAdjacent(specs[0].Args, "--project-name", projectName) {
		t.Fatalf("config check should use deployment project %q, got args: %#v", projectName, specs[0].Args)
	}
	if !containsInOrder(specs[0].Args, []string{"run", "--rm", "--no-deps"}) {
		t.Fatalf("first command was not config check run: %#v", specs[0].Args)
	}
	if !containsInOrder(specs[0].Args, []string{"-e", "REPLOY_FORWARDED_ARG_0=--live"}) {
		t.Fatalf("first command did not forward --live: %#v", specs[0].Args)
	}
	expectedDisplayDir := filepath.Join(deployDir, "conf")
	if !containsAdjacent(specs[0].Args, "-e", "REPLOY_CONFIG_DISPLAY_DIR="+expectedDisplayDir) {
		t.Fatalf("first command did not set config display dir %q: %#v", expectedDisplayDir, specs[0].Args)
	}
}

func TestTemporaryComposeCommandReportsCleanupFailure(t *testing.T) {
	err := runTemporaryComposeCommand(
		func(spec CommandSpec, options RunOptions) error {
			if spec.Name == "cleanup" {
				return errors.New("cleanup boom")
			}
			return errors.New("check boom")
		},
		CommandSpec{Name: "run"},
		CommandSpec{Name: "cleanup"},
		RunOptions{},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "check boom") || !strings.Contains(err.Error(), "cleanup failed: cleanup boom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func makeConfigCheckDeployment(t *testing.T) string {
	t.Helper()
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	markTestBundlePrepared(t, deployDir)
	return deployDir
}

func makeAppCommandDeployment(t *testing.T) string {
	t.Helper()
	manifest := strings.Replace(
		testPackManifest(),
		"    config_check:\n      trigger:\n        - config\n        - check\n      forward_flags:\n        - --live\n      container:\n        argv:\n          - demo-server\n          - --config-dir\n          - /conf\n          - --config-name\n          - ${DEMO_CONFIG_NAME}\n          - config\n          - check\n",
		"    config_check:\n      trigger:\n        - config\n        - check\n      app_command: true\n      forward_flags:\n        - --live\n      container:\n        argv:\n          - demo-server\n          - --config-dir\n          - /conf\n          - --config-name\n          - ${DEMO_CONFIG_NAME}\n          - config\n          - check\n    bootstrap_plugin:\n      trigger:\n        - bootstrap\n        - plugin\n      app_command: true\n      forward_args: true\n      container:\n        argv:\n          - demo-server\n          - --config-dir\n          - /conf\n          - --config-name\n          - ${DEMO_CONFIG_NAME}\n          - bootstrap\n          - plugin\n",
		1,
	)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	markTestBundlePrepared(t, deployDir)
	return deployDir
}

func makeSingleFileConfigAppCommandDeployment(t *testing.T) string {
	t.Helper()
	manifest := strings.Replace(
		testPackManifestWithManagedFile(),
		"    config_check:\n      trigger:\n        - config\n        - check\n      forward_flags:\n        - --live\n      container:\n        argv:\n          - demo-server\n          - --config-dir\n          - /conf\n          - --config-name\n          - ${DEMO_CONFIG_NAME}\n          - config\n          - check\n",
		"    config_check:\n      trigger:\n        - config\n        - check\n      app_command: true\n      deployed_command: true\n      forward_flags:\n        - --live\n      container:\n        argv:\n          - demo-server\n          - --config-dir\n          - /conf\n          - --config-name\n          - ${DEMO_CONFIG_NAME}\n          - config\n          - check\n    bootstrap_plugin:\n      trigger:\n        - bootstrap\n        - plugin\n      app_command: true\n      deployed_command: true\n      forward_args: true\n      container:\n        argv:\n          - demo-server\n          - --config-dir\n          - /conf\n          - --config-name\n          - ${DEMO_CONFIG_NAME}\n          - bootstrap\n          - plugin\n",
		1,
	)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	markTestBundlePrepared(t, deployDir)
	return deployDir
}

func stubConfigCheckRunner(run func(CommandSpec, RunOptions) error) func() {
	previous := runConfigCheckCommand
	runConfigCheckCommand = run
	return func() {
		runConfigCheckCommand = previous
	}
}

func stubAppCommandRunner(run func(CommandSpec, RunOptions) error) func() {
	previous := runAppCommand
	runAppCommand = run
	return func() {
		runAppCommand = previous
	}
}

func mustDeploymentComposeProjectName(t *testing.T, dir string) string {
	t.Helper()
	projectName, err := deploymentComposeProjectName(dir)
	if err != nil {
		t.Fatal(err)
	}
	if projectName == "" {
		t.Fatal("missing deployment compose project name")
	}
	return projectName
}

func containsEnvValue(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsValue(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsAdjacent(values []string, first string, second string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == first && values[index+1] == second {
			return true
		}
	}
	return false
}

func containsInOrder(values []string, sequence []string) bool {
	if len(sequence) == 0 {
		return true
	}
	for start := range values {
		if start+len(sequence) > len(values) {
			return false
		}
		matched := true
		for offset, value := range sequence {
			if values[start+offset] != value {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	value, ok := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}
