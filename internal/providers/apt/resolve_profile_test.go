package apt

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/providers"
)

func TestResolveChildEnvironmentV1IsExactAndClosed(t *testing.T) {
	want := providers.ChildEnvironmentProfile{
		Schema: providers.ChildEnvironmentSchemaV1,
		Name:   ResolveProfileV1, InheritNone: true, Umask: "0022",
		Variables: []providers.EnvironmentVariable{
			{Name: "APT_CONFIG", Value: "/tmp/reploy-apt-resolve/apt.conf"},
			{Name: "DEBIAN_FRONTEND", Value: "noninteractive"},
			{Name: "HOME", Value: "/root"},
			{Name: "LANG", Value: "C"},
			{Name: "LC_ALL", Value: "C"},
			{Name: "PATH", Value: "/usr/sbin:/usr/bin:/sbin:/bin"},
			{Name: "TMPDIR", Value: "/tmp/reploy-apt-resolve"},
		},
	}
	got := ResolveChildEnvironmentV1()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profile = %#v", got)
	}
	if err := providers.ValidateChildEnvironmentProfile(got); err != nil {
		t.Fatal(err)
	}
	got.Variables[0].Value = "changed"
	if ResolveChildEnvironmentV1().Variables[0].Value != ResolveConfigPath {
		t.Fatal("profile variables alias mutable state")
	}
}

func TestResolveAPTArgvV1IsExact(t *testing.T) {
	update := ResolveUpdateArgvV1()
	wantUpdate := []string{
		"/usr/bin/apt-get", "-o", "Dpkg::Use-Pty=0", "-o", "Acquire::Languages=none",
		"-o", "Dir::State::lists=/tmp/reploy-apt-resolve/lists", "update", "--error-on=any",
	}
	if !reflect.DeepEqual(update, wantUpdate) {
		t.Fatalf("update argv = %#v", update)
	}
	download := ResolveDownloadPrefixArgvV1()
	plan := ResolvePlanPrefixArgvV1()
	wantPlan := []string{
		"/usr/bin/apt-get", "-o", "Dpkg::Use-Pty=0", "-o", "Acquire::Languages=none",
		"-o", "Dir::State::lists=/tmp/reploy-apt-resolve/lists",
		"--solver", "internal", "--simulate", "-o", "Debug::pkgDepCache::Marker=1",
		"--no-remove", "--no-install-recommends",
		"-o", "APT::Install-Suggests=false", "install",
	}
	if !reflect.DeepEqual(plan, wantPlan) {
		t.Fatalf("plan argv = %#v", plan)
	}
	baseState := ResolveBaseStatePrefixArgvV1()
	wantBaseState := []string{
		"/usr/bin/dpkg-query", "--show",
		"--showformat=${binary:Package}\t${Version}\t${Architecture}\t${Status}\n",
	}
	if !reflect.DeepEqual(baseState, wantBaseState) {
		t.Fatalf("base state argv = %#v", baseState)
	}
	wantDownload := []string{
		"/usr/bin/apt-get", "-o", "Dpkg::Use-Pty=0", "-o", "Acquire::Languages=none",
		"-o", "Dir::State::lists=/tmp/reploy-apt-resolve/lists",
		"-o", "Dir::Cache::archives=/tmp/reploy-apt-resolve/archives",
		"--assume-yes", "--download-only", "--no-remove", "--no-install-recommends",
		"-o", "APT::Install-Suggests=false", "install",
	}
	if !reflect.DeepEqual(download, wantDownload) {
		t.Fatalf("download argv = %#v", download)
	}
	joined := strings.Join(append(append(append(update, plan...), baseState...), download...), "\x00")
	for _, forbidden := range []string{"upgrade", "full-upgrade", "--allow-downgrades", "--allow-unauthenticated", "--force-yes"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("resolver argv contains forbidden option %q", forbidden)
		}
	}
	update[0] = "changed"
	if ResolveUpdateArgvV1()[0] != "/usr/bin/apt-get" {
		t.Fatal("update argv aliases mutable state")
	}
}

func TestResolveAdditiveConfigV1IsFixed(t *testing.T) {
	if ResolveAdditiveConfigV1 != "Acquire::Languages \"none\";\nDpkg::Use-Pty \"0\";\n" {
		t.Fatalf("config = %q", ResolveAdditiveConfigV1)
	}
	if strings.Contains(ResolveAdditiveConfigV1, "Dir::") {
		t.Fatal("additive config must not replace the base configuration directories")
	}
}
