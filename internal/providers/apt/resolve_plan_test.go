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
