package apt

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

func TestSelectedBundlePackagesAndUpgradePredecessor(t *testing.T) {
	plan := aptMixedResolvePlan()
	selected, err := SelectedBundlePackagesV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].Name != "hello" || selected[1].Name != "iproute2" {
		t.Fatalf("selected = %#v", selected)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	artifact := providerstore.ArtifactDescriptor{LogicalPath: "debs/iproute2.deb", Kind: "deb", Size: "10", SHA256: digest}
	base := []PackageTuple{{Name: "iproute2", Version: "6.1-1", Architecture: "amd64", Status: InstalledPackageStatusV1}}
	bundle, err := NewBundlePackageV1(selected[1], PackageTuple{Name: "iproute2", Version: "6.1-2", Architecture: "amd64", Status: InstalledPackageStatusV1}, artifact, digest, base)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.BasePredecessor == nil || bundle.BasePredecessor.Version != "6.1-1" {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestNewBundleV1BuildsCanonicalMixedOriginClosure(t *testing.T) {
	plan := aptMixedResolvePlan()
	digest := canonical.Digest("sha256:" + strings.Repeat("c", 64))
	baseState := []PackageTuple{
		{Name: "iproute2", Version: "6.1-1", Architecture: "amd64", Status: InstalledPackageStatusV1},
		{Name: "libc6", Version: "2.39", Architecture: "amd64", Status: InstalledPackageStatusV1},
		{Name: "perl-modules", Version: "5.38", Architecture: "all", Status: InstalledPackageStatusV1},
	}
	bundles := []BundlePackage{
		{Tuple: PackageTuple{Name: "hello", Version: "2.10", Architecture: "amd64", Status: InstalledPackageStatusV1}, Artifact: providerstore.ArtifactDescriptor{LogicalPath: "debs/hello.deb", Kind: "deb", Size: "10", SHA256: digest}, FileListDigest: digest},
		{Tuple: PackageTuple{Name: "iproute2", Version: "6.1-2", Architecture: "amd64", Status: InstalledPackageStatusV1}, Artifact: providerstore.ArtifactDescriptor{LogicalPath: "debs/iproute2.deb", Kind: "deb", Size: "20", SHA256: canonical.Digest("sha256:" + strings.Repeat("d", 64))}, BasePredecessor: &baseState[0], FileListDigest: digest},
	}
	bundle, err := NewBundleV1("amd64", plan, baseState, bundles)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.BasePackages) != 2 || bundle.BasePackages[0].Tuple.Name != "libc6" || len(bundle.BundlePackages) != 2 {
		t.Fatalf("bundle = %#v", bundle)
	}
	if bundle.Script.Kind != "script" || bundle.StateManifest.Kind != materializationManifestKindV1 {
		t.Fatalf("materialization artifacts = %#v, %#v", bundle.Script, bundle.StateManifest)
	}
	data, err := CanonicalBundleDataV1(bundle)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCanonicalBundleDataV1(data)
	if err != nil || !reflect.DeepEqual(decoded, bundle) {
		t.Fatalf("decoded = %#v, err = %v", decoded, err)
	}
}

func TestValidateBundleV1RejectsOverlapAndNoncanonicalOrder(t *testing.T) {
	digest := canonical.Digest("sha256:" + strings.Repeat("e", 64))
	tuple := PackageTuple{Name: "hello", Version: "1", Architecture: "amd64", Status: InstalledPackageStatusV1}
	bundle := BundleV1{NativeArchitecture: "amd64", BasePackages: []BasePackage{{Tuple: tuple}}, BundlePackages: []BundlePackage{{Tuple: tuple, Artifact: providerstore.ArtifactDescriptor{LogicalPath: "debs/hello.deb", Kind: "deb", Size: "1", SHA256: digest}, FileListDigest: digest}}}
	if err := ValidateBundleV1(bundle); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("overlap err = %v", err)
	}
	bundle.BasePackages = []BasePackage{{Tuple: PackageTuple{Name: "zlib", Version: "1", Architecture: "amd64", Status: InstalledPackageStatusV1}}, {Tuple: tuple}}
	bundle.BundlePackages = []BundlePackage{}
	if err := ValidateBundleV1(bundle); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Fatalf("order err = %v", err)
	}
}

func TestNewBundlePackageRejectsMismatchAndMissingPredecessor(t *testing.T) {
	plan := ResolvePlanPackageV1{Name: "demo", ResolverArchitecture: "amd64", CurrentVersion: "1", SelectedVersion: "2"}
	digest := canonical.Digest("sha256:" + strings.Repeat("b", 64))
	artifact := providerstore.ArtifactDescriptor{LogicalPath: "debs/demo.deb", Kind: "deb", Size: "1", SHA256: digest}
	if _, err := NewBundlePackageV1(plan, PackageTuple{Name: "demo", Version: "3", Architecture: "amd64", Status: InstalledPackageStatusV1}, artifact, digest, []PackageTuple{}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatch err = %v", err)
	}
	if _, err := NewBundlePackageV1(plan, PackageTuple{Name: "demo", Version: "2", Architecture: "amd64", Status: InstalledPackageStatusV1}, artifact, digest, []PackageTuple{}); err == nil || !strings.Contains(err.Error(), "predecessor") {
		t.Fatalf("predecessor err = %v", err)
	}
}
