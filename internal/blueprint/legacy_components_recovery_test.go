package blueprint

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecoverLegacyComponentsBlueprintV1ConvertsPythonApplication(t *testing.T) {
	source, resolved, current := legacyComponentsPythonRecoveryFixtureV1(t)
	recovery, err := RecoverLegacyComponentsBlueprintV1(source, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Document.Environment.ID != "legacy-demo" ||
		recovery.Document.Environment.Base.Image != "python:3.13-slim" {
		t.Fatalf("recovered document = %#v", recovery.Document)
	}
	application, found := recovery.Document.Environment.Applications["application"]
	if !found || application.Packages.Python == nil ||
		len(application.Packages.Python.Requirements) != 1 ||
		application.Packages.Python.Requirements[0] != "demo" ||
		application.Executables["server"].Source != ContributionProviderPython {
		t.Fatalf("recovered application = %#v", application)
	}
	if strings.Contains(recovery.Source, "components:") ||
		!strings.Contains(recovery.Source, "applications:") ||
		!strings.Contains(recovery.Source, "source: python") {
		t.Fatalf("converted source:\n%s", recovery.Source)
	}
	want, err := EncodeResolvedDocumentV1(current)
	if err != nil {
		t.Fatal(err)
	}
	got, err := EncodeResolvedDocumentV1(recovery.Document)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("recovered resolved document differs from current source")
	}
}

func TestRecoverLegacyComponentsBlueprintV1UsesRetainedSourceInsteadOfStoredProjection(t *testing.T) {
	source, resolved, _ := legacyComponentsPythonRecoveryFixtureV1(t)
	var envelope map[string]any
	if err := json.Unmarshal([]byte(resolved), &envelope); err != nil {
		t.Fatal(err)
	}
	document := envelope["document"].(map[string]any)
	environment := document["Environment"].(map[string]any)
	environment["ID"] = "previous-demo"
	components := environment["Components"].(map[string]any)
	application := components["application"].(map[string]any)
	application["Type"] = string(ComponentTypeAPT)
	application["Python"] = nil
	application["APT"] = map[string]any{"Packages": []any{}}
	content, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := RecoverLegacyComponentsBlueprintV1(
		source,
		ResolvedDocumentV1(content),
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Document.Environment.Applications["application"].Packages.Python == nil {
		t.Fatalf("recovered source was changed by stored projection: %#v", recovery.Document)
	}
	if recovery.Document.Environment.ID != "legacy-demo" ||
		recovery.PreviousEnvironmentID != "previous-demo" {
		t.Fatalf(
			"source/cleanup environments = %q/%q",
			recovery.Document.Environment.ID,
			recovery.PreviousEnvironmentID,
		)
	}
}

func TestRecoverLegacyComponentsBlueprintV1RejectsUnsupportedSourceComponent(t *testing.T) {
	source, resolved, _ := legacyComponentsPythonRecoveryFixtureV1(t)
	source = strings.Replace(source, "type: python", "type: apt", 1)
	_, err := RecoverLegacyComponentsBlueprintV1(source, resolved)
	if err == nil || !strings.Contains(err.Error(), "must declare type: python") {
		t.Fatalf("unsupported source component error = %v", err)
	}
}

func legacyComponentsPythonRecoveryFixtureV1(
	t *testing.T,
) (string, ResolvedDocumentV1, Document) {
	t.Helper()
	const legacySource = `blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: legacy-demo
  components:
    base:
      image: python:3.13-slim
    application:
      type: python
      requirements: [demo]
      options:
        debug:
          description: Install debugging support.
          requirements: [debugpy]
      executables:
        server:
          binary: demo
  commands:
    serve:
      executable: application.server
      argv: [serve]
docker: {}
`
	const currentSource = `blueprint:
  schema: 1
  version: 0.1.0
  compatibility:
    platforms: [linux/amd64]
environment:
  id: legacy-demo
  base:
    image: python:3.13-slim
  applications:
    application:
      packages:
        python:
          requirements: [demo]
      options:
        debug:
          description: Install debugging support.
          packages:
            python:
              requirements: [debugpy]
      executables:
        server:
          source: python
          binary: demo
  commands:
    serve:
      executable: application.server
      argv: [serve]
docker: {}
`
	syntax, err := Decode([]byte(currentSource))
	if err != nil {
		t.Fatal(err)
	}
	current, err := Resolve(syntax)
	if err != nil {
		t.Fatal(err)
	}
	documentContent, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(documentContent, &document); err != nil {
		t.Fatal(err)
	}
	environment := document["Environment"].(map[string]any)
	componentContent, err := json.Marshal(current.Environment.Components)
	if err != nil {
		t.Fatal(err)
	}
	var components map[string]any
	if err := json.Unmarshal(componentContent, &components); err != nil {
		t.Fatal(err)
	}
	application := components[ApplicationContributionID(
		"application",
		ContributionProviderPython,
	)].(map[string]any)
	for _, value := range application["Executables"].(map[string]any) {
		delete(value.(map[string]any), "Source")
	}
	delete(environment, "Base")
	delete(environment, "Packages")
	delete(environment, "Applications")
	environment["Components"] = map[string]any{
		"base":        components["base"],
		"application": application,
	}
	content, err := json.Marshal(map[string]any{
		"schema":   ResolvedDocumentSchemaV1,
		"document": document,
	})
	if err != nil {
		t.Fatal(err)
	}
	return legacySource, ResolvedDocumentV1(content), current
}
