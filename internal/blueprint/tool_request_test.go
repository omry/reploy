package blueprint

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/toolrequest"
)

func TestResolveApplicationPortableToolsV1(t *testing.T) {
	document := resolveToolRequestBlueprintV1(t, `
        tools:
          - tool:java==21
          - tool: java
            version: "==21"
          - tool: playwright
            version: "1.61.0"
            definition_revision: 1
            binding: python
            select: {browser: [webkit, chromium]}
          - tool: asciinema
            version: "3.2.1"
`)
	got := document.Environment.Applications["web"].Packages.Tools
	want := []toolrequest.CanonicalRequirementGroupV1{
		{
			Scope: "application:web", Tool: "asciinema", VersionConstraints: []string{"3.2.1"}, Context: "runtime",
			Binding: toolrequest.CanonicalBindingDemandV1{Infer: true, Explicit: []string{}}, Selections: map[string][]string{},
		},
		{
			Scope: "application:web", Tool: "java", VersionConstraints: []string{"==21"}, Context: "runtime",
			Binding: toolrequest.CanonicalBindingDemandV1{Infer: true, Explicit: []string{}}, Selections: map[string][]string{},
		},
		{
			Scope: "application:web", Tool: "playwright", VersionConstraints: []string{"1.61.0"}, DefinitionRevision: "1", Context: "runtime",
			Binding:    toolrequest.CanonicalBindingDemandV1{Explicit: []string{"python"}},
			Selections: map[string][]string{"browser": {"chromium", "webkit"}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %#v, want %#v", got, want)
	}
	if sources := document.Environment.Applications["web"].Packages.ToolSources["java"]; len(sources) != 2 {
		t.Fatalf("java sources = %#v", sources)
	}
}

func TestResolveApplicationPortableToolsV1KeepsApplicationScopesSeparate(t *testing.T) {
	source, err := Decode([]byte(`
blueprint:
  schema: 1
  version: test
  compatibility: {platforms: [linux/amd64]}
environment:
  id: demo
  base: {image: docker.io/library/debian:13-slim}
  applications:
    api:
      packages: {tools: [tool:java==21]}
    worker:
      packages: {tools: [tool:java==21]}
docker: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	if document.Environment.Applications["api"].Packages.Tools[0].Scope != "application:api" ||
		document.Environment.Applications["worker"].Packages.Tools[0].Scope != "application:worker" {
		t.Fatalf("application scopes were not retained: %#v", document.Environment.Applications)
	}
}

func TestResolvedPortableToolRequestsV1RetainDemandButExcludeDiagnosticSourcesFromIdentity(t *testing.T) {
	document := resolveToolRequestBlueprintV1(t, `
        tools:
          - tool: java
            version: "==21"
          - tool: java
            binding: jdk
`)
	payload, err := EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "ToolSources") || strings.Contains(string(payload), "packages.tools[0]") {
		t.Fatalf("resolved identity contains diagnostic tool sources: %s", payload)
	}
	decoded, err := DecodeResolvedDocumentV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := document.Environment.Applications["web"].Packages.Tools
	if got := decoded.Environment.Applications["web"].Packages.Tools; !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded tools = %#v, want %#v", got, want)
	}
	first, err := DocumentDigestV1(document)
	if err != nil {
		t.Fatal(err)
	}
	application := document.Environment.Applications["web"]
	application.Packages.ToolSources = map[string][]string{"java": {"different-diagnostic-location"}}
	document.Environment.Applications["web"] = application
	second, err := DocumentDigestV1(document)
	if err != nil || first != second {
		t.Fatalf("diagnostic sources changed request identity: %q != %q (%v)", first, second, err)
	}
}

func TestResolveApplicationPortableToolsV1RejectsMalformedRequest(t *testing.T) {
	source, err := Decode([]byte(toolRequestBlueprintTextV1(`
        tools:
          - tool: playwright
            binding: [python, python]
`)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(source); err == nil || !strings.Contains(err.Error(), "duplicate value") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveApplicationPortableToolsV1RejectsNullList(t *testing.T) {
	source, err := Decode([]byte(toolRequestBlueprintTextV1("        tools: null")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(source); err == nil || !strings.Contains(err.Error(), "environment.applications.web.packages.tools must be a list") {
		t.Fatalf("error = %v", err)
	}
}

func resolveToolRequestBlueprintV1(t *testing.T, packages string) Document {
	t.Helper()
	source, err := Decode([]byte(toolRequestBlueprintTextV1(packages)))
	if err != nil {
		t.Fatal(err)
	}
	document, err := Resolve(source)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func toolRequestBlueprintTextV1(packages string) string {
	return `
blueprint:
  schema: 1
  version: test
  compatibility: {platforms: [linux/amd64]}
environment:
  id: demo
  base: {image: docker.io/library/debian:13-slim}
  applications:
    web:
      packages:
` + packages + `
docker: {}
`
}
