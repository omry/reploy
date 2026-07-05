package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadPack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.blueprint.yaml"), []byte(packTestManifest()), 0o644); err != nil {
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
	if pack.AppID != "demo" {
		t.Fatalf("app id = %q", pack.AppID)
	}
	if pack.Docker.DeploymentDirs.Bundle != "bundle" {
		t.Fatalf("bundle dir = %q", pack.Docker.DeploymentDirs.Bundle)
	}
	if !filepath.IsAbs(pack.Ref.Source) || !strings.HasPrefix(pack.Ref.Raw, "file:") {
		t.Fatalf("blueprint ref was not resolved to an absolute file ref: %#v", pack.Ref)
	}
}

func TestLoadSourcePackUsesProjectConventionAndLocalSource(t *testing.T) {
	sourceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "pyproject.toml"), []byte("[project]\nname = \"demo-server\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blueprintDir := filepath.Join(sourceRoot, "demo_server", "reploy")
	if err := os.MkdirAll(blueprintDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := strings.Replace(packTestManifest(), "    local_sources:\n      demo-server: ../../server\n", "", 1)
	if err := os.WriteFile(filepath.Join(blueprintDir, "demo.blueprint.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ParsePackRef("source:" + sourceRoot)
	if err != nil {
		t.Fatal(err)
	}

	pack, err := LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Ref.Scheme != "source" || pack.Ref.Source != sourceRoot || pack.Ref.Subdir != "demo_server/reploy" {
		t.Fatalf("resolved ref = %#v", pack.Ref)
	}
	if pack.RequestedRef.Raw != "source:"+sourceRoot {
		t.Fatalf("requested ref = %#v", pack.RequestedRef)
	}
	if pack.App.Provider.LocalSources["demo-server"] != "../.." {
		t.Fatalf("local sources = %#v", pack.App.Provider.LocalSources)
	}
}

func TestLoadSourcePackUsesExplicitBlueprintFile(t *testing.T) {
	sourceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "pyproject.toml"), []byte("[project]\nname = \"demo-server\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blueprintDir := filepath.Join(sourceRoot, "demo_server", "reploy")
	if err := os.MkdirAll(blueprintDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blueprintDir, "other.blueprint.yaml"), []byte(packTestManifest()), 0o644); err != nil {
		t.Fatal(err)
	}
	selectedManifest := strings.Replace(packTestManifest(), "id: demo\n", "id: selected\n", 1)
	if err := os.WriteFile(filepath.Join(blueprintDir, "selected.blueprint.yaml"), []byte(selectedManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ParsePackRef("source:" + sourceRoot + "#demo_server/reploy/selected.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}

	pack, err := LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if pack.AppID != "selected" {
		t.Fatalf("app id = %q", pack.AppID)
	}
	if pack.Ref.Subdir != "demo_server/reploy/selected.blueprint.yaml" {
		t.Fatalf("resolved ref = %#v", pack.Ref)
	}
	if filepath.Base(pack.ManifestPath) != "selected.blueprint.yaml" {
		t.Fatalf("manifest path = %q", pack.ManifestPath)
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
	if manifest.App.Provider.Identifier != "demo-server" {
		t.Fatalf("provider identifier = %q", manifest.App.Provider.Identifier)
	}
	if manifest.App.Provider.LocalSources["demo-server"] != "../../server" {
		t.Fatalf("local sources = %#v", manifest.App.Provider.LocalSources)
	}
	if manifest.Bundle.Options["imap"].Identifier != "demo-imap" {
		t.Fatalf("bundle options = %#v", manifest.Bundle.Options)
	}
	if manifest.App.Terminal.ColorEnv != "DEMO_COLOR" {
		t.Fatalf("app terminal color env = %q", manifest.App.Terminal.ColorEnv)
	}
	if manifest.Install.Target.DefaultPath != "" || len(manifest.Install.Target.DefaultPaths) != 0 {
		t.Fatalf("install target defaults = %#v", manifest.Install.Target)
	}
	if manifest.Install.Owner.User != "demo" || manifest.Install.Owner.Group != "demo" {
		t.Fatalf("install owner = %#v", manifest.Install.Owner)
	}
	if manifest.Install.Owner.OnMissing != "create" {
		t.Fatalf("install owner on_missing = %q", manifest.Install.Owner.OnMissing)
	}
	if manifest.Install.Ports.Deployed["http"].HostPort != 8080 {
		t.Fatalf("deployed install ports = %#v", manifest.Install.Ports.Deployed)
	}
	if manifest.Install.Ports.Staging["http"].HostPort != 18080 {
		t.Fatalf("staging install ports = %#v", manifest.Install.Ports.Staging)
	}
	if manifest.Install.Ports.Deployed["http"].ContainerPort != 8080 {
		t.Fatalf("defaulted deployed container port = %#v", manifest.Install.Ports.Deployed["http"])
	}
	if len(manifest.Install.ManagedPaths.Dirs) != 1 || manifest.Install.ManagedPaths.Dirs[0].Path != "conf" || manifest.Install.ManagedPaths.Dirs[0].Update != "preserve" || manifest.Install.ManagedPaths.Dirs[0].Mount != "/conf" {
		t.Fatalf("managed install paths = %#v", manifest.Install.ManagedPaths)
	}
	compactMountManifest := strings.Replace(packTestManifest(), "mount: /{{ path }}", "mount: /{{path}}", 1)
	manifest, err = ParsePackManifest(compactMountManifest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Install.ManagedPaths.Dirs[0].Mount != "/conf" {
		t.Fatalf("compact managed path mount = %#v", manifest.Install.ManagedPaths.Dirs[0])
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
	if got := strings.Join(command.Container.Argv, " "); got != "demo-server --config-dir /conf --config-name ${DEMO_CONFIG_NAME} config check" {
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

func TestParsePackManifestAppliesDockerCommandDefaults(t *testing.T) {
	manifest, err := ParsePackManifest(`blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: demo
  provider:
    type: python
    identifier: demo-server

install:
` + packTestInstallBlock() + `

docker:
  deployment_dirs:
    config: conf
    bundle: bundle
    data: data
  default_command: serve
  command_defaults:
    app_command: true
    deployed_command: true
    container:
      argv_prefix: [demo-server, --config-dir, "${REPLOY_CONFIG_CONTAINER_DIR}", --config-name, "${DEMO_CONFIG_NAME}"]
  commands:
    serve:
      container:
        argv_suffix: [serve]
    config_check:
      forward_flags: [--live]
      container:
        argv_suffix: [config, check]
    bootstrap_plugin:
      deployed_command: false
      forward_args: true
      container:
        argv_suffix: [bootstrap, plugin]
    special_trigger:
      trigger: ["odd-command", check]
      container:
        argv_suffix: ["odd-command", check]
    different_tool:
      trigger: [debug, other-tool]
      container:
        argv: [other-tool, inspect]
`)
	if err != nil {
		t.Fatal(err)
	}

	serve := dockerCommandByName(t, manifest, "serve")
	if len(serve.Trigger) != 0 {
		t.Fatalf("default command trigger = %#v", serve.Trigger)
	}
	if got := strings.Join(serve.Container.Argv, " "); got != "demo-server --config-dir ${REPLOY_CONFIG_CONTAINER_DIR} --config-name ${DEMO_CONFIG_NAME} serve" {
		t.Fatalf("serve argv = %q", got)
	}
	if !serve.AppCommand || !serve.Deployed {
		t.Fatalf("serve inherited booleans = app:%t deployed:%t", serve.AppCommand, serve.Deployed)
	}

	configCheck := dockerCommandByName(t, manifest, "config_check")
	if got := strings.Join(configCheck.Trigger, " "); got != "config check" {
		t.Fatalf("config_check trigger = %q", got)
	}
	if got := strings.Join(configCheck.Container.Argv, " "); got != "demo-server --config-dir ${REPLOY_CONFIG_CONTAINER_DIR} --config-name ${DEMO_CONFIG_NAME} config check" {
		t.Fatalf("config_check argv = %q", got)
	}
	if !configCheck.AppCommand || !configCheck.Deployed {
		t.Fatalf("config_check inherited booleans = app:%t deployed:%t", configCheck.AppCommand, configCheck.Deployed)
	}

	pluginBootstrap := dockerCommandByName(t, manifest, "bootstrap_plugin")
	if got := strings.Join(pluginBootstrap.Trigger, " "); got != "bootstrap plugin" {
		t.Fatalf("bootstrap_plugin trigger = %q", got)
	}
	if !pluginBootstrap.AppCommand || pluginBootstrap.Deployed {
		t.Fatalf("bootstrap_plugin booleans = app:%t deployed:%t", pluginBootstrap.AppCommand, pluginBootstrap.Deployed)
	}
	if !pluginBootstrap.ForwardArgs {
		t.Fatal("bootstrap_plugin did not allow forwarded args")
	}

	special := dockerCommandByName(t, manifest, "special_trigger")
	if got := strings.Join(special.Trigger, " "); got != "odd-command check" {
		t.Fatalf("special trigger = %q", got)
	}

	differentTool := dockerCommandByName(t, manifest, "different_tool")
	if got := strings.Join(differentTool.Container.Argv, " "); got != "other-tool inspect" {
		t.Fatalf("different_tool argv = %q", got)
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

install:
  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080

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

func TestParsePackManifestReadsInstallHooks(t *testing.T) {
	manifest, err := ParsePackManifest(strings.Replace(packTestManifest(), "  default_command: serve\n", `  install:
    hooks:
      before_start:
        - app: [config, check]
      after_start:
        - health_check:
            wait: true
        - app: [config, check, --live]
  default_command: serve
`, 1))
	if err != nil {
		t.Fatal(err)
	}
	before := manifest.Docker.Install.Hooks.BeforeStart
	if len(before) != 1 || strings.Join(before[0].App, " ") != "config check" {
		t.Fatalf("before hooks = %#v", before)
	}
	after := manifest.Docker.Install.Hooks.AfterStart
	if len(after) != 2 {
		t.Fatalf("after hooks = %#v", after)
	}
	if after[0].HealthCheck == nil || !after[0].HealthCheck.Wait {
		t.Fatalf("health check hook = %#v", after[0])
	}
	if strings.Join(after[1].App, " ") != "config check --live" {
		t.Fatalf("live check hook = %#v", after[1])
	}
}

func TestParsePackManifestReadsInstallSuccess(t *testing.T) {
	manifest, err := ParsePackManifest(strings.Replace(packTestManifest(), "  default_command: serve\n", `  install:
    success:
      vars:
        server_url:
          server_url: true
      lines:
        - "server url: ${server_url}"
        - "client command: demo-client --url=${server_url} info"
  default_command: serve
`, 1))
	if err != nil {
		t.Fatal(err)
	}
	success := manifest.Docker.Install.Success
	serverURL := success.Vars["server_url"]
	if !serverURL.ServerURL {
		t.Fatalf("server_url var = %#v", serverURL)
	}
	wantLines := []string{
		"server url: ${server_url}",
		"client command: demo-client --url=${server_url} info",
	}
	if strings.Join(success.Lines, "\n") != strings.Join(wantLines, "\n") {
		t.Fatalf("success lines = %#v", success.Lines)
	}
}

func TestParsePackManifestRejectsInvalidInstallHooks(t *testing.T) {
	for _, tc := range []struct {
		name string
		hook string
		want string
	}{
		{
			name: "mixed actions",
			hook: `        - app: [config, check]
          health_check:
            wait: true
`,
			want: "must declare exactly one action",
		},
		{
			name: "empty app arg",
			hook: `        - app: [config, ""]
`,
			want: "must not be empty",
		},
		{
			name: "health check without wait",
			hook: `        - health_check:
            wait: false
`,
			want: "health_check.wait must be true",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePackManifest(strings.Replace(packTestManifest(), "  default_command: serve\n", "  install:\n    hooks:\n      before_start:\n"+tc.hook+"  default_command: serve\n", 1))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParsePackManifestRejectsInvalidInstallSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		success string
		want    string
	}{
		{
			name: "empty line",
			success: `      lines:
        - ""
`,
			want: "must not be empty",
		},
		{
			name:    "line with field break",
			success: "      lines:\n        - \"demo\tclient\"\n",
			want:    "must not contain tabs or newlines",
		},
		{
			name: "invalid variable name",
			success: `      vars:
        server-url:
          app: [config, show]
`,
			want: "invalid variable name",
		},
		{
			name: "variable without source",
			success: `      vars:
        server_url: {}
`,
			want: "must declare exactly one source",
		},
		{
			name: "mixed variable sources",
			success: `      vars:
        server_url:
          app: [config, show]
          server_url: true
`,
			want: "must declare exactly one source",
		},
		{
			name: "empty app arg",
			success: `      vars:
        server_url:
          app: [config, ""]
`,
			want: "must not be empty",
		},
		{
			name: "line references unknown variable",
			success: `      lines:
        - "client command: demo-client --url=${server_url} info"
`,
			want: "references unknown variable: server_url",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParsePackManifest(strings.Replace(packTestManifest(), "  default_command: serve\n", "  install:\n    success:\n"+tc.success+"  default_command: serve\n", 1))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParsePackManifestRejectsInvalidInstallConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		install string
		want    string
	}{
		{
			name: "relative target path",
			install: `  target:
    default_path: var/demo
  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
`,
			want: "install.target.default_path must resolve to an absolute path",
		},
		{
			name: "missing owner",
			install: `  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
`,
			want: "install.owner.user is required",
		},
		{
			name: "root owner",
			install: `  owner:
    user: root
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
`,
			want: "install.owner.user must not be root",
		},
		{
			name: "invalid owner on_missing",
			install: `  owner:
    user: demo
    group: demo
    on_missing: prompt
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
`,
			want: "install.owner.on_missing must be create or fail",
		},
		{
			name: "create owner with numeric user",
			install: `  owner:
    user: "1000"
    group: "1000"
    on_missing: create
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
`,
			want: "install.owner.on_missing=create requires named user and group",
		},
		{
			name: "create owner with unsafe user",
			install: `  owner:
    user: --flag
    group: demo
    on_missing: create
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
`,
			want: "install.owner.user must be a safe system account name",
		},
		{
			name: "create owner with unsafe group",
			install: `  owner:
    user: demo
    group: "bad group"
    on_missing: create
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
`,
			want: "install.owner.group must be a safe system account name",
		},
		{
			name: "missing deployed ports",
			install: `  owner:
    user: demo
    group: demo
  ports:
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
`,
			want: "install.ports.deployed must declare at least one port",
		},
		{
			name: "invalid host port",
			install: `  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 70000
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
`,
			want: "install.ports.deployed.http.host_port must be between 1 and 65535",
		},
		{
			name: "reploy owned managed path",
			install: `  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
  managed_paths:
    dirs:
      - path: .reploy/bundle
        update: replace
`,
			want: "must not include .reploy",
		},
		{
			name: "unsupported managed path mount template",
			install: `  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
  managed_paths:
    dirs:
      - path: conf
        update: preserve
        mount: /{{ app.id }}
`,
			want: "contains unsupported template expression",
		},
		{
			name: "overlapping managed paths",
			install: `  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
  managed_paths:
    files:
      - path: conf/secret.env
        update: preserve
    dirs:
      - path: conf
        update: preserve
`,
			want: "overlaps",
		},
		{
			name: "duplicate managed path mount",
			install: `  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
  managed_paths:
    files:
      - path: .demo.env
        update: preserve
        mount: /conf
    dirs:
      - path: conf
        update: preserve
        mount: /conf
`,
			want: "mount duplicates",
		},
		{
			name: "managed path mount root",
			install: `  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
  managed_paths:
    dirs:
      - path: conf
        update: preserve
        mount: /
`,
			want: "mount must not be /",
		},
		{
			name: "managed path mount contains colon",
			install: `  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
  managed_paths:
    dirs:
      - path: conf
        update: preserve
        mount: /conf:rw
`,
			want: "mount must not contain ':'",
		},
		{
			name: "mounted managed path contains colon",
			install: `  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
  managed_paths:
    dirs:
      - path: conf:prod
        update: preserve
        mount: /conf
`,
			want: "path must not contain ':' when mount is set",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := strings.Replace(packTestManifest(), "install:\n"+packTestInstallBlock(), "install:\n"+tc.install, 1)
			_, err := ParsePackManifest(manifest)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParsePackManifestAcceptsInstallTargetDefaultPaths(t *testing.T) {
	manifestText := strings.Replace(packTestManifest(), "install:\n"+packTestInstallBlock(), `install:
  target:
    default_path: "{{ user.data }}/Acme/{{ app.id }}"
    default_paths:
      linux: /opt/{{ app.id }}
      macos: "{{ user.data }}/Acme/{{ app.id }}"
      windows: "{{ user.local_data }}/Acme/{{ app.id }}"
`+packTestInstallBlock(), 1)
	manifest, err := ParsePackManifest(manifestText)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Install.Target.DefaultPath != "{{ user.data }}/Acme/{{ app.id }}" {
		t.Fatalf("default path = %q", manifest.Install.Target.DefaultPath)
	}
	if manifest.Install.Target.DefaultPaths["linux"] != "/opt/{{ app.id }}" {
		t.Fatalf("default paths = %#v", manifest.Install.Target.DefaultPaths)
	}
}

func TestResolveInstallTargetDefaultUsesPerOSBeforeGlobal(t *testing.T) {
	target, source, ok, err := ResolveInstallTargetDefault(
		InstallTargetConfig{
			DefaultPath: "{{ user.data }}/Global/{{ app.id }}",
			DefaultPaths: map[string]string{
				"windows": "{{ user.local_data }}/Windows/{{ app.id }}",
			},
		},
		"demo",
		"windows",
		sampleInstallTargetRoots("windows"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected install target default")
	}
	if source != "install.target.default_paths.windows" {
		t.Fatalf("source = %q", source)
	}
	want := `C:\Users\app\AppData\Local/Windows/demo`
	if target != want {
		t.Fatalf("target = %q, want %q", target, want)
	}
}

func TestResolveInstallTargetDefaultIgnoresInactivePlatformPath(t *testing.T) {
	target, source, ok, err := ResolveInstallTargetDefault(
		InstallTargetConfig{
			DefaultPaths: map[string]string{
				"linux": "/opt/{{ app.id }}",
			},
		},
		"demo",
		"windows",
		sampleInstallTargetRoots("windows"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if ok || target != "" || source != "" {
		t.Fatalf("target = %q source=%q ok=%v, want no active default", target, source, ok)
	}
}

func TestResolveInstallTargetDefaultRejectsActiveNonNativePath(t *testing.T) {
	_, _, _, err := ResolveInstallTargetDefault(
		InstallTargetConfig{DefaultPath: "/opt/{{ app.id }}"},
		"demo",
		"windows",
		sampleInstallTargetRoots("windows"),
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must resolve to an absolute path on windows") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveInstallTargetDefaultRejectsUnknownTemplate(t *testing.T) {
	_, _, _, err := ResolveInstallTargetDefault(
		InstallTargetConfig{DefaultPath: "{{ user.config }}/{{ app.id }}"},
		"demo",
		"linux",
		sampleInstallTargetRoots("linux"),
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported template expression") {
		t.Fatalf("unexpected error: %v", err)
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

install:
  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080

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

func TestParsePackManifestRejectsReservedDeploymentScopeEnv(t *testing.T) {
	manifest := strings.Replace(packTestManifest(), "  default_command: serve\n", `  environment:
    REPLOY_DEPLOYMENT_SCOPE: staging
  default_command: serve
`, 1)
	_, err := ParsePackManifest(manifest)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker.environment.REPLOY_DEPLOYMENT_SCOPE is reserved by Reploy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePackManifestRejectsLegacyDockerServiceInstallFields(t *testing.T) {
	manifest := strings.Replace(packTestManifest(), "  default_command: serve\n", `  service:
    install_owner: demo:demo
  default_command: serve
`, 1)
	_, err := ParsePackManifest(manifest)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker.service.install_owner has moved to install.*") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePackManifestRejectsLegacyDockerPorts(t *testing.T) {
	manifest := strings.Replace(packTestManifest(), "  default_command: serve\n", `  ports:
    http:
      host_bind: 127.0.0.1
      host_port: "18080"
      container_port: "8080"
  default_command: serve
`, 1)
	_, err := ParsePackManifest(manifest)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker.ports has moved to install.ports") {
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

func TestDeployedCommandsOnlyReturnsExplicitDeployedCommands(t *testing.T) {
	manifest, err := ParsePackManifest(packTestManifest())
	if err != nil {
		t.Fatal(err)
	}
	commands := manifest.Docker.DeployedCommands()
	if len(commands) != 1 {
		t.Fatalf("deployed commands = %#v", commands)
	}
	if got := strings.Join(commands[0].Trigger, " "); got != "config check" {
		t.Fatalf("deployed command trigger = %q", got)
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
  id: demo
  provider:
    type: python
    identifier: demo-server
    local_sources:
      demo-server: ../../server
  terminal:
    color_env: DEMO_COLOR

install:
  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080

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
          - demo-server
          - serve
    config_check:
      trigger:
        - config
        - check
      forward_flags:
        - --live
      container:
        argv:
          - demo-server
          - --config-dir
          - /conf
          - --config-name
          - ${DEMO_CONFIG_NAME}
          - config
          - check
`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker.deployment_dirs.config") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), `must stay inside the deployment root, got "../conf"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePackManifestRejectsDeploymentRootDir(t *testing.T) {
	for _, tc := range []struct {
		name    string
		oldLine string
		newLine string
		field   string
		example string
	}{
		{name: "config", oldLine: "    config: conf\n", newLine: "    config: .\n", field: "docker.deployment_dirs.config", example: "conf"},
		{name: "bundle", oldLine: "    bundle: bundle\n", newLine: "    bundle: .\n", field: "docker.deployment_dirs.bundle", example: ".reploy/bundle"},
		{name: "data", oldLine: "    data: data\n", newLine: "    data: .\n", field: "docker.deployment_dirs.data", example: "data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := strings.Replace(packTestManifest(), tc.oldLine, tc.newLine, 1)
			_, err := ParsePackManifest(manifest)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("unexpected error: %v", err)
			}
			want := fmt.Sprintf(`must name a subdirectory under the deployment root, not "."; use a value like %q`, tc.example)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadPackRejectsMissingAppID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.blueprint.yaml"), []byte("blueprint:\n  schema: 1\n  version: 0.1.0\n  requires_reploy: \">=0.1.0\"\n"), 0o644); err != nil {
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
  id: demo
  provider:
    type: python
    identifier: demo-server
    local_sources:
      demo-server: ../../server
  terminal:
    color_env: DEMO_COLOR

install:
` + packTestInstallBlock() + `

bundle:
  options:
    imap:
      identifier: demo-imap
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
          - demo-server
          - --config-dir
          - /conf
          - --config-name
          - ${DEMO_CONFIG_NAME}
          - serve
    config_check:
      trigger:
        - config
        - check
      app_command: true
      deployed_command: true
      forward_flags:
        - --live
        - --profile
      container:
        argv:
          - demo-server
          - --config-dir
          - /conf
          - --config-name
          - ${DEMO_CONFIG_NAME}
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
          - demo-server
          - --config-dir
          - /conf
          - --config-name
          - ${DEMO_CONFIG_NAME}
          - bootstrap
          - demo
    bootstrap_plugin:
      trigger:
        - bootstrap
        - plugin
      app_command: true
      forward_args: true
      container:
        argv:
          - demo-server
          - --config-dir
          - /conf
          - --config-name
          - ${DEMO_CONFIG_NAME}
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
          - demo-server
          - --config-dir
          - /conf
          - --config-name
          - ${DEMO_CONFIG_NAME}
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
          - demo-server
          - --config-dir
          - /conf
          - --config-name
          - ${DEMO_CONFIG_NAME}
          - config
          - show
`
}

func packTestInstallBlock() string {
	return `  owner:
    user: demo
    group: demo
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
  managed_paths:
    dirs:
      - path: conf
        update: preserve
        mount: /{{ path }}
`
}

func TestAppPackReadFileRejectsEscape(t *testing.T) {
	pack := AppPack{Dir: t.TempDir()}
	_, err := pack.ReadFile("../secret")
	if err == nil {
		t.Fatal("expected error")
	}
}
