package apt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const BundleSchemaV1 = "apt-bundle-v1"

type BasePackage struct {
	Tuple PackageTuple `json:"tuple"`
}

type BundlePackage struct {
	Tuple           PackageTuple                     `json:"tuple"`
	Artifact        providerstore.ArtifactDescriptor `json:"artifact"`
	BasePredecessor *PackageTuple                    `json:"base_predecessor,omitempty"`
	FileListDigest  canonical.Digest                 `json:"file_list_digest"`
}

type BundleV1 struct {
	NativeArchitecture string                           `json:"native_architecture"`
	BasePackages       []BasePackage                    `json:"base_packages"`
	BundlePackages     []BundlePackage                  `json:"bundle_packages"`
	Script             providerstore.ArtifactDescriptor `json:"script"`
	StateManifest      providerstore.ArtifactDescriptor `json:"state_manifest"`
}

func NewBundleV1(nativeArchitecture string, plan ResolvePlanV1, baseState []PackageTuple, bundlePackages []BundlePackage) (BundleV1, error) {
	if !validResolverArchitectureV1(nativeArchitecture) {
		return BundleV1{}, fmt.Errorf("APT bundle native architecture is invalid")
	}
	selected, err := SelectedBundlePackagesV1(plan)
	if err != nil {
		return BundleV1{}, err
	}
	baseByName := make(map[string]PackageTuple, len(baseState))
	for index, tuple := range baseState {
		if err := validatePackageTupleV1(tuple, nativeArchitecture); err != nil {
			return BundleV1{}, fmt.Errorf("APT base state tuple %d: %w", index, err)
		}
		if index > 0 && comparePackageTupleV1(baseState[index-1], tuple) >= 0 {
			return BundleV1{}, fmt.Errorf("APT base state tuples must be unique and sorted")
		}
		baseByName[tuple.Name] = tuple
	}
	bundleByName := make(map[string]BundlePackage, len(bundlePackages))
	for _, pkg := range bundlePackages {
		if _, exists := bundleByName[pkg.Tuple.Name]; exists {
			return BundleV1{}, fmt.Errorf("APT bundle contains duplicate archive package %q", pkg.Tuple.Name)
		}
		bundleByName[pkg.Tuple.Name] = pkg
	}
	if len(bundleByName) != len(selected) {
		return BundleV1{}, fmt.Errorf("APT bundle archive package count does not match selected closure")
	}
	for _, pkg := range selected {
		bundle, found := bundleByName[pkg.Name]
		if !found || bundle.Tuple.Version != pkg.SelectedVersion {
			return BundleV1{}, fmt.Errorf("APT selected package %q is missing its exact bundle archive", pkg.Name)
		}
	}
	bases := make([]BasePackage, 0, len(plan.Packages)-len(selected))
	for _, pkg := range plan.Packages {
		if pkg.CurrentVersion == "" || pkg.CurrentVersion != pkg.SelectedVersion {
			continue
		}
		tuple, found := baseByName[pkg.Name]
		if !found || tuple.Version != pkg.SelectedVersion {
			return BundleV1{}, fmt.Errorf("APT retained package %q is missing its exact base tuple", pkg.Name)
		}
		bases = append(bases, BasePackage{Tuple: tuple})
	}
	bundles := append([]BundlePackage{}, bundlePackages...)
	sort.Slice(bases, func(left int, right int) bool {
		return comparePackageTupleV1(bases[left].Tuple, bases[right].Tuple) < 0
	})
	sort.Slice(bundles, func(left int, right int) bool {
		return comparePackageTupleV1(bundles[left].Tuple, bundles[right].Tuple) < 0
	})
	bundle := BundleV1{NativeArchitecture: nativeArchitecture, BasePackages: bases, BundlePackages: bundles}
	bundle.Script = materializationScriptDescriptorV1()
	manifest, err := materializationStateManifestBytesV1(bundle)
	if err != nil {
		return BundleV1{}, err
	}
	bundle.StateManifest = materializationStateManifestDescriptorV1(manifest)
	if err := ValidateBundleV1(bundle); err != nil {
		return BundleV1{}, err
	}
	return bundle, nil
}

func ValidateBundleV1(bundle BundleV1) error {
	if !validResolverArchitectureV1(bundle.NativeArchitecture) {
		return fmt.Errorf("APT bundle native architecture is invalid")
	}
	if bundle.BasePackages == nil || bundle.BundlePackages == nil {
		return fmt.Errorf("APT bundle package collections must use arrays")
	}
	seenNames := map[string]string{}
	seenArtifacts := map[string]bool{}
	for index, pkg := range bundle.BasePackages {
		if err := validatePackageTupleV1(pkg.Tuple, bundle.NativeArchitecture); err != nil {
			return fmt.Errorf("APT base package %d: %w", index, err)
		}
		if index > 0 && comparePackageTupleV1(bundle.BasePackages[index-1].Tuple, pkg.Tuple) >= 0 {
			return fmt.Errorf("APT base packages must be unique and sorted")
		}
		seenNames[pkg.Tuple.Name] = "base"
	}
	for index, pkg := range bundle.BundlePackages {
		if err := validatePackageTupleV1(pkg.Tuple, bundle.NativeArchitecture); err != nil {
			return fmt.Errorf("APT bundle package %d: %w", index, err)
		}
		if index > 0 && comparePackageTupleV1(bundle.BundlePackages[index-1].Tuple, pkg.Tuple) >= 0 {
			return fmt.Errorf("APT bundle packages must be unique and sorted")
		}
		if origin, exists := seenNames[pkg.Tuple.Name]; exists {
			return fmt.Errorf("APT package %q appears in both %s and bundle origins", pkg.Tuple.Name, origin)
		}
		seenNames[pkg.Tuple.Name] = "bundle"
		if err := pkg.Artifact.Validate(); err != nil || pkg.Artifact.Kind != "deb" || !strings.HasPrefix(pkg.Artifact.LogicalPath, "debs/") || !strings.HasSuffix(pkg.Artifact.LogicalPath, ".deb") {
			return fmt.Errorf("APT bundle package %q artifact is invalid", pkg.Tuple.Name)
		}
		if seenArtifacts[pkg.Artifact.LogicalPath] {
			return fmt.Errorf("APT bundle artifact %q is duplicated", pkg.Artifact.LogicalPath)
		}
		seenArtifacts[pkg.Artifact.LogicalPath] = true
		if err := pkg.FileListDigest.Validate(); err != nil {
			return fmt.Errorf("APT bundle package %q file-list digest is invalid", pkg.Tuple.Name)
		}
		if pkg.BasePredecessor != nil {
			if err := validatePackageTupleV1(*pkg.BasePredecessor, bundle.NativeArchitecture); err != nil {
				return fmt.Errorf("APT bundle package %q predecessor: %w", pkg.Tuple.Name, err)
			}
			if pkg.BasePredecessor.Name != pkg.Tuple.Name || pkg.BasePredecessor.Version == pkg.Tuple.Version {
				return fmt.Errorf("APT bundle package %q predecessor does not describe a replaced version", pkg.Tuple.Name)
			}
		}
	}
	if bundle.Script != materializationScriptDescriptorV1() {
		return fmt.Errorf("APT bundle materialization script does not match the provider-owned script")
	}
	manifest, err := materializationStateManifestBytesV1(bundle)
	if err != nil {
		return err
	}
	if bundle.StateManifest != materializationStateManifestDescriptorV1(manifest) {
		return fmt.Errorf("APT bundle state manifest does not match its package closure")
	}
	return nil
}

func CanonicalBundleDataV1(bundle BundleV1) (providers.CanonicalProviderData, error) {
	if err := ValidateBundleV1(bundle); err != nil {
		return providers.CanonicalProviderData{}, err
	}
	encoded, err := canonical.Marshal(bundle)
	if err != nil {
		return providers.CanonicalProviderData{}, err
	}
	var value canonical.Object
	if err := json.Unmarshal(encoded, &value); err != nil {
		return providers.CanonicalProviderData{}, err
	}
	return providers.CanonicalProviderData{Schema: BundleSchemaV1, Value: value}, nil
}

func DecodeCanonicalBundleDataV1(data providers.CanonicalProviderData) (BundleV1, error) {
	if data.Schema != BundleSchemaV1 || len(data.Value) != 5 {
		return BundleV1{}, fmt.Errorf("APT bundle data must use schema %q and the exact value shape", BundleSchemaV1)
	}
	encoded, err := canonical.Marshal(data.Value)
	if err != nil {
		return BundleV1{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var bundle BundleV1
	if err := decoder.Decode(&bundle); err != nil {
		return BundleV1{}, fmt.Errorf("decode APT bundle data: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return BundleV1{}, fmt.Errorf("decode APT bundle data trailer")
	}
	if err := ValidateBundleV1(bundle); err != nil {
		return BundleV1{}, err
	}
	normalized, err := CanonicalBundleDataV1(bundle)
	if err != nil {
		return BundleV1{}, err
	}
	actual, _ := canonical.Marshal(data)
	expected, _ := canonical.Marshal(normalized)
	if !bytes.Equal(actual, expected) {
		return BundleV1{}, fmt.Errorf("APT bundle data is not canonically normalized")
	}
	return bundle, nil
}

func comparePackageTupleV1(left PackageTuple, right PackageTuple) int {
	if left.Name != right.Name {
		return strings.Compare(left.Name, right.Name)
	}
	if left.Architecture != right.Architecture {
		return strings.Compare(left.Architecture, right.Architecture)
	}
	return strings.Compare(left.Version, right.Version)
}

func validatePackageTupleV1(tuple PackageTuple, nativeArchitecture string) error {
	if !debianPackageNameV1.MatchString(tuple.Name) || !validDebianVersionTokenV1(tuple.Version) {
		return fmt.Errorf("package identity is invalid")
	}
	if tuple.Architecture != nativeArchitecture && tuple.Architecture != "all" {
		return fmt.Errorf("package %q has unsupported architecture %q", tuple.Name, tuple.Architecture)
	}
	if tuple.Status != InstalledPackageStatusV1 {
		return fmt.Errorf("package %q status is not exact installed state", tuple.Name)
	}
	return nil
}

// SelectedBundlePackagesV1 returns the planned packages that require a .deb:
// packages absent from the base and packages selected at a newer exact version.
func SelectedBundlePackagesV1(plan ResolvePlanV1) ([]ResolvePlanPackageV1, error) {
	if _, err := ResolveBasePackageNamesV1(plan); err != nil {
		return nil, err
	}
	result := make([]ResolvePlanPackageV1, 0, len(plan.Packages))
	for _, pkg := range plan.Packages {
		if pkg.CurrentVersion == "" || pkg.CurrentVersion != pkg.SelectedVersion {
			result = append(result, pkg)
		}
	}
	return result, nil
}

// NewBundlePackageV1 binds an inspected selected archive to its dependency
// plan and optional exact base predecessor.
func NewBundlePackageV1(
	plan ResolvePlanPackageV1,
	tuple PackageTuple,
	artifact providerstore.ArtifactDescriptor,
	fileListDigest canonical.Digest,
	baseState []PackageTuple,
) (BundlePackage, error) {
	if plan.CurrentVersion != "" && plan.CurrentVersion == plan.SelectedVersion {
		return BundlePackage{}, fmt.Errorf("APT planned package %q does not require a bundle archive", plan.Name)
	}
	if tuple.Name != plan.Name || tuple.Version != plan.SelectedVersion || tuple.Status != InstalledPackageStatusV1 {
		return BundlePackage{}, fmt.Errorf("APT archive tuple does not match selected package %q", plan.Name)
	}
	if tuple.Architecture != plan.ResolverArchitecture && tuple.Architecture != "all" {
		return BundlePackage{}, fmt.Errorf("APT archive package %q has unexpected architecture %q", tuple.Name, tuple.Architecture)
	}
	if err := artifact.Validate(); err != nil || artifact.Kind != "deb" {
		return BundlePackage{}, fmt.Errorf("APT archive package %q artifact is invalid", tuple.Name)
	}
	if err := fileListDigest.Validate(); err != nil {
		return BundlePackage{}, fmt.Errorf("APT archive package %q file-list digest is invalid", tuple.Name)
	}
	var predecessor *PackageTuple
	if plan.CurrentVersion != "" {
		index := sort.Search(len(baseState), func(index int) bool { return baseState[index].Name >= plan.Name })
		if index == len(baseState) || baseState[index].Name != plan.Name || baseState[index].Version != plan.CurrentVersion {
			return BundlePackage{}, fmt.Errorf("APT archive package %q is missing its exact base predecessor", tuple.Name)
		}
		value := baseState[index]
		predecessor = &value
	}
	return BundlePackage{
		Tuple: tuple, Artifact: artifact, BasePredecessor: predecessor,
		FileListDigest: fileListDigest,
	}, nil
}
