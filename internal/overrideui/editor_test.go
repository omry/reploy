package overrideui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

func TestNewModelLoadsExistingOverridesAndBlueprintRoots(t *testing.T) {
	dir := stagedEditorDir(t)
	project := pythonProject(t, filepath.Join(dir, "workspace", "demo"), "demo")
	overrides := deploy.EmptyPackageOverridesV1("example")
	overrides.Environment.Vars["workspace_root"] = filepath.Dir(project)
	overrides.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"demo":  {Path: "{{ workspace_root }}/demo"},
		"extra": {Version: "2.0"},
	}
	commitOverrides(t, dir, overrides)

	m, err := newModel(Config{
		Context:       t.Context(),
		DeploymentDir: dir,
		Document:      editorDocument("other>=1"),
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
}

func TestNewModelRejectsWorkspaceRootTerminalControls(t *testing.T) {
	dir := stagedEditorDir(t)
	overrides := deploy.EmptyPackageOverridesV1("example")
	overrides.Environment.Vars["workspace_root"] = "/tmp/project-\x1b]52;c;injected\a"
	commitOverrides(t, dir, overrides)

	_, err := newModel(Config{
		Context: t.Context(), DeploymentDir: dir, Document: editorDocument("demo"),
	})
	if err == nil || !strings.Contains(err.Error(), "workspace_root must not contain terminal control sequences") {
		t.Fatalf("newModel error = %v", err)
	}
}

func TestEditorItemsListsAllExplicitDependenciesBeforeOverrideOnlyRows(t *testing.T) {
	document := editorDocument("base-root>=1")
	component := document.Environment.Components["python"]
	component.Options = map[string]blueprint.ComponentOption{
		"feature": {PythonRequirements: []string{"option-root"}},
	}
	document.Environment.Components["python"] = component
	direct, err := pythonprovider.CanonicalPackageRequestV1("direct-root==2")
	if err != nil {
		t.Fatal(err)
	}
	overlay := deploy.RequestOverlayV1{
		Schema: deploy.RequestOverlaySchemaV1,
		SelectedOptions: []deploy.QualifiedOption{{
			Component: "python", Option: "feature",
		}},
		DirectPackages: []deploy.DirectPackageRequest{{
			Component: "python", Package: providers.CanonicalPackageRequest(direct),
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

	updated, _ := m.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = updated.(*model)
	if m.screen != screenPath || m.input.Value() != "demow" {
		t.Fatalf("typing w changed screens: screen %v, input %q", m.screen, m.input.Value())
	}
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = updated.(*model)
	if m.screen != screenWorkspace {
		t.Fatalf("workspace shortcut screen = %v", m.screen)
	}
	workspace := t.TempDir()
	m.input.SetValue(workspace)
	updated, _ = m.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*model)
	if m.screen != screenPath || m.workspace != workspace || m.input.Value() != "demow" || !m.input.Focused() {
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

func TestProjectResultsDoNotRenderTerminalControls(t *testing.T) {
	m := editorModelForInteraction(t)
	m.width = 100
	m.height = 24
	m.screen = screenPath
	m.results = []string{"/tmp/project-\x1b]52;c;injected\a\nspoofed\trow"}
	m.resultCursor = 0

	view := m.View()
	if strings.ContainsAny(view, "\x1b\a") {
		t.Fatalf("project result rendered terminal controls: %q", view)
	}
	if !strings.Contains(view, "/tmp/project-") {
		t.Fatalf("sanitized project result missing from view: %q", view)
	}
	if strings.Contains(view, "spoofed\trow") || !strings.Contains(view, "spoofedrow") {
		t.Fatalf("project result retained line-oriented control text: %q", view)
	}
}

func TestTruncatePreservesUTF8AndDisplayWidth(t *testing.T) {
	got := truncate("项目-directory", 8)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate returned invalid UTF-8: %q", got)
	}
	if width := lipgloss.Width(got); width > 8 {
		t.Fatalf("truncate width = %d, want at most 8: %q", width, got)
	}
}

func TestMainViewFitsNarrowTerminal(t *testing.T) {
	m := editorModelForInteraction(t)
	m.width = 48
	m.height = 24
	m.items[0].Choice = deploy.PackageOverrideChoiceV1{Path: "/a/very/long/path/to/a/local/project/that/must/not-overflow"}
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
}

func TestDoubleCtrlCIsRequiredToQuit(t *testing.T) {
	m := editorModelForInteraction(t)
	updated, command := m.updateKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*model)
	if command != nil || !m.ctrlCArmed {
		t.Fatalf("first Ctrl-C command=%v armed=%v", command, m.ctrlCArmed)
	}
	_, command = m.updateKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if command == nil {
		t.Fatal("second Ctrl-C did not request exit")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("second Ctrl-C message = %T", command())
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

func TestMatchingProjectsFindsPythonProjectsOnly(t *testing.T) {
	root := t.TempDir()
	project := pythonProject(t, filepath.Join(root, "hydra"), "hydra-core")
	if err := os.MkdirAll(filepath.Join(root, "hydra-not-a-project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git", "hydra-hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	results, err := matchingProjects(root, "hydra", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != project {
		t.Fatalf("results = %#v", results)
	}
}

func TestSaveSidecarRejectsConcurrentChange(t *testing.T) {
	dir := stagedEditorDir(t)
	original, err := readSidecarSnapshot(dir)
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
	original, err := readSidecarSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeEditorState(t, dir, editorDocumentForEnvironment("replacement", "demo"))
	overrides := deploy.EmptyPackageOverridesV1("example")
	overrides.Environment.PackageOverrides["python"] = map[string]deploy.PackageOverrideChoiceV1{
		"demo": {Version: "1"},
	}
	if _, err := saveSidecarAt(t.Context(), dir, original, overrides); err == nil ||
		!strings.Contains(err.Error(), `changed from "example" to "replacement"`) {
		t.Fatalf("error = %v", err)
	}
	if _, found, err := deploy.ReadPackageOverridesV1(dir); err != nil || found {
		t.Fatalf("sidecar after rejected save: found=%v err=%v", found, err)
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
			Components: map[string]blueprint.Component{
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
