package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadPack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "arbiter.blueprint.yaml"), []byte(packTestManifest()), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ParsePackRef("file:" + dir)
	if err != nil {
		t.Fatal(err)
	}

	pack, err := LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if pack.AppID != "arbiter" {
		t.Fatalf("app id = %q", pack.AppID)
	}
	if pack.Docker.DeploymentDirs.Bundle != "bundle" {
		t.Fatalf("bundle dir = %q", pack.Docker.DeploymentDirs.Bundle)
	}
	if !filepath.IsAbs(pack.Ref.Source) || !strings.HasPrefix(pack.Ref.Raw, "file:/") {
		t.Fatalf("blueprint ref was not resolved to an absolute file ref: %#v", pack.Ref)
	}
}

func TestParsePackManifestReadsDockerLayout(t *testing.T) {
	manifest, err := ParsePackManifest(packTestManifest())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.App.Provider.Type != "python" {
		t.Fatalf("provider type = %q", manifest.App.Provider.Type)
	}
	if manifest.App.Provider.Identifier != "arbiter-server" {
		t.Fatalf("provider identifier = %q", manifest.App.Provider.Identifier)
	}
	if manifest.App.Provider.LocalSources["arbiter-server"] != "../../server" {
		t.Fatalf("local sources = %#v", manifest.App.Provider.LocalSources)
	}
	if manifest.Bundle.Options["imap"].Identifier != "arbiter-imap" {
		t.Fatalf("bundle options = %#v", manifest.Bundle.Options)
	}
	if manifest.App.Terminal.ColorEnv != "ARBITER_COLOR" {
		t.Fatalf("app terminal color env = %q", manifest.App.Terminal.ColorEnv)
	}
	if manifest.Pack.Schema != 1 || manifest.Pack.Version != "0.1.0" || manifest.Pack.RequiresReploy != ">=0.1.0" {
		t.Fatalf("pack metadata = %#v", manifest.Pack)
	}
	if got := manifest.Docker.DeploymentDirs.All(); strings.Join(got, ",") != "conf,bundle,data" {
		t.Fatalf("deployment dirs = %#v", got)
	}
	if manifest.Docker.DefaultCommand != "serve" {
		t.Fatalf("default command = %q", manifest.Docker.DefaultCommand)
	}
	command := dockerCommandByName(t, manifest, "config_check")
	if got := strings.Join(command.Trigger, " "); got != "config check" {
		t.Fatalf("trigger = %q", got)
	}
	if got := strings.Join(command.ForwardFlags, " "); got != "--live --profile" {
		t.Fatalf("forward flags = %q", got)
	}
	if !command.AppCommand {
		t.Fatal("config_check was not marked as app command")
	}
	if got := strings.Join(command.Container.Argv, " "); got != "arbiter-server --config-dir /config --config-name ${ARBITER_CONFIG_NAME} config check" {
		t.Fatalf("config check argv = %q", got)
	}
	serverBootstrap := dockerCommandByName(t, manifest, "bootstrap_server")
	if got := strings.Join(serverBootstrap.Trigger, " "); got != "bootstrap server" {
		t.Fatalf("server bootstrap trigger = %q", got)
	}
	if !serverBootstrap.AppCommand {
		t.Fatal("bootstrap_server was not marked as app command")
	}
	pluginBootstrap := dockerCommandByName(t, manifest, "bootstrap_plugin")
	if got := strings.Join(pluginBootstrap.Trigger, " "); got != "bootstrap plugin" {
		t.Fatalf("plugin bootstrap trigger = %q", got)
	}
	if !pluginBootstrap.AppCommand {
		t.Fatal("bootstrap_plugin was not marked as app command")
	}
	if !pluginBootstrap.ForwardArgs {
		t.Fatal("bootstrap_plugin did not allow forwarded args")
	}
	configActivate := dockerCommandByName(t, manifest, "config_activate")
	if got := strings.Join(configActivate.Trigger, " "); got != "config activate" {
		t.Fatalf("config activate trigger = %q", got)
	}
	if !configActivate.AppCommand {
		t.Fatal("config_activate was not marked as app command")
	}
	if !configActivate.ForwardArgs {
		t.Fatal("config_activate did not allow forwarded args")
	}
	configShow := dockerCommandByName(t, manifest, "config_show")
	if got := strings.Join(configShow.Trigger, " "); got != "config show" {
		t.Fatalf("config show trigger = %q", got)
	}
	if !configShow.AppCommand {
		t.Fatal("config_show was not marked as app command")
	}
	if !configShow.ForwardArgs {
		t.Fatal("config_show did not allow forwarded args")
	}
}

func TestParsePackManifestRequiresPackMetadata(t *testing.T) {
	_, err := ParsePackManifest(`app:
  id: demo

docker:
  deployment_dirs:
    config: conf
    bundle: bundle
    data: data
`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "missing blueprint.schema") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePackManifestRejectsUnsupportedSchema(t *testing.T) {
	_, err := ParsePackManifest(`blueprint:
  schema: 99
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: demo

docker:
  deployment_dirs:
    config: conf
    bundle: bundle
    data: data
`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported blueprint.schema") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePackManifestAllowsSimpleWheelPack(t *testing.T) {
	manifest, err := ParsePackManifest(`blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: demo
  provider:
    type: python
    identifier: demo

docker:
  deployment_dirs:
    config: conf
    bundle: bundle
    data: data
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - demo
          - serve
`)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.App.Provider.Identifier != "demo" {
		t.Fatalf("provider identifier = %q", manifest.App.Provider.Identifier)
	}
	if len(manifest.Bundle.Options) != 0 {
		t.Fatalf("bundle options = %#v", manifest.Bundle.Options)
	}
}

func TestParsePackManifestRejectsInvalidTerminalColorEnv(t *testing.T) {
	_, err := ParsePackManifest(`blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: demo
  provider:
    type: python
    identifier: demo
  terminal:
    color_env: not-an-env

docker:
  deployment_dirs:
    config: conf
    bundle: bundle
    data: data
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - demo
          - serve
`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "app.terminal.color_env") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePackManifestRejectsBundleOptionFieldBreaks(t *testing.T) {
	_, err := ParsePackManifest(`blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: demo
  provider:
    type: python
    identifier: demo

bundle:
  options:
    imap:
      identifier: "demo-imap	evil"
      group: plugins
      description: Receive email.

docker:
  deployment_dirs:
    config: conf
    bundle: bundle
    data: data
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - demo
          - serve
`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bundle.options.imap.identifier must not contain tabs or newlines") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func dockerCommandByName(t *testing.T, manifest PackManifest, name string) DockerCommandConfig {
	t.Helper()
	for _, command := range manifest.Docker.Commands {
		if command.Name == name {
			return command
		}
	}
	t.Fatalf("missing docker command %q in %#v", name, manifest.Docker.Commands)
	return DockerCommandConfig{}
}

func TestDockerCommandMatchingForwardsDeclaredFlags(t *testing.T) {
	manifest, err := ParsePackManifest(packTestManifest())
	if err != nil {
		t.Fatal(err)
	}
	command, forwarded, err := manifest.Docker.MatchCommand([]string{"config", "check", "--live", "--profile=full", "--profile", "quick"})
	if err != nil {
		t.Fatal(err)
	}
	if command.Name != "config_check" {
		t.Fatalf("command = %q", command.Name)
	}
	if got := strings.Join(forwarded, " "); got != "--live --profile=full --profile quick" {
		t.Fatalf("forwarded args = %q", got)
	}
}

func TestDockerCommandMatchingRejectsUndeclaredFlags(t *testing.T) {
	manifest, err := ParsePackManifest(packTestManifest())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = manifest.Docker.MatchCommand([]string{"config", "check", "--delete-everything"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown forwarded flag: --delete-everything") || !strings.Contains(err.Error(), "--live") || !strings.Contains(err.Error(), "--profile") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerCommandMatchingSuggestsCloseForwardedFlag(t *testing.T) {
	manifest, err := ParsePackManifest(packTestManifest())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = manifest.Docker.MatchCommand([]string{"bootstrap", "server", "--foce"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown forwarded flag: --foce") || !strings.Contains(err.Error(), "did you mean --force?") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerCommandMatchingForwardsAppArgs(t *testing.T) {
	manifest, err := ParsePackManifest(packTestManifest())
	if err != nil {
		t.Fatal(err)
	}
	command, forwarded, err := manifest.Docker.MatchCommand([]string{"bootstrap", "plugin", "imap", "account", "primary", "--force"})
	if err != nil {
		t.Fatal(err)
	}
	if command.Name != "bootstrap_plugin" {
		t.Fatalf("command = %q", command.Name)
	}
	if got := strings.Join(forwarded, " "); got != "imap account primary --force" {
		t.Fatalf("forwarded args = %q", got)
	}
}

func TestAppCommandMatchingAcceptsConfigCheck(t *testing.T) {
	manifest, err := ParsePackManifest(packTestManifest())
	if err != nil {
		t.Fatal(err)
	}
	command, forwarded, err := manifest.Docker.MatchAppCommand([]string{"config", "check", "--live"})
	if err != nil {
		t.Fatal(err)
	}
	if command.Name != "config_check" {
		t.Fatalf("command = %q", command.Name)
	}
	if got := strings.Join(forwarded, " "); got != "--live" {
		t.Fatalf("forwarded args = %q", got)
	}
}

func TestAppCommandsListsOnlyAppCommands(t *testing.T) {
	manifest, err := ParsePackManifest(packTestManifest())
	if err != nil {
		t.Fatal(err)
	}
	commands := manifest.Docker.AppCommands()
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.Name)
	}
	expected := []string{
		"bootstrap_server",
		"bootstrap_plugin",
		"config_activate",
		"config_check",
		"config_show",
	}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("app commands = %#v, want %q", commands, expected)
	}
}

func TestParsePackManifestRejectsEscapingDeploymentDir(t *testing.T) {
	_, err := ParsePackManifest(`blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: arbiter
  provider:
    type: python
    identifier: arbiter-server
    local_sources:
      arbiter-server: ../../server
  terminal:
    color_env: ARBITER_COLOR

docker:
  deployment_dirs:
    config: ../conf
    bundle: bundle
    data: data
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - arbiter-server
          - serve
    config_check:
      trigger:
        - config
        - check
      forward_flags:
        - --live
      container:
        argv:
          - arbiter-server
          - --config-dir
          - /config
          - --config-name
          - ${ARBITER_CONFIG_NAME}
          - config
          - check
`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker.deployment_dirs.config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadPackRejectsMissingAppID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "arbiter.blueprint.yaml"), []byte("blueprint:\n  schema: 1\n  version: 0.1.0\n  requires_reploy: \">=0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ParsePackRef("file:" + dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LoadPack(ref)
	if err == nil {
		t.Fatal("expected error")
	}
}

func packTestManifest() string {
	return `blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: arbiter
  provider:
    type: python
    identifier: arbiter-server
    local_sources:
      arbiter-server: ../../server
  terminal:
    color_env: ARBITER_COLOR

bundle:
  options:
    imap:
      identifier: arbiter-imap
      group: plugins
      description: Receive email through IMAP.

docker:
  deployment_dirs:
    config: conf
    bundle: bundle
    data: data
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - arbiter-server
          - --config-dir
          - /config
          - --config-name
          - ${ARBITER_CONFIG_NAME}
          - serve
    config_check:
      trigger:
        - config
        - check
      app_command: true
      forward_flags:
        - --live
        - --profile
      container:
        argv:
          - arbiter-server
          - --config-dir
          - /config
          - --config-name
          - ${ARBITER_CONFIG_NAME}
          - config
          - check
    bootstrap_server:
      trigger:
        - bootstrap
        - server
      app_command: true
      forward_flags:
        - --force
      container:
        argv:
          - arbiter-server
          - --config-dir
          - /config
          - --config-name
          - ${ARBITER_CONFIG_NAME}
          - bootstrap
          - arbiter
    bootstrap_plugin:
      trigger:
        - bootstrap
        - plugin
      app_command: true
      forward_args: true
      container:
        argv:
          - arbiter-server
          - --config-dir
          - /config
          - --config-name
          - ${ARBITER_CONFIG_NAME}
          - bootstrap
          - plugin
    config_activate:
      trigger:
        - config
        - activate
      app_command: true
      forward_args: true
      container:
        argv:
          - arbiter-server
          - --config-dir
          - /config
          - --config-name
          - ${ARBITER_CONFIG_NAME}
          - config
          - activate
    config_show:
      trigger:
        - config
        - show
      app_command: true
      forward_args: true
      container:
        argv:
          - arbiter-server
          - --config-dir
          - /config
          - --config-name
          - ${ARBITER_CONFIG_NAME}
          - config
          - show
`
}

func TestAppPackReadFileRejectsEscape(t *testing.T) {
	pack := AppPack{Dir: t.TempDir()}
	_, err := pack.ReadFile("../secret")
	if err == nil {
		t.Fatal("expected error")
	}
}
