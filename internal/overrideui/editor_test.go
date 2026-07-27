package overrideui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func TestNewModelLoadsExistingOverridesAndBlueprintRoots(t *testing.T) {
	dir := stagedEditorDir(t)
	document := editorDocument("other>=1")
	writeEditorState(t, dir, document)
	project := pythonProject(t, filepath.Join(dir, "workspace", "demo"), "demo")
	overrides := deploy.EmptyPackageOverridesV1("example")
	overrides.Environment.Base = &deploy.BaseImageOverrideV1{Image: "python:3.13-slim"}
	overrides.Environment.Vars["workspace_root"] = filepath.Dir(project)
	overrides.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"demo":  {Path: "{{ workspace_root }}/demo"},
		"extra": {Version: "2.0"},
	}
	commitOverrides(t, dir, overrides)

	m, err := newModel(Config{
		Context:       t.Context(),
		DeploymentDir: dir,
		Document:      document,
		Versions: func(_ context.Context, _ string) ([]string, error) {
			return []string{"2.0"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.workspace != filepath.Dir(project) {
		t.Fatalf("workspace = %q", m.workspace)
	}
	if m.baseImage != "python:3.13-slim" {
		t.Fatalf("base image = %q", m.baseImage)
	}
	if len(m.items) != 3 {
		t.Fatalf("items = %#v", m.items)
	}
	if m.items[0].Package != "other" || !m.items[0].Explicit || m.items[0].Choice != (deploy.PackageOverrideChoiceV1{}) {
		t.Fatalf("blueprint root = %#v", m.items[0])
	}
	if m.items[1].Package != "demo" || m.items[1].Explicit || m.items[1].Choice.Path == "" {
		t.Fatalf("demo item = %#v", m.items[1])
	}
	if m.items[2].Package != "extra" || m.items[2].Explicit || m.items[2].Choice.Version != "2.0" {
		t.Fatalf("extra item = %#v", m.items[2])
	}
	if !strings.Contains(m.items[2].Notice, "checking") || m.items[2].Error != "" {
		t.Fatalf("existing upstream version was shown as verified before catalog lookup: %#v", m.items[2])
	}
	if view := m.View(); !strings.Contains(view, "PyPI") {
		t.Fatalf("view does not show the Python package source:\n%s", view)
	}
}

func TestEditorAddsNativeOSPackageToDevelopmentOverrides(t *testing.T) {
	dir := stagedEditorDir(t)
	document := editorDocument("demo>=1")
	writeEditorState(t, dir, document)
	m, err := newModel(Config{
		Context: t.Context(), DeploymentDir: dir, Document: document,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.screen = screenAdd
	m.input.SetValue("os:default-jre-headless")
	updated, _ := m.updateAdd(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	raw := m.buildRaw()
	if got := raw.Environment.PackageAdditions["os"]; len(got) != 1 || got[0] != "default-jre-headless" {
		t.Fatalf("package additions = %#v", raw.Environment.PackageAdditions)
	}
	if !m.dirty || m.current().Provider != "os" || !m.current().Addition {
		t.Fatalf("OS package row = %#v, dirty = %v", m.current(), m.dirty)
	}
	if !strings.Contains(m.View(), "native OS package") {
		t.Fatalf("OS package source is not visible:\n%s", m.View())
	}
}

func TestEditorItemsListsAllExplicitDependenciesBeforeOverrideOnlyRows(t *testing.T) {
	document := editorDocument("base-root>=1")
	application := document.Environment.Applications["python"]
	application.Options = map[string]blueprint.ApplicationOption{
		"feature": {
			Packages: blueprint.ApplicationOptionPackages{
				Python: &blueprint.PythonOptionPackages{Requirements: []string{"option-root"}},
			},
		},
	}
	document.Environment.Applications["python"] = application
	direct, err := pythonprovider.CanonicalPackageRequestV1("direct-root==2")
	if err != nil {
		t.Fatal(err)
	}
	overlay := deploy.RequestOverlayV1{
		Schema: deploy.RequestOverlaySchemaV1,
		SelectedOptions: []deploy.QualifiedOption{{
			Application: "python", Option: "feature",
		}},
		DirectPackages: []deploy.DirectPackageRequest{{
			Contribution: "application/python/python", Package: providers.CanonicalPackageRequest(direct),
		}},
	}
	raw := deploy.EmptyPackageOverridesV1("example")
	raw.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"Base_Root":     {Version: "3"},
		"override-only": {Version: "4"},
	}
	items, err := editorItems(document, overlay, raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"base-root", "direct-root", "option-root", "override-only"}
	for index, packageID := range want {
		if items[index].Package != packageID {
			t.Fatalf("items = %#v", items)
		}
		if items[index].Explicit != (index < 3) {
			t.Fatalf("item %q explicit = %v", packageID, items[index].Explicit)
		}
	}
	if items[0].Choice.Version != "3" {
		t.Fatalf("explicit dependency lost its override choice: %#v", items[0])
	}
	wantSources := []string{"PyPI · >=1", "PyPI · ==2", "PyPI"}
	for index, source := range wantSources {
		if len(items[index].Sources) != 1 || items[index].Sources[0] != source {
			t.Fatalf("item %q sources = %#v, want %q", items[index].Package, items[index].Sources, source)
		}
	}
}

func TestEditorItemsIdentifiesDirectPythonRequirementSource(t *testing.T) {
	items, err := editorItems(
		editorDocument("demo @ https://packages.example.test/demo.whl"),
		deploy.RequestOverlayV1{},
		deploy.EmptyPackageOverridesV1("example"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Sources) != 1 ||
		items[0].Sources[0] != "direct URL · https://packages.example.test/demo.whl" {
		t.Fatalf("items = %#v", items)
	}
}

func TestLocalExplicitProjectExposesOnlyItsDirectDependencies(t *testing.T) {
	dir := stagedEditorDir(t)
	document := editorDocument("app")
	writeEditorState(t, dir, document)
	root := pythonProjectWithDependencies(t, filepath.Join(dir, "workspace", "app"), "app", []string{"hydra-core>=1.3"})
	hydra := pythonProjectWithDependencies(t, filepath.Join(dir, "workspace", "hydra"), "hydra-core", []string{"omegaconf>=2.3"})
	overrides := deploy.EmptyPackageOverridesV1("example")
	overrides.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"app":        {Path: root},
		"hydra-core": {Path: hydra},
	}
	commitOverrides(t, dir, overrides)
	m, err := newModel(Config{
		Context: t.Context(), DeploymentDir: dir, Document: document,
		Versions: func(context.Context, string) ([]string, error) { return []string{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.items) != 2 {
		t.Fatalf("items = %#v", m.items)
	}
	if !m.items[0].Explicit || m.items[0].Package != "app" {
		t.Fatalf("explicit root = %#v", m.items[0])
	}
	if !m.items[1].Discovered || m.items[1].Package != "hydra-core" {
		t.Fatalf("direct dependency = %#v", m.items[1])
	}
	if len(m.items[1].Sources) != 1 || m.items[1].Sources[0] != "PyPI · >=1.3" {
		t.Fatalf("direct dependency source = %#v", m.items[1].Sources)
	}
	for _, item := range m.items {
		if item.Package == "omegaconf" {
			t.Fatalf("dependency of an overridden dependency was recursively exposed: %#v", m.items)
		}
	}
}

func TestDynamicLocalDependenciesRequireTrialBuild(t *testing.T) {
	m := editorModelForInteraction(t)
	project := pythonProject(t, filepath.Join(t.TempDir(), "demo"), "demo")
	if err := os.WriteFile(
		filepath.Join(project, "pyproject.toml"),
		[]byte("[project]\nname = \"demo\"\ndynamic = [\"dependencies\"]\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Path: project}
	m.validateItems()
	if !strings.Contains(m.items[0].Notice, "trial build") {
		t.Fatalf("dynamic dependency notice = %#v", m.items[0])
	}
}

func TestNonPEP621DependenciesRequireTrialBuild(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{
			name:    "setuptools metadata",
			content: "[build-system]\nrequires = [\"setuptools\"]\n",
		},
		{
			name:    "poetry metadata",
			content: "[tool.poetry]\nname = \"demo\"\n[tool.poetry.dependencies]\npython = \">=3.11\"\nrequests = \"^2\"\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, "pyproject.toml"), []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			dependencies, available, err := pythonProjectDirectDependencies(project)
			if err != nil || available || dependencies != nil {
				t.Fatalf("dependencies=%#v available=%v err=%v", dependencies, available, err)
			}
		})
	}
}

func TestValidateActionSavesChoicesAndTurnsStatusGreen(t *testing.T) {
	m := editorModelForInteraction(t)
	m.validate = func(context.Context, io.Writer) (ValidationResult, error) {
		return ValidationResult{Packages: []DiscoveredPackage{{Provider: "python", Package: "dependency"}}}, nil
	}
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Version: "2.0"}
	m.versionCache["demo"] = []string{"2.0"}
	m.dirty = true
	m.validated = false
	m.validateItems()

	updated, command := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(*model)
	if command == nil || !m.validating {
		t.Fatalf("validation did not start: validating=%v command=%v", m.validating, command)
	}
	updated, _ = m.Update(validationResultFromBatch(t, command))
	m = updated.(*model)
	if m.validating || !m.validated || m.dirty {
		t.Fatalf("validation result = validating=%v validated=%v dirty=%v status=%q", m.validating, m.validated, m.dirty, m.status)
	}
	if view := m.View(); !strings.Contains(view, "Validation succeeded") {
		t.Fatalf("validated status is not visible:\n%s", view)
	}
	if len(m.items) != 2 || !m.items[1].Discovered || m.items[1].Package != "dependency" {
		t.Fatalf("validated direct dependencies = %#v", m.items)
	}
}

func TestValidateActionKeepsSuccessfulSaveWhenTrialBuildFails(t *testing.T) {
	m := editorModelForInteraction(t)
	m.validate = func(context.Context, io.Writer) (ValidationResult, error) {
		return ValidationResult{}, errors.New("trial build failed")
	}
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Path: pythonProject(
		t, filepath.Join(t.TempDir(), "demo"), "demo",
	)}
	m.dirty = true
	m.validateItems()

	updated, command := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(*model)
	if command == nil {
		t.Fatal("validation did not start")
	}
	updated, _ = m.Update(validationResultFromBatch(t, command))
	m = updated.(*model)
	if m.validated || m.dirty || !strings.Contains(m.validationError, "trial build failed") {
		t.Fatalf(
			"failed validation state = validated=%v dirty=%v error=%q",
			m.validated, m.dirty, m.validationError,
		)
	}
	saved, found, err := deploy.ReadPackageOverridesV1(m.deploymentDir)
	if err != nil || !found || saved.Environment.PackageOverrides["python"]["demo"].Path == "" {
		t.Fatalf("saved choices = %#v found=%v err=%v", saved, found, err)
	}
}

func TestValidateActionRejectsSidecarChangedDuringTrialBuild(t *testing.T) {
	m := editorModelForInteraction(t)
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Version: "2.0"}
	m.versionCache["demo"] = []string{"2.0"}
	m.dirty = true
	m.validateItems()
	m.validate = func(context.Context, io.Writer) (ValidationResult, error) {
		changed := deploy.EmptyPackageOverridesV1("example")
		changed.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
			"demo": {Version: "1.0"},
		}
		lock, err := deploy.AcquireOperationLock(t.Context(), m.deploymentDir)
		if err != nil {
			return ValidationResult{}, err
		}
		defer lock.Unlock()
		if err := lock.CommitPackageOverridesV1(changed); err != nil {
			return ValidationResult{}, err
		}
		return ValidationResult{}, errors.New("trial build failed")
	}

	updated, command := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(*model)
	updated, _ = m.Update(validationResultFromBatch(t, command))
	m = updated.(*model)
	if m.validated || !m.dirty ||
		!strings.Contains(m.validationError, "trial build failed") ||
		!strings.Contains(m.validationError, "changed during validation") {
		t.Fatalf("validated=%v dirty=%v error=%q", m.validated, m.dirty, m.validationError)
	}
}

func TestValidationProgressUsesModalAndRetainsFailure(t *testing.T) {
	m := editorModelForInteraction(t)
	m.width = 100
	m.height = 22
	m.validationVisible = true
	m.validating = true
	m.validationCurrent = "Resolving Python packages for component application"
	for index := range 30 {
		m.appendValidationLog(fmt.Sprintf("log line %02d", index))
	}
	view := m.View()
	for _, want := range []string{
		"Validating package choices",
		"Resolving Python packages",
		"Validation log",
		"↑/↓ scroll",
		"log line 29",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("progress overlay missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "log line 00") {
		t.Fatalf("validation log did not use a bounded viewport:\n%s", view)
	}
	bottom := m.validationViewport.YOffset
	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(*model)
	if m.validationViewport.YOffset >= bottom {
		t.Fatalf("page up did not scroll validation log: before=%d after=%d", bottom, m.validationViewport.YOffset)
	}

	m.validating = false
	m.validationError = "dependency conflict"
	m.appendValidationLog("dependency conflict")
	m.validationViewport.GotoBottom()
	view = m.View()
	for _, want := range []string{"Validation failed", "dependency conflict", "S save log", "Enter or Esc", "│", "█"} {
		if !strings.Contains(view, want) {
			t.Fatalf("failure overlay missing %q:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > m.width {
			t.Fatalf("validation overlay line width = %d, terminal width = %d:\n%s", width, m.width, view)
		}
	}
	for index, line := range strings.Split(m.renderValidationLogPanel(), "\n") {
		if width := lipgloss.Width(line); width != m.validationViewport.Width+4 {
			t.Fatalf(
				"validation viewport row %d width = %d, want %d:\n%s",
				index, width, m.validationViewport.Width+4, m.renderValidationLogPanel(),
			)
		}
	}
}

func TestValidationLogSavePromptWritesNewFileWithoutOverwriting(t *testing.T) {
	m := editorModelForInteraction(t)
	m.validationVisible = true
	m.validationError = "build failed"
	m.appendValidationLog("first line\nbuild failed")

	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	m = updated.(*model)
	if m.validationSavePrompt {
		t.Fatal("legacy L shortcut opened the save-log prompt")
	}
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = updated.(*model)
	if !m.validationSavePrompt {
		t.Fatal("save-log prompt did not open")
	}
	path := filepath.Join(t.TempDir(), "validation.log")
	m.validationSaveInput.SetValue(path)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first line\nbuild failed\n" || m.validationSavedPath != path {
		t.Fatalf("saved log = %q, path = %q", content, m.validationSavedPath)
	}

	m.validationSavePrompt = true
	m.validationSaveInput.SetValue(path)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if !strings.Contains(m.validationSaveError, "already exists") {
		t.Fatalf("overwrite error = %q", m.validationSaveError)
	}
}

func TestValidationProgressWriterEmitsCompleteSteps(t *testing.T) {
	events := make(chan string, 3)
	writer := &validationProgressWriter{events: events}
	if _, err := writer.Write([]byte("preparing environ")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("ment\nresolving packages\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("preparing environment\n")); err != nil {
		t.Fatal(err)
	}
	writer.flush()
	if first, second := <-events, <-events; first != "preparing environment" || second != "resolving packages" {
		t.Fatalf("progress steps = %q, %q", first, second)
	}
	if repeated := <-events; repeated != "preparing environment" {
		t.Fatalf("repeated progress event = %q", repeated)
	}
	if got := writer.log.String(); got != "preparing environment\nresolving packages\n" {
		t.Fatalf("deduplicated progress log = %q", got)
	}
}

func TestRecordValidationStepKeepsDistinctHistoryWhileUpdatingCurrentStep(t *testing.T) {
	m := editorModelForInteraction(t)
	m.validationCurrent = "Preparing environment"
	m.completedSteps = map[string]struct{}{}
	m.loggedSteps = map[string]struct{}{"Preparing environment": {}}
	m.validationLog = "Preparing environment\n"

	m.recordValidationStep("Resolving packages")
	m.recordValidationStep("Preparing build tools")
	m.recordValidationStep("Resolving packages")

	if got := m.validationLog; got != "Preparing environment\nResolving packages\nPreparing build tools\n" {
		t.Fatalf("validation log = %q", got)
	}
	wantCompleted := []string{
		"Preparing environment",
		"Resolving packages",
		"Preparing build tools",
	}
	if !reflect.DeepEqual(m.validationCompleted, wantCompleted) {
		t.Fatalf("completed steps = %#v, want %#v", m.validationCompleted, wantCompleted)
	}
	if m.validationCurrent != "Resolving packages" {
		t.Fatalf("current step = %q", m.validationCurrent)
	}
}

func TestValidationProgressWriterSanitizesAndNeverBlocksOnFullEventBuffer(t *testing.T) {
	events := make(chan string, 1)
	writer := &validationProgressWriter{events: events}
	if _, err := writer.Write([]byte("\x1b]52;c;clipboard\aone\n\x1b[31mtwo\x1b[0m\n")); err != nil {
		t.Fatal(err)
	}
	writer.flush()
	if got := writer.log.String(); got != "one\ntwo\n" {
		t.Fatalf("sanitized log = %q", got)
	}
	if got := <-events; got != "one" {
		t.Fatalf("first progress event = %q", got)
	}
}

func TestTerminalSanitizersSeparateSingleLineDataFromMultilineLogs(t *testing.T) {
	value := "\x1b[31mfirst\tcolumn\nsecond\a\x1b[0m"
	if got := sanitizeTerminalText(value); got != "firstcolumnsecond" {
		t.Fatalf("single-line text = %q", got)
	}
	if got := sanitizeTerminalBlock(value); got != "first\tcolumn\nsecond" {
		t.Fatalf("multiline block = %q", got)
	}
}

func TestPathAndVersionArrowsMoveResultsWithoutBlurringFilter(t *testing.T) {
	m := editorModelForInteraction(t)
	m.screen = screenPath
	m.results = []string{"/one", "/two"}
	m.resultCursor = 1
	m.input.Focus()
	m.input.SetValue("om")

	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(*model)
	if m.resultCursor != 0 || !m.input.Focused() || m.input.Value() != "om" {
		t.Fatalf("path navigation changed input: cursor=%d focused=%v value=%q", m.resultCursor, m.input.Focused(), m.input.Value())
	}

	m.screen = screenVersion
	m.results = []string{"2.0", "1.0"}
	m.resultCursor = 0
	m.input.Focus()
	m.input.SetValue("2")
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*model)
	if m.resultCursor != 1 || !m.input.Focused() || m.input.Value() != "2" {
		t.Fatalf("version navigation changed input: cursor=%d focused=%v value=%q", m.resultCursor, m.input.Focused(), m.input.Value())
	}
}

func TestLocalSourceScreenMakesWorkspaceChoiceVisibleAndReturnsToSelection(t *testing.T) {
	m := editorModelForInteraction(t)
	m.screen = screenPath
	m.input.SetValue("demo")
	m.input.Focus()
	view := m.View()
	for _, want := range []string{"Workspace root", "not set", "Ctrl+W", "absolute"} {
		if !strings.Contains(view, want) {
			t.Fatalf("local-source view missing %q:\n%s", want, view)
		}
	}

	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = updated.(*model)
	if m.screen != screenWorkspace {
		t.Fatalf("workspace shortcut screen = %v", m.screen)
	}
	workspace := t.TempDir()
	m.input.SetValue(workspace)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.screen != screenPath || m.workspace != workspace || m.input.Value() != "demo" || !m.input.Focused() {
		t.Fatalf("workspace return = screen %v, workspace %q, input %q, focused %v", m.screen, m.workspace, m.input.Value(), m.input.Focused())
	}
}

func TestSourceScreensUseSourceSpecificFields(t *testing.T) {
	m := editorModelForInteraction(t)
	m.screen = screenPath
	if view := m.View(); !strings.Contains(view, "Find a local Python project") || !strings.Contains(view, "absolute directory") {
		t.Fatalf("local source screen is not source-specific:\n%s", view)
	}
	m.screen = screenVersion
	m.versionCache["demo"] = []string{"2.0", "1.0"}
	m.filterVersionResults()
	if view := m.View(); !strings.Contains(view, "Select upstream version") || !strings.Contains(view, "upstream release catalog") {
		t.Fatalf("upstream version screen is not source-specific:\n%s", view)
	}
}

func TestBaseImageSelectorSupportsFromBlueprint(t *testing.T) {
	m := editorModelForInteraction(t)
	m.setBaseImage("ubuntu:24.04")

	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(*model)
	if m.cursor != -1 || !strings.Contains(m.View(), "base") ||
		!strings.Contains(m.View(), "Base image") {
		t.Fatalf("base row is not selected:\n%s", m.View())
	}
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.screen != screenBase || !strings.Contains(m.View(), "From blueprint") {
		t.Fatalf("base choice screen = %v\n%s", m.screen, m.View())
	}
	if strings.Contains(m.View(), "Local image") ||
		!strings.Contains(m.View(), "Exact image name") {
		t.Fatalf("base choices include unsupported discovery:\n%s", m.View())
	}
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(*model)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.baseImage != "" || m.buildRaw().Environment.Base != nil {
		t.Fatalf("From blueprint retained base override: %#v", m.buildRaw().Environment.Base)
	}
}

func TestBaseImageSelectorAcceptsDirectReference(t *testing.T) {
	m := editorModelForInteraction(t)
	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(*model)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*model)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.screen != screenBaseReference {
		t.Fatalf("reference screen = %v", m.screen)
	}
	m.input.SetValue("python:3.13-slim")
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.baseImage != "python:3.13-slim" || m.screen != screenMain {
		t.Fatalf("direct base = %q, screen = %v", m.baseImage, m.screen)
	}
}

func TestMainViewFitsNarrowTerminal(t *testing.T) {
	m := editorModelForInteraction(t)
	m.width = 48
	m.height = 24
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Path: "/a/very/long/path/to/a/local/project/that/must/not-overflow"}
	m.items = append(m.items, overrideItem{
		Provider: "python",
		Package:  "a-long-package-name-that-is-only-an-override",
		Choice: deploy.PackageOverrideChoiceV1{
			Path: "/another/very/long/path/to/a/local/project/that/must/not-overflow",
		},
	})
	m.validateItems()
	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 48 {
			t.Fatalf("narrow line width = %d:\n%s", width, line)
		}
	}
	if !strings.Contains(view, "explicit") {
		t.Fatalf("narrow view lost dependency classification:\n%s", view)
	}
	if !strings.Contains(view, "override-only") {
		t.Fatalf("narrow view lost override-only classification:\n%s", view)
	}
}

func TestMainViewShowsFullLocalPathWhenTerminalHasRoom(t *testing.T) {
	m := editorModelForInteraction(t)
	m.width = 140
	path := "/home/omry/dev/reploy/examples/omegaconf-inspector"
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Path: path}
	view := m.View()
	if !strings.Contains(view, "local · "+path) {
		t.Fatalf("wide view truncated the local source path:\n%s", view)
	}
}

func TestProjectResultsDoNotRenderTerminalControls(t *testing.T) {
	m := editorModelForInteraction(t)
	m.width = 100
	m.height = 24
	m.screen = screenPath
	m.results = []string{"/tmp/project-\x1b]52;c;injected\a"}
	m.resultCursor = 0

	view := m.View()
	if strings.ContainsAny(view, "\x1b\a") {
		t.Fatalf("project result rendered terminal controls: %q", view)
	}
	if !strings.Contains(view, "/tmp/project-") {
		t.Fatalf("sanitized project result missing from view: %q", view)
	}
}

func TestExitPromptOffersValidationSkipAndBack(t *testing.T) {
	m := editorModelForInteraction(t)
	m.dirty = true
	m.validated = false

	updated, command := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}})
	m = updated.(*model)
	if command != nil || m.screen != screenExit {
		t.Fatalf("exit prompt command=%v screen=%v", command, m.screen)
	}
	for _, want := range []string{"Validate and exit", "Save without validation and exit", "Back to editor"} {
		if view := m.View(); !strings.Contains(view, want) {
			t.Fatalf("exit prompt missing %q:\n%s", want, view)
		}
	}

	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*model)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*model)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.screen != screenMain {
		t.Fatalf("back returned to screen %v", m.screen)
	}
}

func TestDoubleCtrlCExitsWithoutSavingAndDoesNotOpenExitPrompt(t *testing.T) {
	m := editorModelForInteraction(t)
	m.dirty = true
	m.validated = false
	m.screen = screenPath

	updated, command := m.updateKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*model)
	if command != nil || !m.ctrlCArmed || m.screen != screenPath {
		t.Fatalf("first Ctrl-C command=%v armed=%v screen=%v", command, m.ctrlCArmed, m.screen)
	}
	if !strings.Contains(m.status, "exit without saving") {
		t.Fatalf("first Ctrl-C status = %q", m.status)
	}

	updated, command = m.updateKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*model)
	if command == nil || m.screen == screenExit {
		t.Fatalf("second Ctrl-C command=%v screen=%v", command, m.screen)
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("second Ctrl-C message = %T", command())
	}
}

func TestDoubleCtrlCCancelsRunningValidationAndExits(t *testing.T) {
	m := editorModelForInteraction(t)
	validationContext, cancel := context.WithCancel(t.Context())
	m.validationVisible = true
	m.validating = true
	m.validationCancel = cancel

	updated, command := m.updateValidationOverlay(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*model)
	if command != nil || !m.ctrlCArmed {
		t.Fatalf("first Ctrl-C command=%v armed=%v", command, m.ctrlCArmed)
	}
	if view := m.View(); !strings.Contains(view, ctrlCValidationExitStatus) {
		t.Fatalf("validation UI did not explain the second Ctrl-C:\n%s", view)
	}

	updated, command = m.updateValidationOverlay(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*model)
	if command != nil || !m.canceled {
		t.Fatalf("second Ctrl-C command=%v canceled=%v", command, m.canceled)
	}
	select {
	case <-validationContext.Done():
	default:
		t.Fatal("running validation context was not canceled")
	}
	updated, command = m.Update(validatedMsg{ID: m.validationID, Err: context.Canceled})
	m = updated.(*model)
	if command == nil {
		t.Fatal("validation completion did not exit after cancellation")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("canceled validation completion message = %T", command())
	}
}

func TestExitWithoutValidationSavesBeforeQuitting(t *testing.T) {
	m := editorModelForInteraction(t)
	m.dirty = true
	m.validated = false

	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}})
	m = updated.(*model)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*model)
	updated, command := m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if command == nil {
		t.Fatal("save-and-exit did not start a save")
	}
	updated, quit := m.Update(command())
	m = updated.(*model)
	if quit == nil {
		t.Fatal("successful save did not request exit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatalf("save-and-exit message = %T", quit())
	}
	if m.dirty {
		t.Fatal("save-and-exit left the model dirty")
	}
}

func TestValidateAndExitQuitsOnSuccessAndShowsLogOnFailure(t *testing.T) {
	success := editorModelForInteraction(t)
	success.validate = func(context.Context, io.Writer) (ValidationResult, error) {
		return ValidationResult{}, nil
	}
	success.dirty = true
	updated, _ := success.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}})
	success = updated.(*model)
	updated, command := success.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	success = updated.(*model)
	updated, quit := success.Update(validationResultFromBatch(t, command))
	success = updated.(*model)
	if quit == nil {
		t.Fatal("successful validation did not request exit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatalf("validate-and-exit message = %T", quit())
	}

	failure := editorModelForInteraction(t)
	failure.validate = func(context.Context, io.Writer) (ValidationResult, error) {
		return ValidationResult{}, errors.New("dependency conflict")
	}
	failure.dirty = true
	updated, _ = failure.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}})
	failure = updated.(*model)
	updated, command = failure.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	failure = updated.(*model)
	updated, quit = failure.Update(validationResultFromBatch(t, command))
	failure = updated.(*model)
	if quit != nil || !failure.validationVisible || !strings.Contains(failure.validationLog, "dependency conflict") {
		t.Fatalf(
			"failed validate-and-exit: quit=%v visible=%v log=%q",
			quit, failure.validationVisible, failure.validationLog,
		)
	}
	updated, _ = failure.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	failure = updated.(*model)
	if failure.validationVisible || failure.screen != screenMain {
		t.Fatalf("closing failed validation returned to visible=%v screen=%v", failure.validationVisible, failure.screen)
	}
}

func TestValidatedExitQuitsWithoutPrompt(t *testing.T) {
	m := editorModelForInteraction(t)
	m.dirty = false
	m.validated = true
	updated, command := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}})
	m = updated.(*model)
	if command == nil || m.screen == screenExit {
		t.Fatalf("validated exit command=%v screen=%v", command, m.screen)
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("validated exit message = %T", command())
	}
}

func TestAdvertisedValidationShortcutAcceptsUppercaseV(t *testing.T) {
	m := editorModelForInteraction(t)
	updated, command := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'V'}})
	m = updated.(*model)
	if command != nil || !strings.Contains(m.status, "unavailable") {
		t.Fatalf("uppercase V command=%v status=%q", command, m.status)
	}
}

func TestValidateItemsMarksBadLocalProjectAndMissingVersion(t *testing.T) {
	m := editorModelForInteraction(t)
	wrong := pythonProject(t, filepath.Join(t.TempDir(), "wrong"), "not-demo")
	m.items = []overrideItem{
		{Provider: "python", Package: "demo", Choice: deploy.PackageOverrideChoiceV1{Path: wrong}},
		{Provider: "python", Package: "other", Choice: deploy.PackageOverrideChoiceV1{Version: "9.9"}},
		{Provider: "python", Package: "unsafe", Choice: deploy.PackageOverrideChoiceV1{Version: "1.0; python_version < '0'"}},
	}
	m.versionCache["other"] = []string{"1.0", "2.0"}
	m.validateItems()
	if !strings.Contains(m.items[0].Error, `provides "not-demo"`) {
		t.Fatalf("local validation = %q", m.items[0].Error)
	}
	if !strings.Contains(m.items[1].Error, "not present") {
		t.Fatalf("version validation = %q", m.items[1].Error)
	}
	if !strings.Contains(m.items[2].Error, "PEP 440") {
		t.Fatalf("unsafe version validation = %q", m.items[2].Error)
	}
}

func TestResetRemovesOverrideOnlyRowButRetainsExplicitDependency(t *testing.T) {
	m := editorModelForInteraction(t)
	m.items = append(m.items, overrideItem{
		Provider: "python", Package: "extra",
		Choice: deploy.PackageOverrideChoiceV1{Version: "1.0"},
	})
	m.cursor = 1

	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyDelete})
	m = updated.(*model)
	if len(m.items) != 1 || m.items[0].Package != "demo" {
		t.Fatalf("reset override-only items = %#v", m.items)
	}

	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Version: "2.0"}
	m.cursor = 0
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyDelete})
	m = updated.(*model)
	if len(m.items) != 1 || m.items[0].Choice != (deploy.PackageOverrideChoiceV1{}) {
		t.Fatalf("reset explicit items = %#v", m.items)
	}
}

func TestExistingVersionIsNotValidUntilCatalogVerificationSucceeds(t *testing.T) {
	m := editorModelForInteraction(t)
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Version: "2.0"}
	m.versionPending["demo"] = true
	m.validateItems()
	if !strings.Contains(m.items[0].Notice, "checking") || m.items[0].Error != "" {
		t.Fatalf("pending validation = %#v", m.items[0])
	}

	updated, _ := m.Update(versionsLoadedMsg{Package: "demo", Err: context.DeadlineExceeded})
	m = updated.(*model)
	if !strings.Contains(m.items[0].Notice, "load upstream versions") || m.items[0].Error != "" {
		t.Fatalf("catalog error validation = %#v", m.items[0])
	}

	updated, _ = m.Update(versionsLoadedMsg{Package: "demo", Versions: []string{"2.0"}})
	m = updated.(*model)
	if m.items[0].Error != "" || m.items[0].Notice != "" {
		t.Fatalf("successful catalog validation = %#v", m.items[0])
	}
}

func TestChoiceStatusReservesValidForCompletedSelectionValidation(t *testing.T) {
	m := editorModelForInteraction(t)
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Version: "2.0"}
	m.versionCache["demo"] = []string{"2.0"}
	m.validateItems()
	if view := m.View(); !strings.Contains(view, "available") {
		t.Fatalf("unvalidated upstream choice was not shown as available:\n%s", view)
	}

	m.validated = true
	if view := m.View(); !strings.Contains(view, "valid") || strings.Contains(view, "available") {
		t.Fatalf("validated selection did not mark the choice valid:\n%s", view)
	}

	m.validated = false
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Path: pythonProject(
		t, filepath.Join(t.TempDir(), "demo"), "demo",
	)}
	m.validateItems()
	if view := m.View(); !strings.Contains(view, "source found") {
		t.Fatalf("unvalidated local choice was not shown as a found source:\n%s", view)
	}
}

func TestStoredPathUsesWorkspaceVariableAndAbsoluteEscapeHatch(t *testing.T) {
	workspace := filepath.Join(string(filepath.Separator), "work")
	if got := storedPath(workspace, filepath.Join(workspace, "demo")); got != "{{ workspace_root }}/demo" {
		t.Fatalf("workspace path = %q", got)
	}
	outside := filepath.Join(string(filepath.Separator), "elsewhere", "demo")
	if got := storedPath(workspace, outside); got != outside {
		t.Fatalf("outside path = %q", got)
	}
	if got := storedPath("", filepath.Join(workspace, "demo")); got != filepath.Join(workspace, "demo") {
		t.Fatalf("path without workspace = %q", got)
	}
}

func TestWorkspaceIsOptionalAndAbsentByDefault(t *testing.T) {
	m := editorModelForInteraction(t)
	if m.workspace != "" {
		t.Fatalf("workspace = %q, want empty", m.workspace)
	}
	if m.projectSearchRoot() == "" {
		t.Fatal("project browser has no default search root")
	}
	raw := m.buildRaw()
	if _, found := raw.Environment.Vars["workspace_root"]; found {
		t.Fatalf("default sidecar vars = %#v", raw.Environment.Vars)
	}

	m.screen = screenWorkspace
	m.workspace = filepath.Join(string(filepath.Separator), "existing")
	m.input.SetValue("")
	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.workspace != "" {
		t.Fatalf("cleared workspace = %q", m.workspace)
	}
}

func TestWorkspaceExpandsCurrentUserHome(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "dev")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := editorModelForInteraction(t)
	m.screen = screenWorkspace
	m.input.SetValue("~/dev")

	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.workspace != "~/dev" || m.workspaceResolved != workspace || m.screen != screenMain {
		t.Fatalf(
			"workspace=%q resolved=%q screen=%v status=%q",
			m.workspace, m.workspaceResolved, m.screen, m.status,
		)
	}
	if got := m.buildRaw().Environment.Vars["workspace_root"]; got != "~/dev" {
		t.Fatalf("stored workspace_root = %#v", got)
	}

	m.input.SetValue("short prior value")
	m.input.CursorStart()
	m.openWorkspace(screenMain)
	if got := m.input.Value(); got != "~/dev" {
		t.Fatalf("reopened workspace input = %q", got)
	}
	if got, want := m.input.Position(), len([]rune("~/dev")); got != want {
		t.Fatalf("reopened workspace cursor = %d, want %d", got, want)
	}
}

func TestMatchingProjectsFindsPythonProjectsOnly(t *testing.T) {
	root := t.TempDir()
	project := pythonProject(t, filepath.Join(root, "hydra"), "hydra-core")
	if err := os.MkdirAll(filepath.Join(root, "hydra-not-a-project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git", "hydra-hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	pythonProject(t, filepath.Join(root, "vendor", "hydra-nested"), "hydra-nested")
	results, err := matchingProjects(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != project {
		t.Fatalf("results = %#v", results)
	}
}

func TestProjectPickerResolvesNestedWorkspaceRelativePath(t *testing.T) {
	workspace := t.TempDir()
	project := pythonProject(
		t,
		filepath.Join(workspace, "reploy", "examples", "omegaconf-inspector"),
		"omegaconf-inspector",
	)
	m := editorModelForInteraction(t)
	m.items[0].Package = "omegaconf-inspector"
	m.workspace = "~/dev"
	m.workspaceResolved = workspace
	m.screen = screenPath
	m.projectCatalogRoot = workspace
	m.input.SetValue("reploy/examples/")
	m.filterProjectResults()
	if len(m.results) != 1 || m.results[0] != project {
		t.Fatalf("nested workspace results = %#v", m.results)
	}
	updated, _ := m.updatePath(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if got := m.items[0].Choice.Path; got != "{{ workspace_root }}/reploy/examples/omegaconf-inspector" {
		t.Fatalf("selected nested workspace path = %q", got)
	}
}

func TestLocalSourceEditorPrefillsWorkspaceRelativeSelection(t *testing.T) {
	m := editorModelForInteraction(t)
	m.workspace = "~/dev"
	m.workspaceResolved = filepath.Join(string(filepath.Separator), "home", "developer", "dev")
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{
		Path: "{{ workspace_root }}/reploy/examples/omegaconf-inspector",
	}
	m.screen = screenChoose
	m.optionCursor = 1
	updated, _ := m.updateChoose(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.screen != screenPath || m.input.Value() != "reploy/examples/omegaconf-inspector" {
		t.Fatalf("local source input = %q on screen %v", m.input.Value(), m.screen)
	}
}

func TestMatchingProjectsDoesNotSilentlyTruncateWorkspace(t *testing.T) {
	root := t.TempDir()
	for index := range 105 {
		pythonProject(t, filepath.Join(root, fmt.Sprintf("project-%03d", index)), fmt.Sprintf("project-%03d", index))
	}
	results, err := matchingProjects(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 105 {
		t.Fatalf("project count = %d, want 105", len(results))
	}
}

func TestMatchingProjectsFindsWorkspaceProjectNamedInSetupPy(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "omegaconf")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(project, "pyproject.toml"),
		[]byte("[build-system]\nrequires = [\"setuptools\"]\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(project, "setup.py"),
		[]byte("setuptools.setup(\n    name=\"omegaconf\",\n    version=find_version(),\n)\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	results, err := matchingProjects(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != project {
		t.Fatalf("results = %#v", results)
	}
	if got, err := pythonProjectName(project); err != nil || got != "omegaconf" {
		t.Fatalf("project name = %q, error = %v", got, err)
	}

	m := editorModelForInteraction(t)
	m.items[0].Package = "omegaconf"
	m.workspace = "~/dev"
	m.workspaceResolved = root
	m.screen = screenPath
	m.input.Focus()
	m.projectCatalog = results
	m.projectCatalogRoot = root
	m.input.SetValue("")
	m.filterProjectResults()
	if len(m.results) != 1 || !strings.Contains(m.View(), "omegaconf") {
		t.Fatalf("workspace choices were not shown before filtering:\n%s", m.View())
	}
	m.input.SetValue("mega")
	m.filterProjectResults()
	if len(m.results) != 1 || m.results[0] != project {
		t.Fatalf("filtered results = %#v", m.results)
	}
	m.input.SetValue("")
	m.input.CursorEnd()
	updated, _ := m.updatePath(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(*model)
	if m.screen != screenPath || m.input.Value() != "w" {
		t.Fatalf("typing w changed screen or field: screen=%v input=%q", m.screen, m.input.Value())
	}
	updated, _ = m.updatePath(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'W'}})
	m = updated.(*model)
	if m.screen != screenPath || m.input.Value() != "wW" {
		t.Fatalf("typing W changed screen or field: screen=%v input=%q", m.screen, m.input.Value())
	}
	updated, _ = m.updatePath(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = updated.(*model)
	if m.screen != screenWorkspace || m.workspaceReturn != screenPath {
		t.Fatalf("Ctrl+W did not open workspace root: screen=%v return=%v", m.screen, m.workspaceReturn)
	}
	m.restoreWorkspaceReturn()
	m.input.SetValue("omegaconf")
	m.filterProjectResults()
	updated, _ = m.updatePath(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if got := m.items[0].Choice.Path; got != "{{ workspace_root }}/omegaconf" {
		t.Fatalf("selected workspace path = %q", got)
	}
}

func TestSaveSidecarRejectsConcurrentChange(t *testing.T) {
	dir := stagedEditorDir(t)
	original, err := readEditorSnapshot(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := deploy.EmptyPackageOverridesV1("example")
	first.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{"demo": {Version: "1"}}
	if _, err := saveSidecarAt(t.Context(), dir, original, first); err != nil {
		t.Fatal(err)
	}
	second := deploy.EmptyPackageOverridesV1("example")
	second.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{"demo": {Version: "2"}}
	if _, err := saveSidecarAt(t.Context(), dir, original, second); err == nil || !strings.Contains(err.Error(), "changed while the editor was open") {
		t.Fatalf("error = %v", err)
	}
	loaded, found, err := deploy.ReadPackageOverridesV1(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !found || loaded.Environment.PackageOverrides["python"]["demo"].Version != "1" {
		t.Fatalf("loaded = %#v, found=%v", loaded, found)
	}
}

func TestSaveSidecarRejectsStagingEnvironmentReplacement(t *testing.T) {
	dir := stagedEditorDir(t)
	original, err := readEditorSnapshot(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	writeEditorState(t, dir, editorDocumentForEnvironment("replacement", "demo"))
	overrides := deploy.EmptyPackageOverridesV1("example")
	overrides.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"demo": {Version: "1"},
	}
	if _, err := saveSidecarAt(t.Context(), dir, original, overrides); err == nil ||
		!strings.Contains(err.Error(), "staged deployment changed") {
		t.Fatalf("error = %v", err)
	}
	if _, found, err := deploy.ReadPackageOverridesV1(dir); err != nil || found {
		t.Fatalf("sidecar after rejected save: found=%v err=%v", found, err)
	}
}

func TestSaveSidecarRejectsSameEnvironmentRestage(t *testing.T) {
	dir := stagedEditorDir(t)
	original, err := readEditorSnapshot(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	writeEditorState(t, dir, editorDocumentForEnvironment("example", "replacement"))
	overrides := deploy.EmptyPackageOverridesV1("example")
	overrides.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"demo": {Version: "1"},
	}
	if _, err := saveSidecarAt(t.Context(), dir, original, overrides); err == nil ||
		!strings.Contains(err.Error(), "staged deployment changed") {
		t.Fatalf("error = %v", err)
	}
}

func TestVersionResultsPreserveFullSortedCatalogAndFilter(t *testing.T) {
	m := editorModelForInteraction(t)
	m.screen = screenVersion
	m.versionCache["demo"] = []string{"10.0", "2.4.0.dev3", "2.4.0", "2.3.0", "1.0"}
	sortVersionsNewestFirst(m.versionCache["demo"])
	m.input.SetValue("")
	m.filterVersionResults()
	if got := strings.Join(m.results, ","); got != "10.0,2.4.0,2.4.0.dev3,2.3.0,1.0" {
		t.Fatalf("sorted versions = %q", got)
	}
	m.input.SetValue("2.4")
	m.filterVersionResults()
	if got := strings.Join(m.results, ","); got != "2.4.0,2.4.0.dev3" {
		t.Fatalf("filtered versions = %q", got)
	}
}

func validationResultFromBatch(t *testing.T, command tea.Cmd) tea.Msg {
	t.Helper()
	message := command()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		t.Fatalf("validation command returned %T, want tea.BatchMsg", message)
	}
	messages := make(chan tea.Msg, len(batch))
	for _, item := range batch {
		item := item
		go func() {
			messages <- item()
		}()
	}
	timeout := time.After(5 * time.Second)
	for range batch {
		select {
		case message := <-messages:
			if _, ok := message.(validatedMsg); ok {
				return message
			}
		case <-timeout:
			t.Fatal("validation command did not finish")
		}
	}
	t.Fatal("validation command did not return a result")
	return nil
}

func editorModelForInteraction(t *testing.T) *model {
	t.Helper()
	dir := stagedEditorDir(t)
	m, err := newModel(Config{
		Context:       t.Context(),
		DeploymentDir: dir,
		Document:      editorDocument("demo"),
		Versions: func(_ context.Context, _ string) ([]string, error) {
			return []string{"2.0", "1.0"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func stagedEditorDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".reploy")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeEditorState(t, dir, editorDocument("demo"))
	return dir
}

func writeEditorState(t *testing.T, dir string, document blueprint.Document) {
	t.Helper()
	stateDir := filepath.Join(dir, ".reploy")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := blueprint.EncodeResolvedDocumentV1(document)
	if err != nil {
		t.Fatal(err)
	}
	state, err := deploy.EncodeStateV1(deploy.StateV1{
		Schema:          deploy.StateSchemaV1,
		Blueprint:       resolved,
		BlueprintSource: "test.blueprint.yaml",
		Platform:        document.Blueprint.Compatibility.Platforms[0],
		Overlay:         deploy.EmptyRequestOverlayV1(),
		Staging:         &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitOverrides(t *testing.T, dir string, overrides deploy.PackageOverridesV1) {
	t.Helper()
	lock, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	if err := lock.CommitPackageOverridesV1(overrides); err != nil {
		t.Fatal(err)
	}
}

func pythonProject(t *testing.T, dir string, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("[project]\nname = \"" + name + "\"\n")
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pythonProjectWithDependencies(t *testing.T, dir string, name string, dependencies []string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		quoted = append(quoted, `"`+dependency+`"`)
	}
	content := []byte("[project]\nname = \"" + name + "\"\ndependencies = [" + strings.Join(quoted, ", ") + "]\n")
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func editorDocument(requirements ...string) blueprint.Document {
	return editorDocumentForEnvironment("example", requirements...)
}

func editorDocumentForEnvironment(environmentID string, requirements ...string) blueprint.Document {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		panic(err)
	}
	return blueprint.Document{
		Blueprint: blueprint.Metadata{
			Schema:  1,
			Version: "0.1.0",
			Compatibility: blueprint.Compatibility{
				Platforms: []blueprint.Platform{platform},
			},
		},
		Environment: blueprint.Environment{
			ID: environmentID,
			Base: blueprint.BaseComponent{
				Image: "python:3.11-slim", Exports: map[string]blueprint.BaseExecutableExport{},
			},
			Applications: map[string]blueprint.Application{
				"python": {
					Packages: blueprint.ApplicationPackages{
						Python: &blueprint.PythonComponent{Requirements: requirements},
					},
				},
			},
			Components: map[string]blueprint.Component{
				"base": {
					Type: blueprint.ComponentTypeBase,
					Base: &blueprint.BaseComponent{
						Image: "python:3.11-slim", Exports: map[string]blueprint.BaseExecutableExport{},
					},
				},
				"python": {
					Type: blueprint.ComponentTypePython,
					Python: &blueprint.PythonComponent{
						Requirements: requirements,
					},
				},
			},
		},
	}
}
