//go:build linux

package dockerdeploy

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func systemdControllerCacheSettings(t *testing.T, plan providerInstallationPlanV1) (string, string) {
	t.Helper()
	files, err := providerInstallSystemdFileV1(plan, "/usr/bin/docker", false)
	if err != nil || len(files) != 1 {
		t.Fatalf("render system service: files=%d error=%v", len(files), err)
	}
	var directory, environment, mode string
	for _, line := range strings.Split(string(files[0].Content), "\n") {
		if value, ok := strings.CutPrefix(line, "CacheDirectory="); ok {
			directory = value
		}
		if value, ok := strings.CutPrefix(line, "CacheDirectoryMode="); ok {
			mode = value
		}
		if value, ok := strings.CutPrefix(line, "Environment="); ok {
			assignment, err := strconv.Unquote(value)
			if err != nil {
				t.Fatal(err)
			}
			if value, ok := strings.CutPrefix(assignment, "XDG_CACHE_HOME="); ok {
				environment = value
			}
		}
	}
	if directory != "reploy" || mode != "0700" || environment != "%C/"+directory {
		t.Fatalf("unsafe or absent controller cache: directory=%q mode=%q environment=%q", directory, mode, environment)
	}
	return directory, environment
}

func TestProviderInstallSystemdControllerCacheIsPrivateStableAndSharedByControllerAccount(t *testing.T) {
	dir := t.TempDir()
	plan := providerInstallRunPlanFixture(dir, fixedPublicationReferences(t, dir, 0xd6))
	first, _ := systemdControllerCacheSettings(t, plan)
	second, _ := systemdControllerCacheSettings(t, plan)
	if first != second {
		t.Fatal("cache changes when the same service is rendered again")
	}
	// The controller account shares a cache of common pinned data; installation
	// metadata must never become a cache path or a systemd specifier.
	plan.Installation.Service = "cache %n $HOME"
	plan.Installation.ComposeProject = "../../escape:%n $HOME"
	same, _ := systemdControllerCacheSettings(t, plan)
	if same != first {
		t.Fatal("free-form service metadata changes the controller cache")
	}
	plan.Installation.TargetDir = filepath.Join(dir, "other installation %n $HOME")
	other, _ := systemdControllerCacheSettings(t, plan)
	if other != first {
		t.Fatal("installations owned by one controller account do not share its cache")
	}
}

func TestProviderInstallSystemdControllerCacheSupportsCleanServiceEnvironment(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if _, err := materializePodmanDockerSeccomp(); err == nil {
		t.Fatal("missing service cache did not reproduce the reported failure")
	}
	dir := t.TempDir()
	plan := providerInstallRunPlanFixture(dir, fixedPublicationReferences(t, dir, 0xd7))
	_, setting := systemdControllerCacheSettings(t, plan)
	// Resolve the generated %C setting inside a disposable cache hierarchy.
	// Actual systemd directory provisioning is qualified separately.
	cache := strings.Replace(setting, "%C", t.TempDir(), 1)
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", cache)
	first, err := materializePodmanDockerSeccomp()
	if err != nil {
		t.Fatal(err)
	}
	second, err := materializePodmanDockerSeccomp()
	if err != nil || first != second {
		t.Fatalf("reuse: %q %q %v", first, second, err)
	}
	data, err := os.ReadFile(first)
	if err != nil || !bytes.Equal(data, podmanDockerSeccompProfile) {
		t.Fatal("pinned profile changed", err)
	}
	info, err := os.Stat(first)
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Fatal("profile lost its restrictive mode", err)
	}
	if err := os.Chmod(first, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := materializePodmanDockerSeccomp(); err == nil {
		t.Fatal("service cache accepted unsafe existing profile permissions")
	}
}
