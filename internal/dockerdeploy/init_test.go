package dockerdeploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		t.Fatal("expected stage results")
	}
	assertResultStatus(t, results, filepath.Join(deployDir, ComposeFileName), deploy.UpdateStatusUpdated)
	for _, relativePath := range []string{
		ComposeFileName,
		"democtl",
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
	if requirements != "demo-suite\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	dockerEnv := readFile(t, filepath.Join(deployDir, DockerEnvFileName))
	for _, unexpected := range []string{"DEMO_DOCKER_SUBNET", "DEMO_DOCKER_BRIDGE_NAME"} {
		if strings.Contains(dockerEnv, unexpected) {
			t.Fatalf("docker.env should not pin network internals with %s:\n%s", unexpected, dockerEnv)
		}
	}
	if strings.Contains(dockerEnv, "DEMO_APP_ENV_FILE") {
		t.Fatalf("docker.env should not point at app env files:\n%s", dockerEnv)
	}
	if !strings.Contains(dockerEnv, "REPLOY_INSTALL_OWNER=1000:1000") {
		t.Fatalf("docker.env should include blueprint install owner:\n%s", dockerEnv)
	}
	if !strings.Contains(dockerEnv, "REPLOY_INSTALL_OWNER_ON_MISSING=fail") {
		t.Fatalf("docker.env should fail on missing numeric owner:\n%s", dockerEnv)
	}
	if !strings.Contains(dockerEnv, "REPLOY_DEPLOYMENT_SCOPE=staging") {
		t.Fatalf("docker.env should identify staging scope:\n%s", dockerEnv)
	}
	if !strings.Contains(dockerEnv, "REPLOY_HOST_PORT=18075") || !strings.Contains(dockerEnv, "REPLOY_CONTAINER_PORT=18075") {
		t.Fatalf("docker.env should use install.ports.staging defaults:\n%s", dockerEnv)
	}
	compose := readFile(t, filepath.Join(deployDir, ComposeFileName))
	if !strings.Contains(compose, "REPLOY_DEPLOYMENT_SCOPE: ${REPLOY_DEPLOYMENT_SCOPE:-staging}") {
		t.Fatalf("compose should expose deployment scope to the app:\n%s", compose)
	}
	if !strings.Contains(compose, `"${REPLOY_PORT_HTTPS_HOST_BIND:-127.0.0.1}:${REPLOY_PORT_HTTPS_HOST_PORT:-18075}:${REPLOY_PORT_HTTPS_CONTAINER_PORT:-18075}"`) {
		t.Fatalf("compose should use install.ports.staging defaults:\n%s", compose)
	}
	for _, unexpected := range []string{"ipam:", "com.docker.network.bridge.name", "env_file:"} {
		if strings.Contains(compose, unexpected) {
			t.Fatalf("compose contains unexpected deployment coupling %s:\n%s", unexpected, compose)
		}
	}
	manifest := readFile(t, filepath.Join(deployDir, ManifestFileName))
	if strings.Contains(manifest, `"`+ComposeFileName+`"`) {
		t.Fatalf("manifest should not track runtime compose:\n%s", manifest)
	}
	if strings.Contains(manifest, `"requirements.txt"`) {
		t.Fatalf("requirements should be operator-owned local state:\n%s", manifest)
	}
	if !strings.Contains(manifest, `"democtl"`) {
		t.Fatalf("manifest did not track staging control script:\n%s", manifest)
	}
	if strings.Contains(manifest, `"`+ToolBinaryFileName+`"`) {
		t.Fatalf("manifest should not track a vendored reploy binary:\n%s", manifest)
	}
	if strings.Contains(manifest, `".env"`) {
		t.Fatalf("app env file should be operator-owned local state:\n%s", manifest)
	}
	helper := readFile(t, filepath.Join(deployDir, "democtl"))
	for _, want := range []string{
		`docker compose`,
		`run_compose_up`,
		`run_app_command()`,
		`REPLOY_APP_COMMAND_PREFIX=democtl`,
	} {
		if !strings.Contains(helper, want) {
			t.Fatalf("staging control script missing %q:\n%s", want, helper)
		}
	}
	if strings.Contains(helper, `command -v reploy`) || strings.Contains(helper, `exec reploy`) {
		t.Fatalf("staging control script should not invoke reploy:\n%s", helper)
	}
	if strings.Contains(helper, `"$reploy_bin" docker`) || strings.Contains(helper, `if [ "${1:-}" = "docker" ]`) {
		t.Fatalf("helper still accepts the old docker command prefix:\n%s", helper)
	}
	state := readFile(t, filepath.Join(deployDir, StateFileName))
	if !strings.Contains(state, `"target": "docker"`) || !strings.Contains(state, `"phase": "staged"`) {
		t.Fatalf("state missing target/phase:\n%s", state)
	}
}

func TestStagingControlScriptRunsComposeLifecycleAndAppCommands(t *testing.T) {
	manifest := strings.Replace(testPackManifest(), "      forward_flags:\n", "      app_command: true\n      deployed_command: true\n      forward_flags:\n", 1)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(deployDir, "democtl")

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerArgs := filepath.Join(t.TempDir(), "docker.args")
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_ARGS_FILE\"\nprintf 'docker output\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"REPLOY_COLOR=never",
	)

	statusCommand := exec.Command(script, "status")
	statusCommand.Env = env
	statusOutput, err := statusCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, statusOutput)
	}
	if !strings.Contains(string(statusOutput), "[STAGING : demo] docker output\n") {
		t.Fatalf("status output missing script prefix:\n%s", statusOutput)
	}
	statusArgs := readFile(t, dockerArgs)
	for _, want := range []string{
		"compose\n",
		"--project-name\n",
		"--project-directory\n",
		deployDir + "\n",
		"ps\n",
	} {
		if !strings.Contains(statusArgs, want) {
			t.Fatalf("status docker args missing %q:\n%s", want, statusArgs)
		}
	}

	appCommand := exec.Command(script, "config", "check", "--live")
	appCommand.Env = env
	appOutput, err := appCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("app command failed: %v\n%s", err, appOutput)
	}
	if !strings.Contains(string(appOutput), "[STAGING : demo] docker output\n") {
		t.Fatalf("app output missing script prefix:\n%s", appOutput)
	}
	appArgs := readFile(t, dockerArgs)
	for _, want := range []string{
		"run\n",
		"--rm\n",
		"--no-deps\n",
		"REPLOY_CONTAINER_COMMAND=config_check\n",
		"REPLOY_FORWARDED_ARGC=1\n",
		"REPLOY_FORWARDED_ARG_0=--live\n",
		"REPLOY_APP_COMMAND_PREFIX=democtl\n",
		"app\n",
	} {
		if !strings.Contains(appArgs, want) {
			t.Fatalf("app command docker args missing %q:\n%s", want, appArgs)
		}
	}

	colorEnv := withoutEnvKey(os.Environ(), "DEMO_COLOR")
	colorCommand := exec.Command(script, "config", "check")
	colorCommand.Env = append(colorEnv,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"REPLOY_COLOR=always",
		"TERM=xterm-256color",
		"COLUMNS=120",
	)
	colorOutput, err := colorCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("color app command failed: %v\n%s", err, colorOutput)
	}
	if !strings.Contains(string(colorOutput), "\x1b[38;5;117m[STAGING : demo]\x1b[0m docker output\n") {
		t.Fatalf("color app output missing colored staging prefix:\n%q", colorOutput)
	}
	colorArgs := readFile(t, dockerArgs)
	for _, want := range []string{
		"DEMO_COLOR=always\n",
		"COLUMNS=120\n",
	} {
		if !strings.Contains(colorArgs, want) {
			t.Fatalf("color app command docker args missing %q:\n%s", want, colorArgs)
		}
	}

	explicitColorCommand := exec.Command(script, "config", "check")
	explicitColorCommand.Env = append(colorEnv,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"REPLOY_COLOR=always",
		"DEMO_COLOR=never",
	)
	explicitColorOutput, err := explicitColorCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("explicit color app command failed: %v\n%s", err, explicitColorOutput)
	}
	explicitColorArgs := readFile(t, dockerArgs)
	if !strings.Contains(explicitColorArgs, "DEMO_COLOR=never\n") {
		t.Fatalf("explicit color app command did not preserve DEMO_COLOR:\n%s", explicitColorArgs)
	}

	neverColorCommand := exec.Command(script, "config", "check")
	neverColorCommand.Env = append(colorEnv,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"REPLOY_COLOR=never",
	)
	neverColorOutput, err := neverColorCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("never color app command failed: %v\n%s", err, neverColorOutput)
	}
	neverColorArgs := readFile(t, dockerArgs)
	if !strings.Contains(neverColorArgs, "DEMO_COLOR=never\n") {
		t.Fatalf("never color app command did not pass DEMO_COLOR=never:\n%s", neverColorArgs)
	}

	uppercaseColorCommand := exec.Command(script, "config", "check")
	uppercaseColorCommand.Env = append(colorEnv,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"REPLOY_COLOR=NEVER",
	)
	uppercaseColorOutput, err := uppercaseColorCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("uppercase color app command failed: %v\n%s", err, uppercaseColorOutput)
	}
	uppercaseColorArgs := readFile(t, dockerArgs)
	if !strings.Contains(uppercaseColorArgs, "DEMO_COLOR=never\n") {
		t.Fatalf("uppercase color app command did not normalize REPLOY_COLOR:\n%s", uppercaseColorArgs)
	}

	unknownColorCommand := exec.Command(script, "config", "check")
	unknownColorCommand.Env = append(colorEnv,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"REPLOY_COLOR=unknown",
		"NO_COLOR=1",
	)
	unknownColorOutput, err := unknownColorCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("unknown color app command failed: %v\n%s", err, unknownColorOutput)
	}
	unknownColorArgs := readFile(t, dockerArgs)
	if strings.Contains(unknownColorArgs, "DEMO_COLOR=") {
		t.Fatalf("unknown REPLOY_COLOR should not derive DEMO_COLOR:\n%s", unknownColorArgs)
	}
}

func TestStagingControlScriptCreatesMissingSingleFileConfigArtifactForAppCommand(t *testing.T) {
	deployDir := makeSingleFileConfigAppCommandDeployment(t)
	script := filepath.Join(deployDir, "democtl")

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerArgs := filepath.Join(t.TempDir(), "docker.args")
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_ARGS_FILE\"\nprintf 'docker output\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "bootstrap", "plugin", "imap")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"REPLOY_COLOR=never",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("app command failed: %v\n%s", err, output)
	}
	path := filepath.Join(deployDir, ".arbiter.env")
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("control script did not create .arbiter.env placeholder: %v", statErr)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf(".arbiter.env placeholder is not a regular file: %s", info.Mode())
	}
	if info.Size() != 0 {
		t.Fatalf(".arbiter.env placeholder should start empty, size=%d", info.Size())
	}
	appArgs := readFile(t, dockerArgs)
	for _, want := range []string{
		"run\n",
		"REPLOY_CONTAINER_COMMAND=bootstrap_plugin\n",
		"REPLOY_CONFIG_CONTAINER_DIR=/config/conf\n",
		"REPLOY_CONFIG_MOUNT=rw\n",
		"app\n",
	} {
		if !strings.Contains(appArgs, want) {
			t.Fatalf("app command docker args missing %q:\n%s", want, appArgs)
		}
	}
}

func TestStagingControlScriptChownsCreatedConfigArtifactPlaceholderWhenRunAsRoot(t *testing.T) {
	deployDir := makeSingleFileConfigAppCommandDeployment(t)
	script := filepath.Join(deployDir, "democtl")
	envValues, err := readDockerEnv(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	containerUser := envValues["REPLOY_CONTAINER_USER"]
	if containerUser == "" {
		t.Fatal("missing REPLOY_CONTAINER_USER")
	}

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerArgs := filepath.Join(t.TempDir(), "docker.args")
	chownArgs := filepath.Join(t.TempDir(), "chown.args")
	if err := os.WriteFile(filepath.Join(fakeBin, "docker"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_ARGS_FILE\"\nprintf 'docker output\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "id"), []byte("#!/bin/sh\nprintf '0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "chown"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CHOWN_ARGS_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "bootstrap", "plugin", "imap")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"CHOWN_ARGS_FILE="+chownArgs,
		"REPLOY_COLOR=never",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("app command failed: %v\n%s", err, output)
	}
	args := readFile(t, chownArgs)
	for _, want := range []string{
		containerUser + "\n",
		filepath.Join(deployDir, ".arbiter.env") + "\n",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("chown args missing %q:\n%s", want, args)
		}
	}
}

func TestStagingControlScriptUpRecoversStaleDockerNetwork(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(deployDir, "democtl")

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerArgs := filepath.Join(t.TempDir(), "docker.args")
	upCount := filepath.Join(t.TempDir(), "up.count")
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
{
  printf '%s\n' '---'
  printf '%s\n' "$@"
} >> "$DOCKER_ARGS_FILE"
is_up=0
for arg in "$@"; do
  if [ "$arg" = "up" ]; then
    is_up=1
  fi
done
if [ "$is_up" = 1 ]; then
  count=0
  if [ -f "$DOCKER_UP_COUNT_FILE" ]; then
    count="$(cat "$DOCKER_UP_COUNT_FILE")"
  fi
  count=$((count + 1))
  printf '%s\n' "$count" > "$DOCKER_UP_COUNT_FILE"
  if [ "$count" = 1 ]; then
    echo "Error response from daemon: failed to set up container networking: network b2f601ad24f6dbb403c8f25b418d314854c35d7fc33ac351355b45d12937cbb3 not found" >&2
    exit 1
  fi
fi
printf 'docker output\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(script, "up")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCKER_ARGS_FILE="+dockerArgs,
		"DOCKER_UP_COUNT_FILE="+upCount,
		"REPLOY_COLOR=never",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("up failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "network b2f601ad24f6dbb403c8f25b418d314854c35d7fc33ac351355b45d12937cbb3 not found") {
		t.Fatalf("up output did not include original stale network error:\n%s", output)
	}
	if !strings.Contains(text, "[STAGING : demo] detected stale Docker network state; running down --remove-orphans and retrying up\n") {
		t.Fatalf("up output did not explain stale network recovery:\n%s", output)
	}
	args := readFile(t, dockerArgs)
	if strings.Count(args, "\nup\n") != 2 {
		t.Fatalf("up did not retry compose up once:\n%s", args)
	}
	if !strings.Contains(args, "\ndown\n--remove-orphans\n") {
		t.Fatalf("up did not clean stale compose state before retry:\n%s", args)
	}
}

func TestStagingControlScriptPrefixesHealthOutput(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	curlArgs := filepath.Join(t.TempDir(), "curl.args")
	fakeCurl := filepath.Join(fakeBin, "curl")
	if err := os.WriteFile(fakeCurl, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CURL_ARGS_FILE\"\nprintf 'health ok\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(filepath.Join(deployDir, "democtl"), "health")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CURL_ARGS_FILE="+curlArgs,
		"REPLOY_COLOR=never",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("health failed: %v\n%s", err, output)
	}
	if string(output) != "[STAGING : demo] health ok\n" {
		t.Fatalf("health output = %q", output)
	}
	args := readFile(t, curlArgs)
	want := "-fsS\n--insecure\nhttps://127.0.0.1:18075/_health_\n"
	if args != want {
		t.Fatalf("curl args = %q, want %q", args, want)
	}
}

func TestStagingControlScriptPrefixesOutputWithoutTrailingNewline(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeDocker := filepath.Join(fakeBin, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nprintf partial"), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(filepath.Join(deployDir, "democtl"), "status")
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"REPLOY_COLOR=never",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, output)
	}
	if string(output) != "[STAGING : demo] partial\n" {
		t.Fatalf("status output = %q", output)
	}
}

func TestStagingControlScriptDoesNotEvaluateConfigDirAsShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	manifest := strings.Replace(testPackManifest(), "    config: conf\n", "    config: 'conf/$(touch "+marker+")'\n", 1)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(filepath.Join(deployDir, "democtl"), "--help")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("help failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("staging control script evaluated config dir command substitution: err=%v", err)
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

install:
  owner:
    user: mailhub
    group: mailhub
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 2525
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 12525

docker:
  deployment_dirs:
    config: config
    bundle: .reploy/bundle
    data: data
  service:
    image: python:3.11-slim
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
	hash, err := pathIdentityHash(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	stagingID := "mailhub-staging-" + hash

	dockerEnv := readFile(t, filepath.Join(deployDir, DockerEnvFileName))
	for _, expected := range []string{
		"REPLOY_CONTAINER_NAME=" + stagingID,
		"REPLOY_CONTAINER_PORT=12525",
		"REPLOY_HOST_PORT=12525",
		"REPLOY_PUBLIC_SCHEME=http",
		"REPLOY_DOCKER_NETWORK_NAME=" + stagingID,
		"REPLOY_INSTALL_OWNER=mailhub:mailhub",
		"REPLOY_INSTALL_OWNER_ON_MISSING=create",
		"MAILHUB_CONFIG_NAME=mailhub",
	} {
		if !strings.Contains(dockerEnv, expected) {
			t.Fatalf("docker.env missing %q:\n%s", expected, dockerEnv)
		}
	}
	for _, unexpected := range []string{"DEMO_", "demo-staging", "ignored-blueprint-container", "ignored-blueprint-network"} {
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
		"name: ${REPLOY_DOCKER_NETWORK_NAME:-" + stagingID + "}",
	} {
		if !strings.Contains(compose, expected) {
			t.Fatalf("compose missing %q:\n%s", expected, compose)
		}
	}
	for _, unexpected := range []string{"DEMO_", "demo-staging", "ignored-blueprint-container", "ignored-blueprint-network", "  demo:", "/source/demo"} {
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
  id: demo
  provider:
    type: python
    identifier: demo-server

install:
  owner:
    user: "1000"
    group: "1000"
  ports:
    deployed:
      https:
        host_bind: 127.0.0.1
        host_port: 8075
    staging:
      https:
        host_bind: 127.0.0.1
        host_port: 18075

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
          - ${DEMO_CONFIG_NAME}
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
          - ${DEMO_CONFIG_NAME}
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
          - ${DEMO_CONFIG_NAME}
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
	if !strings.Contains(compose, `container_command_config_check() { "custom-check" "--name" "$${DEMO_CONFIG_NAME}" "$$@"; };`) {
		t.Fatalf("compose did not render pack config_check command:\n%s", compose)
	}
	if !strings.Contains(compose, `container_command_serve() { "custom-serve" "--name" "$${DEMO_CONFIG_NAME}" "$$@"; };`) {
		t.Fatalf("compose did not render pack serve command:\n%s", compose)
	}
	if !strings.Contains(compose, `container_command_config_show() { "custom-show" "--name" "$${DEMO_CONFIG_NAME}" "$$@"; };`) {
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
	if !strings.Contains(compose, "reploy_status_start()") || !strings.Contains(compose, `printf "\r%s |" "$$reploy_status_label"`) {
		t.Fatalf("compose did not render reusable status spinner:\n%s", compose)
	}
	if strings.Contains(compose, "load_reploy_app_env_file") || strings.Contains(compose, "done < /config/.env;") {
		t.Fatalf("compose should not parse app env files; the app owns its env parser:\n%s", compose)
	}
	if !strings.Contains(compose, `reploy_status_start "Preparing Python runtime" &&`) || !strings.Contains(compose, "reploy_status_stop 0") {
		t.Fatalf("compose did not use Python runtime spinner around preparation:\n%s", compose)
	}
	if !strings.Contains(compose, "__reploy_runtime_warmup") || !strings.Contains(compose, "exit 0;\n      fi &&\n      venv_python=") {
		t.Fatalf("compose did not expose runtime warmup command before app dispatch:\n%s", compose)
	}
}

func TestInitRendersSingleFileConfigArtifactMounts(t *testing.T) {
	packDir := makeTestPackWithManifest(t, testPackManifestWithSingleFileConfigArtifact())
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")

	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	dockerEnv := readFile(t, filepath.Join(deployDir, DockerEnvFileName))
	if !strings.Contains(dockerEnv, "REPLOY_CONFIG_DIR=./conf") {
		t.Fatalf("docker.env should keep conf as the host config dir:\n%s", dockerEnv)
	}
	compose := readFile(t, filepath.Join(deployDir, ComposeFileName))
	for _, want := range []string{
		`      - "${REPLOY_CONFIG_DIR:?set REPLOY_CONFIG_DIR}:/config/conf:${REPLOY_CONFIG_MOUNT:-ro}"`,
		`      - "./.arbiter.env:/config/.arbiter.env:${REPLOY_CONFIG_MOUNT:-ro}"`,
		`container_command_config_check() { "demo-server" "--config-dir" "/config/conf" "--config-name" "$${DEMO_CONFIG_NAME}" "config" "check" "$$@"; };`,
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose missing %q:\n%s", want, compose)
		}
	}
	if strings.Contains(compose, "      - ${REPLOY_CONFIG_DIR:?set REPLOY_CONFIG_DIR}:/config:${REPLOY_CONFIG_MOUNT:-ro}") ||
		strings.Contains(compose, `      - "${REPLOY_CONFIG_DIR:?set REPLOY_CONFIG_DIR}:/config:${REPLOY_CONFIG_MOUNT:-ro}"`) {
		t.Fatalf("compose should not mount conf directly at /config when sibling config files are declared:\n%s", compose)
	}
	helper := readFile(t, filepath.Join(deployDir, "democtl"))
	for _, want := range []string{
		"REPLOY_CONFIG_CONTAINER_DIR=/config/conf",
		"validate_config_artifact_files",
		"ensure_config_artifact_files",
		"config artifact path must be a file: $target_dir/",
		"config artifact file is missing: $target_dir/",
		".arbiter.env",
	} {
		if !strings.Contains(helper, want) {
			t.Fatalf("staging control script missing %q:\n%s", want, helper)
		}
	}
}

func TestInitQuotesSingleFileConfigArtifactVolumePaths(t *testing.T) {
	manifest := strings.Replace(testPackManifestWithSingleFileConfigArtifact(), "- .arbiter.env", "- '.arbiter #1.env'", 1)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")

	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}

	compose := readFile(t, filepath.Join(deployDir, ComposeFileName))
	want := "      - " + strconv.Quote("./.arbiter #1.env:/config/.arbiter #1.env:${REPLOY_CONFIG_MOUNT:-ro}")
	if !strings.Contains(compose, want) {
		t.Fatalf("compose did not quote special config artifact volume %q:\n%s", want, compose)
	}
}

func TestInitRendersNamedDockerPorts(t *testing.T) {
	packDir := makeTestPackWithManifest(t, `blueprint:
  schema: 1
  version: 0.1.0
  requires_reploy: ">=0.1.0"

app:
  id: demo
  provider:
    type: python
    identifier: demo-server

install:
  owner:
    user: "1000"
    group: "1000"
  ports:
    deployed:
      http:
        host_bind: 127.0.0.1
        host_port: 8080
      metrics:
        host_bind: 127.0.0.1
        host_port: 9090
    staging:
      http:
        host_bind: 127.0.0.1
        host_port: 18080
        container_port: 8080
      metrics:
        host_bind: 127.0.0.1
        host_port: 19090
        container_port: 9090

docker:
  deployment_dirs:
    config: conf
    bundle: .reploy/bundle
    data: data
  health:
    scheme_env: REPLOY_PUBLIC_SCHEME
    host_env: REPLOY_PORT_HTTP_HOST_BIND
    port_env: REPLOY_PORT_HTTP_HOST_PORT
    default_scheme: http
    default_host: 127.0.0.1
    default_port: "18080"
    path: /health
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
      container:
        argv:
          - demo-server
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
		"REPLOY_HOST_PORT=18080",
		"REPLOY_CONTAINER_PORT=8080",
		"REPLOY_PORT_HTTP_HOST_BIND=127.0.0.1",
		"REPLOY_PORT_HTTP_HOST_PORT=18080",
		"REPLOY_PORT_HTTP_CONTAINER_PORT=8080",
		"REPLOY_PORT_METRICS_HOST_BIND=127.0.0.1",
		"REPLOY_PORT_METRICS_HOST_PORT=19090",
		"REPLOY_PORT_METRICS_CONTAINER_PORT=9090",
	} {
		if !strings.Contains(dockerEnv, expected) {
			t.Fatalf("docker.env missing %q:\n%s", expected, dockerEnv)
		}
	}
	compose := readFile(t, filepath.Join(deployDir, ComposeFileName))
	for _, expected := range []string{
		`      - "${REPLOY_PORT_HTTP_HOST_BIND:-127.0.0.1}:${REPLOY_PORT_HTTP_HOST_PORT:-18080}:${REPLOY_PORT_HTTP_CONTAINER_PORT:-8080}"`,
		`      - "${REPLOY_PORT_METRICS_HOST_BIND:-127.0.0.1}:${REPLOY_PORT_METRICS_HOST_PORT:-19090}:${REPLOY_PORT_METRICS_CONTAINER_PORT:-9090}"`,
	} {
		if !strings.Contains(compose, expected) {
			t.Fatalf("compose missing %q:\n%s", expected, compose)
		}
	}
}

func TestInitRejectsNamedInstallPortEnvironmentSuffixCollision(t *testing.T) {
	manifest := strings.Replace(testPackManifest(), `    staging:
      https:
        host_bind: 127.0.0.1
        host_port: 18075
`, `    staging:
      api-port:
        host_bind: 127.0.0.1
        host_port: 18080
      api_port:
        host_bind: 127.0.0.1
        host_port: 18081
`, 1)
	packDir := makeTestPackWithManifest(t, manifest)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")

	_, err = Init(InitOptions{Dir: deployDir, Pack: ref})
	if err == nil {
		t.Fatal("expected port suffix collision error")
	}
	if !strings.Contains(err.Error(), "both map to environment suffix") {
		t.Fatalf("unexpected error: %v", err)
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
	compose, err := renderComposeTemplate(pack, nil, "demo-staging-test")
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
	if err := os.WriteFile(filepath.Join(deployDir, DockerEnvFileName), []byte("existing\n"), 0o644); err != nil {
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
		Requirements: []string{"demo-server==1.2.3", "demo-smtp==1.2.3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(deployDir, RequirementsFileName)); got != "demo-server==1.2.3\ndemo-smtp==1.2.3\n" {
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
			Raw:    "pypi://demo-pkg/demo/reploy/demo.blueprint.yaml?version=1.2.3",
			Scheme: "pypi",
		},
		RequestedRef: deploy.PackRef{Raw: "pypi:demo-pkg#demo/reploy/demo.blueprint.yaml"},
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
		Requirements: []string{"demo-server"},
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

func TestUpdateMigratesExistingDockerIdentityKeys(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	localDockerEnv := strings.Join([]string{
		"# local docker env",
		"REPLOY_CONTAINER_NAME=demo-staging",
		"REPLOY_DOCKER_NETWORK_NAME=demo-staging",
		"REPLOY_BUNDLE_DIR=./custom-bundle",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(deployDir, DockerEnvFileName), []byte(localDockerEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := pathIdentityHash(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	stagingID := "demo-staging-" + hash

	results, err := Update(UpdateOptions{Dir: deployDir})
	if err != nil {
		t.Fatal(err)
	}

	assertResultStatus(t, results, filepath.Join(deployDir, DockerEnvFileName), deploy.UpdateStatusUpdated)
	dockerEnv := readFile(t, filepath.Join(deployDir, DockerEnvFileName))
	for _, expected := range []string{
		"REPLOY_CONTAINER_NAME=" + stagingID,
		"REPLOY_DOCKER_NETWORK_NAME=" + stagingID,
		"REPLOY_BUNDLE_DIR=./custom-bundle",
	} {
		if !strings.Contains(dockerEnv, expected) {
			t.Fatalf("docker.env missing %q:\n%s", expected, dockerEnv)
		}
	}
}

func TestUpdatePreservesInstalledState(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	plan := installPlan{
		TargetDir:      deployDir,
		Service:        "demo2",
		UnitPath:       "/etc/systemd/system/demo2.service",
		InstanceID:     "demo2-12345678",
		ComposeProject: "demo2-12345678",
		ContainerName:  "demo2-12345678",
		NetworkName:    "demo2-12345678",
		Ports: []dockerPortBinding{{
			Name:          "default",
			HostBind:      "127.0.0.1",
			HostPort:      "18082",
			ContainerPort: "8080",
		}},
	}
	if err := writeInstalledState(plan); err != nil {
		t.Fatal(err)
	}

	if _, err := Update(UpdateOptions{Dir: deployDir}); err != nil {
		t.Fatal(err)
	}

	state, err := loadState(deployDir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != deploy.PhaseInstalled {
		t.Fatalf("phase = %s, want %s", state.Phase, deploy.PhaseInstalled)
	}
	if state.Install == nil {
		t.Fatal("missing install state")
	}
	if state.Install.ComposeProject != "demo2-12345678" || state.Install.ContainerName != "demo2-12345678" || state.Install.NetworkName != "demo2-12345678" {
		t.Fatalf("install state = %#v", state.Install)
	}
	if state.Install.Ports["default"].HostPort != "18082" || state.Install.Ports["default"].ContainerPort != "8080" {
		t.Fatalf("install ports = %#v", state.Install.Ports)
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

func TestUpdateRejectsLocallyEditedGeneratedFileWithoutForce(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deployDir, "democtl"), []byte("local edit\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = Update(UpdateOptions{Dir: deployDir})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite locally modified generated files") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, filepath.Join(deployDir, "democtl")); got != "local edit\n" {
		t.Fatalf("control script was not preserved: %q", got)
	}

	results, err := Update(UpdateOptions{Dir: deployDir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	assertResultStatus(t, results, filepath.Join(deployDir, "democtl"), deploy.UpdateStatusUpdated)
	if got := readFile(t, filepath.Join(deployDir, "democtl")); got == "local edit\n" {
		t.Fatal("control script was not overwritten with force")
	}
}

func TestUpdateRejectsLocallyEditedRemovedGeneratedFileWithoutForce(t *testing.T) {
	packDir := makeTestPack(t)
	ref, err := deploy.ParsePackRef("file:" + packDir)
	if err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(t.TempDir(), "deployment")
	if _, err := Init(InitOptions{Dir: deployDir, Pack: ref}); err != nil {
		t.Fatal(err)
	}
	oldScript := filepath.Join(deployDir, "democtl")
	if err := os.WriteFile(oldScript, []byte("local edit\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	updatedPackDir := makeTestPackWithManifest(t, strings.Replace(testPackManifest(), "  id: demo\n", "  id: renamed\n", 1))
	updatedRef, err := deploy.ParsePackRef("file:" + updatedPackDir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Update(UpdateOptions{Dir: deployDir, Pack: updatedRef})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite locally modified generated files") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readFile(t, oldScript); got != "local edit\n" {
		t.Fatalf("old control script was not preserved: %q", got)
	}

	results, err := Update(UpdateOptions{Dir: deployDir, Pack: updatedRef, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	assertResultStatus(t, results, oldScript, deploy.UpdateStatusRemoved)
	assertResultStatus(t, results, filepath.Join(deployDir, "renamedctl"), deploy.UpdateStatusUpdated)
	if _, err := os.Stat(oldScript); !os.IsNotExist(err) {
		t.Fatalf("old control script still exists: %v", err)
	}
}

func makeTestPack(t *testing.T) string {
	t.Helper()
	return makeTestPackWithManifest(t, testPackManifest())
}

func makeTestPackWithManifest(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.blueprint.yaml"), []byte(manifest), 0o644); err != nil {
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
  id: demo
  provider:
    type: python
    identifier: demo-suite
  terminal:
    color_env: DEMO_COLOR

install:
  owner:
    user: "1000"
    group: "1000"
  ports:
    deployed:
      https:
        host_bind: 127.0.0.1
        host_port: 8075
    staging:
      https:
        host_bind: 127.0.0.1
        host_port: 18075
  upgrade:
    artifacts:
      config:
        default: preserve
        paths:
          - conf/

bundle:
  options:
    demo-suite:
      identifier: demo-suite
      group: meta
      description: Install the full Demo suite.
    imap:
      identifier: demo-imap
      group: plugins
      description: Receive email through IMAP.
    smtp:
      identifier: demo-smtp
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
          - demo-server
          - --config-dir
          - /config
          - --config-name
          - ${DEMO_CONFIG_NAME}
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
          - /config
          - --config-name
          - ${DEMO_CONFIG_NAME}
          - config
          - check
`
}

func testPackManifestWithSingleFileConfigArtifact() string {
	manifest := strings.Replace(testPackManifest(), "          - conf/\n", "          - conf/\n          - .arbiter.env\n", 1)
	return strings.ReplaceAll(manifest, "          - /config\n", "          - /config/conf\n")
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

func withoutEnvKey(values []string, key string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}
