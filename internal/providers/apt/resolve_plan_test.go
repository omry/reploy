package apt

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolvePlanMarkerParserBuildsCanonicalMixedClosureFromChunks(t *testing.T) {
	parser, err := NewResolvePlanMarkerParserV1("amd64", []string{"hello=2.10-3build1", "iproute2"})
	if err != nil {
		t.Fatal(err)
	}
	input := "  MarkInstall iproute2:amd64 < 6.1-1 -> 6.1-2 @ii pumU > FU=1\n" +
		"    MarkInstall libc6:amd64 < 2.39-0ubuntu8.7 @ii pmK Ib > FU=0\n" +
		"  MarkInstall hello:amd64 < none -> 2.10-3build1 @un puN > FU=1\n" +
		"  Ignore MarkGarbage of libexample:amd64 < none -> 1.0 @un pK Ib > as its mode (Install) is protected\n"
	for _, chunk := range []string{input[:17], input[17:91], input[91:]} {
		if _, err := parser.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := parser.Finish()
	if err != nil {
		t.Fatal(err)
	}
	want := ResolvePlanV1{Schema: ResolvePlanSchemaV1, Packages: []ResolvePlanPackageV1{
		{Name: "hello", ResolverArchitecture: "amd64", SelectedVersion: "2.10-3build1"},
		{Name: "iproute2", ResolverArchitecture: "amd64", CurrentVersion: "6.1-1", SelectedVersion: "6.1-2"},
		{Name: "libc6", ResolverArchitecture: "amd64", CurrentVersion: "2.39-0ubuntu8.7", SelectedVersion: "2.39-0ubuntu8.7"},
	}}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan = %#v, want %#v", plan, want)
	}
}

func TestResolvePlanMarkerParserRejectsPartialUnsupportedAndConflictingEvidence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "missing root", input: "  MarkInstall libc6:amd64 < 1 @ii pmK > FU=0\n", want: "did not report requested"},
		{name: "unknown marker", input: "  MarkDelete hello:amd64 < 1 @ii pmK > FU=1\n", want: "unsupported output"},
		{name: "incomplete line", input: "  MarkInstall hello:amd64 < none -> 1 @un puN > FU=1", want: "incomplete line"},
		{name: "foreign architecture", input: "  MarkInstall hello:arm64 < none -> 1 @un puN > FU=1\n", want: "unexpected resolver architecture"},
		{name: "invalid absent state", input: "  MarkInstall hello:amd64 < none -> 1 @ii puN > FU=1\n", want: "inconsistent absent state"},
		{name: "conflict", input: "  MarkInstall hello:amd64 < none -> 1 @un puN > FU=1\n  MarkInstall hello:amd64 < none -> 2 @un puN > FU=1\n", want: "conflicting selections"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser, err := NewResolvePlanMarkerParserV1("amd64", []string{"hello"})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = parser.Write([]byte(test.input))
			_, err = parser.Finish()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolvePlanMarkerParserRejectsWrongExactRootVersionWithoutLeakingIt(t *testing.T) {
	parser, err := NewResolvePlanMarkerParserV1("amd64", []string{"hello=2"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = parser.Write([]byte("  MarkInstall hello:amd64 < none -> 1 @un puN > FU=1\n"))
	_, err = parser.Finish()
	if err == nil || !strings.Contains(err.Error(), "does not match") || strings.Contains(err.Error(), "hello=2") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveInstallParserCompletesUpgradedDependencyClosure(t *testing.T) {
	parser, err := NewResolveInstallParserV1("amd64")
	if err != nil {
		t.Fatal(err)
	}
	input := "Reading package lists...\n" +
		"Inst libssl3t64 [3.0.13-0ubuntu3.11] (3.0.13-0ubuntu3.12 Ubuntu:24.04/noble-updates [amd64])\n" +
		"Inst openssl-provider-legacy [3.5.5-1ubuntu3.2] (3.5.5-1ubuntu3.3 Ubuntu:26.04/resolute-updates [amd64])\n" +
		"Inst ca-certificates (20260601~24.04.1 Ubuntu:24.04/noble-updates [all])\n" +
		"Conf ca-certificates (20260601~24.04.1 Ubuntu:24.04/noble-updates [all])\n"
	for _, chunk := range []string{input[:31], input[31:117], input[117:]} {
		if _, err := parser.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := parser.Finish()
	if err != nil {
		t.Fatal(err)
	}
	want := []ResolvePlanPackageV1{
		{Name: "ca-certificates", ResolverArchitecture: "amd64", SelectedVersion: "20260601~24.04.1"},
		{Name: "libssl3t64", ResolverArchitecture: "amd64", CurrentVersion: "3.0.13-0ubuntu3.11", SelectedVersion: "3.0.13-0ubuntu3.12"},
		{Name: "openssl-provider-legacy", ResolverArchitecture: "amd64", CurrentVersion: "3.5.5-1ubuntu3.2", SelectedVersion: "3.5.5-1ubuntu3.3"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}

	marker := ResolvePlanV1{Schema: ResolvePlanSchemaV1, Packages: []ResolvePlanPackageV1{
		{Name: "ca-certificates", ResolverArchitecture: "amd64", SelectedVersion: "20260601~24.04.1"},
		{Name: "mawk", ResolverArchitecture: "amd64", CurrentVersion: "1.3", SelectedVersion: "1.3"},
	}}
	plan, err := CompleteResolvePlanV1(marker, changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Packages) != 4 || plan.Packages[1].Name != "libssl3t64" || plan.Packages[3].Name != "openssl-provider-legacy" {
		t.Fatalf("completed plan = %#v", plan)
	}
}

func TestResolveInstallParserAcceptsOptionalEmptyMarkerAcrossChunks(t *testing.T) {
	parser, err := NewResolveInstallParserV1("amd64")
	if err != nil {
		t.Fatal(err)
	}
	chunks := []string{
		"Inst dovecot-sieve (1:2.4.1-4 Debian:13/stable [amd64]) [",
		"]\nInst libssl3t64 [3.0.13-0ubuntu3.11] ",
		"(3.0.13-0ubuntu3.12 Ubuntu:24.04/noble-updates [amd64]) []\n",
	}
	for _, chunk := range chunks {
		if _, err := parser.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := parser.Finish()
	if err != nil {
		t.Fatal(err)
	}
	want := []ResolvePlanPackageV1{
		{Name: "dovecot-sieve", ResolverArchitecture: "amd64", SelectedVersion: "1:2.4.1-4"},
		{Name: "libssl3t64", ResolverArchitecture: "amd64", CurrentVersion: "3.0.13-0ubuntu3.11", SelectedVersion: "3.0.13-0ubuntu3.12"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}
}

func TestResolveInstallParserRejectsMalformedTrailingMarkers(t *testing.T) {
	tests := []string{
		"Inst hello (2.10-3 Debian:13/stable [amd64]) [amd64]\n",
		"Inst hello (2.10-3 Debian:13/stable [amd64]) [] []\n",
		"Inst hello (2.10-3 Debian:13/stable [amd64]) [ ]\n",
		"Inst hello (2.10-3 Debian:13/stable [amd64])[]\n",
		"Inst hello (2.10-3 Debian:13/stable [amd64]) [] extra\n",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			parser, err := NewResolveInstallParserV1("amd64")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = parser.Write([]byte(input))
			if _, err := parser.Finish(); err == nil || !strings.Contains(err.Error(), "malformed") {
				t.Fatalf("err = %v, want malformed record", err)
			}
		})
	}
}

func TestCompleteResolvePlanRejectsMissingAndConflictingInstallEvidence(t *testing.T) {
	marker := ResolvePlanV1{Schema: ResolvePlanSchemaV1, Packages: []ResolvePlanPackageV1{{
		Name: "hello", ResolverArchitecture: "amd64", SelectedVersion: "1",
	}}}
	if _, err := CompleteResolvePlanV1(marker, nil); err == nil || !strings.Contains(err.Error(), "no install transaction") {
		t.Fatalf("missing evidence error = %v", err)
	}
	_, err := CompleteResolvePlanV1(marker, []ResolvePlanPackageV1{{
		Name: "hello", ResolverArchitecture: "amd64", SelectedVersion: "2",
	}})
	if err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("conflicting evidence error = %v", err)
	}
	_, err = CompleteResolvePlanV1(marker, []ResolvePlanPackageV1{{
		Name: "other", ResolverArchitecture: "arm64", SelectedVersion: "1",
	}})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("architecture evidence error = %v", err)
	}
}
