package dockerdeploy

import (
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPodmanTemporaryHomeRetainsRestrictionsAndApplicationArgv(t *testing.T) {
	plan := DockerExecutionPlan{ContainerName: "home", Image: "image", DeploymentDir: t.TempDir(), Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 1000, GID: 1001, DockerUser: "1000:1001"})}
	argv := []string{"/bin/echo", "--tmpfs", "/mnt/reploy-home:bad", "--prepare-temporary-home"}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: argv}, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	original := slices.Clone(spec.Args)
	translated, err := preparePodmanTemporaryHomeCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.Args, original) || !reflect.DeepEqual(translated.Args[len(translated.Args)-len(argv):], argv) {
		t.Fatal("modified canonical input or application argv")
	}
	index := slices.Index(translated.Args, "--tmpfs")
	if translated.Args[index+1] != "/mnt/reploy-home:rw,noexec,nosuid,nodev,size=64m,mode=0700" {
		t.Fatal("lost private mount restrictions")
	}
	entry := slices.Index(translated.Args, "--entrypoint")
	if translated.Args[entry-2] != "--cap-add" || translated.Args[entry-1] != "CHOWN" || translated.Args[entry+4] != podmanPrepareTemporaryHome {
		t.Fatal("missing trusted setup authority")
	}
	for _, change := range []string{"uid=999", "mode=0777", "size=128m"} {
		bad := slices.Clone(original)
		index := slices.Index(bad, "--tmpfs") + 1
		switch change {
		case "uid=999":
			bad[index] = strings.Replace(bad[index], "uid=1000", change, 1)
		case "mode=0777":
			bad[index] = strings.Replace(bad[index], "mode=0700", change, 1)
		default:
			bad[index] = strings.Replace(bad[index], "size=64m", change, 1)
		}
		invalid := spec
		invalid.Args = bad
		if _, err := preparePodmanTemporaryHomeCommand(invalid); err == nil {
			t.Fatalf("accepted %s", change)
		}
	}
}

func TestPodmanTemporaryHomeComposeUsesTrustedSetupOnly(t *testing.T) {
	data := "user: '0:0'\nentrypoint: [/reploy-probe]\ncommand: [restricted-exec, --uid, '1000', --gid, '1001', --, /bin/echo, --cap-add, CHOWN]\ncap_add: [SETGID, SETUID, SETPCAP]\ntmpfs: ['/mnt/reploy-home:rw,noexec,nosuid,nodev,size=64m,mode=0700,uid=1000,gid=1001', '/masked:ro,mode=000']\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(data), &doc); err != nil {
		t.Fatal(err)
	}
	service := doc.Content[0]
	if err := preparePodmanTemporaryHomeCompose(service); err != nil {
		t.Fatal(err)
	}
	command, _ := composePodmanField(service, "command")
	if command.Content[1].Value != podmanPrepareTemporaryHome || command.Content[len(command.Content)-1].Value != "CHOWN" {
		t.Fatal("changed application arguments")
	}
	mounts, _ := composePodmanField(service, "tmpfs")
	if strings.Contains(mounts.Content[0].Value, "uid=") || mounts.Content[1].Value != "/masked:ro,mode=000" {
		t.Fatal("changed unrelated mount")
	}
	capabilities, _ := composePodmanField(service, "cap_add")
	if capabilities.Content[len(capabilities.Content)-1].Value != "CHOWN" {
		t.Fatal("missing setup capability")
	}
	// A second translation is rejected instead of duplicating setup authority.
	if err := preparePodmanTemporaryHomeCompose(service); err == nil {
		t.Fatal("accepted noncanonical translated input")
	}
}

func TestPodmanPrivateEnvironmentLauncherRetainsIdentityAndMountRules(t *testing.T) {
	command := []string{"sandbox-exec", "--uid", "1000", "--gid", "1001", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--private-environment", "--", "/bin/echo", "--uid", "9999"}
	mount, translated, err := podmanTemporaryHome("/mnt/reploy-home:rw,noexec,nosuid,nodev,size=64m,mode=0700,uid=1000,gid=1001", command)
	if err != nil || strings.Contains(mount, "uid=") || !slices.Equal(translated[2:], command[1:]) {
		t.Fatal(mount, translated, err)
	}
	uid, gid, err := podmanApplicationIdentity(translated)
	if err != nil || uid != 1000 || gid != 1001 {
		t.Fatal(uid, gid, err)
	}
	duplicate := slices.Insert(slices.Clone(command), 1, "--private-environment")
	if _, _, err := podmanTemporaryHome("/mnt/reploy-home:rw,noexec,nosuid,nodev,size=64m,mode=0700,uid=1000,gid=1001", duplicate); err == nil {
		t.Fatal("duplicate boolean startup option accepted")
	}
}

func TestPodmanMetadataShadowMountsRemainEmpty(t *testing.T) {
	dir := t.TempDir()
	plan := DockerExecutionPlan{ContainerName: "mask", Image: "image", DeploymentDir: dir, Sandbox: newApplicationSandboxPlanV1(RuntimeUserPlan{UID: 1000, GID: 1001, DockerUser: "1000:1001"}), Mounts: []MountExecutionPlan{{Name: "workspace", Mode: blueprint.MountBind, Source: dir, SourceKind: deploy.RuntimeMountSourceDirectory, Target: "/workspace"}}}
	spec, err := TransientCommandSpec(plan, ResolvedEnvironmentCommand{Argv: []string{"/bin/true"}}, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	translated, err := preparePodmanTemporaryHomeCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	canonical := "/workspace/.reploy:" + privateRuntimeDirectoryMaskOptionsV1
	if !slices.Contains(spec.Args, canonical) || !slices.Contains(translated.Args, canonical+",notmpcopyup") {
		t.Fatal("transient mask copied metadata or changed restrictions", translated.Args)
	}
	data := "user: '0:0'\nentrypoint: [/reploy-probe]\ncommand: [restricted-exec, --uid, '1000', --gid, '1001', --, /bin/true]\ncap_add: [SETGID, SETUID, SETPCAP]\ntmpfs: ['/mnt/reploy-home:rw,noexec,nosuid,nodev,size=64m,mode=0700,uid=1000,gid=1001', '" + canonical + "', '/unrelated:rw,mode=1777']\n"
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(data), &document); err != nil {
		t.Fatal(err)
	}
	if err := preparePodmanTemporaryHomeCompose(document.Content[0]); err != nil {
		t.Fatal(err)
	}
	mounts, _ := composePodmanField(document.Content[0], "tmpfs")
	if mounts.Content[1].Value != canonical+",notmpcopyup" || mounts.Content[2].Value != "/unrelated:rw,mode=1777" {
		t.Fatal("Compose mask did not preserve the empty shadow contract")
	}
}
