package dockerdeploy

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/distribution/reference"
)

func writePodmanAuthConfig(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func readPodmanTestAuth(t *testing.T, author string) map[string]any {
	t.Helper()
	named, err := reference.ParseNormalizedNamed(author)
	if err != nil {
		t.Fatal(err)
	}
	data, err := podmanBaseRegistryAuth(named)
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]any
	if err := json.Unmarshal(data, &auth); err != nil {
		t.Fatal(err)
	}
	return auth
}
func TestPodmanBaseRegistryAuthInlineAndEnvironment(t *testing.T) {
	for _, test := range []struct{ name, author, registry string }{
		{"hub", "debian:13", "https://index.docker.io/v1/"},
		{"private", "registry.example:5000/team/base:tag", "registry.example:5000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolatePodmanBaseAuth(t)
			config := `{"auths":{"` + test.registry + `":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("inline-user:inline-secret")) + `"},"other.example":{"auth":"` + base64.StdEncoding.EncodeToString([]byte("unrelated:unrelated-secret")) + `"}}}`
			dir := writePodmanAuthConfig(t, config)
			auth := readPodmanTestAuth(t, test.author)
			if auth["username"] != "inline-user" || auth["password"] != "inline-secret" {
				t.Fatal("inline credentials were not resolved")
			}
			t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"`+test.registry+`":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("env-user:env-secret"))+`"}}}`)
			auth = readPodmanTestAuth(t, test.author)
			if auth["username"] != "env-user" || auth["password"] != "env-secret" {
				t.Fatal("environment credential precedence changed")
			}
			data, err := os.ReadFile(filepath.Join(dir, "config.json"))
			if err != nil || string(data) != config {
				t.Fatal("resolver wrote credential file")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatal("resolver created credential files")
			}
		})
	}
}
func TestPodmanBaseRegistryAuthIdentityToken(t *testing.T) {
	isolatePodmanBaseAuth(t)
	writePodmanAuthConfig(t, `{"auths":{"registry.example":{"identitytoken":"fixture-token"}}}`)
	auth := readPodmanTestAuth(t, "registry.example/team/base:tag")
	if auth["identitytoken"] != "fixture-token" {
		t.Fatal("identity token was not preserved")
	}
}
func TestPodmanBaseRegistryAuthHelpers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper fixture")
	}
	for _, test := range []struct{ name, config, registry, user, secret string }{
		{"global", `{"credsStore":"fixture"}`, "registry.example", "helper-user", "helper-secret"},
		{"hub key", `{"credHelpers":{"https://index.docker.io/v1/":"fixture"}}`, "https://index.docker.io/v1/", "helper-user", "helper-secret"},
		{"registry precedence", `{"credsStore":"missing-global","credHelpers":{"registry.example":"fixture"}}`, "registry.example", "helper-user", "helper-secret"},
		{"token", `{"credsStore":"fixture"}`, "registry.example", "<token>", "fixture-token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolatePodmanBaseAuth(t)
			dir := writePodmanAuthConfig(t, test.config)
			t.Setenv("PATH", dir)
			// The helper rejects list/store/erase and any lookup for another registry.
			script := "#!/bin/sh\n[ \"$1\" = get ] || exit 3\nread -r registry\n[ \"$registry\" = '" + test.registry + "' ] || exit 4\nprintf '%s' '{\"Username\":\"" + test.user + "\",\"Secret\":\"" + test.secret + "\"}'\n"
			if err := os.WriteFile(filepath.Join(dir, "docker-credential-fixture"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			author := "registry.example/team/base:tag"
			if test.name == "hub key" {
				author = "debian:13"
			}
			auth := readPodmanTestAuth(t, author)
			if test.user == "<token>" {
				if auth["identitytoken"] != test.secret {
					t.Fatal("helper token was not preserved")
				}
			} else if auth["username"] != test.user || auth["password"] != test.secret {
				t.Fatal("helper credentials were not resolved")
			}
		})
	}
}
func TestPodmanBaseRegistryAuthFailuresAreRedacted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper fixture")
	}
	for _, failure := range []string{"malformed file", "helper failure", "missing configured helper"} {
		t.Run(failure, func(t *testing.T) {
			isolatePodmanBaseAuth(t)
			config := `{"credsStore":"fixture"}`
			if failure == "malformed file" {
				config = `{"auths":"sensitive-secret"}`
			}
			dir := writePodmanAuthConfig(t, config)
			if failure == "helper failure" {
				t.Setenv("PATH", dir)
				if err := os.WriteFile(filepath.Join(dir, "docker-credential-fixture"), []byte("#!/bin/sh\nprintf 'sensitive-secret'\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			named, _ := reference.ParseNormalizedNamed("registry.example/base:tag")
			_, err := podmanBaseRegistryAuth(named)
			if err == nil || strings.Contains(err.Error(), "sensitive-secret") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
