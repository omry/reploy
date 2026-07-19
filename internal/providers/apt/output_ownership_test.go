package apt

import (
	"strings"
	"testing"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestParseDPKGSearchOutputRequiresOneLiteralOwnerPerPath(t *testing.T) {
	paths := []string{"/usr/bin/python3", "/usr/bin/python3.13"}
	owners, err := ParseDPKGSearchOutputV1(
		[]byte("python3-minimal: /usr/bin/python3\npython3.13-minimal:amd64: /usr/bin/python3.13\n"),
		paths,
		"amd64",
	)
	if err != nil {
		t.Fatal(err)
	}
	if owners[paths[0]] != "python3-minimal" || owners[paths[1]] != "python3.13-minimal" {
		t.Fatalf("owners = %#v", owners)
	}
	for _, test := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "missing", output: "python3-minimal: /usr/bin/python3\n", want: "did not identify"},
		{name: "expanded", output: "python3-minimal: /usr/bin/python3\npython3-minimal: /usr/bin/python3.12\n", want: "expanded"},
		{name: "duplicate", output: "python3-minimal: /usr/bin/python3\nother: /usr/bin/python3\n", want: "multiple owners"},
		{name: "foreign qualifier", output: "python3-minimal:arm64: /usr/bin/python3\n", want: "unexpected architecture"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseDPKGSearchOutputV1([]byte(test.output), paths, "amd64")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestApplyOutputOwnershipBindsEveryOrdinaryPathToLockedTuple(t *testing.T) {
	bundle := BundleV1{
		NativeArchitecture: "amd64",
		BasePackages: []BasePackage{{Tuple: PackageTuple{
			Name: "python3-minimal", Version: "3.13.1-1", Architecture: "amd64", Status: InstalledPackageStatusV1,
		}}},
		BundlePackages: []BundlePackage{}, Script: materializationScriptDescriptorV1(),
	}
	manifest, err := materializationStateManifestBytesV1(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.StateManifest = materializationStateManifestDescriptorV1(manifest)
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	evidence := []providers.ExecutableEvidence{{
		Schema: providers.ExecutableEvidenceSchemaV1,
		Output: providers.QualifiedOutput{Component: "system", Name: "python"}, InvocationPath: "/usr/bin/python3",
		LinkChain: []providers.LinkEvidence{{Path: "/usr/bin/python3", Target: "python3.13", ResolvedPath: "/usr/bin/python3.13", Kind: "ordinary"}},
		Terminal:  providers.FileEvidence{Schema: providers.FileEvidenceSchemaV1, Path: "/usr/bin/python3.13", Kind: "regular", Mode: "0755", Size: "1", SHA256: digest},
		Access: providers.PortableAccessEvidence{Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1, Paths: []providers.AccessPathEvidence{
			{Path: "/", Kind: "directory", Mode: "0755", Required: "other-search"},
			{Path: "/usr/bin/python3.13", Kind: "regular", Mode: "0755", Required: "other-read-execute"},
		}},
		Facts: providers.CanonicalProviderData{Schema: WellKnownToolSchemaV1, Value: canonical.Object{}},
	}}
	owned, err := ApplyOutputOwnershipV1(bundle, evidence, map[string]string{
		"/usr/bin/python3": "python3-minimal", "/usr/bin/python3.13": "python3-minimal",
	}, map[string]AlternativeSelectionV1{})
	if err != nil {
		t.Fatal(err)
	}
	if owned[0].LinkChain[0].Owner == nil || owned[0].Terminal.Owner == nil || owned[0].Terminal.Owner.Data.Schema != DPKGOwnerDataSchemaV1 || owned[0].Terminal.Owner.Data.Value["version"] != "3.13.1-1" {
		t.Fatalf("owned evidence = %#v", owned[0])
	}
	if _, err := ApplyOutputOwnershipV1(bundle, evidence, map[string]string{
		"/usr/bin/python3": "other", "/usr/bin/python3.13": "python3-minimal",
	}, map[string]AlternativeSelectionV1{}); err == nil || !strings.Contains(err.Error(), "outside the locked closure") {
		t.Fatalf("outside owner error = %v", err)
	}
}

func TestAlternativeQueryAndObservedChainMustAgree(t *testing.T) {
	query := "Name: java\nLink: /usr/bin/java\nSlaves:\nStatus: auto\nBest: /usr/lib/jvm/java-21/bin/java\nValue: /usr/lib/jvm/java-21/bin/java\n\nAlternative: /usr/lib/jvm/java-21/bin/java\nPriority: 2111\nSlaves:\n"
	selection, err := ParseAlternativeQueryV1([]byte(query), "java")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Link != "/usr/bin/java" || selection.Value != "/usr/lib/jvm/java-21/bin/java" {
		t.Fatalf("selection = %#v", selection)
	}
	bundle := BundleV1{
		NativeArchitecture: "amd64",
		BasePackages:       []BasePackage{{Tuple: PackageTuple{Name: "openjdk-21-jre-headless", Version: "21.0.1", Architecture: "amd64", Status: InstalledPackageStatusV1}}},
		BundlePackages:     []BundlePackage{}, Script: materializationScriptDescriptorV1(),
	}
	manifest, err := materializationStateManifestBytesV1(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.StateManifest = materializationStateManifestDescriptorV1(manifest)
	digest := canonical.Digest("sha256:" + strings.Repeat("b", 64))
	evidence := []providers.ExecutableEvidence{{
		Schema: providers.ExecutableEvidenceSchemaV1,
		Output: providers.QualifiedOutput{Component: "system", Name: "java"}, InvocationPath: "/usr/bin/java",
		LinkChain: []providers.LinkEvidence{
			{Path: "/usr/bin/java", Target: "/etc/alternatives/java", ResolvedPath: "/etc/alternatives/java", Kind: "ordinary"},
			{Path: "/etc/alternatives/java", Target: "/usr/lib/jvm/java-21/bin/java", ResolvedPath: "/usr/lib/jvm/java-21/bin/java", Kind: "ordinary"},
		},
		Terminal: providers.FileEvidence{Schema: providers.FileEvidenceSchemaV1, Path: "/usr/lib/jvm/java-21/bin/java", Kind: "regular", Mode: "0755", Size: "1", SHA256: digest},
		Access: providers.PortableAccessEvidence{Schema: providers.PortableAccessSchemaV1, Profile: providers.PortableOutputAccessV1, Paths: []providers.AccessPathEvidence{
			{Path: "/", Kind: "directory", Mode: "0755", Required: "other-search"},
			{Path: "/usr/lib/jvm/java-21/bin/java", Kind: "regular", Mode: "0755", Required: "other-read-execute"},
		}},
		Facts: providers.CanonicalProviderData{Schema: ExplicitExportSchemaV1, Value: canonical.Object{}},
	}}
	owned, err := ApplyOutputOwnershipV1(
		bundle,
		evidence,
		map[string]string{"/usr/lib/jvm/java-21/bin/java": "openjdk-21-jre-headless"},
		map[string]AlternativeSelectionV1{"/etc/alternatives/java": selection},
	)
	if err != nil {
		t.Fatal(err)
	}
	public, alternative := owned[0].LinkChain[0], owned[0].LinkChain[1]
	if public.Kind != "alternative" || public.Owner != nil || public.ProviderDetail == nil || alternative.Kind != "alternative" || alternative.Owner != nil || alternative.ProviderDetail == nil || alternative.ProviderDetail.Schema != AlternativeSelectionSchemaV1 {
		t.Fatalf("alternative evidence = %#v; %#v", public, alternative)
	}
	selection.Value = "/usr/lib/jvm/other/bin/java"
	if _, err := ApplyOutputOwnershipV1(
		bundle,
		evidence,
		map[string]string{"/usr/lib/jvm/java-21/bin/java": "openjdk-21-jre-headless"},
		map[string]AlternativeSelectionV1{"/etc/alternatives/java": selection},
	); err == nil || !strings.Contains(err.Error(), "mismatched alternatives") {
		t.Fatalf("mismatched selection error = %v", err)
	}
}
