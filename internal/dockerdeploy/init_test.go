package dockerdeploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
)

func TestInitWritesDeploymentDirectory(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")

	results, err := Init(InitOptions{Dir: deployDir, Pack: ref})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected init results")
	}
	assertResultStatus(t, results, filepath.Join(deployDir, ComposeFileName), deploy.UpdateStatusUpdated)
	for _, relativePath := range []string{
		ComposeFileName,
		"reploy",
		ToolBinaryFileName,
		DockerEnvFileName,
		RequirementsFileName,
		ManifestFileName,
		StateFileName,
		"conf",
		BundleDirName,
		"data",
	} {
		if _, err := os.Stat(filepath.Join(deployDir, relativePath)); err != nil {
			t.Fatalf("missing %s: %v", relativePath, err)
		}
	}
	requirements := readFile(t, filepath.Join(deployDir, RequirementsFileName))
	if requirements != "arbiter-suite\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	dockerEnv := readFile(t, filepath.Join(deployDir, DockerEnvFileName))
	for _, unexpected := range []string{"ARBITER_DOCKER_SUBNET", "ARBITER_DOCKER_BRIDGE_NAME"} {
		if strings.Contains(dockerEnv, unexpected) {
			t.Fatalf("docker.env should not pin network internals with %s:\n%s", unexpected, dockerEnv)
		}
	}
	if strings.Contains(dockerEnv, "ARBITER_APP_ENV_FILE") {
		t.Fatalf("docker.env should not point at app env files:\n%s", dockerEnv)
	}
	compose := readFile(t, filepath.Join(deployDir, ComposeFileName))
	for _, unexpected := range []string{"ipam:", "com.docker.network.bridge.name", "env_file:"} {
		if strings.Contains(compose, unexpected) {
			t.Fatalf("compose contains unexpected deployment coupling %s:\n%s", unexpected, compose)
		}
	}
	manifest := readFile(t, filepath.Join(deployDir, ManifestFileName))
	if !strings.Contains(manifest, `"`+ComposeFileName+`"`) {
		t.Fatalf("manifest did not track compose.yaml:\n%s", manifest)
	}
	if strings.Contains(manifest, `"requirements.txt"`) {
		t.Fatalf("requirements should be operator-owned local state:\n%s", manifest)
	}
	if !strings.Contains(manifest, `"reploy"`) {
		t.Fatalf("manifest did not track helper:\n%s", manifest)
	}
	if !strings.Contains(manifest, `"`+ToolBinaryFileName+`"`) {
		t.Fatalf("manifest did not track vendored binary:\n%s", manifest)
	}
	if strings.Contains(manifest, `".env"`) {
		t.Fatalf("app env file should be operator-owned local state:\n%s", manifest)
	}
	helper := readFile(t, filepath.Join(deployDir, "reploy"))
	if !strings.Contains(helper, `vendored_reploy="$deploy_dir/.reploy/bin/reploy"`) || !strings.Contains(helper, `exec "$reploy_bin" "$@" --dir "$deploy_dir"`) {
		t.Fatalf("helper does not invoke the root command surface:\n%s", helper)
	}
	if strings.Contains(helper, `"$reploy_bin" docker`) || strings.Contains(helper, `if [ "${1:-}" = "docker" ]`) {
		t.Fatalf("helper still accepts the old docker command prefix:\n%s", helper)
	}
	state := readFile(t, filepath.Join(deployDir, StateFileName))
	if !strings.Contains(state, `"target": "docker"`) || !strings.Contains(state, `"phase": "staged"`) {
		t.Fatalf("state missing target/phase:\n%s", state)
	}
}

func TestInitUsesDefaultDeploymentDir(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	t.Chdir(workDir)

	if _, err := Init(InitOptions{Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "reploy-staging", StateFileName)); err != nil {
		t.Fatalf("missing default deployment state: %v", err)
	}
}

func TestInitRendersGenericDockerSurfaceFromBlueprint(t *testing.T) {
	packDir := makeTestPackWithManifest(t, `blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: mailhub
  provider:
    type: python
    identifier: mailhub-server

docker:
  deployment_dirs:
    config: config
    bundle: .reploy/bundle
    data: data
  service:
    container_name: mailhub-staging
    container_port: "2525"
    host_port: "12525"
    public_scheme: http
    network_name: mailhub-staging
  environment:
    MAILHUB_CONFIG_NAME: mailhub
  runtime:
    overrides:
      - mailhub.bind.host=${REPLOY_CONTAINER_HOST}
      - mailhub.bind.port=${REPLOY_CONTAINER_PORT}
      - mailhub.public.host=${REPLOY_PUBLIC_HOST}
    optional_env_overrides:
      REPLOY_PUBLIC_BASE_URL: mailhub.public.base_url
  health:
    scheme_env: REPLOY_PUBLIC_SCHEME
    host_env: REPLOY_HOST_BIND
    port_env: REPLOY_HOST_PORT
    default_scheme: http
    default_host: 127.0.0.1
    default_port: "12525"
    path: /health
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - mailhub-server
          - --config
          - ${MAILHUB_CONFIG_NAME}
          - serve
    config_check:
      trigger:
        - config
        - check
      container:
        argv:
          - mailhub-server
          - --config
          - ${MAILHUB_CONFIG_NAME}
          - config
          - check
`)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")

	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	dockerEnv := readFile(t, filepath.Join(deployDir, DockerEnvFileName))
	for _, expected := range []string{
		"REPLOY_CONTAINER_NAME=mailhub-staging",
		"REPLOY_CONTAINER_PORT=2525",
		"REPLOY_HOST_PORT=12525",
		"REPLOY_PUBLIC_SCHEME=http",
		"REPLOY_DOCKER_NETWORK_NAME=mailhub-staging",
		"MAILHUB_CONFIG_NAME=mailhub",
	} {
		if !strings.Contains(dockerEnv, expected) {
			t.Fatalf("docker.env missing %q:\n%s", expected, dockerEnv)
		}
	}
	for _, unexpected := range []string{"ARBITER_", "arbiter-staging"} {
		if strings.Contains(dockerEnv, unexpected) {
			t.Fatalf("docker.env leaked %s:\n%s", unexpected, dockerEnv)
		}
	}

	compose := readFile(t, filepath.Join(deployDir, ComposeFileName))
	for _, expected := range []string{
		"  app:",
		"MAILHUB_CONFIG_NAME: \"${MAILHUB_CONFIG_NAME:-mailhub}\"",
		`set -- "$$@" "mailhub.bind.host=$${REPLOY_CONTAINER_HOST}" &&`,
		`set -- "$$@" "mailhub.public.host=$${REPLOY_PUBLIC_HOST}" &&`,
		`if [ -n "$${REPLOY_PUBLIC_BASE_URL:-}" ]; then set -- "$$@" "mailhub.public.base_url=$${REPLOY_PUBLIC_BASE_URL}"; fi &&`,
		"name: ${REPLOY_DOCKER_NETWORK_NAME:-mailhub-staging}",
	} {
		if !strings.Contains(compose, expected) {
			t.Fatalf("compose missing %q:\n%s", expected, compose)
		}
	}
	for _, unexpected := range []string{"ARBITER_", "arbiter-staging", "  arbiter:", "/source/arbiter"} {
		if strings.Contains(compose, unexpected) {
			t.Fatalf("compose leaked %s:\n%s", unexpected, compose)
		}
	}
}

func TestInitUsesPackDeclaredDockerLayout(t *testing.T) {
	packDir := makeTestPackWithManifest(t, `blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: arbiter
  provider:
    type: python
    identifier: arbiter-server

docker:
  deployment_dirs:
    config: app-conf
    bundle: artifacts
    data: state
  health:
    scheme_env: REPLOY_PUBLIC_SCHEME
    host_env: REPLOY_HOST_BIND
    port_env: REPLOY_HOST_PORT
    default_scheme: https
    default_host: 127.0.0.1
    default_port: "18075"
    path: /_health_
    tls_verify: false
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - custom-serve
          - --name
          - ${ARBITER_CONFIG_NAME}
    config_check:
      trigger:
        - config
        - check
      forward_flags:
        - --live
      container:
        argv:
          - custom-check
          - --name
          - ${ARBITER_CONFIG_NAME}
    config_show:
      trigger:
        - config
        - show
      app_command: true
      forward_args: true
      container:
        argv:
          - custom-show
          - --name
          - ${ARBITER_CONFIG_NAME}
`)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")

	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	for _, relativePath := range []string{"app-conf", "artifacts", "state"} {
		if _, err := os.Stat(filepath.Join(deployDir, relativePath)); err != nil {
			t.Fatalf("missing blueprint-declared dir %s: %v", relativePath, err)
		}
	}
	dockerEnv := readFile(t, filepath.Join(deployDir, DockerEnvFileName))
	for _, line := range []string{
		"REPLOY_CONFIG_DIR=./app-conf",
		"REPLOY_BUNDLE_DIR=./artifacts",
		"REPLOY_DATA_DIR=./state",
	} {
		if !strings.Contains(dockerEnv, line) {
			t.Fatalf("docker.env missing %q:\n%s", line, dockerEnv)
		}
	}
	compose := readFile(t, filepath.Join(deployDir, ComposeFileName))
	if !strings.Contains(compose, `container_command_config_check() { "custom-check" "--name" "$${ARBITER_CONFIG_NAME}" "$$@"; };`) {
		t.Fatalf("compose did not render pack config_check command:\n%s", compose)
	}
	if !strings.Contains(compose, `container_command_serve() { "custom-serve" "--name" "$${ARBITER_CONFIG_NAME}" "$$@"; };`) {
		t.Fatalf("compose did not render pack serve command:\n%s", compose)
	}
	if !strings.Contains(compose, `container_command_config_show() { "custom-show" "--name" "$${ARBITER_CONFIG_NAME}" "$$@"; };`) {
		t.Fatalf("compose did not render pack app config_show command:\n%s", compose)
	}
	if !strings.Contains(compose, `config_check) container_command_config_check "$$@" ;;`) {
		t.Fatalf("compose did not render command dispatch:\n%s", compose)
	}
	if !strings.Contains(compose, `config_show) container_command_config_show "$$@" ;;`) {
		t.Fatalf("compose did not render app command dispatch:\n%s", compose)
	}
	if !strings.Contains(compose, "run_reploy_container_command \"$$@\";\n        exit $$?;") {
		t.Fatalf("compose app command path did not exit before default command:\n%s", compose)
	}
	if !strings.Contains(compose, "reploy_status_start()") || !strings.Contains(compose, `printf "\r| %s" "$$reploy_status_label"`) {
		t.Fatalf("compose did not render reusable status spinner:\n%s", compose)
	}
	if strings.Contains(compose, "load_reploy_app_env_file") || strings.Contains(compose, "done < /config/.env;") {
		t.Fatalf("compose should not parse app env files; the app owns its env parser:\n%s", compose)
	}
	if !strings.Contains(compose, `reploy_status_start "Preparing Python runtime" &&`) || !strings.Contains(compose, "reploy_status_stop 0") {
		t.Fatalf("compose did not use Python runtime spinner around preparation:\n%s", compose)
	}
}

func TestRenderedComposeCommandIsShellParseable(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := deploy.LoadPack(ref)
	if err != nil {
		t.Fatal(err)
	}
	compose, err := renderComposeTemplate(pack, nil)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "      sh -lc\n      '"
	start := strings.Index(compose, prefix)
	if start == -1 {
		t.Fatalf("compose missing shell command prefix:\n%s", compose)
	}
	script := compose[start+len(prefix):]
	end := strings.LastIndex(script, "'\n\nnetworks:")
	if end == -1 {
		t.Fatalf("compose missing shell command terminator:\n%s", compose)
	}
	script = script[:end]
	script = strings.ReplaceAll(script, "$$", "$")

	command := exec.Command("sh", "-n", "-c", script)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("rendered shell command did not parse: %v\n%s\nscript:\n%s", err, output, script)
	}
}

func TestInitRefusesExistingDeploymentFile(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(deployDir, ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ComposeFileName), []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = Init(InitOptions{Dir: deployDir, Pack: ref})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite existing deployment file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitAcceptsExplicitRequirements(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	_, err = Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"arbiter-server==1.2.3", "arbiter-smtp==1.2.3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "arbiter-server==1.2.3\narbiter-smtp==1.2.3\n" {
		t.Fatalf("requirements = %q", got)
	}
}

func TestInitRequirementsUsesResolvedPyPIPackArtifact(t *testing.T) {
	requirements, err := initRequirements(deploy.AppPack{
		Ref: deploy.PackRef{Scheme: "pypi"},
		ResolvedArtifact: &deploy.ResolvedPackArtifact{
			Package: "demo-pkg",
			Version: "1.2.3",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-pkg==1.2.3\n" {
		t.Fatalf("requirements = %q", requirements)
	}
}

func TestInitSupportsResolvedPyPIPackWithoutPackFiles(t *testing.T) {
	packDir := makeTestPackWithManifest(t, `blueprint:
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
    bundle: .reploy/bundle
    data: data
  health:
    scheme_env: REPLOY_PUBLIC_SCHEME
    host_env: REPLOY_HOST_BIND
    port_env: REPLOY_HOST_PORT
    default_scheme: https
    default_host: 127.0.0.1
    default_port: "18075"
    path: /_health_
    tls_verify: false
  default_command: serve
  commands:
    serve:
      container:
        argv:
          - demo
          - serve
`)
	pack := deploy.AppPack{
		Ref: deploy.PackRef{
			Raw:    "pypi:demo-pkg==1.2.3//demo/reploy",
			Scheme: "pypi",
		},
		RequestedRef: deploy.PackRef{Raw: "pypi:demo-pkg//demo/reploy"},
		ResolvedArtifact: &deploy.ResolvedPackArtifact{
			Package: "demo-pkg",
			Version: "1.2.3",
		},
		Dir: packDir,
		App: deploy.AppPackConfig{
			ID: "demo",
			Provider: deploy.AppProviderConfig{
				Type:       "python",
				Identifier: "demo",
			},
		},
		Docker: deploy.DockerPackConfig{
			DeploymentDirs: deploy.DockerDeploymentDirs{
				Config: "conf",
				Bundle: BundleDirName,
				Data:   "data",
			},
			DefaultCommand: "serve",
			Commands: []deploy.DockerCommandConfig{{
				Name:      "serve",
				Container: deploy.AppCommandConfig{Argv: []string{"demo", "serve"}},
			}},
		},
	}
	requirements, err := initRequirements(pack, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-pkg==1.2.3\n" {
		t.Fatalf("requirements = %q", requirements)
	}
}

func TestInitRequirementsRejectsMissingPackRequirementSource(t *testing.T) {
	_, err := initRequirements(deploy.AppPack{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no app.provider.identifier") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitRejectsUnpinnedExplicitRequirement(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	_, err = Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"arbiter-server"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "exact package pin or absolute container path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitAcceptsAbsoluteContainerPathRequirement(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	_, err = Init(InitOptions{
		Dir:          deployDir,
		Pack:         ref,
		Requirements: []string{"/source/app/server"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRefreshesGeneratedFilesAndPreservesLocalState(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, DockerEnvFileName), []byte("LOCAL=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Update(UpdateOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected update results")
	}
	if got := readFile(t, filepath.Join(deployDir, DockerEnvFileName)); got != "LOCAL=1\n" {
		t.Fatalf("docker.env was not preserved: %q", got)
	}
}

func TestUpdateDoesNotCreateAppEnvFile(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	appEnv := filepath.Join(deployDir, "conf", ".env")

	results, err := Update(UpdateOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}

	assertResultMissing(t, results, appEnv)
	if _, err := os.Stat(appEnv); !os.IsNotExist(err) {
		t.Fatalf("update created app env file: %v", err)
	}
}

func TestUpdatePreservesLocallyEditedDockerEnv(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	localDockerEnv := "# local docker env\nREPLOY_BUNDLE_DIR=./custom-bundle\n"
	if err := os.WriteFile(filepath.Join(deployDir, DockerEnvFileName), []byte(localDockerEnv), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Update(UpdateOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}

	assertResultStatus(t, results, filepath.Join(deployDir, DockerEnvFileName), deploy.UpdateStatusUpToDate)
	if got := readFile(t, filepath.Join(deployDir, DockerEnvFileName)); got != localDockerEnv {
		t.Fatalf("docker.env was not preserved: %q", got)
	}
}

func TestUpdateRemovesObsoleteDockerEnvKeys(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	dockerEnvPath := filepath.Join(deployDir, DockerEnvFileName)
	content := readFile(t, dockerEnvPath)
	content = strings.Replace(content, "REPLOY_CONFIG_DIR=./conf\n", "ARBITER_APP_ENV_FILE=./conf/.env\nREPLOY_CONFIG_DIR=./conf\n", 1)
	if err := os.WriteFile(dockerEnvPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Update(UpdateOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}

	assertResultStatus(t, results, dockerEnvPath, deploy.UpdateStatusUpdated)
	dockerEnv := readFile(t, dockerEnvPath)
	if strings.Contains(dockerEnv, "ARBITER_APP_ENV_FILE") {
		t.Fatalf("docker.env still contains obsolete app env key:\n%s", dockerEnv)
	}
	if !strings.Contains(dockerEnv, "REPLOY_CONFIG_DIR=./conf\n") {
		t.Fatalf("docker.env lost current config dir key:\n%s", dockerEnv)
	}
}

func TestUpdateReportsMetadataUpToDateWhenUnchanged(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	results, err := Update(UpdateOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}

	assertResultStatus(t, results, filepath.Join(deployDir, StateFileName), deploy.UpdateStatusUpToDate)
	assertResultStatus(t, results, filepath.Join(deployDir, ManifestFileName), deploy.UpdateStatusUpToDate)
}

func TestUpdateSkipsLocallyEditedGeneratedFile(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, ComposeFileName), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Update(UpdateOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}
	var composeStatus deploy.UpdateStatus
	for _, result := range results {
		if result.Path == filepath.Join(deployDir, ComposeFileName) {
			composeStatus = result.Status
		}
	}
	if composeStatus != deploy.UpdateStatusSkipped {
		t.Fatalf("compose status = %q", composeStatus)
	}
	if got := readFile(t, filepath.Join(deployDir, ComposeFileName)); got != "local edit\n" {
		t.Fatalf("compose was not preserved: %q", got)
	}
}

func makeTestPack(t *testing.T) string {
	t.Helper()
	return makeTestPackWithManifest(t, testPackManifest())
}

func makeTestPackWithManifest(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "arbiter.blueprint.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testPackManifest() string {
	return `blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: arbiter
  provider:
    type: python
    identifier: arbiter-suite
  terminal:
    color_env: ARBITER_COLOR

bundle:
  options:
    arbiter-suite:
      identifier: arbiter-suite
      group: meta
      description: Install the full Arbiter suite.
    imap:
      identifier: arbiter-imap
      group: plugins
      description: Receive email through IMAP.
    smtp:
      identifier: arbiter-smtp
      group: plugins
      description: Send email through SMTP.

docker:
  deployment_dirs:
    config: conf
    bundle: .reploy/bundle
    data: data
  health:
    scheme_env: REPLOY_PUBLIC_SCHEME
    host_env: REPLOY_HOST_BIND
    port_env: REPLOY_HOST_PORT
    default_scheme: https
    default_host: 127.0.0.1
    default_port: "18075"
    path: /_health_
    tls_verify: false
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
`
}

func assertResultStatus(t *testing.T, results []UpdateResult, path string, expected deploy.UpdateStatus) {
	t.Helper()
	for _, result := range results {
		if result.Path == path {
			if result.Status != expected {
				t.Fatalf("%s status = %q, want %q", path, result.Status, expected)
			}
			return
		}
	}
	t.Fatalf("missing result for %s", path)
}

func assertResultMissing(t *testing.T, results []UpdateResult, path string) {
	t.Helper()
	for _, result := range results {
		if result.Path == path {
			t.Fatalf("unexpected result for %s: %#v", path, result)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
