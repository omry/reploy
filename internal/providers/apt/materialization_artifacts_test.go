package apt

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

type recordingAPTArtifactSink struct {
	contents map[string][]byte
}

func (sink *recordingAPTArtifactSink) Publish(_ context.Context, logicalPath string, kind string, reader io.Reader) (providerstore.ArtifactDescriptor, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, err
	}
	if sink.contents == nil {
		sink.contents = map[string][]byte{}
	}
	sink.contents[logicalPath] = content
	return materializationArtifactDescriptorV1(logicalPath, kind, content), nil
}

var _ providers.ArtifactSink = (*recordingAPTArtifactSink)(nil)

func TestMaterializationStateManifestBindsExactMixedClosure(t *testing.T) {
	bundle, err := NewBundleV1("amd64", aptMixedResolvePlan(), []PackageTuple{
		{Name: "iproute2", Version: "6.1-1", Architecture: "amd64", Status: InstalledPackageStatusV1},
		{Name: "libc6", Version: "2.39", Architecture: "amd64", Status: InstalledPackageStatusV1},
		{Name: "perl-modules", Version: "5.38", Architecture: "all", Status: InstalledPackageStatusV1},
	}, aptMixedBundlePackages())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := materializationStateManifestBytesV1(bundle)
	if err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	for _, want := range []string{
		"reploy-apt-state-v1\tamd64\n",
		"base\tlibc6\t2.39\tamd64",
		"bundle\thello\t2.10\tamd64\t-",
		"bundle\tiproute2\t6.1-2\tamd64\t6.1-1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest does not contain %q:\n%s", want, text)
		}
	}
	if descriptor := materializationStateManifestDescriptorV1(manifest); descriptor != bundle.StateManifest {
		t.Fatalf("manifest descriptor = %#v, bundle = %#v", descriptor, bundle.StateManifest)
	}

	sink := &recordingAPTArtifactSink{}
	if err := PublishMaterializationArtifactsV1(context.Background(), sink, bundle); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sink.contents[bundle.StateManifest.LogicalPath], manifest) || !bytes.Equal(sink.contents[bundle.Script.LogicalPath], []byte(materializationScriptV1)) {
		t.Fatalf("published artifacts do not match bundle materialization content")
	}
}

func TestMaterializationScriptIsPOSIXShellSyntax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell syntax validation requires a POSIX host")
	}
	command := exec.Command("/bin/sh", "-n")
	command.Stdin = strings.NewReader(materializationScriptV1)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("materialization script syntax: %v: %s", err, output)
	}
}

func TestMaterializationScriptRetainsOfflineAPTPolicy(t *testing.T) {
	for _, required := range []string{
		"--assume-yes", "--no-remove", "--no-install-recommends",
		"APT::Install-Suggests=false", "Dpkg::Use-Pty=0",
		"Dpkg::Options::=--force-confdef", "Dpkg::Options::=--force-confold",
		`if [ "$#" -ne 0 ]`, "apt.artifact_invalid", "materialization.failed",
		"validation.mismatch", "the installed package state differs from the resolved APT bundle",
	} {
		if !strings.Contains(materializationScriptV1, required) {
			t.Fatalf("materialization script is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"--no-download", " upgrade", "full-upgrade", "dist-upgrade",
		"--allow-downgrades", "--allow-unauthenticated", "--force-yes",
		"--allow-remove-essential", "--allow-change-held-packages",
	} {
		if strings.Contains(materializationScriptV1, forbidden) {
			t.Fatalf("materialization script contains forbidden operation %q", forbidden)
		}
	}
}

func aptMixedBundlePackages() []BundlePackage {
	digest := testDigestForAPTMaterialization("a")
	predecessor := PackageTuple{Name: "iproute2", Version: "6.1-1", Architecture: "amd64", Status: InstalledPackageStatusV1}
	return []BundlePackage{
		{Tuple: PackageTuple{Name: "hello", Version: "2.10", Architecture: "amd64", Status: InstalledPackageStatusV1}, Artifact: testAPTArtifact("debs/hello.deb", digest), FileListDigest: digest},
		{Tuple: PackageTuple{Name: "iproute2", Version: "6.1-2", Architecture: "amd64", Status: InstalledPackageStatusV1}, Artifact: testAPTArtifact("debs/iproute2.deb", testDigestForAPTMaterialization("b")), BasePredecessor: &predecessor, FileListDigest: digest},
	}
}

func testDigestForAPTMaterialization(character string) canonical.Digest {
	return canonical.Digest("sha256:" + strings.Repeat(character, 64))
}
