package deploy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestOperationLockRoundTripsValidatedBuild(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	record := ValidatedBuildV1{
		Schema: ValidatedBuildSchemaV1, BlueprintDigest: digest, OverlayDigest: digest,
		PackageOverridesDigest: digest, Platform: platform, BuildLockDigest: digest,
		Image: validatedBuildTestImage(digest), ImageReference: "reploy/env/demo:validated",
		PendingCleanup: []ValidatedBuildReferenceV1{{
			Image: validatedBuildTestImage(digest), ImageReference: "reploy/env/demo:older",
		}},
	}
	if err := lock.CommitValidatedBuildV1(record); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := lock.ReadValidatedBuildV1()
	if err != nil || !found || !reflect.DeepEqual(loaded, record) {
		t.Fatalf("loaded = %#v, found=%v, err=%v", loaded, found, err)
	}
	if err := lock.RemoveValidatedBuildV1(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := lock.ReadValidatedBuildV1(); err != nil || found {
		t.Fatalf("validated build remained: found=%v err=%v", found, err)
	}
}

func TestPackageOverridesDigestV1SupportsNormalizedYAMLScalars(t *testing.T) {
	first, err := DecodePackageOverridesV1([]byte(`
environment:
  id: demo
  vars:
    count: 01
    enabled: true
    ratio: 1.5
  package_overrides: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := DecodePackageOverridesV1([]byte(`
environment:
  package_overrides: {}
  vars: {ratio: 1.50, enabled: true, count: 1}
  id: demo
`))
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := PackageOverridesDigestV1(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := PackageOverridesDigestV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("equivalent overrides have different digests: %s != %s", firstDigest, secondDigest)
	}

	stringValue, err := DecodePackageOverridesV1([]byte(`
environment:
  id: demo
  vars:
    count: "1"
    enabled: true
    ratio: 1.5
  package_overrides: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	stringDigest, err := PackageOverridesDigestV1(stringValue)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == stringDigest {
		t.Fatal("numeric and string override variables have the same digest")
	}
}

func TestPackageOverridesDigestV1NormalizesEmptyOptionalVars(t *testing.T) {
	withoutVars := PackageOverridesV1{Environment: PackageOverridesEnvironmentV1{
		ID: "demo", PackageOverrides: map[string]map[string]PackageOverrideChoiceV1{},
	}}
	withEmptyVars := withoutVars
	withEmptyVars.Environment.Vars = map[string]any{}
	if reflect.DeepEqual(withoutVars, withEmptyVars) {
		t.Fatal("test inputs unexpectedly have the same Go representation")
	}
	first, err := PackageOverridesDigestV1(withoutVars)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PackageOverridesDigestV1(withEmptyVars)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("equivalent empty vars have different digests: %s != %s", first, second)
	}
}

func TestPackageOverridesDigestV1NormalizesExclusionOrder(t *testing.T) {
	first := EmptyPackageOverridesV1("demo")
	first.Environment.PackageOverrides["python"] = map[string]PackageOverrideChoiceV1{
		"demo": {Path: "../demo", Exclude: []string{"recordings/.omegaflow", ".venv"}},
	}
	second := EmptyPackageOverridesV1("demo")
	second.Environment.PackageOverrides["python"] = map[string]PackageOverrideChoiceV1{
		"demo": {Path: "../demo", Exclude: []string{".venv", "recordings/.omegaflow"}},
	}

	firstDigest, err := PackageOverridesDigestV1(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := PackageOverridesDigestV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("equivalent exclusion orders have different digests: %s != %s", firstDigest, secondDigest)
	}
	if got := first.Environment.PackageOverrides["python"]["demo"].Exclude; !reflect.DeepEqual(got, []string{"recordings/.omegaflow", ".venv"}) {
		t.Fatalf("digest mutated caller exclusions: %#v", got)
	}
}

func TestValidateValidatedBuildV1RejectsUnsafeImageReference(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	valid := ValidatedBuildV1{
		Schema: ValidatedBuildSchemaV1, BlueprintDigest: digest, OverlayDigest: digest,
		PackageOverridesDigest: digest, Platform: platform, BuildLockDigest: digest,
		Image: validatedBuildTestImage(digest), ImageReference: "reploy/env/demo:validated",
	}
	for _, reference := range []string{
		" reploy/env/demo:validated",
		"-reploy/env/demo:validated",
		"docker://reploy/env/demo:validated",
		"reploy/env/demo:\nvalidated",
	} {
		record := valid
		record.ImageReference = reference
		if err := ValidateValidatedBuildV1(record); err == nil {
			t.Fatalf("unsafe image reference %q was accepted", reference)
		}
	}
}

func TestValidateValidatedBuildV1RejectsAmbiguousPendingCleanup(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	valid := ValidatedBuildV1{
		Schema: ValidatedBuildSchemaV1, BlueprintDigest: digest, OverlayDigest: digest,
		PackageOverridesDigest: digest, Platform: platform, BuildLockDigest: digest,
		Image: validatedBuildTestImage(digest), ImageReference: "reploy/env/demo:validated",
	}
	sameAsCurrent := valid
	sameAsCurrent.PendingCleanup = []ValidatedBuildReferenceV1{{
		Image: valid.Image, ImageReference: valid.ImageReference,
	}}
	if err := ValidateValidatedBuildV1(sameAsCurrent); err == nil {
		t.Fatal("current image reference was accepted as pending cleanup")
	}
	unsorted := valid
	unsorted.PendingCleanup = []ValidatedBuildReferenceV1{
		{Image: valid.Image, ImageReference: "reploy/env/demo:z"},
		{Image: valid.Image, ImageReference: "reploy/env/demo:a"},
	}
	if err := ValidateValidatedBuildV1(unsorted); err == nil {
		t.Fatal("unsorted pending cleanup was accepted")
	}
}

func TestValidateValidatedBuildV1RejectsInvalidDiscardedState(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	valid := ValidatedBuildV1{
		Schema: ValidatedBuildSchemaV1, BlueprintDigest: digest, OverlayDigest: digest,
		PackageOverridesDigest: digest, Platform: platform, BuildLockDigest: digest,
		Image: validatedBuildTestImage(digest), ImageReference: "reploy/env/demo:validated",
		Discarded: true, PendingStorageCleanup: true,
	}
	if err := ValidateValidatedBuildV1(valid); err != nil {
		t.Fatalf("valid discarded state was rejected: %v", err)
	}
	withoutStorageCleanup := valid
	withoutStorageCleanup.PendingStorageCleanup = false
	if err := ValidateValidatedBuildV1(withoutStorageCleanup); err == nil {
		t.Fatal("discarded state without pending storage cleanup was accepted")
	}
	withImageReference := valid
	withImageReference.PendingCleanup = []ValidatedBuildReferenceV1{{
		Image: valid.Image, ImageReference: "reploy/env/demo:old",
	}}
	if err := ValidateValidatedBuildV1(withImageReference); err == nil {
		t.Fatal("discarded state with a pending image reference was accepted")
	}
}

func validatedBuildTestImage(digest canonical.Digest) providers.RealizedImageV1 {
	return providers.RealizedImageV1{
		Digest: digest, ConfigDigest: digest, RootFSSubject: digest,
	}
}
