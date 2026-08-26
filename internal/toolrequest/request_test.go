package toolrequest

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNormalizeAndMergeV1CompactAndStructuredRequestsMatch(t *testing.T) {
	compact := decodeRequestsV1(t, "- tool:java==21\n")
	structured := decodeRequestsV1(t, "- tool: java\n  version: '==21'\n")
	left, err := NormalizeAndMergeV1(compact, "application:demo", "runtime", "compact")
	if err != nil {
		t.Fatal(err)
	}
	right, err := NormalizeAndMergeV1(structured, "application:demo", "runtime", "structured")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left.Groups, right.Groups) {
		t.Fatalf("compact and structured groups differ:\n%#v\n%#v", left.Groups, right.Groups)
	}
	want := CanonicalRequirementGroupV1{
		Scope: "application:demo", Tool: "java", VersionConstraints: []string{"==21"}, Context: "runtime",
		Binding: CanonicalBindingDemandV1{Infer: true, Explicit: []string{}}, Selections: map[string][]string{},
	}
	if len(left.Groups) != 1 || !reflect.DeepEqual(left.Groups[0], want) {
		t.Fatalf("group = %#v, want %#v", left.Groups, want)
	}
}

func TestNormalizeAndMergeV1CompactAndStructuredQualifiedToolNamesMatch(t *testing.T) {
	compact := decodeRequestsV1(t, "- tool:acme/playwright==1.61.0\n")
	structured := decodeRequestsV1(t, "- tool: acme/playwright\n  version: '==1.61.0'\n")
	left, err := NormalizeAndMergeV1(compact, "application:web", "runtime", "compact")
	if err != nil {
		t.Fatal(err)
	}
	right, err := NormalizeAndMergeV1(structured, "application:web", "runtime", "structured")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left.Groups, right.Groups) {
		t.Fatalf("compact and structured groups differ:\n%#v\n%#v", left.Groups, right.Groups)
	}
	if len(left.Groups) != 1 || left.Groups[0].Tool != "acme/playwright" {
		t.Fatalf("groups = %#v", left.Groups)
	}
}

func TestNormalizeAndMergeV1RetainsCanonicalCumulativeDemand(t *testing.T) {
	requests := decodeRequestsV1(t, `
- tool: playwright
  version: ">=1.60"
- tool: playwright
  version: "<2"
  definition_revision: 2
  binding: python
  select: {browser: [webkit, chromium]}
- tool: playwright
  binding: [node]
  select: {browser: chromium}
`)
	set, err := NormalizeAndMergeV1(requests, "application:web", "runtime", "tools")
	if err != nil {
		t.Fatal(err)
	}
	want := CanonicalRequirementGroupV1{
		Scope: "application:web", Tool: "playwright", VersionConstraints: []string{"<2", ">=1.60"},
		DefinitionRevision: "2", Context: "runtime",
		Binding:    CanonicalBindingDemandV1{Infer: true, Explicit: []string{"node", "python"}},
		Selections: map[string][]string{"browser": {"chromium", "webkit"}},
	}
	if len(set.Groups) != 1 || !reflect.DeepEqual(set.Groups[0], want) {
		t.Fatalf("group = %#v, want %#v", set.Groups, want)
	}
	if !reflect.DeepEqual(set.Sources["playwright"], []string{"tools[0]", "tools[1]", "tools[2]"}) {
		t.Fatalf("sources = %#v", set.Sources)
	}
}

func TestNormalizeAndMergeV1AllBindingsDominates(t *testing.T) {
	requests := decodeRequestsV1(t, `
- tool: playwright
- tool: playwright
  binding: python
- tool: playwright
  binding: "*"
`)
	set, err := NormalizeAndMergeV1(requests, "source-builder:demo", "build", "requires")
	if err != nil {
		t.Fatal(err)
	}
	if got := set.Groups[0].Binding; !reflect.DeepEqual(got, CanonicalBindingDemandV1{All: true, Explicit: []string{}}) {
		t.Fatalf("binding = %#v", got)
	}
}

func TestNormalizeAndMergeV1CompactRevision(t *testing.T) {
	set, err := NormalizeAndMergeV1(decodeRequestsV1(t, "- tool:java==21~2\n"), "source-builder:demo", "build", "requires")
	if err != nil {
		t.Fatal(err)
	}
	group := set.Groups[0]
	if group.DefinitionRevision != "" || !reflect.DeepEqual(group.VersionConstraints, []string{"==21~2"}) {
		t.Fatalf("group = %#v", group)
	}
}

func TestNormalizeAndMergeV1RejectsMalformedStructures(t *testing.T) {
	for _, test := range []struct {
		name string
		yaml string
		want string
	}{
		{name: "compact brackets", yaml: "- tool:playwright[python]\n", want: "bracket syntax"},
		{name: "empty qualified name", yaml: "- tool:acme/\n", want: "tool name"},
		{name: "nested qualified name", yaml: "- tool: acme/tools/playwright\n", want: "tool name"},
		{name: "unknown field", yaml: "- tool: java\n  option: value\n", want: "unknown field"},
		{name: "empty version", yaml: "- tool: java\n  version: ''\n", want: "version must not be empty"},
		{name: "empty binding", yaml: "- tool: java\n  binding: []\n", want: "must not be empty"},
		{name: "duplicate binding", yaml: "- tool: java\n  binding: [jdk, jdk]\n", want: "duplicate value"},
		{name: "wildcard mixed", yaml: "- tool: java\n  binding: ['*', jdk]\n", want: "cannot mix wildcard"},
		{name: "empty selection", yaml: "- tool: playwright\n  select: {browser: []}\n", want: "must not be empty"},
		{name: "duplicate selection", yaml: "- tool: playwright\n  select: {browser: [chromium, chromium]}\n", want: "duplicate value"},
		{name: "revision zero", yaml: "- tool: java\n  definition_revision: 0\n", want: "positive canonical decimal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests []SyntaxV1
			decodeErr := yaml.Unmarshal([]byte(test.yaml), &requests)
			if decodeErr != nil {
				if !strings.Contains(decodeErr.Error(), test.want) {
					t.Fatalf("decode error = %v, want %q", decodeErr, test.want)
				}
				return
			}
			_, err := NormalizeAndMergeV1(requests, "application:demo", "runtime", "tools")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMergeV1RejectsConflictingRevisionPins(t *testing.T) {
	left := CanonicalRequirementGroupV1{Scope: "application:demo", Tool: "java", DefinitionRevision: "1", Context: "runtime"}
	right := left
	right.DefinitionRevision = "2"
	if _, err := MergeV1(left, right); err == nil || !strings.Contains(err.Error(), "conflicting definition revisions") {
		t.Fatalf("error = %v", err)
	}
}

func decodeRequestsV1(t *testing.T, content string) []SyntaxV1 {
	t.Helper()
	var requests []SyntaxV1
	if err := yaml.Unmarshal([]byte(content), &requests); err != nil {
		t.Fatal(err)
	}
	return requests
}
