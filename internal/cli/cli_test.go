package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	reploy "github.com/omry/reploy"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/dockerdeploy"
)

func runCLI(args ...string) (int, string, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Main(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func requireLinuxHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux/systemd-specific CLI behavior is covered by Linux CI")
	}
}

func setCLITestPackIndex(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	content := `{
  "schema_version": 1,
  "blueprints": {
    "demo-server": {
      "ref": "pypi://demo-server/demo_server/reploy/demo-server.blueprint.yaml"
    },
    "demo-suite": {
      "ref": "pypi://demo-suite/demo_suite/reploy/demo-suite.blueprint.yaml"
    }
  }
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+path)
}

func TestHelp(t *testing.T) {
	code, stdout, stderr := runCLI("--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND") {
		t.Fatalf("stdout did not contain usage:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--docker-timeout DURATION") {
		t.Fatalf("stdout did not contain Docker timeout option:\n%s", stdout)
	}
	if !strings.Contains(stdout, "index        Manage the cached blueprint shorthand index") {
		t.Fatalf("stdout did not contain index command:\n%s", stdout)
	}
	if strings.Contains(stdout, "blueprint-index") {
		t.Fatalf("stdout contained removed blueprint-index alias:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestParseGlobalDeploymentOptionsDockerTimeout(t *testing.T) {
	options, args, err := parseGlobalDeploymentOptions([]string{"--docker-timeout", "12s", "bundle", "build"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Target != "docker" {
		t.Fatalf("target = %q, want docker", options.Target)
	}
	if !options.DockerTimeoutSet || options.DockerTimeout != 12*time.Second {
		t.Fatalf("docker timeout = %s set=%v, want 12s set", options.DockerTimeout, options.DockerTimeoutSet)
	}
	if strings.Join(args, " ") != "bundle build" {
		t.Fatalf("args = %#v", args)
	}

	options, args, err = parseGlobalDeploymentOptions([]string{"--docker-timeout=250ms", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.DockerTimeoutSet || options.DockerTimeout != 250*time.Millisecond {
		t.Fatalf("docker timeout = %s set=%v, want 250ms set", options.DockerTimeout, options.DockerTimeoutSet)
	}
	if strings.Join(args, " ") != "status" {
		t.Fatalf("args = %#v", args)
	}
}

func TestParseDockerBundleOptionsBuildBackends(t *testing.T) {
	options, err := parseDockerBundleOptions([]string{"--wheelhouse-backend", "pip", "--build-backend=uv", "--dir", "stage"}, dockerBundleParseOptions{
		AllowWheelhouseBackend: true,
		AllowBuildBackend:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.WheelhouseBackend != "pip" || options.BuildBackend != "uv" || options.Dir != "stage" {
		t.Fatalf("options = %#v", options)
	}

	_, err = parseDockerBundleOptions([]string{"--wheelhouse-backend", "pip"}, dockerBundleParseOptions{})
	if err == nil || !strings.Contains(err.Error(), "unknown option: --wheelhouse-backend") {
		t.Fatalf("err = %v, want unknown wheelhouse backend option", err)
	}
}

func TestParseGlobalDeploymentOptionsRejectsInvalidDockerTimeout(t *testing.T) {
	for _, args := range [][]string{
		{"--docker-timeout"},
		{"--docker-timeout", "nope"},
		{"--docker-timeout", "0"},
		{"--docker-timeout", "-1s"},
	} {
		if _, _, err := parseGlobalDeploymentOptions(args); err == nil {
			t.Fatalf("parseGlobalDeploymentOptions(%#v) err = nil, want error", args)
		}
	}
}

func TestVersion(t *testing.T) {
	code, stdout, stderr := runCLI("--version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stdout != "reploy "+reploy.DisplayVersion()+"\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestWindowsWSLBoundaryError(t *testing.T) {
	tests := []struct {
		name string
		goos string
		env  map[string]string
		cwd  string
		want bool
	}{
		{
			name: "windows process launched from WSL distro",
			goos: "windows",
			env:  map[string]string{"WSL_DISTRO_NAME": "Ubuntu"},
			want: true,
		},
		{
			name: "windows process launched with WSL interop marker",
			goos: "windows",
			env:  map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"},
			want: true,
		},
		{
			name: "windows process in WSL localhost filesystem",
			goos: "windows",
			cwd:  `\\wsl.localhost\Ubuntu\home\omry\dev\reploy`,
			want: true,
		},
		{
			name: "windows process in legacy WSL filesystem",
			goos: "windows",
			cwd:  `\\wsl$\Ubuntu\home\omry\dev\reploy`,
			want: true,
		},
		{
			name: "windows process in extended WSL filesystem path",
			goos: "windows",
			cwd:  `\\?\UNC\wsl.localhost\Ubuntu\home\omry\dev\reploy`,
			want: true,
		},
		{
			name: "native windows shell with WSLENV only in windows filesystem",
			goos: "windows",
			env:  map[string]string{"WSLENV": "REPLOY_HOME/p"},
			cwd:  `C:\Users\omry\dev\reploy`,
			want: false,
		},
		{
			name: "linux host is not native windows",
			goos: "linux",
			env:  map[string]string{"WSL_DISTRO_NAME": "Ubuntu"},
			cwd:  "/home/omry/dev/reploy",
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := windowsWSLBoundaryError(
				test.goos,
				func(name string) (string, bool) {
					value, ok := test.env[name]
					return value, ok
				},
				func() (string, error) {
					return test.cwd, nil
				},
			)
			if (got != "") != test.want {
				t.Fatalf("windowsWSLBoundaryError() = %q, want present=%v", got, test.want)
			}
			if got != "" && !strings.Contains(got, "use the Linux reploy binary inside WSL") {
				t.Fatalf("error does not explain WSL Linux binary path: %q", got)
			}
		})
	}
}

func TestNoArgsShowsVersionAndNextSteps(t *testing.T) {
	t.Chdir(t.TempDir())

	code, stdout, stderr := runCLI()
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"reploy " + reploy.Version,
		"Usage: reploy COMMAND",
		"Next steps:",
		"reploy stage APP_REF",
		"reploy install APP_REF --scope user|system",
		"reploy index search QUERY",
		"Run 'reploy --help' for all commands.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestUsageErrorsDoNotShowGlobalOnboarding(t *testing.T) {
	t.Chdir(t.TempDir())

	tests := []struct {
		name        string
		args        []string
		wantError   string
		wantUsage   string
		forbidExtra []string
	}{
		{
			name:      "unknown top-level short option",
			args:      []string{"-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "unknown top-level long option",
			args:      []string{"--wat"},
			wantError: "reploy usage error: unknown option: --wat",
			wantUsage: "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "global target without command",
			args:      []string{"--docker"},
			wantError: "reploy usage error: expected command",
			wantUsage: "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "global timeout without command",
			args:      []string{"--docker-timeout", "5s"},
			wantError: "reploy usage error: expected command",
			wantUsage: "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "global timeout missing value",
			args:      []string{"--docker-timeout"},
			wantError: "reploy usage error: --docker-timeout requires a value",
			wantUsage: "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "unknown top-level command",
			args:      []string{"wat"},
			wantError: "reploy usage error: unknown command: wat",
			wantUsage: "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND",
			forbidExtra: []string{
				"Next steps:",
			},
		},
		{
			name:      "index command slot option",
			args:      []string{"index", "-fd"},
			wantError: "reploy index usage error: unknown option: -fd",
			wantUsage: "Usage: reploy index COMMAND",
		},
		{
			name:      "bundle command slot option",
			args:      []string{"bundle", "-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] bundle COMMAND",
		},
		{
			name:      "bundle action short option",
			args:      []string{"bundle", "build", "-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] bundle COMMAND",
		},
		{
			name:      "app command slot option",
			args:      []string{"app", "-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] app COMMAND",
		},
		{
			name:      "app option after explicit dir before command",
			args:      []string{"app", "--dir", "deployment", "-fd"},
			wantError: "reploy usage error: unknown option: -fd",
			wantUsage: "Usage: reploy [--docker-timeout DURATION] app COMMAND",
		},
	}

	globalOnboarding := []string{
		"reploy " + reploy.Version,
		"Usage: reploy COMMAND",
		"reploy stage APP_REF",
		"reploy install APP_REF --scope user|system",
		"Run 'reploy --help' for all commands.",
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tc.args...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			for _, want := range []string{tc.wantError, tc.wantUsage} {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr)
				}
			}
			for _, unexpected := range append(globalOnboarding, tc.forbidExtra...) {
				if strings.Contains(stderr, unexpected) {
					t.Fatalf("stderr contained onboarding text %q:\n%s", unexpected, stderr)
				}
			}
		})
	}
}

func TestPackIndexRefreshLoadsFileIndex(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	if err := os.WriteFile(indexPath, []byte(`{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI("index", "update", "--url", "file:"+indexPath)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "updated blueprint index\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, indexPath) || strings.Contains(stdout, "blueprint-index") || strings.Contains(stdout, "shorthands") {
		t.Fatalf("stdout leaked cache details:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRemovedBlueprintIndexAliasIsUnknown(t *testing.T) {
	code, stdout, stderr := runCLI("blueprint-index", "refresh")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "unknown command: blueprint-index") {
		t.Fatalf("stderr did not reject removed alias:\n%s", stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
}

func TestPackIndexNoArgsShowsNextSteps(t *testing.T) {
	code, stdout, stderr := runCLI("index")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		"reploy index usage error: expected command",
		"Usage: reploy index COMMAND",
		"Next steps:",
		"reploy index update",
		"reploy index search QUERY",
		"reploy index show NAME[==PIN]",
		"Run 'reploy index --help' for blueprint index help.",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestPackIndexSearchAndShow(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	if err := os.WriteFile(indexPath, []byte(`{"schema_version":1,"blueprints":{"arbiter-server":{"ref":"pypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml"},"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml"},"github-demo":{"ref":"github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	code, stdout, stderr := runCLI("index", "search", "arbiter")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "arbiter-server\tpypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	code, stdout, stderr = runCLI("index", "show", "arbiter-server")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"name: arbiter-server", "ref: pypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "resolved ref:") {
		t.Fatalf("unpinned show should not print a resolved ref:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	code, stdout, stderr = runCLI("index", "show", "arbiter-server==1.2.3")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"name: arbiter-server",
		"ref: pypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml",
		"resolved ref: pypi://arbiter-server/arbiter_server/reploy/arbiter.blueprint.yaml?version=1.2.3",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	code, stdout, stderr = runCLI("index", "show", "github-demo==feature/demo")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"name: github-demo",
		"ref: github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml",
		"resolved ref: github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml?ref=feature%2Fdemo",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestPackIndexRefreshDownloadsAndCachesHTTPIndex(t *testing.T) {
	indexContent := `{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml"}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, indexContent)
	}))
	defer server.Close()
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("REPLOY_CACHE_DIR", cacheDir)

	code, stdout, stderr := runCLI("index", "update", "--url", server.URL+"/index.json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	cachePath := packIndexCachePath(server.URL + "/index.json")
	expectedCacheDir := filepath.Dir(cachePath)
	if stdout != "updated blueprint index: "+expectedCacheDir+"\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, server.URL) || strings.Contains(stdout, "shorthands") {
		t.Fatalf("stdout leaked cache details:\n%s", stdout)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("missing cached index: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerHelp(t *testing.T) {
	code, stdout, stderr := runCLI("--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND") {
		t.Fatalf("stdout did not contain deployment usage:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--docker") || !strings.Contains(stdout, "--docker-timeout DURATION") || !strings.Contains(stdout, "--aws") {
		t.Fatalf("stdout did not contain target options:\n%s", stdout)
	}
	if strings.Contains(stdout, "smoke") {
		t.Fatalf("stdout should not contain premature smoke command:\n%s", stdout)
	}
	if strings.Contains(stdout, "Demo health endpoint") || !strings.Contains(stdout, "staging app health endpoint") {
		t.Fatalf("stdout did not describe generic health probe:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Bundle:") || !strings.Contains(stdout, "add") || !strings.Contains(stdout, "upgrade") {
		t.Fatalf("stdout did not contain bundle command tree:\n%s", stdout)
	}
	if strings.Contains(stdout, "add-wheel") || strings.Contains(stdout, "add-source") {
		t.Fatalf("stdout exposed internal bundle artifact helpers:\n%s", stdout)
	}
	if !strings.Contains(stdout, "list") || !strings.Contains(stdout, "all") || !strings.Contains(stdout, "list-options") {
		t.Fatalf("stdout did not contain bundle list commands:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--preinstall") || !strings.Contains(stdout, "--quiet") {
		t.Fatalf("stdout did not contain doctor options:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--follow") || !strings.Contains(stdout, "Follow logs instead of exiting after current output") {
		t.Fatalf("stdout did not contain logs follow option:\n%s", stdout)
	}
	if strings.Contains(stdout, "--wait") || !strings.Contains(stdout, "--timeout DURATION") {
		t.Fatalf("stdout did not contain expected test timeout options:\n%s", stdout)
	}
	for _, want := range []string{"install      Install or update a deployed host service", "--to DIR", "--scope user|system", "--port NAME=PORT", "--replace PATH", "--clean", "--in-place", "--dry-run"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing install help %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Install or update a deployed host service from staging") {
		t.Fatalf("stdout did not contain install command/options:\n%s", stdout)
	}
	if !strings.Contains(stdout, "app") {
		t.Fatalf("stdout did not contain app command:\n%s", stdout)
	}
	if strings.Contains(stdout, "bootstrap demo") || strings.Contains(stdout, "imap account") {
		t.Fatalf("stdout contained app-specific examples in generic help:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerTargetOptionUsesDefaultDeploymentCommands(t *testing.T) {
	code, stdout, stderr := runCLI("--docker", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker] [--docker-timeout DURATION] COMMAND") || !strings.Contains(stdout, "bundle") {
		t.Fatalf("stdout did not contain deployment help:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerInstallHelpShowsPortOptions(t *testing.T) {
	code, stdout, stderr := runCLI("install", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{"--scope user|system", "--port PORT", "--port NAME=PORT", "--replace PATH", "--clean", "--in-place"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout did not contain install option %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAppHelp(t *testing.T) {
	code, stdout, stderr := runCLI("app", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker-timeout DURATION] app COMMAND") {
		t.Fatalf("stdout did not contain app usage:\n%s", stdout)
	}
	if !strings.Contains(stdout, "staging bundle, not a host executable from") {
		t.Fatalf("stdout did not explain staging app runtime:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Show this staging directory's app subcommands") || !strings.Contains(stdout, "reploy app COMMAND") {
		t.Fatalf("stdout did not contain generic app command guidance:\n%s", stdout)
	}
	if strings.Contains(stdout, "Demo") || strings.Contains(stdout, "bootstrap plugin PLUGIN account NAME") {
		t.Fatalf("stdout contained app-specific help:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestParseDockerAppOptionsPreservesAppArgs(t *testing.T) {
	options, err := parseDockerAppOptions([]string{"--dir", "deployment", "bootstrap", "plugin", "imap", "account", "primary", "--force"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dir != "deployment" {
		t.Fatalf("dir = %q", options.Dir)
	}
	if got := strings.Join(options.CommandArgs, " "); got != "bootstrap plugin imap account primary --force" {
		t.Fatalf("command args = %q", got)
	}

	options, err = parseDockerAppOptions([]string{"--dir", "deployment", "config", "check"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dir != "deployment" {
		t.Fatalf("dir = %q", options.Dir)
	}
	if got := strings.Join(options.CommandArgs, " "); got != "config check" {
		t.Fatalf("command args = %q", got)
	}
}

func expectedDemoAppSummary() string {
	return "[STAGING : demo] app: demo\n" +
		"[STAGING : demo] app subcommands:\n" +
		"[STAGING : demo]   bootstrap server\n" +
		"[STAGING : demo]   bootstrap plugin\n" +
		"[STAGING : demo]   config activate\n" +
		"[STAGING : demo]   config check\n" +
		"[STAGING : demo]   config show\n" +
		"[STAGING : demo]   env bootstrap\n" +
		"[STAGING : demo]   env check\n"
}

func expectedBareDemoStagingSummary(dir string) string {
	return "[STAGING : demo] app: demo\n" +
		"[STAGING : demo] reploy: " + reploy.DisplayVersion() + "\n" +
		"[STAGING : demo] context: staged deployment\n" +
		"[STAGING : demo] directory: " + dir + "\n" +
		"[STAGING : demo] useful commands:\n" +
		"[STAGING : demo]   reploy info\n" +
		"[STAGING : demo]   reploy bundle list\n" +
		"[STAGING : demo]   reploy up|down|status\n" +
		"[STAGING : demo]   reploy logs --tail 50\n" +
		"[STAGING : demo]   reploy install --scope user --to DIR\n" +
		"[STAGING : demo] app command examples:\n" +
		"[STAGING : demo]   reploy app bootstrap server\n" +
		"[STAGING : demo]   reploy app bootstrap plugin\n" +
		"[STAGING : demo]   reploy app config activate\n" +
		"[STAGING : demo]   reploy app ...\n" +
		"[STAGING : demo] Run 'reploy app' for all app commands.\n"
}

func expectedBareDemoInstalledSummary(dir string) string {
	return "[DEPLOYED : demo] app: demo\n" +
		"[DEPLOYED : demo] reploy: " + reploy.DisplayVersion() + "\n" +
		"[DEPLOYED : demo] context: installed deployment\n" +
		"[DEPLOYED : demo] directory: " + dir + "\n" +
		"[DEPLOYED : demo] useful commands:\n" +
		"[DEPLOYED : demo]   reploy up|down|status\n" +
		"[DEPLOYED : demo]   reploy logs --tail 100\n" +
		"[DEPLOYED : demo]   reploy restart\n" +
		"[DEPLOYED : demo]   reploy uninstall --from .\n" +
		"[DEPLOYED : demo] app command examples:\n" +
		"[DEPLOYED : demo]   reploy app --deployed-only config check\n" +
		"[DEPLOYED : demo] Run 'reploy app --deployed-only' for all app commands.\n"
}

func TestAppShowsAppIDAndPackSubcommands(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("app failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedDemoAppSummary()
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAppCommandsDeployedOnlyJSON(t *testing.T) {
	manifest := strings.Replace(
		cliTestPackManifest(),
		"      app_command: true\n      forward_flags:\n        - --live\n",
		"      app_command: true\n      deployed_command: true\n      forward_flags:\n        - --live\n",
		1,
	)
	packDir := makeCLITestPackWithManifest(t, manifest)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "--commands", "--deployed-only", "--format", "json", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("app --commands failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "[STAGING") {
		t.Fatalf("json stdout should not be status-prefixed:\n%s", stdout)
	}
	var result dockerdeploy.AppCommandListResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if result.AppID != "demo" {
		t.Fatalf("app id = %q, want demo", result.AppID)
	}
	if len(result.Commands) != 1 {
		t.Fatalf("commands = %#v, want one deployed command", result.Commands)
	}
	if got := strings.Join(result.Commands[0].Trigger, " "); got != "config check" {
		t.Fatalf("deployed trigger = %q, want config check", got)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlRunsDeployedAppCommandWithScriptPrefix(t *testing.T) {
	manifest := strings.Replace(
		cliTestPackManifest(),
		"      app_command: true\n      forward_flags:\n        - --live\n",
		"      app_command: true\n      deployed_command: true\n      forward_flags:\n        - --live\n",
		1,
	)
	packDir := makeCLITestPackWithManifest(t, manifest)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerArgs := filepath.Join(t.TempDir(), "docker.args")
	fakeDockerName := "docker"
	fakeDockerContent := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOCKER_ARGS_FILE\"\nprintf 'docker output\\n'\n"
	if runtime.GOOS == "windows" {
		fakeDockerName = "docker.cmd"
		fakeDockerContent = "@echo off\r\nbreak > \"%DOCKER_ARGS_FILE%\"\r\n:loop\r\nif \"%~1\"==\"\" goto done\r\n>> \"%DOCKER_ARGS_FILE%\" echo %~1\r\nshift\r\ngoto loop\r\n:done\r\necho docker output\r\n"
	}
	fakeDocker := filepath.Join(fakeBin, fakeDockerName)
	if err := os.WriteFile(fakeDocker, []byte(fakeDockerContent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_ARGS_FILE", dockerArgs)
	t.Setenv("REPLOY_COLOR", "never")

	code, stdout, stderr = runCLI("_control", "--dir", deployDir, "--script-name", "democtl", "config", "check", "--live")
	if code != 0 {
		t.Fatalf("_control app command failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "[STAGING : demo] docker output") {
		t.Fatalf("stdout missing deployment prefix:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	content, err := os.ReadFile(dockerArgs)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.ReplaceAll(string(content), "\r\n", "\n")
	for _, want := range []string{
		"run",
		"--rm",
		"--no-deps",
		"REPLOY_CONTAINER_COMMAND",
		"config_check",
		"REPLOY_FORWARDED_ARGC",
		"1",
		"REPLOY_FORWARDED_ARG_0",
		"--live",
		"REPLOY_APP_COMMAND_PREFIX",
		"reploy app",
		"app",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("docker args missing %q:\n%s", want, args)
		}
	}
	if strings.Contains(args, "democtl") {
		t.Fatalf("_control leaked control script name into app command args:\n%s", args)
	}
}

func TestEmbeddedControlRuntimeAcceptsInstalledDeploymentDir(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	if err := os.WriteFile(filepath.Join(installDir, dockerdeploy.DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Dir != installDir {
			t.Fatalf("dir = %q, want %q", options.Dir, installDir)
		}
		if options.Action != "status" {
			t.Fatalf("action = %q, want status", options.Action)
		}
		fmt.Fprintln(options.Stdout, "installed status")
		return nil
	}

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "status")
	if code != 0 {
		t.Fatalf("_control status failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "installed status\n" {
		t.Fatalf("stdout = %q, want installed status", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlLogsHelp(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "logs", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Usage: democtl logs [OPTIONS]") {
		t.Fatalf("stdout did not contain control logs usage:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--tail N") || !strings.Contains(stdout, "default: 100") {
		t.Fatalf("stdout did not contain bounded tail help:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--tail all") || !strings.Contains(stdout, "complete available log") {
		t.Fatalf("stdout did not contain full log help:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--follow, -f") {
		t.Fatalf("stdout did not contain follow help:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlLogsDefaultsToBoundedTail(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	if err := os.WriteFile(filepath.Join(installDir, dockerdeploy.DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Action != "logs" {
			t.Fatalf("action = %q, want logs", options.Action)
		}
		if options.Dir != installDir {
			t.Fatalf("dir = %q, want %q", options.Dir, installDir)
		}
		if options.Tail != "100" {
			t.Fatalf("tail = %q, want 100", options.Tail)
		}
		if !options.Follow {
			t.Fatal("follow = false, want true")
		}
		fmt.Fprintln(options.Stdout, "installed logs")
		return nil
	}

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "logs", "--follow")
	if code != 0 {
		t.Fatalf("_control logs failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "installed logs\n" {
		t.Fatalf("stdout = %q, want installed logs", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlLogsTailAllDisablesBoundedTail(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	if err := os.WriteFile(filepath.Join(installDir, dockerdeploy.DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Tail != "" {
			t.Fatalf("tail = %q, want empty full-log tail", options.Tail)
		}
		if !options.Follow {
			t.Fatal("follow = false, want true")
		}
		return nil
	}

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "logs", "--tail", "all", "--follow")
	if code != 0 {
		t.Fatalf("_control logs failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlLogsTailAllUsesLastTailValue(t *testing.T) {
	args := embeddedControlLogsArgs([]string{"--tail", "25", "--tail=all", "--follow"})
	if got, want := strings.Join(args, " "), "--follow"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}

	args = embeddedControlLogsArgs([]string{"--tail=all", "--tail", "25", "--follow"})
	if got, want := strings.Join(args, " "), "--tail=all --tail 25 --follow"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestEmbeddedControlLogsPreservesExplicitTail(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")
	if err := os.WriteFile(filepath.Join(installDir, dockerdeploy.DockerEnvFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Tail != "25" {
			t.Fatalf("tail = %q, want 25", options.Tail)
		}
		return nil
	}

	code, stdout, stderr := runCLI("_control", "--dir", installDir, "--script-name", "democtl", "logs", "--tail=25")
	if code != 0 {
		t.Fatalf("_control logs failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEmbeddedControlHealthRejectsUnexpectedArgs(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("_control", "--dir", deployDir, "--script-name", "democtl", "health", "extra")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "health: unexpected argument: extra") {
		t.Fatalf("stderr missing unexpected argument error:\n%s", stderr)
	}
}

func TestAppFormatRequiresCommands(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "--format", "json", "--dir", deployDir)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "--format is only supported with --commands") {
		t.Fatalf("stderr did not explain --format requirement:\n%s", stderr)
	}
}

func TestAppUsesCurrentDeploymentDirByDefault(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	t.Chdir(deployDir)

	code, stdout, stderr = runCLI("app")
	if code != 0 {
		t.Fatalf("app failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedDemoAppSummary()
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNoArgsUsesDefaultStagingDirByDefault(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)

	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI()
	if code != 0 {
		t.Fatalf("no-args failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedBareDemoStagingSummary(filepath.Join(workDir, "reploy-staging"))
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNoArgsUsesCurrentDeploymentDirByDefault(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	t.Chdir(deployDir)

	code, stdout, stderr = runCLI()
	if code != 0 {
		t.Fatalf("no-args failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedBareDemoStagingSummary(deployDir)
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestNoArgsUsesInstalledDeploymentDirByDefault(t *testing.T) {
	manifest := strings.Replace(
		cliTestPackManifest(),
		"      app_command: true\n      forward_flags:\n        - --live\n",
		"      app_command: true\n      deployed_command: true\n      forward_flags:\n        - --live\n",
		1,
	)
	packDir := makeCLITestPackWithManifest(t, manifest)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	markCLITestDeploymentInstalled(t, deployDir)
	t.Chdir(deployDir)

	code, stdout, stderr = runCLI()
	if code != 0 {
		t.Fatalf("no-args failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	expected := expectedBareDemoInstalledSummary(deployDir)
	if stdout != expected {
		t.Fatalf("stdout = %q, want %q", stdout, expected)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestStagingCommandsRejectInstalledDeploymentDir(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	markCLITestDeploymentInstalled(t, deployDir)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "info", args: []string{"info", "--dir", deployDir}},
		{name: "app", args: []string{"app", "--dir", deployDir}},
		{name: "bundle", args: []string{"bundle", "list", "--dir", deployDir}},
		{name: "status", args: []string{"status", "--dir", deployDir}},
		{name: "test", args: []string{"test", "--dir", deployDir}},
		{name: "doctor", args: []string{"doctor", "--dir", deployDir}},
		{name: "install", args: []string{"install", "--dir", deployDir, "--to", filepath.Join(t.TempDir(), "target"), "--scope", "system", "--dry-run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tc.args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, "is an installed deployment") || !strings.Contains(stderr, "generated app control script") {
				t.Fatalf("stderr did not explain installed deployment rejection:\n%s", stderr)
			}
		})
	}
}

func TestAppListIsNotSpecial(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "list", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "no app command matches: list") {
		t.Fatalf("stderr did not show pack-command miss:\n%s", stderr)
	}
}

func TestAppCommandSuggestsForwardedFlagTypo(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)
	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("app", "bootstrap", "server", "--foce")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown forwarded flag: --foce") || !strings.Contains(stderr, "did you mean --force?") {
		t.Fatalf("stderr did not suggest --force:\n%s", stderr)
	}
}

func TestAWSTargetOptionIsReserved(t *testing.T) {
	code, stdout, stderr := runCLI("--aws", "up")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "deployment target aws is not supported yet") {
		t.Fatalf("stderr missing unsupported target message:\n%s", stderr)
	}
}

func TestRemovedInitCommandIsUnknown(t *testing.T) {
	code, stdout, stderr := runCLI("init", "demo-suite")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: init") {
		t.Fatalf("stderr did not reject removed init command:\n%s", stderr)
	}
}

func TestDockerStageHelp(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{
		"Usage: reploy [--docker] [--docker-timeout DURATION] stage APP_REF [OPTIONS]",
		"reploy [--docker] [--docker-timeout DURATION] stage --update [APP_REF] [OPTIONS]",
		"Create a staging directory from an app blueprint reference.",
		"Use --update to refresh an existing staging directory",
		"Indexed shorthand from the Reploy blueprint index:",
		"arbiter-server==0.4.2",
		"Local filesystem refs:",
		"./PATH",
		"/ABS/PATH",
		"file:PATH",
		"Python provider refs:",
		"pypi://PACKAGE/PATH/APP.blueprint.yaml",
		"pypi://PACKAGE/PATH/APP.blueprint.yaml?version=VERSION",
		"Git provider refs:",
		"github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF",
		"github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF&transport=ssh",
		"Local paths without file: must start with . or /.",
		"PyPI paths must point to the blueprint file inside the package.",
		"GitHub paths must point to the blueprint file inside the repository.",
		"--dir DIR",
		"--update",
		"--force",
		"--verbose",
		"Show generated file update details",
		"Python provider options:",
		"--requirement REQ",
		"Exact Python package pin or absolute container path for requirements.txt",
		"Show stage help",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "init") {
		t.Fatalf("stage help contained old init wording:\n%s", stdout)
	}
	for _, hidden := range []string{"git:https://HOST/REPO.git", "git:https://github.com"} {
		if strings.Contains(stdout, hidden) {
			t.Fatalf("stage help exposed hidden git ref %q:\n%s", hidden, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerLogsHelp(t *testing.T) {
	code, stdout, stderr := runCLI("logs", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker] logs [OPTIONS]") {
		t.Fatalf("stdout did not contain logs usage:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--follow, -f") || !strings.Contains(stdout, "Follow logs instead of exiting after current output") {
		t.Fatalf("stdout did not contain logs follow option:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--tail N") || !strings.Contains(stdout, "Show only the last N log lines") {
		t.Fatalf("stdout did not contain logs tail option:\n%s", stdout)
	}
	if strings.Contains(stdout, "Commands:") || strings.Contains(stdout, "bundle       Manage staging bundle contents") {
		t.Fatalf("stdout showed global help instead of logs help:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerUpdateCommandRemoved(t *testing.T) {
	code, stdout, stderr := runCLI("update")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: update") {
		t.Fatalf("stderr did not reject removed update command:\n%s", stderr)
	}
}

func TestDockerLogsOptionsParse(t *testing.T) {
	options, err := parseDockerRuntimeOptions([]string{"--dir", "deployment", "--tail", "100", "--follow", "--verbose"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dir != "deployment" {
		t.Fatalf("dir = %q", options.Dir)
	}
	if !options.Follow {
		t.Fatal("follow = false, want true")
	}
	if options.Tail != "100" {
		t.Fatalf("tail = %q, want 100", options.Tail)
	}
	if !options.Verbose {
		t.Fatal("verbose = false, want true")
	}

	options, err = parseDockerRuntimeOptions([]string{"--tail=25"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Tail != "25" {
		t.Fatalf("tail = %q, want 25", options.Tail)
	}
}

func TestRuntimeLifecycleActionsShowSpinnerWhenNotVerbose(t *testing.T) {
	for _, action := range []string{"up", "restart", "down"} {
		if !runtimeActionShowsSpinner(action, false) {
			t.Fatalf("%s should show a spinner when not verbose", action)
		}
		if runtimeActionShowsSpinner(action, true) {
			t.Fatalf("%s should not show a spinner when verbose", action)
		}
	}
	for _, action := range []string{"ps", "status", "logs"} {
		if runtimeActionShowsSpinner(action, false) {
			t.Fatalf("%s should stream output instead of showing a spinner", action)
		}
	}
}

func TestRuntimeSpinnerLabelUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	label, err := runtimeSpinnerLabel(deployDir, "up", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if label != "[STAGING : demo] up" {
		t.Fatalf("label = %q", label)
	}
}

func TestDeploymentSpinnerLabelUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	label, err := deploymentSpinnerLabel(deployDir, "validating installation bundle", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if label != "[STAGING : demo] validating installation bundle" {
		t.Fatalf("label = %q", label)
	}
}

func TestDeploymentSpinnerLabelUsesInstalledPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")

	label, err := deploymentSpinnerLabel(installDir, "uninstalling", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if label != "[DEPLOYED : demo] uninstalling" {
		t.Fatalf("label = %q", label)
	}
}

func TestDeploymentStdoutOrFallbackPrefixesInstalledOutput(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")

	var stdout bytes.Buffer
	writer := deploymentStdoutOrFallback(installDir, &stdout)
	fmt.Fprintln(writer, "server url: https://127.0.0.1:8075")
	if got, want := stdout.String(), "[DEPLOYED : demo] server url: https://127.0.0.1:8075\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestPhaseKnownTestErrorUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldTestServer := dockerTestServer
	t.Cleanup(func() {
		dockerTestServer = oldTestServer
	})
	dockerTestServer = func(options dockerdeploy.TestOptions) error {
		if options.Dir != deployDir {
			t.Fatalf("dir = %q, want %q", options.Dir, deployDir)
		}
		return errors.New("service is not started; run reploy up before testing health")
	}

	code, stdout, stderr = runCLI("test", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "[STAGING : demo] reploy test error: service is not started; run reploy up before testing health") {
		t.Fatalf("stderr missing staging-prefixed test error:\n%s", stderr)
	}
}

func TestPhaseKnownRuntimeErrorUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Action != "status" {
			t.Fatalf("action = %q, want status", options.Action)
		}
		return errors.New("runtime exploded")
	}

	code, stdout, stderr = runCLI("status", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "[STAGING : demo] reploy status error: runtime exploded") {
		t.Fatalf("stderr missing staging-prefixed runtime error:\n%s", stderr)
	}
}

func TestPhaseKnownRuntimePrepareHintUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Action != "up" {
			t.Fatalf("action = %q, want up", options.Action)
		}
		return errors.New("prepare installation bundle: docker failed: exit status 1")
	}

	code, stdout, stderr = runCLI("up", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		"[STAGING : demo] up",
		"[STAGING : demo] reploy up error: prepare installation bundle: docker failed: exit status 1",
		"[STAGING : demo] next step: run `reploy bundle build --verbose --dir " + shellQuoteArg(deployDir) + "` to inspect and fix the bundle build, then rerun `reploy up`.",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestRuntimeBundleBuildVerboseCommandQuotesExplicitDir(t *testing.T) {
	got := runtimeBundleBuildVerboseCommand("/tmp/reploy staging/it's live", true)
	want := `reploy bundle build --verbose --dir '/tmp/reploy staging/it'"'"'s live'`
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestDockerTimeoutAppliesDuringDockerCommand(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldRuntime := dockerRuntime
	t.Cleanup(func() {
		dockerRuntime = oldRuntime
	})
	dockerRuntime = func(options dockerdeploy.RuntimeOptions) error {
		if options.Action != "status" {
			t.Fatalf("action = %q, want status", options.Action)
		}
		if got := options.DockerPreflightTimeout; got != 11*time.Second {
			t.Fatalf("DockerPreflightTimeout = %s, want 11s", got)
		}
		return nil
	}

	code, stdout, stderr = runCLI("--docker-timeout", "11s", "status", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("status failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestBundleErrorHasEnoughOutputForDockerPreflight(t *testing.T) {
	for _, err := range []error{
		errors.New("docker daemon did not respond within 5s"),
		errors.New("docker daemon check failed: exit status 1"),
	} {
		if !bundleErrorHasEnoughOutput(err) {
			t.Fatalf("%v should not suggest rerunning with --verbose", err)
		}
	}
}

func TestDockerRuntimeRejectsFollowOutsideLogs(t *testing.T) {
	code, stdout, stderr := runCLI("ps", "--follow")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "--follow is only supported with logs") {
		t.Fatalf("stderr missing follow validation message:\n%s", stderr)
	}
}

func TestDockerRuntimeRejectsTailOutsideLogs(t *testing.T) {
	code, stdout, stderr := runCLI("ps", "--tail", "100")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "--tail is only supported with logs") {
		t.Fatalf("stderr missing tail validation message:\n%s", stderr)
	}
}

func TestDockerTestTimeoutOptionParses(t *testing.T) {
	options, err := parseDockerTestOptions([]string{"--dir", "deployment", "--timeout", "2s"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Dir != "deployment" {
		t.Fatalf("dir = %q", options.Dir)
	}
	if options.Timeout != 2*time.Second {
		t.Fatalf("timeout = %s", options.Timeout)
	}
}

func TestDockerTestRejectsWaitOption(t *testing.T) {
	code, stdout, stderr := runCLI("test", "--wait")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown option: --wait") {
		t.Fatalf("stderr missing wait validation message:\n%s", stderr)
	}
}

func TestDockerDoctorScopeOptionsParse(t *testing.T) {
	options, err := parseDockerDoctorOptions([]string{
		"--preinstall",
		"--scope",
		"user",
		"--dir",
		"deployment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !options.Preinstall || options.Scope != dockerdeploy.InstallScopeUser || options.Dir != "deployment" {
		t.Fatalf("doctor options = %#v", options)
	}

	options, err = parseDockerDoctorOptions([]string{"--preinstall", "--scope=system"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Scope != dockerdeploy.InstallScopeSystem {
		t.Fatalf("scope = %q, want system", options.Scope)
	}
}

func TestDockerDoctorScopeRequiresPreinstall(t *testing.T) {
	if _, err := parseDockerDoctorOptions([]string{"--scope", "user"}); err == nil || !strings.Contains(err.Error(), "--scope requires --preinstall") {
		t.Fatalf("error = %v, want scope/preinstall validation", err)
	}
}

func TestDockerInstallPortOptionsParse(t *testing.T) {
	options, err := parseDockerInstallOptions([]string{
		"--dir", "deployment",
		"--to", "/opt/demo2",
		"--scope", "system",
		"--service", "demo2",
		"--port", "http=18082",
		"--port=metrics=19092",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Target != "/opt/demo2" || options.Service != "demo2" {
		t.Fatalf("target/service = %q/%q", options.Target, options.Service)
	}
	if options.Scope != dockerdeploy.InstallScopeSystem {
		t.Fatalf("scope = %q, want system", options.Scope)
	}
	if len(options.PortOverrides) != 2 {
		t.Fatalf("port overrides = %#v", options.PortOverrides)
	}
	if options.PortOverrides[0].Name != "http" || options.PortOverrides[0].HostPort != "18082" {
		t.Fatalf("first override = %#v", options.PortOverrides[0])
	}
	if options.PortOverrides[1].Name != "metrics" || options.PortOverrides[1].HostPort != "19092" {
		t.Fatalf("second override = %#v", options.PortOverrides[1])
	}

	options, err = parseDockerInstallOptions([]string{"--to", "/opt/demo2", "--scope", "system", "--port", "18082"})
	if err != nil {
		t.Fatal(err)
	}
	if len(options.PortOverrides) != 1 || options.PortOverrides[0].Name != "" || options.PortOverrides[0].HostPort != "18082" {
		t.Fatalf("shorthand override = %#v", options.PortOverrides)
	}

	options, err = parseDockerInstallOptions([]string{"pypi:demo-server#demo_server/reploy/demo-server.blueprint.yaml", "--scope=user", "--dry-run", "--in-place", "--replace", "conf", "--replace=.env", "--clean"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "pypi:demo-server#demo_server/reploy/demo-server.blueprint.yaml" || !options.DryRun || !options.InPlace || !options.Clean {
		t.Fatalf("direct install options = %#v", options)
	}
	if options.Scope != dockerdeploy.InstallScopeUser {
		t.Fatalf("scope = %q, want user", options.Scope)
	}
	if strings.Join(options.Replace, ",") != "conf,.env" {
		t.Fatalf("replace = %#v", options.Replace)
	}
}

func TestDockerInstallScopeIsRequired(t *testing.T) {
	_, err := parseDockerInstallOptions([]string{"--to", "/opt/demo"})
	if err == nil {
		t.Fatal("expected missing scope error")
	}
	if !strings.Contains(err.Error(), "--scope is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerInstallRejectsInvalidScope(t *testing.T) {
	_, err := parseDockerInstallOptions([]string{"--to", "/opt/demo", "--scope", "default"})
	if err == nil {
		t.Fatal("expected invalid scope error")
	}
	if !strings.Contains(err.Error(), "--scope must be user or system") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePackRefArgumentSupportsPyPIHashBlueprintPath(t *testing.T) {
	ref, err := parsePackRefArgument("pypi:demo-pkg#demo_pkg/reploy/app.blueprint.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "pypi" || ref.Source != "demo-pkg" || ref.Subdir != "demo_pkg/reploy/app.blueprint.yaml" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParsePackRefArgumentSupportsBareLocalPaths(t *testing.T) {
	absolutePath := filepath.Join(t.TempDir(), "demo.blueprint.yaml")
	for _, value := range []string{"./demo.blueprint.yaml", "../demo", absolutePath} {
		ref, err := parsePackRefArgument(value)
		if err != nil {
			t.Fatalf("parse %q: %v", value, err)
		}
		if ref.Scheme != "file" || ref.Source != value || ref.Raw != value {
			t.Fatalf("ref for %q = %#v", value, ref)
		}
	}
}

func TestParsePackRefArgumentDoesNotGuessPlainPath(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	if err := os.WriteFile(indexPath, []byte(`{"schema_version":1,"blueprints":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	_, err := parsePackRefArgument("demo/path")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `unknown blueprint shorthand "demo/path"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseDockerCommandOptionsWarnsWhenShorthandMatchesLocalPath(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	if err := os.Mkdir("demo-server", 0o755); err != nil {
		t.Fatal(err)
	}
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-server"}, true, dockerCommandParseConfig{AllowUpdate: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Warnings) != 1 {
		t.Fatalf("warnings = %#v", options.Warnings)
	}
	for _, want := range []string{`APP_REF "demo-server" also exists as a local path`, "treating it as a blueprint shorthand", "Use ./demo-server or file:demo-server"} {
		if !strings.Contains(options.Warnings[0], want) {
			t.Fatalf("warning missing %q:\n%s", want, options.Warnings[0])
		}
	}
	if options.Pack.Raw != "demo-server" || options.Pack.Scheme != "pypi" {
		t.Fatalf("pack = %#v", options.Pack)
	}
}

func TestParsePackRefArgumentSupportsGitHTTPSRef(t *testing.T) {
	ref, err := parsePackRefArgument("git:https://github.com/acme/demo.git#demo_pkg/reploy?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != "git" || ref.Source != "https://github.com/acme/demo.git" || ref.Subdir != "demo_pkg/reploy" {
		t.Fatalf("ref = %#v", ref)
	}
	if ref.Query.Get("ref") != "main" {
		t.Fatalf("query = %#v", ref.Query)
	}
}

func TestParsePackRefArgumentSupportsGitHubRef(t *testing.T) {
	raw := "github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml?ref=main"
	ref, err := parsePackRefArgument(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Raw != raw || ref.Scheme != "git" || ref.Source != "https://github.com/acme/demo.git" || ref.Subdir != "demo_pkg/reploy/demo.blueprint.yaml" {
		t.Fatalf("ref = %#v", ref)
	}
	if ref.Query.Get("ref") != "main" {
		t.Fatalf("query = %#v", ref.Query)
	}
}

func TestDockerStageGitHubRefErrorDoesNotExposeInternalGitRef(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "github://acme/demo/demo_pkg/reploy?ref=main")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "github blueprint path must point to a *.blueprint.yaml file") {
		t.Fatalf("stderr did not contain github-facing error:\n%s", stderr)
	}
	for _, leaked := range []string{
		"git:",
		"https://github.com/acme/demo.git",
		"ssh://git@github.com/acme/demo.git",
		"git blueprint",
	} {
		if strings.Contains(stderr, leaked) {
			t.Fatalf("stderr exposed internal git representation %q:\n%s", leaked, stderr)
		}
	}
}

func TestDirectInstallFileRefDryRunUsesBlueprintDefaults(t *testing.T) {
	requireLinuxHost(t)
	packDir := makeCLITestPack(t)
	code, stdout, stderr := runCLI("install", "file:"+packDir, "--scope", "system", "--dry-run", "--no-start")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"would install deployment:",
		"target: /opt/demo",
		"service: demo",
		"port https: 127.0.0.1:8075 -> 8075",
		"start: no",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDirectInstallInPlaceDryRunUsesRequestedTarget(t *testing.T) {
	requireLinuxHost(t)
	packDir := makeCLITestPack(t)
	target := filepath.Join(t.TempDir(), "installed")
	code, stdout, stderr := runCLI("install", "file:"+packDir, "--to", target, "--scope", "system", "--in-place", "--dry-run", "--no-start")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "target: "+target) || !strings.Contains(stdout, "start: no") {
		t.Fatalf("stdout did not show requested target dry-run:\n%s", stdout)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create in-place target, stat err=%v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDirectInstallPrintsSuccessFromResolvedDefaultTarget(t *testing.T) {
	oldDirectInstall := dockerDirectInstall
	oldPrintInstallSuccess := dockerPrintInstallSuccess
	t.Cleanup(func() {
		dockerDirectInstall = oldDirectInstall
		dockerPrintInstallSuccess = oldPrintInstallSuccess
	})

	dockerDirectInstall = func(options dockerdeploy.DirectInstallOptions) (string, error) {
		if options.Target != "" {
			t.Fatalf("target option = %q, want empty default target", options.Target)
		}
		if options.Scope != dockerdeploy.InstallScopeSystem {
			t.Fatalf("scope = %q, want system", options.Scope)
		}
		return "/opt/demo", nil
	}
	dockerPrintInstallSuccess = func(dir string, stdout io.Writer, dockerPreflightTimeout time.Duration) error {
		if dir != "/opt/demo" {
			t.Fatalf("success dir = %q, want resolved default target", dir)
		}
		if dockerPreflightTimeout != time.Second {
			t.Fatalf("success docker timeout = %s, want 1s", dockerPreflightTimeout)
		}
		fmt.Fprintln(stdout, "installed at "+dir)
		return nil
	}

	code, stdout, _ := runCLI("--docker-timeout", "1s", "install", "file:/does/not/need/to/exist", "--scope", "system")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "installed at /opt/demo") {
		t.Fatalf("stdout missing success output:\n%s", stdout)
	}
}

func TestStagedInstallSpinnerUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	t.Setenv("TERM", "xterm-256color")
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldDockerInstall := dockerInstall
	t.Cleanup(func() {
		dockerInstall = oldDockerInstall
	})
	dockerInstall = func(options dockerdeploy.InstallOptions) error {
		if options.Dir != deployDir {
			t.Fatalf("install dir = %q, want %q", options.Dir, deployDir)
		}
		if options.Scope != dockerdeploy.InstallScopeSystem {
			t.Fatalf("scope = %q, want system", options.Scope)
		}
		fmt.Fprintln(options.Progress, "running before start hook: app config check")
		return nil
	}

	code, _, stderr = runCLI("install", "--dir", deployDir, "--scope", "system")
	if code != 0 {
		t.Fatalf("install failed: code=%d\nstderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "[STAGING : demo] installing") {
		t.Fatalf("stderr missing deployment-scoped install spinner:\n%s", stderr)
	}
	if strings.Contains(stderr, "installing from staging") {
		t.Fatalf("stderr should not use generic staged install label:\n%s", stderr)
	}
}

func TestStagedInstallDryRunUsesDeploymentPrefix(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	packDir := makeCLITestPack(t)
	tempDir := t.TempDir()
	deployDir := filepath.Join(tempDir, "deployment")
	targetDir := filepath.Join(tempDir, "installed")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	oldDockerInstall := dockerInstall
	t.Cleanup(func() {
		dockerInstall = oldDockerInstall
	})
	dockerInstall = func(options dockerdeploy.InstallOptions) error {
		if options.Dir != deployDir {
			t.Fatalf("install dir = %q, want %q", options.Dir, deployDir)
		}
		if options.Target != targetDir {
			t.Fatalf("install target = %q, want %q", options.Target, targetDir)
		}
		if options.Scope != dockerdeploy.InstallScopeSystem {
			t.Fatalf("scope = %q, want system", options.Scope)
		}
		if !options.DryRun {
			t.Fatal("install should be dry-run")
		}
		fmt.Fprintln(options.Stdout, "would install deployment: "+options.Dir)
		return nil
	}

	code, stdout, stderr = runCLI("install", "--dir", deployDir, "--to", targetDir, "--scope", "system", "--dry-run", "--no-start")
	if code != 0 {
		t.Fatalf("install dry-run failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "[STAGING : demo] would install deployment: "+deployDir) {
		t.Fatalf("stdout missing deployment-prefixed dry-run plan:\n%s", stdout)
	}
	if strings.Contains(stdout, "\nwould install deployment:") {
		t.Fatalf("stdout contains unprefixed dry-run line:\n%s", stdout)
	}
}

func TestDockerUninstallOptionsParse(t *testing.T) {
	options, err := parseDockerUninstallOptions([]string{
		"--from", "/opt/demo2",
		"--service-name", "demo2",
		"--remove-dir",
		"--dry-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.From != "/opt/demo2" || options.ServiceName != "demo2" {
		t.Fatalf("from/service-name = %q/%q", options.From, options.ServiceName)
	}
	if !options.RemoveDir || !options.DryRun {
		t.Fatalf("remove-dir/dry-run = %v/%v", options.RemoveDir, options.DryRun)
	}

	options, err = parseDockerUninstallOptions([]string{"--from=/opt/demo3", "--service-name=demo3"})
	if err != nil {
		t.Fatal(err)
	}
	if options.From != "/opt/demo3" || options.ServiceName != "demo3" {
		t.Fatalf("from/service-name = %q/%q", options.From, options.ServiceName)
	}

	_, err = parseDockerUninstallOptions([]string{"--list-services"})
	if err == nil || !strings.Contains(err.Error(), "unknown option: --list-services") {
		t.Fatalf("expected unknown list-services option, got %v", err)
	}
}

func TestServicesListRunsSystemdServiceInventory(t *testing.T) {
	oldPrint := printReploySystemdServices
	t.Cleanup(func() {
		printReploySystemdServices = oldPrint
	})
	printReploySystemdServices = func(stdout io.Writer) error {
		fmt.Fprintln(stdout, "SERVICE\tTARGET")
		fmt.Fprintln(stdout, "demo\t/opt/demo")
		return nil
	}

	code, stdout, stderr := runCLI("services", "list")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "demo\t/opt/demo") {
		t.Fatalf("stdout missing services list:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerUninstallRequiresRootBeforeSpinner(t *testing.T) {
	requireLinuxHost(t)
	if os.Geteuid() == 0 {
		t.Skip("root test environment cannot exercise non-root CLI path")
	}
	code, stdout, stderr := runCLI("uninstall", "--service-name", "demo")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		"root privileges are required",
		"rerun with sudo",
		"--dry-run",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "uninstalling deployment") {
		t.Fatalf("stderr should not contain spinner output:\n%s", stderr)
	}
}

func TestDockerUninstallAnimatedSpinnerKeepsDeploymentOutputSeparate(t *testing.T) {
	t.Setenv("REPLOY_COLOR", "never")
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	installDir := filepath.Join(t.TempDir(), "installed")
	writeCLITestInstalledState(t, installDir, "demo", "demo-service")

	oldDockerUninstall := dockerUninstall
	oldDockerUninstallNeedsRoot := dockerUninstallNeedsRoot
	t.Cleanup(func() {
		dockerUninstall = oldDockerUninstall
		dockerUninstallNeedsRoot = oldDockerUninstallNeedsRoot
	})
	dockerUninstallNeedsRoot = func(dockerdeploy.UninstallOptions) bool {
		return false
	}
	dockerUninstall = func(options dockerdeploy.UninstallOptions) error {
		if options.From != installDir {
			t.Fatalf("from = %q, want %q", options.From, installDir)
		}
		fmt.Fprintln(options.Stdout, "uninstalled service: demo")
		return nil
	}

	code, stdout, stderr := runCLI("uninstall", "--from", installDir, "--remove-dir")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty while animated spinner owns uninstall output", stdout)
	}
	if !strings.Contains(stderr, "[DEPLOYED : demo] uninstalled service: demo\n") {
		t.Fatalf("stderr missing deployed uninstall output:\n%q", stderr)
	}
	if !strings.Contains(stderr, "[DEPLOYED : demo] uninstalling... done") {
		t.Fatalf("stderr missing spinner completion:\n%q", stderr)
	}
	if strings.Contains(stderr, "\\[DEPLOYED") ||
		strings.Contains(stderr, "/[DEPLOYED") ||
		strings.Contains(stderr, "|[DEPLOYED") ||
		strings.Contains(stderr, "-[DEPLOYED") {
		t.Fatalf("spinner frame collided with deployment output:\n%q", stderr)
	}
}

func TestDockerInitWritesDeployment(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "created staging directory for demo: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
	if strings.Contains(stdout, "updated ") {
		t.Fatalf("stdout should not include generated file updates without --verbose:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if _, err := os.Stat(filepath.Join(deployDir, dockerdeploy.StateFileName)); err != nil {
		t.Fatalf("missing state: %v", err)
	}
}

func TestDockerInitVerboseReportsGeneratedFiles(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--verbose", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "created staging directory for demo: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
	if !strings.Contains(stdout, "updated "+filepath.Join(deployDir, dockerdeploy.ComposeFileName)) {
		t.Fatalf("stdout did not include compose write:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerInitUsesDefaultDeploymentDir(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)

	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	deployDir := filepath.Join(workDir, "reploy-staging")
	if _, err := os.Stat(filepath.Join(deployDir, dockerdeploy.StateFileName)); err != nil {
		t.Fatalf("missing state in default deployment dir: %v", err)
	}
	if !strings.Contains(stdout, "created staging directory for demo: reploy-staging") {
		t.Fatalf("stdout did not include default staging summary:\n%s", stdout)
	}
	if strings.Contains(stdout, "updated ") {
		t.Fatalf("stdout should not include generated file updates without --verbose:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerInitAcceptsBareDotRelativePath(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	t.Chdir(packDir)

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, ".")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(deployDir, dockerdeploy.StateFileName)); err != nil {
		t.Fatalf("missing state: %v", err)
	}
	if !strings.Contains(stdout, "created staging directory for demo: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerInitWarnsWhenShorthandMatchesLocalPath(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)
	if err := os.Mkdir("demo", 0o755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(t.TempDir(), "reploy-blueprint-index.json")
	indexContent := fmt.Sprintf(`{"schema_version":1,"blueprints":{"demo":{"ref":%q}}}`, "file:"+packDir)
	if err := os.WriteFile(indexPath, []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "demo")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, `reploy warning: APP_REF "demo" also exists as a local path`) {
		t.Fatalf("stderr missing shorthand/local path warning:\n%s", stderr)
	}
	if !strings.Contains(stdout, "created staging directory for demo: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
}

func TestDockerInitExistingDefaultDeploymentSuggestsUpdate(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)

	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("initial stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("stage", "file:"+packDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "staging directory already exists at reploy-staging") {
		t.Fatalf("stderr missing existing deployment message:\n%s", stderr)
	}
	if !strings.Contains(stderr, "use --update to update it") {
		t.Fatalf("stderr missing update hint:\n%s", stderr)
	}
}

func TestDockerInitRequiresPack(t *testing.T) {
	code, stdout, stderr := runCLI("stage")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "APP_REF is required") {
		t.Fatalf("stderr did not contain required blueprint message:\n%s", stderr)
	}
	for _, want := range []string{
		"Usage: reploy [--docker] [--docker-timeout DURATION] stage APP_REF [OPTIONS]",
		"arbiter-server==VERSION",
		"pypi://PACKAGE/PATH/APP.blueprint.yaml",
		"pypi://PACKAGE/PATH/APP.blueprint.yaml?version=VERSION",
		"github://ORG/REPO/PATH/APP.blueprint.yaml?ref=REF",
		"./PATH",
		"/ABS/PATH",
		"file:PATH",
		"GitHub paths must point to the blueprint file inside the repository.",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	for _, stale := range []string{"source:PATH"} {
		if strings.Contains(stderr, stale) {
			t.Fatalf("stderr contains stale ref guidance %q:\n%s", stale, stderr)
		}
	}
}

func TestDockerInitValidatesPack(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "oci:example")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unsupported blueprint reference scheme: oci") {
		t.Fatalf("stderr did not contain blueprint validation message:\n%s", stderr)
	}
}

func TestDockerInitRejectsRemovedBlueprintFlag(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "--blueprint", "demo-suite")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown option: --blueprint") {
		t.Fatalf("stderr did not reject removed --blueprint flag:\n%s", stderr)
	}
}

func TestDockerInitAcceptsExplicitRequirements(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI(
		"stage",
		"--dir",
		deployDir,
		"file:"+packDir,
		"--requirement",
		"demo-server==1.2.3",
		"--requirement=demo-imap==1.2.3",
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	requirements, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-server==1.2.3\ndemo-imap==1.2.3\n" {
		t.Fatalf("requirements = %q", requirements)
	}
}

func TestDockerStageAcceptsSourcePackRef(t *testing.T) {
	sourceDir := makeCLITestSourcePack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "source:"+sourceDir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	requirements, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-suite\n/source/app/demo-suite\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	compose, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.ComposeFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), strconv.Quote(sourceDir+":/source/app/demo-suite:rw")) {
		t.Fatalf("compose did not mount source checkout:\n%s", compose)
	}
}

func TestDockerStageAcceptsGitPackRef(t *testing.T) {
	sourceDir, commit := makeCLITestGitSourcePack(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("REPLOY_CACHE_DIR", cacheDir)
	sourceURL := localFileURL(sourceDir)
	deployDir := filepath.Join(t.TempDir(), "deployment")

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "git:"+sourceURL+"?ref=main")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	requirements, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "setuptools>=68\nwheel\ngit-source-app\n/source/app/git-source-app\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	stateContent, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var state deploy.DeploymentState
	if err := json.Unmarshal(stateContent, &state); err != nil {
		t.Fatal(err)
	}
	expectedRequestedRef := "git:" + sourceURL + "?ref=main"
	expectedResolvedRef := "git:" + sourceURL + "#git_source_app/reploy?ref=" + commit
	if state.RequestedBlueprintRef != expectedRequestedRef {
		t.Fatalf("requested blueprint ref = %q, want %q", state.RequestedBlueprintRef, expectedRequestedRef)
	}
	if state.Blueprint.Raw != expectedResolvedRef || !state.Blueprint.IsPinned {
		t.Fatalf("state blueprint = %#v, want pinned %q", state.Blueprint, expectedResolvedRef)
	}
	if state.ResolvedArtifact == nil || state.ResolvedArtifact.Scheme != "git" || state.ResolvedArtifact.Version != commit {
		t.Fatalf("resolved artifact = %#v", state.ResolvedArtifact)
	}
	if !strings.HasPrefix(state.ResolvedArtifact.CachePath, cacheDir) {
		t.Fatalf("cache path = %q, want under %q", state.ResolvedArtifact.CachePath, cacheDir)
	}
	compose, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.ComposeFileName))
	if err != nil {
		t.Fatal(err)
	}
	expectedMount := strconv.Quote(state.ResolvedArtifact.CachePath + ":/source/app/git-source-app:rw")
	if !strings.Contains(string(compose), expectedMount) {
		t.Fatalf("compose did not mount cached git checkout %q:\n%s", expectedMount, compose)
	}
}

func TestParseDockerCommandOptionsAcceptsExplicitPyPIPackageRef(t *testing.T) {
	options, err := parseDockerCommandOptions([]string{"pypi:demo-suite==1.2.3#demo_suite/reploy/demo-suite.blueprint.yaml"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "pypi:demo-suite==1.2.3#demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Scheme != "pypi" {
		t.Fatalf("scheme = %q", options.Pack.Scheme)
	}
	if options.Pack.Source != "demo-suite==1.2.3" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if !options.Pack.IsPinned {
		t.Fatal("pinned pypi ref should be pinned")
	}
}

func TestParseDockerCommandOptionsRejectsRemovedFCDOption(t *testing.T) {
	for _, args := range [][]string{
		{"--fcd", "pypi:demo-suite"},
		{"--fcd=pypi:demo-suite"},
	} {
		_, err := parseDockerCommandOptions(args, true)
		if err == nil {
			t.Fatalf("expected error for %v", args)
		}
		if !strings.Contains(err.Error(), "unknown option: --fcd") {
			t.Fatalf("unexpected error for %v: %v", args, err)
		}
	}
}

func TestParseDockerCommandOptionsExpandsDemoSuitePackAlias(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-suite"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-suite" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Scheme != "pypi" {
		t.Fatalf("scheme = %q", options.Pack.Scheme)
	}
	if options.Pack.Source != "demo-suite" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if options.Pack.IsPinned {
		t.Fatal("latest alias should not be pinned")
	}
}

func TestParseDockerCommandOptionsExpandsPinnedDemoSuitePackAlias(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-suite==1.2.3"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-suite==1.2.3" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Source != "demo-suite==1.2.3" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if !options.Pack.IsPinned {
		t.Fatal("pinned alias should be pinned")
	}
}

func TestParseDockerCommandOptionsExpandsDemoServerPackAlias(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-server"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-server" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Scheme != "pypi" {
		t.Fatalf("scheme = %q", options.Pack.Scheme)
	}
	if options.Pack.Source != "demo-server" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_server/reploy/demo-server.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if options.Pack.IsPinned {
		t.Fatal("latest alias should not be pinned")
	}
}

func TestParseDockerCommandOptionsExpandsPinnedDemoServerPackAlias(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-server==1.2.3"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-server==1.2.3" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Source != "demo-server==1.2.3" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_server/reploy/demo-server.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if !options.Pack.IsPinned {
		t.Fatal("pinned alias should be pinned")
	}
}

func TestParseDockerCommandOptionsPreservesDemoSuitePackAliasQuery(t *testing.T) {
	setCLITestPackIndex(t)

	options, err := parseDockerCommandOptions([]string{"demo-suite?index-url=http://example.test"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Raw != "demo-suite?index-url=http://example.test" {
		t.Fatalf("raw = %q", options.Pack.Raw)
	}
	if options.Pack.Source != "demo-suite" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_suite/reploy/demo-suite.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if options.Pack.Query.Get("index-url") != "http://example.test" {
		t.Fatalf("index-url query = %q", options.Pack.Query.Get("index-url"))
	}
	if options.Pack.IsPinned {
		t.Fatal("latest alias with query should not be pinned")
	}
}

func TestParseDockerCommandOptionsRejectsDuplicatePack(t *testing.T) {
	setCLITestPackIndex(t)

	_, err := parseDockerCommandOptions([]string{"demo-suite", "file:deploy/demo.blueprint.yaml"}, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "APP_REF may only be provided once") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDockerCommandOptionsLoadsPackIndexFromHTTPAndCache(t *testing.T) {
	indexContent := `{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml"}}}`
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, indexContent)
	}))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("REPLOY_CACHE_DIR", cacheDir)
	t.Setenv(packIndexURLEnv, server.URL+"/index.json")

	options, err := parseDockerCommandOptions([]string{"demo==1.2.3"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Source != "demo-pkg==1.2.3" || options.Pack.Subdir != "demo_pkg/reploy/demo.blueprint.yaml" {
		t.Fatalf("pack = %#v", options.Pack)
	}
	server.Close()

	options, err = parseDockerCommandOptions([]string{"demo"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Source != "demo-pkg" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestParseDockerCommandOptionsRejectsPinnedShorthandWhenRefAlreadyHasVersion(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "blueprint-index.json")
	content := `{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml?version=1.0.0"}}}`
	if err := os.WriteFile(indexPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	_, err := parseDockerCommandOptions([]string{"demo==1.2.3"}, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "already declares version") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDockerCommandOptionsRejectsRemovedVersionPlaceholder(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "blueprint-index.json")
	content := `{"schema_version":1,"blueprints":{"demo":{"ref":"pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml?version={version}"}}}`
	if err := os.WriteFile(indexPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	_, err := parseDockerCommandOptions([]string{"demo==1.2.3"}, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must not use the removed {version} placeholder") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDockerCommandOptionsAppendsGitRefForPinnedGitHubShorthand(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "blueprint-index.json")
	content := `{"schema_version":1,"blueprints":{"demo":{"ref":"github://acme/demo/demo_pkg/reploy/demo.blueprint.yaml"}}}`
	if err := os.WriteFile(indexPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(packIndexURLEnv, "file:"+indexPath)

	options, err := parseDockerCommandOptions([]string{"demo==feature/demo"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if options.Pack.Scheme != "git" {
		t.Fatalf("scheme = %q", options.Pack.Scheme)
	}
	if options.Pack.Source != "https://github.com/acme/demo.git" {
		t.Fatalf("source = %q", options.Pack.Source)
	}
	if options.Pack.Subdir != "demo_pkg/reploy/demo.blueprint.yaml" {
		t.Fatalf("subdir = %q", options.Pack.Subdir)
	}
	if options.Pack.Query.Get("ref") != "feature/demo" {
		t.Fatalf("ref query = %#v", options.Pack.Query)
	}
}

func TestDockerInitLoadsPyPIPackAndRecordsResolvedArtifact(t *testing.T) {
	version := "4.5.6"
	blueprintPath := "demo_pkg/reploy/demo.blueprint.yaml"
	wheel := makeCLITestPackWheel(t, "demo_pkg/reploy", version)
	indexURL := makeCLITestPyPIIndex(t, wheel, version)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("REPLOY_CACHE_DIR", cacheDir)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	packRef := "pypi:demo-pkg#" + blueprintPath + "?index-url=" + indexURL

	code, stdout, stderr := runCLI("stage", "--dir", deployDir, packRef)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "created staging directory for demo.blueprint.yaml: "+deployDir) {
		t.Fatalf("stdout did not include staging summary:\n%s", stdout)
	}
	if strings.Contains(stdout, "updated ") {
		t.Fatalf("stdout should not include generated file updates without --verbose:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	requirements, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-pkg==4.5.6\n" {
		t.Fatalf("requirements = %q", requirements)
	}

	stateContent, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var state deploy.DeploymentState
	if err := json.Unmarshal(stateContent, &state); err != nil {
		t.Fatal(err)
	}
	expectedResolvedRef := "pypi://demo-pkg/demo_pkg/reploy/demo.blueprint.yaml?version=" + version
	if state.Blueprint.Raw != expectedResolvedRef {
		t.Fatalf("state blueprint raw = %q, want %q", state.Blueprint.Raw, expectedResolvedRef)
	}
	if !state.Blueprint.IsPinned {
		t.Fatalf("state blueprint was not pinned: %+v", state.Blueprint)
	}
	if state.RequestedBlueprintRef != packRef {
		t.Fatalf("requested blueprint ref = %q, want %q", state.RequestedBlueprintRef, packRef)
	}
	if state.ResolvedArtifact == nil {
		t.Fatal("missing resolved artifact")
	}
	artifact := *state.ResolvedArtifact
	expectedFilename := fmt.Sprintf("demo_pkg-%s-py3-none-any.whl", version)
	if artifact.Scheme != "pypi" {
		t.Fatalf("artifact scheme = %q, want pypi", artifact.Scheme)
	}
	if artifact.Package != "demo-pkg" {
		t.Fatalf("artifact package = %q, want demo-pkg", artifact.Package)
	}
	if artifact.Version != version {
		t.Fatalf("artifact version = %q, want %q", artifact.Version, version)
	}
	if artifact.Filename != expectedFilename {
		t.Fatalf("artifact filename = %q, want %q", artifact.Filename, expectedFilename)
	}
	if artifact.SHA256 != deploy.HashBytes(wheel) {
		t.Fatalf("artifact sha256 = %q, want %q", artifact.SHA256, deploy.HashBytes(wheel))
	}
	if artifact.Subdir != blueprintPath {
		t.Fatalf("artifact subdir = %q, want %q", artifact.Subdir, blueprintPath)
	}
	if !strings.HasPrefix(artifact.CachePath, cacheDir) {
		t.Fatalf("artifact cache path = %q, want under %q", artifact.CachePath, cacheDir)
	}
	if !strings.HasPrefix(artifact.BlueprintPath, cacheDir) {
		t.Fatalf("artifact blueprint path = %q, want under %q", artifact.BlueprintPath, cacheDir)
	}
	if _, err := os.Stat(artifact.CachePath); err != nil {
		t.Fatalf("missing cached wheel: %v", err)
	}
	if _, err := os.Stat(artifact.BlueprintPath); err != nil {
		t.Fatalf("missing extracted blueprint: %v", err)
	}
}

func TestDockerStageUpdateRejectsExplicitRequirements(t *testing.T) {
	code, stdout, stderr := runCLI("stage", "--update", "--requirement", "demo-suite")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "--requirement is only supported when creating a staging directory") {
		t.Fatalf("stderr did not contain requirement message:\n%s", stderr)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, stdout, stderr := runCLI("wat")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: wat") {
		t.Fatalf("stderr did not contain unknown command:\n%s", stderr)
	}
}

func TestBootstrapCommandIsNotPublicSurface(t *testing.T) {
	code, stdout, stderr := runCLI("bootstrap")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: bootstrap") {
		t.Fatalf("stderr did not contain unknown command:\n%s", stderr)
	}
}

func TestSmokeCommandIsNotPublicSurface(t *testing.T) {
	code, stdout, stderr := runCLI("smoke")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: smoke") {
		t.Fatalf("stderr did not contain unknown command:\n%s", stderr)
	}
}

func TestTopLevelConfigCommandIsNotAppConfigSurface(t *testing.T) {
	code, stdout, stderr := runCLI("config", "check")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: config") {
		t.Fatalf("stderr did not contain unknown command:\n%s", stderr)
	}
}

func TestTopLevelAppCommandSuggestsAppPrefix(t *testing.T) {
	packDir := makeCLITestPack(t)
	workDir := t.TempDir()
	t.Chdir(workDir)
	code, stdout, stderr := runCLI("stage", "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("config", "check")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command: config") || !strings.Contains(stderr, "did you mean `reploy app config check`?") {
		t.Fatalf("stderr did not suggest app prefix:\n%s", stderr)
	}
}

func TestDockerStageUpdateUsesExistingState(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("stage --update failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "[STAGING : demo] up_to_date\n" {
		t.Fatalf("stdout = %q, want one up_to_date line", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerStageUpdateAcceptsPackRef(t *testing.T) {
	packDir := makeCLITestPack(t)
	updatedPackDir := makeCLITestPackWithManifest(t, strings.ReplaceAll(cliTestPackManifest(), "color_env: DEMO_COLOR", "color_env: UPDATED_COLOR"))
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--dir", deployDir, "file:"+updatedPackDir)
	if code != 0 {
		t.Fatalf("stage --update failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "updated staging directory: "+deployDir) {
		t.Fatalf("stdout missing staging update summary:\n%s", stdout)
	}
	if strings.Contains(stdout, filepath.Join(deployDir, dockerdeploy.StateFileName)) {
		t.Fatalf("stdout should not include generated file updates without --verbose:\n%s", stdout)
	}
	stateContent, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var state deploy.DeploymentState
	if err := json.Unmarshal(stateContent, &state); err != nil {
		t.Fatal(err)
	}
	if state.RequestedBlueprintRef != "file:"+updatedPackDir {
		t.Fatalf("requested blueprint ref = %q", state.RequestedBlueprintRef)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerStageUpdateReportsFriendlyDeploymentDirError(t *testing.T) {
	packDir := makeCLITestPack(t)
	invalidPackDir := makeCLITestPackWithManifest(t, strings.Replace(cliTestPackManifest(), "    config: conf\n", "    config: .\n", 1))
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--dir", deployDir, "file:"+invalidPackDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{
		"reploy stage --update error: parse blueprint manifest: docker.deployment_dirs.config:",
		`must name a subdirectory under the deployment root, not "."; use a value like "conf"`,
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestDockerStageUpdateForceOverwritesLocalGeneratedEdits(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	controlScriptPath := filepath.Join(deployDir, "democtl")
	localEdit := []byte("locally edited compose\n")
	if err := os.WriteFile(controlScriptPath, localEdit, 0o755); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "refusing to overwrite locally modified generated files") || !strings.Contains(stderr, "--force") {
		t.Fatalf("stderr missing refusal and force hint:\n%s", stderr)
	}
	content, err := os.ReadFile(controlScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(localEdit) {
		t.Fatalf("control script content changed without force: %q", content)
	}

	code, stdout, stderr = runCLI("stage", "--update", "--verbose", "--force", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("stage --update --force failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "updated "+controlScriptPath) {
		t.Fatalf("stdout missing forced control script update:\n%s", stdout)
	}
	content, err = os.ReadFile(controlScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) == string(localEdit) {
		t.Fatalf("control script content was not overwritten with --force")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleListShowsStateRoots(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "list", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle list failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "[STAGING : demo] demo-suite\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleListReportsMissingRequirementsProjection(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if err := os.Remove(filepath.Join(deployDir, dockerdeploy.RequirementsFileName)); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runCLI("bundle", "list", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "requirements projection is missing") ||
		!strings.Contains(stderr, "reploy stage --update --dir") ||
		!strings.Contains(stderr, deployDir) {
		t.Fatalf("stderr missing update hint:\n%s", stderr)
	}
}

func TestDockerBundleAddAndRemoveUpdateRequirements(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "add", "--extra", "demo-imap==1.2.3", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle add failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "updated "+filepath.Join(deployDir, dockerdeploy.RequirementsFileName)) {
		t.Fatalf("stdout missing requirements update:\n%s", stdout)
	}
	requirements, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-suite\ndemo-imap==1.2.3\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	code, stdout, stderr = runCLI("bundle", "remove", "--extra", "demo-imap==1.2.3", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle remove failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	requirements, err = os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-suite\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleAddAndRemoveAcceptMultipleRoots(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI(
		"stage",
		"--dir",
		deployDir,
		"file:"+packDir,
		"--requirement",
		"demo-server==1.2.3",
	)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "add", "imap,smtp", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle add failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Count(stdout, "requirements.txt") != 1 {
		t.Fatalf("stdout should show one requirements update:\n%s", stdout)
	}
	if !strings.Contains(stdout, "selected Python packages: demo-imap, demo-smtp (dependencies included when the bundle is prepared)") {
		t.Fatalf("stdout missing selected package summary:\n%s", stdout)
	}
	requirements, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-server==1.2.3\ndemo-imap\ndemo-smtp\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	code, stdout, stderr = runCLI("bundle", "add", "imap,smtp", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("second bundle add failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "already selected Python packages: demo-imap, demo-smtp (dependencies included when the bundle is prepared)") {
		t.Fatalf("stdout missing already-selected package summary:\n%s", stdout)
	}
	if !strings.Contains(stdout, "up_to_date") {
		t.Fatalf("stdout missing up_to_date:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	code, stdout, stderr = runCLI("bundle", "remove", "imap,smtp", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle remove failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	requirements, err = os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-server==1.2.3\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleAddWithoutRootsShowsUsefulHint(t *testing.T) {
	code, stdout, stderr := runCLI("bundle", "add")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "bundle add expects option names or --extra ROOT") ||
		!strings.Contains(stderr, "reploy bundle add imap,smtp") ||
		!strings.Contains(stderr, "reploy bundle add --extra PACKAGE[==VERSION]") {
		t.Fatalf("stderr missing useful hint:\n%s", stderr)
	}
}

func TestDockerBundleAddRejectsLikelyOptionTypoWithoutWriting(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI(
		"stage",
		"--dir",
		deployDir,
		"file:"+packDir,
		"--requirement",
		"demo-server==1.2.3",
	)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "add", "imap,smtpa", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `unknown bundle option "smtpa"`) || !strings.Contains(stderr, `did you mean "smtp"`) || !strings.Contains(stderr, "--extra") {
		t.Fatalf("stderr missing validation message:\n%s", stderr)
	}
	requirements, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-server==1.2.3\n" {
		t.Fatalf("requirements were partially updated: %q", requirements)
	}
}

func TestDockerBundleAddUnknownOptionListsOptionsOnSeparateLines(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI(
		"stage",
		"--dir",
		deployDir,
		"file:"+packDir,
		"--requirement",
		"demo-server==1.2.3",
	)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "add", "foo", "--dir", deployDir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "[STAGING : demo] use one of:\n[STAGING : demo]   demo-suite\n[STAGING : demo]   imap\n[STAGING : demo]   smtp") {
		t.Fatalf("stderr did not list options on separate lines:\n%s", stderr)
	}
	if !strings.Contains(stderr, `unknown bundle option "foo"`) || !strings.Contains(stderr, "--extra") {
		t.Fatalf("stderr missing validation message:\n%s", stderr)
	}
}

func TestDockerBundleAddExtraAcceptsUnknownUnpinnedPackage(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI(
		"stage",
		"--dir",
		deployDir,
		"file:"+packDir,
		"--requirement",
		"demo-server==1.2.3",
	)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "add", "--extra", "aa", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle add failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	requirements, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-server==1.2.3\naa\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleAddExtraTreatsUnknownNameAsPackage(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI(
		"stage",
		"--dir",
		deployDir,
		"file:"+packDir,
		"--requirement",
		"demo-server==1.2.3",
	)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "add", "--extra", "smtpa", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle add failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	requirements, err := os.ReadFile(filepath.Join(deployDir, dockerdeploy.RequirementsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(requirements) != "demo-server==1.2.3\nsmtpa\n" {
		t.Fatalf("requirements = %q", requirements)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleListOptions(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "list-options", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle list-options failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "imap\tReceive email through IMAP.") {
		t.Fatalf("stdout missing imap option:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleArtifactHelpersAreNotPublicSurface(t *testing.T) {
	for _, command := range []string{"add-wheel", "add-source"} {
		code, stdout, stderr := runCLI("bundle", command, "foo")
		if code != 2 {
			t.Fatalf("%s exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", command, code, stdout, stderr)
		}
		if stdout != "" {
			t.Fatalf("%s stdout = %q, want empty", command, stdout)
		}
		if !strings.Contains(stderr, "unknown bundle command: "+command) {
			t.Fatalf("%s stderr missing unknown command:\n%s", command, stderr)
		}
		if strings.Contains(stderr, "state.json") || strings.Contains(stderr, "reploy-staging") {
			t.Fatalf("%s stderr should reject helper before resolving staging:\n%s", command, stderr)
		}
	}

	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	wheel := filepath.Join(t.TempDir(), "demo-1.0.0-py3-none-any.whl")
	if err := os.WriteFile(wheel, []byte("wheel content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runCLI("bundle", "add-wheel", wheel, "--dir", deployDir)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown bundle command: add-wheel") {
		t.Fatalf("stderr missing unknown command:\n%s", stderr)
	}

	code, stdout, stderr = runCLI("bundle", "add-source", filepath.Dir(wheel), "--dir", deployDir)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown bundle command: add-source") {
		t.Fatalf("stderr missing unknown command:\n%s", stderr)
	}
}

func TestDockerBundleCheckDryRun(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "check", "--dry-run", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle check failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "would validate installation bundle:") || !strings.Contains(stdout, "docker run --rm") {
		t.Fatalf("stdout missing dry-run command:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleCheckVerboseDryRun(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "check", "--verbose", "--dry-run", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle check failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "would validate installation bundle:") || !strings.Contains(stdout, "docker run --rm") {
		t.Fatalf("stdout missing dry-run command:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundlePrepareDryRun(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "build", "--dry-run", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle build failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"would build installation bundle:", "would warm Python runtime:", "__reploy_runtime_warmup"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing dry-run output %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleWarmRuntimeDryRun(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("bundle", "warm-runtime", "--dry-run", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle warm-runtime failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "would warm Python runtime:") || !strings.Contains(stdout, "__reploy_runtime_warmup") {
		t.Fatalf("stdout missing warm-runtime dry-run command:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleWithoutCommandShowsSubcommands(t *testing.T) {
	code, stdout, stderr := runCLI("bundle")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage: reploy [--docker-timeout DURATION] bundle COMMAND") ||
		!strings.Contains(stderr, "build") ||
		!strings.Contains(stderr, "clean") ||
		!strings.Contains(stderr, "warm-runtime") ||
		!strings.Contains(stderr, "list-options") {
		t.Fatalf("stderr missing bundle subcommands:\n%s", stderr)
	}
	if strings.Contains(stderr, "add-wheel") || strings.Contains(stderr, "add-source") {
		t.Fatalf("stderr exposed internal artifact helpers:\n%s", stderr)
	}
}

func TestDockerBundleHelpShowsSubcommands(t *testing.T) {
	code, stdout, stderr := runCLI("bundle", "--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage: reploy [--docker-timeout DURATION] bundle COMMAND") ||
		!strings.Contains(stdout, "build") ||
		!strings.Contains(stdout, "clean") ||
		!strings.Contains(stdout, "warm-runtime") ||
		!strings.Contains(stdout, "--verbose") {
		t.Fatalf("stdout missing bundle help:\n%s", stdout)
	}
	if strings.Contains(stdout, "add-wheel") || strings.Contains(stdout, "add-source") {
		t.Fatalf("stdout exposed internal artifact helpers:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleCleanRemovesBuiltWheels(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	useFilesystemRuntimeCache(t, deployDir)
	builtWheel := filepath.Join(deployDir, dockerdeploy.BundleDirName, "demo_suite-1.2.3-py3-none-any.whl")
	if err := os.WriteFile(builtWheel, []byte("wheel\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runCLI("bundle", "clean", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle clean failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want quiet clean", stdout)
	}
	if _, err := os.Stat(builtWheel); !os.IsNotExist(err) {
		t.Fatalf("built wheel still exists: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestDockerBundleCleanVerboseReportsRemovedWheels(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	useFilesystemRuntimeCache(t, deployDir)
	builtWheel := filepath.Join(deployDir, dockerdeploy.BundleDirName, "demo_suite-1.2.3-py3-none-any.whl")
	if err := os.WriteFile(builtWheel, []byte("wheel\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runCLI("bundle", "clean", "--verbose", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("bundle clean failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "removed "+filepath.Join(deployDir, dockerdeploy.BundleDirName)) {
		t.Fatalf("stdout missing removed bundle dir:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestStartSpinnerPrintsCompletion(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop := startSpinner(&stderr, "building installation bundle")
	stop(true)
	if !strings.Contains(stderr.String(), "\x1b[?25l") || !strings.Contains(stderr.String(), "\x1b[?25h") {
		t.Fatalf("spinner did not hide and restore cursor:\n%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "building installation bundle |") {
		t.Fatalf("spinner did not print label before frame:\n%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "building installation bundle... done") {
		t.Fatalf("spinner did not print completion:\n%q", stderr.String())
	}
}

func TestStartSpinnerUsesPlainProgressInCI(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop := startSpinner(&stderr, "building installation bundle")
	stop(true)
	if got, want := stderr.String(), "building installation bundle...\nbuilding installation bundle... done\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestStartSpinnerUsesPlainProgressForDumbTerminal(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("TERM", "dumb")
	var stderr bytes.Buffer
	stop := startSpinner(&stderr, "building installation bundle")
	stop(false)
	if got, want := stderr.String(), "building installation bundle...\nbuilding installation bundle... failed\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestStartProgressSpinnerUpdatesAnimatedLabel(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop, progress := startProgressSpinner(&stderr, "installing from staging")
	fmt.Fprintln(progress, "copying staged deployment")
	time.Sleep(20 * time.Millisecond)
	stop(true)
	if !strings.Contains(stderr.String(), "installing from staging: copying staged deployment") {
		t.Fatalf("spinner did not update label:\n%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "installing from staging... done") {
		t.Fatalf("spinner did not print completion:\n%q", stderr.String())
	}
}

func TestStartProgressSpinnerWithLogsKeepsLogLinesSeparate(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop, progress, logs := startProgressSpinnerWithLogs(&stderr, "installing from staging")
	terminalOutput, ok := logs.(interface{ TerminalOutput() io.Writer })
	if !ok {
		t.Fatalf("spinner log writer does not expose terminal output")
	}
	if terminalOutput.TerminalOutput() != &stderr {
		t.Fatalf("terminal output = %#v, want stderr", terminalOutput.TerminalOutput())
	}
	fmt.Fprintln(progress, "running before start hook: app config check")
	fmt.Fprint(logs, "[STAGING : smoke-app] warn: warning")
	fmt.Fprint(logs, ": Docker-managed install\n")
	time.Sleep(20 * time.Millisecond)
	stop(true)
	got := stderr.String()
	if !strings.Contains(got, "[STAGING : smoke-app] warn: warning: Docker-managed install\n") {
		t.Fatalf("spinner log writer did not keep prefixed log line intact:\n%q", got)
	}
	if strings.Contains(got, "/[STAGING") || strings.Contains(got, "|[STAGING") || strings.Contains(got, "-[STAGING") || strings.Contains(got, "\\[STAGING") {
		t.Fatalf("spinner frame collided with log line:\n%q", got)
	}
	if !strings.Contains(got, "installing from staging: running before start hook: app config check") {
		t.Fatalf("spinner did not keep progress label:\n%q", got)
	}
	if !strings.Contains(got, "installing from staging... done") {
		t.Fatalf("spinner did not print completion:\n%q", got)
	}
}

func TestStartProgressSpinnerUsesPlainProgressInCI(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	stop, progress := startProgressSpinner(&stderr, "installing from staging")
	fmt.Fprintln(progress, "copying staged deployment")
	fmt.Fprintln(progress, "starting Docker-managed app")
	stop(true)
	want := strings.Join([]string{
		"installing from staging...",
		"installing from staging: copying staged deployment",
		"installing from staging: starting Docker-managed app",
		"installing from staging... done",
		"",
	}, "\n")
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestDockerBundleCheckRejectsPyPIOnly(t *testing.T) {
	code, stdout, stderr := runCLI("bundle", "check", "--pypi-only")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown option: --pypi-only") {
		t.Fatalf("stderr missing option error:\n%s", stderr)
	}
}

func TestDockerBundleRejectsNoWarmRuntime(t *testing.T) {
	code, stdout, stderr := runCLI("bundle", "build", "--no-warm-runtime")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown option: --no-warm-runtime") {
		t.Fatalf("stderr missing option error:\n%s", stderr)
	}
}

func TestPrintUpdateResultsShowsOnlyActionablePaths(t *testing.T) {
	var stdout bytes.Buffer
	printUpdateResults(&stdout, []dockerdeploy.UpdateResult{
		{Path: "deployment/compose.yaml", Status: deploy.UpdateStatusUpToDate},
		{Path: "deployment/democtl", Status: deploy.UpdateStatusUpdated},
		{Path: "deployment/docker.env", Status: deploy.UpdateStatusSkipped},
	})

	expected := "updated deployment/democtl\nskipped deployment/docker.env\n"
	if stdout.String() != expected {
		t.Fatalf("stdout = %q, want %q", stdout.String(), expected)
	}
}

func TestDockerInfoShowsDeploymentState(t *testing.T) {
	packDir := makeCLITestPack(t)
	deployDir := filepath.Join(t.TempDir(), "deployment")
	code, stdout, stderr := runCLI("stage", "--dir", deployDir, "file:"+packDir)
	if code != 0 {
		t.Fatalf("stage failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLI("info", "--dir", deployDir)
	if code != 0 {
		t.Fatalf("info failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "target: docker") || !strings.Contains(stdout, "phase: staged") {
		t.Fatalf("stdout missing target/phase:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func makeCLITestPack(t *testing.T) string {
	t.Helper()
	return makeCLITestPackWithManifest(t, cliTestPackManifest())
}

func useFilesystemRuntimeCache(t *testing.T, deployDir string) {
	t.Helper()
	path := filepath.Join(deployDir, dockerdeploy.DockerEnvFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	replaced := false
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "REPLOY_RUNTIME_DIR=") {
			lines[index] = "REPLOY_RUNTIME_DIR=./" + dockerdeploy.RuntimeDirName
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, "", "REPLOY_RUNTIME_DIR=./"+dockerdeploy.RuntimeDirName)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeCLITestSourcePack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"demo-suite\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blueprintDir := filepath.Join(dir, "demo_suite", "reploy")
	if err := os.MkdirAll(blueprintDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blueprintDir, "demo.blueprint.yaml"), []byte(cliTestPackManifest()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func makeCLITestGitSourcePack(t *testing.T) (string, string) {
	t.Helper()
	sourceDir := filepath.Join(t.TempDir(), "git-source-app")
	copyCLITestTree(t, filepath.Join(cliTestRepoRoot(t), "tests", "e2e", "python", "packages", "git-source-app"), sourceDir)
	repository, err := git.PlainInit(sourceDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		_, err = worktree.Add(filepath.ToSlash(relativePath))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	hash, err := worktree.Commit("add git source app fixture", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Reploy Test",
			Email: "test@example.com",
			When:  time.Unix(1, 0),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sourceDir, hash.String()
}

func localFileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(slashed) >= 2 && slashed[1] == ':' {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func copyCLITestTree(t *testing.T, sourceDir string, targetDir string) {
	t.Helper()
	if err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(targetDir, relativePath)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(targetPath, content, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
}

func cliTestRepoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate cli test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func makeCLITestPackWithManifest(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.blueprint.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func markCLITestDeploymentInstalled(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, dockerdeploy.StateFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state deploy.DeploymentState
	if err := json.Unmarshal(content, &state); err != nil {
		t.Fatal(err)
	}
	state.Phase = deploy.PhaseInstalled
	state.Install = &deploy.InstallState{TargetDir: dir, Service: "demo"}
	content, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeCLITestPackWheel(t *testing.T, subdir string, version string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	files := map[string]string{
		subdir + "/demo.blueprint.yaml":                        cliTestPackManifest(),
		fmt.Sprintf("demo_pkg-%s.dist-info/WHEEL", version):    "Wheel-Version: 1.0\nGenerator: reploy-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
		fmt.Sprintf("demo_pkg-%s.dist-info/METADATA", version): "Metadata-Version: 2.1\nName: demo-pkg\nVersion: " + version + "\n",
	}
	for path, content := range files {
		file, err := writer.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeCLITestInstalledState(t *testing.T, dir string, appID string, service string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, dockerdeploy.ReployInternalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	state := deploy.DeploymentState{
		SchemaVersion: 1,
		ToolVersion:   "test",
		Target:        "docker",
		Phase:         deploy.PhaseInstalled,
		AppID:         appID,
		Install: &deploy.InstallState{
			TargetDir:      dir,
			Service:        service,
			ComposeProject: service,
			ContainerName:  service,
			NetworkName:    service,
		},
	}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, dockerdeploy.StateFileName), append(content, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeCLITestPyPIIndex(t *testing.T, wheel []byte, version string) string {
	t.Helper()
	filename := fmt.Sprintf("demo_pkg-%s-py3-none-any.whl", version)
	sha256 := deploy.HashBytes(wheel)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pypi/demo-pkg/json":
			w.Header().Set("Content-Type", "application/json")
			wheelURL := "http://" + r.Host + "/files/" + filename
			response := map[string]any{
				"info": map[string]string{"version": version},
				"releases": map[string]any{
					version: []map[string]any{{
						"filename":    filename,
						"url":         wheelURL,
						"packagetype": "bdist_wheel",
						"digests":     map[string]string{"sha256": sha256},
					}},
				},
				"urls": []any{},
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Logf("write pypi response: %v", err)
			}
		case "/files/" + filename:
			w.Header().Set("Content-Type", "application/octet-stream")
			if _, err := w.Write(wheel); err != nil {
				t.Logf("write wheel response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func cliTestPackManifest() string {
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
  managed_paths:
    dirs:
      - path: conf
        update: preserve
        mount: /{{ path }}
      - path: data
        update: preserve
        mount: /{{ path }}

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
  command_defaults:
    container:
      argv_prefix:
        - demo-server
        - --config-dir
        - /conf
        - --config-name
        - ${DEMO_CONFIG_NAME}
  commands:
    serve:
      container:
        argv_suffix:
          - serve
    config_check:
      trigger:
        - config
        - check
      app_command: true
      forward_flags:
        - --live
      container:
        argv_suffix:
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
        argv_suffix:
          - bootstrap
          - demo
    bootstrap_plugin:
      trigger:
        - bootstrap
        - plugin
      app_command: true
      forward_args: true
      container:
        argv_suffix:
          - bootstrap
          - plugin
    config_activate:
      trigger:
        - config
        - activate
      app_command: true
      forward_args: true
      container:
        argv_suffix:
          - config
          - activate
    config_show:
      trigger:
        - config
        - show
      app_command: true
      forward_args: true
      container:
        argv_suffix:
          - config
          - show
    env_bootstrap:
      trigger:
        - env
        - bootstrap
      app_command: true
      forward_args: true
      container:
        argv_suffix:
          - env
          - bootstrap
    env_check:
      trigger:
        - env
        - check
      app_command: true
      forward_args: true
      container:
        argv_suffix:
          - env
          - check
`
}
