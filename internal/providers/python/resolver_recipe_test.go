package python

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestWheelResolverArgvUsesOnePipClosureWithExactSourceRoots(t *testing.T) {
	requirement, err := CanonicalPackageRequestV1("demo>=1")
	if err != nil {
		t.Fatal(err)
	}
	request, err := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	wheel := providerstore.ArtifactDescriptor{LogicalPath: "wheels/local_demo-2-py3-none-any.whl", Kind: "wheel", Size: "10", SHA256: digest}
	source := providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV1, Component: "application", LogicalPackage: "local-demo",
		SourceManifestDigest: canonical.Digest("sha256:" + strings.Repeat("b", 64)), BuilderProfile: "uv-v1",
		BuildSettings:     providers.CanonicalProviderData{Schema: "source-settings-v1", Value: canonical.Object{}},
		EcosystemMetadata: providers.CanonicalProviderData{Schema: "python-source-v1", Value: canonical.Object{}},
		ArtifactDigest:    digest,
	}
	got, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{source}, []providerstore.ArtifactDescriptor{wheel})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/usr/bin/python3", "-m", "pip", "--disable-pip-version-check", "wheel", "--no-cache-dir",
		"--progress-bar", "off", "--find-links", ResolverInputDirectory,
		"--wheel-dir", ResolverOutputDirectory, "demo>=1", ResolverInputDirectory + "/local_demo-2-py3-none-any.whl",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func TestWheelResolverArgvRequiresSourceWheelInReusableInputs(t *testing.T) {
	requirement, _ := CanonicalPackageRequestV1("demo")
	request, _ := CanonicalProviderRequestV1(PythonProviderRequestV1{
		Component: "application", Interpreter: blueprint.CommandRequirement{Command: "python"},
		Requirements: []providers.CanonicalPackageRequest{requirement},
	})
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	source := providers.ResolvedSourceInput{
		Schema: providers.ResolvedSourceInputSchemaV1, Component: "application", LogicalPackage: "demo",
		SourceManifestDigest: digest, BuilderProfile: "uv-v1",
		BuildSettings:     providers.CanonicalProviderData{Schema: "source-settings-v1", Value: canonical.Object{}},
		EcosystemMetadata: providers.CanonicalProviderData{Schema: "python-source-v1", Value: canonical.Object{}},
		ArtifactDigest:    digest,
	}
	if _, err := WheelResolverArgv("/usr/bin/python3", request, []providers.ResolvedSourceInput{source}, nil); err == nil || !strings.Contains(err.Error(), "exactly one reusable wheel") {
		t.Fatalf("error = %v", err)
	}
}
