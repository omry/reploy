package overrideui

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/pelletier/go-toml/v2"
)

type VersionCatalog func(context.Context, string) ([]string, error)

type Config struct {
	Context       context.Context
	DeploymentDir string
	Document      blueprint.Document
	Overlay       deploy.RequestOverlayV1
	Input         io.Reader
	Output        io.Writer
	WorkingDir    string
	Versions      VersionCatalog
}

type fileSnapshot struct {
	Found  bool
	Digest string
}

type overrideItem struct {
	Provider string
	Package  string
	Explicit bool
	Sources  []string
	Choice   deploy.PackageOverrideChoiceV1
	Error    string
	Notice   string
}

type screenKind int

const (
	screenMain screenKind = iota
	screenChoose
	screenAdd
	screenWorkspace
	screenPath
	screenVersion
	screenPreview
)

type versionsLoadedMsg struct {
	Package  string
	Versions []string
	Err      error
}

type projectsLoadedMsg struct {
	Query    string
	Root     string
	Projects []string
	Err      error
}

type savedMsg struct {
	Snapshot fileSnapshot
	Err      error
}

type model struct {
	ctx           context.Context
	deploymentDir string
	document      blueprint.Document
	raw           deploy.PackageOverridesV1
	original      fileSnapshot
	save          func(context.Context, fileSnapshot, deploy.PackageOverridesV1) (fileSnapshot, error)
	versions      VersionCatalog

	screen          screenKind
	width           int
	height          int
	cursor          int
	optionCursor    int
	input           textinput.Model
	items           []overrideItem
	workspace       string
	workspaceReturn screenKind
	workspaceInput  string
	browseRoot      string
	results         []string
	resultCursor    int
	versionCache    map[string][]string
	versionError    map[string]string
	versionPending  map[string]bool
	status          string
	dirty           bool
	ctrlCArmed      bool
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	goodStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	badStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	focusStyle  = lipgloss.NewStyle().Background(lipgloss.Color("25")).Foreground(lipgloss.Color("255"))
	directStyle = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	panelStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("220")).Padding(1, 2)
)

func Run(config Config) error {
	m, err := newModel(config)
	if err != nil {
		return err
	}
	options := []tea.ProgramOption{tea.WithContext(m.ctx), tea.WithAltScreen()}
	if config.Input != nil {
		options = append(options, tea.WithInput(config.Input))
	}
	if config.Output != nil {
		options = append(options, tea.WithOutput(config.Output))
	}
	_, err = tea.NewProgram(m, options...).Run()
	return err
}

func newModel(config Config) (*model, error) {
	if config.Context == nil {
		config.Context = context.Background()
	}
	if err := config.Context.Err(); err != nil {
		return nil, err
	}
	deploymentDir, err := filepath.Abs(config.DeploymentDir)
	if err != nil {
		return nil, fmt.Errorf("resolve package override editor directory: %w", err)
	}
	statePath := filepath.Join(deploymentDir, ".reploy", "state.json")
	if config.Document.Environment.ID == "" {
		return nil, fmt.Errorf("package override editor requires a resolved environment")
	}
	if _, err := os.Stat(statePath); err != nil {
		return nil, fmt.Errorf("package override editor requires a staged deployment at %s: %w", deploymentDir, err)
	}
	raw, found, err := deploy.ReadPackageOverridesV1(deploymentDir)
	if err != nil {
		return nil, err
	}
	if !found {
		raw = deploy.EmptyPackageOverridesV1(config.Document.Environment.ID)
	}
	if raw.Environment.ID != config.Document.Environment.ID {
		return nil, fmt.Errorf("package overrides target environment %q, want %q", raw.Environment.ID, config.Document.Environment.ID)
	}
	snapshot, err := readSidecarSnapshot(deploymentDir)
	if err != nil {
		return nil, err
	}
	workingDir := config.WorkingDir
	if workingDir == "" {
		workingDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve package override editor working directory: %w", err)
		}
	}
	workingDir, err = filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve package override editor workspace: %w", err)
	}
	workspace := ""
	if value, ok := raw.Environment.Vars["workspace_root"].(string); ok {
		if sanitizeTerminalText(value) != value {
			return nil, fmt.Errorf("package overrides environment.vars.workspace_root must not contain terminal control sequences")
		}
		if filepath.IsAbs(value) {
			workspace = filepath.Clean(value)
		}
	}
	overlay := config.Overlay
	if overlay.Schema == "" && overlay.SelectedOptions == nil && overlay.DirectPackages == nil {
		overlay = deploy.EmptyRequestOverlayV1()
	}
	if _, err := deploy.RequestOverlayDigestV1(overlay); err != nil {
		return nil, fmt.Errorf("package override editor request overlay: %w", err)
	}
	items, err := editorItems(config.Document, overlay, raw)
	if err != nil {
		return nil, err
	}
	input := textinput.New()
	input.CharLimit = 512
	input.Width = 72
	m := &model{
		ctx: config.Context, deploymentDir: deploymentDir, document: config.Document,
		raw: raw, original: snapshot,
		save: func(ctx context.Context, original fileSnapshot, overrides deploy.PackageOverridesV1) (fileSnapshot, error) {
			return saveSidecarAt(ctx, deploymentDir, original, overrides)
		},
		versions: config.Versions,
		screen:   screenMain, input: input, items: items, workspace: workspace, browseRoot: filepath.Clean(workingDir),
		versionCache: map[string][]string{}, versionError: map[string]string{},
		versionPending: map[string]bool{},
	}
	if m.versions == nil {
		m.versions = FetchPyPIVersions
	}
	for _, item := range m.items {
		if item.Provider == "python" && item.Choice.Version != "" {
			m.versionPending[item.Package] = true
		}
	}
	m.validateItems()
	return m, nil
}

func editorItems(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	raw deploy.PackageOverridesV1,
) ([]overrideItem, error) {
	byKey := map[string]overrideItem{}
	addExplicit := func(distribution string, source string) {
		key := "python\x00" + distribution
		item := byKey[key]
		item.Provider = "python"
		item.Package = distribution
		item.Explicit = true
		if !contains(item.Sources, source) {
			item.Sources = append(item.Sources, source)
			sort.Strings(item.Sources)
		}
		byKey[key] = item
	}
	for componentName, component := range document.Environment.Components {
		if component.Type != blueprint.ComponentTypePython || component.Python == nil {
			continue
		}
		for _, requirement := range component.Python.Requirements {
			distribution, err := pythonprovider.RequirementDistributionName(requirement)
			if err != nil {
				return nil, fmt.Errorf("identify Python requirement %q: %w", requirement, err)
			}
			addExplicit(distribution, pythonRequirementSource(requirement))
		}
		for _, selected := range overlay.SelectedOptions {
			if selected.Component != componentName {
				continue
			}
			for _, requirement := range component.Options[selected.Option].PythonRequirements {
				distribution, err := pythonprovider.RequirementDistributionName(requirement)
				if err != nil {
					return nil, fmt.Errorf("identify Python option requirement %q: %w", requirement, err)
				}
				addExplicit(distribution, pythonRequirementSource(requirement))
			}
		}
	}
	for _, direct := range overlay.DirectPackages {
		component, found := document.Environment.Components[direct.Component]
		if !found || component.Type != blueprint.ComponentTypePython {
			continue
		}
		requirement, ok := direct.Package.Value["requirement"].(string)
		if !ok {
			return nil, fmt.Errorf("identify direct Python requirement for component %q: requirement must be a string", direct.Component)
		}
		distribution, err := pythonprovider.RequirementDistributionName(requirement)
		if err != nil {
			return nil, fmt.Errorf("identify direct Python requirement %q: %w", requirement, err)
		}
		addExplicit(distribution, pythonRequirementSource(requirement))
	}
	for provider, packages := range raw.Environment.PackageOverrides {
		for packageID, choice := range packages {
			normalizedPackage := packageID
			if provider == "python" {
				normalizedPackage = pythonprovider.NormalizeDistributionName(packageID)
			}
			key := provider + "\x00" + normalizedPackage
			item := byKey[key]
			item.Provider = provider
			item.Package = normalizedPackage
			item.Choice = choice
			byKey[key] = item
		}
	}
	items := make([]overrideItem, 0, len(byKey))
	for _, item := range byKey {
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].Explicit != items[right].Explicit {
			return items[left].Explicit
		}
		if items[left].Provider != items[right].Provider {
			return items[left].Provider < items[right].Provider
		}
		return items[left].Package < items[right].Package
	})
	return items, nil
}

func pythonRequirementSource(requirement string) string {
	value := strings.TrimSpace(requirement)
	if at := strings.IndexByte(value, '@'); at >= 0 {
		location := strings.TrimSpace(value[at+1:])
		if marker := strings.Index(location, " ; "); marker >= 0 {
			location = strings.TrimSpace(location[:marker])
		}
		if strings.HasPrefix(location, "file://") {
			return "file URL · " + location
		}
		return "direct URL · " + location
	}
	nameEnd := 0
	for nameEnd < len(value) {
		char := value[nameEnd]
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.') {
			break
		}
		nameEnd++
	}
	constraint := strings.TrimSpace(value[nameEnd:])
	if strings.HasPrefix(constraint, "[") {
		if close := strings.IndexByte(constraint, ']'); close >= 0 {
			constraint = strings.TrimSpace(constraint[close+1:])
		}
	}
	if marker := strings.IndexByte(constraint, ';'); marker >= 0 {
		constraint = strings.TrimSpace(constraint[:marker])
	}
	if constraint == "" {
		return "PyPI"
	}
	return "PyPI · " + constraint
}

func (m *model) Init() tea.Cmd {
	commands := []tea.Cmd{}
	for _, item := range m.items {
		if item.Choice.Version != "" && item.Provider == "python" {
			commands = append(commands, loadVersions(m.ctx, m.versions, item.Package))
		}
	}
	return tea.Batch(commands...)
}

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(24, min(72, msg.Width-12))
		return m, nil
	case versionsLoadedMsg:
		delete(m.versionPending, msg.Package)
		if msg.Err != nil {
			m.versionError[msg.Package] = "load upstream versions: " + msg.Err.Error()
			delete(m.versionCache, msg.Package)
		} else {
			m.versionCache[msg.Package] = msg.Versions
			delete(m.versionError, msg.Package)
		}
		if m.screen == screenVersion && m.current().Package == msg.Package {
			m.filterVersionResults()
		}
		m.validateItems()
		return m, nil
	case projectsLoadedMsg:
		if m.screen == screenPath && msg.Query == m.input.Value() && msg.Root == m.projectSearchRoot() {
			m.results = msg.Projects
			m.resultCursor = clamp(m.resultCursor, 0, max(0, len(m.results)-1))
			if msg.Err != nil {
				m.status = msg.Err.Error()
			}
		}
		return m, nil
	case savedMsg:
		if msg.Err != nil {
			m.status = "Save failed: " + msg.Err.Error()
		} else {
			m.original = msg.Snapshot
			m.raw = m.buildRaw()
			m.dirty = false
			m.status = "Saved " + deploy.PackageOverridesFilename
		}
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *model) View() string {
	content := ""
	switch m.screen {
	case screenMain:
		content = m.viewMain()
	case screenChoose:
		content = m.viewChoose()
	case screenAdd:
		content = m.viewInputDialog("Add Python package", "Enter creates the row · Esc cancels")
	case screenWorkspace:
		content = m.viewInputDialog(
			"Workspace root",
			"Optional existing absolute directory · leave empty to store absolute paths · Enter applies · Esc cancels",
		)
	case screenPath:
		content = m.viewPath()
	case screenVersion:
		detail := "Type to filter the provider's upstream release catalog."
		if err := m.versionError[m.current().Package]; err != "" {
			detail = badStyle.Render(sanitizeTerminalText(err))
		}
		content = m.viewResults(
			"Select upstream version for "+m.current().Package,
			detail,
			"Type filters · ↑/↓ select · Enter applies · Esc cancels",
		)
	case screenPreview:
		encoded, err := deploy.EncodePackageOverridesV1(m.buildRaw())
		if err != nil {
			content = panelStyle.Render(badStyle.Render(sanitizeTerminalText(err.Error())))
		} else {
			content = panelStyle.Render(titleStyle.Render(deploy.PackageOverridesFilename) + "\n\n" + string(encoded) + "\nEsc returns")
		}
	}
	if m.status != "" {
		content += "\n" + mutedStyle.Render(sanitizeTerminalText(m.status))
	}
	return content
}

func (m *model) viewMain() string {
	var body strings.Builder
	contentWidth := max(20, m.panelWidth()-6)
	body.WriteString(titleStyle.Render("Reploy Development Overrides"))
	body.WriteString("\n")
	body.WriteString(mutedStyle.Render(truncate(
		sanitizeTerminalText(m.document.Environment.ID+" · "+filepath.Join(m.deploymentDir, deploy.PackageOverridesFilename)),
		contentWidth,
	)))
	body.WriteString("\n")
	workspace := "not set (local paths remain absolute)"
	if m.workspace != "" {
		workspace = sanitizeTerminalText(m.workspace)
	}
	body.WriteString(mutedStyle.Render("Workspace " + workspace))
	body.WriteString("\n\n")
	compact := m.width > 0 && m.width < 90
	if !compact {
		body.WriteString(fmt.Sprintf("  %-10s %-28s %-34s %s\n", "Provider", "Package", "Source", "Status"))
	}
	if len(m.items) == 0 {
		body.WriteString(mutedStyle.Render("  No package roots or overrides. Press A to add one."))
		body.WriteString("\n")
	}
	for index, item := range m.items {
		source := "not selected"
		if len(item.Sources) != 0 {
			source = strings.Join(item.Sources, ", ")
		} else if item.Provider == "python" && item.Explicit {
			source = "PyPI"
		}
		if item.Choice.Path != "" {
			source = "local · " + item.Choice.Path
		}
		if item.Choice.Version != "" {
			if item.Provider == "python" {
				source = "PyPI · " + item.Choice.Version
			} else {
				source = item.Provider + " provider · " + item.Choice.Version
			}
		}
		source = sanitizeTerminalText(source)
		status := "—"
		if item.Error != "" {
			status = badStyle.Render(sanitizeTerminalText(item.Error))
			source = badStyle.Render(source)
		} else if item.Notice != "" {
			status = warnStyle.Render(sanitizeTerminalText(item.Notice))
		} else if item.Choice.Path != "" || item.Choice.Version != "" {
			status = goodStyle.Render("valid")
		}
		line := ""
		if compact {
			classification := "additional"
			if item.Explicit {
				classification = "explicit"
			}
			line = fmt.Sprintf("  %s / %s [%s]\n  %s · %s",
				item.Provider,
				truncate(item.Package, max(8, contentWidth-len(item.Provider)-15)),
				classification,
				truncate(source, max(8, contentWidth/2)),
				truncate(status, max(6, contentWidth/2-3)),
			)
		} else {
			line = fmt.Sprintf("  %-10s %-28s %-34s %s", item.Provider, truncate(item.Package, 28), truncate(source, 34), status)
		}
		if index == m.cursor {
			line = focusStyle.Render(line)
		} else if item.Explicit {
			line = directStyle.Render(line)
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	body.WriteString("\n")
	body.WriteString(mutedStyle.Render("Shaded rows are explicit dependencies."))
	body.WriteString("\n")
	body.WriteString("↑/↓ select · Enter edit · A add · D reset · W workspace · P preview · Ctrl+S save · Ctrl+C twice exit")
	if m.dirty {
		body.WriteString("\n" + goodStyle.Render("Unsaved changes"))
	}
	return panelStyle.Width(m.panelWidth()).Render(body.String())
}

func (m *model) viewChoose() string {
	options := []string{"Blueprint default", "Local path", "Upstream version"}
	if !m.current().Explicit {
		options[0] = "No override"
	}
	var body strings.Builder
	body.WriteString(titleStyle.Render("Source for " + m.current().Package))
	body.WriteString("\n\n")
	for index, option := range options {
		line := "  " + option
		if index == m.optionCursor {
			line = focusStyle.Render(line)
		}
		body.WriteString(line + "\n")
	}
	body.WriteString("\n↑/↓ choose · Enter continue · Esc cancel")
	return panelStyle.Width(m.dialogWidth()).Render(body.String())
}

func (m *model) viewInputDialog(title string, help string) string {
	body := titleStyle.Render(title) + "\n\n" + m.input.View() + "\n\n" + mutedStyle.Render(help)
	return panelStyle.Width(m.dialogWidth()).Render(body)
}

func (m *model) viewPath() string {
	workspace := "not set; selected paths are stored as absolute paths"
	if m.workspace != "" {
		workspace = sanitizeTerminalText(m.workspace) + "; paths inside it use {{ workspace_root }}"
	}
	return m.viewResults(
		"Find a local Python project for "+m.current().Package,
		"Workspace root: "+workspace+"\nSearch root: "+sanitizeTerminalText(m.projectSearchRoot())+". An absolute directory outside it is also accepted.",
		"Type filters · ↑/↓ select · Ctrl+W change workspace root · Enter applies · Esc cancels",
	)
}

func (m *model) viewResults(title string, detail string, help string) string {
	var body strings.Builder
	body.WriteString(titleStyle.Render(title))
	body.WriteString("\n\n")
	body.WriteString(m.input.View())
	body.WriteString("\n")
	body.WriteString(mutedStyle.Render(detail))
	body.WriteString("\n\n")
	if len(m.results) == 0 {
		body.WriteString(mutedStyle.Render("  No matches"))
		body.WriteByte('\n')
	} else {
		limit := min(len(m.results), max(4, m.height-14))
		start := clamp(m.resultCursor-limit/2, 0, max(0, len(m.results)-limit))
		for index := start; index < start+limit; index++ {
			line := "  " + truncate(sanitizeTerminalText(m.results[index]), max(12, m.panelWidth()-8))
			if index == m.resultCursor {
				line = focusStyle.Render(line)
			}
			body.WriteString(line + "\n")
		}
	}
	body.WriteString("\n" + help)
	return panelStyle.Width(m.panelWidth()).Render(body.String())
}

func (m *model) panelWidth() int {
	if m.width <= 0 {
		return 100
	}
	return max(28, min(118, m.width-2))
}

func (m *model) dialogWidth() int {
	if m.width <= 0 {
		return 72
	}
	return max(28, min(78, m.width-2))
}

func (m *model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() != "ctrl+c" {
		m.ctrlCArmed = false
	}
	if key.String() == "ctrl+c" {
		if m.ctrlCArmed {
			return m, tea.Quit
		}
		m.ctrlCArmed = true
		m.status = "Press Ctrl+C again to exit without saving."
		return m, nil
	}
	if key.String() == "ctrl+s" {
		raw := m.buildRaw()
		if err := deploy.ValidatePackageOverridesV1(raw); err != nil {
			m.status = "Cannot save: " + err.Error()
			return m, nil
		}
		m.validateItems()
		for _, item := range m.items {
			if item.Error != "" {
				m.status = "Cannot save while an override is invalid."
				return m, nil
			}
		}
		return m, func() tea.Msg {
			snapshot, err := m.save(m.ctx, m.original, raw)
			return savedMsg{Snapshot: snapshot, Err: err}
		}
	}
	switch m.screen {
	case screenMain:
		return m.updateMain(key)
	case screenChoose:
		return m.updateChoose(key)
	case screenAdd:
		return m.updateAdd(key)
	case screenWorkspace:
		return m.updateWorkspace(key)
	case screenPath:
		return m.updatePath(key)
	case screenVersion:
		return m.updateVersion(key)
	case screenPreview:
		if key.String() == "esc" || key.String() == "enter" || key.String() == "p" {
			m.screen = screenMain
		}
	}
	return m, nil
}

func (m *model) updateMain(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "up":
		m.cursor = clamp(m.cursor-1, 0, max(0, len(m.items)-1))
	case "down":
		m.cursor = clamp(m.cursor+1, 0, max(0, len(m.items)-1))
	case "enter", "e":
		if len(m.items) != 0 {
			m.screen = screenChoose
			m.optionCursor = choiceIndex(m.current().Choice)
		}
	case "a":
		m.screen = screenAdd
		m.input.SetValue("")
		m.input.Placeholder = "Python package name"
		m.input.Focus()
	case "d", "backspace", "delete":
		if len(m.items) != 0 {
			m.resetCurrentOverride()
		}
	case "w":
		m.openWorkspace(screenMain)
	case "p":
		m.screen = screenPreview
	}
	return m, nil
}

func (m *model) updateChoose(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		if !m.current().Explicit && m.current().Choice == (deploy.PackageOverrideChoiceV1{}) {
			m.resetCurrentOverride()
		}
		m.screen = screenMain
	case "up", "left":
		m.optionCursor = clamp(m.optionCursor-1, 0, 2)
	case "down", "right":
		m.optionCursor = clamp(m.optionCursor+1, 0, 2)
	case "enter":
		switch m.optionCursor {
		case 0:
			m.resetCurrentOverride()
			m.screen = screenMain
		case 1:
			m.screen = screenPath
			m.input.SetValue("")
			m.input.Placeholder = "Filter projects, or enter an absolute directory"
			m.input.Focus()
			m.results = []string{}
			m.resultCursor = 0
		case 2:
			m.screen = screenVersion
			m.input.SetValue("")
			m.input.Placeholder = "Filter upstream versions"
			m.input.Focus()
			m.results = []string{}
			m.resultCursor = 0
			if m.current().Choice.Version != "" {
				m.versionPending[m.current().Package] = true
				m.validateItems()
			}
			return m, loadVersions(m.ctx, m.versions, m.current().Package)
		}
	}
	return m, nil
}

func (m *model) updateAdd(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.input.Blur()
		m.screen = screenMain
		return m, nil
	case "enter":
		packageID := pythonprovider.NormalizeDistributionName(strings.TrimSpace(m.input.Value()))
		if err := blueprint.ValidatePythonDistributionName("Python package override", packageID); err != nil {
			m.status = err.Error()
			return m, nil
		}
		for index := range m.items {
			if m.items[index].Provider == "python" && m.items[index].Package == packageID {
				m.cursor = index
				m.input.Blur()
				m.screen = screenChoose
				m.optionCursor = choiceIndex(m.current().Choice)
				return m, nil
			}
		}
		m.items = append(m.items, overrideItem{Provider: "python", Package: packageID})
		sort.Slice(m.items, func(left, right int) bool {
			if m.items[left].Explicit != m.items[right].Explicit {
				return m.items[left].Explicit
			}
			if m.items[left].Provider != m.items[right].Provider {
				return m.items[left].Provider < m.items[right].Provider
			}
			return m.items[left].Package < m.items[right].Package
		})
		for index := range m.items {
			if m.items[index].Provider == "python" && m.items[index].Package == packageID {
				m.cursor = index
				break
			}
		}
		m.input.Blur()
		m.screen = screenChoose
		m.optionCursor = 0
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return m, cmd
}

func (m *model) updateWorkspace(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.input.Blur()
		m.restoreWorkspaceReturn()
		return m, nil
	case "enter":
		rawValue := strings.TrimSpace(m.input.Value())
		if rawValue == "" {
			m.workspace = ""
			m.input.Blur()
			m.restoreWorkspaceReturn()
			m.dirty = true
			m.validateItems()
			return m, m.refreshPathResults()
		}
		value := filepath.Clean(rawValue)
		info, err := os.Stat(value)
		if err != nil || !filepath.IsAbs(value) || !info.IsDir() {
			m.status = "Workspace must be an existing absolute directory."
			return m, nil
		}
		m.workspace = value
		m.input.Blur()
		m.restoreWorkspaceReturn()
		m.dirty = true
		m.validateItems()
		return m, m.refreshPathResults()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return m, cmd
}

func (m *model) updatePath(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.input.Blur()
		m.screen = screenChoose
		return m, nil
	case "up":
		m.resultCursor = clamp(m.resultCursor-1, 0, max(0, len(m.results)-1))
		return m, nil
	case "down":
		m.resultCursor = clamp(m.resultCursor+1, 0, max(0, len(m.results)-1))
		return m, nil
	case "ctrl+w":
		m.openWorkspace(screenPath)
		return m, nil
	case "enter":
		selected := ""
		if len(m.results) != 0 {
			selected = m.results[m.resultCursor]
		} else {
			value := strings.TrimSpace(m.input.Value())
			if filepath.IsAbs(value) {
				selected = filepath.Clean(value)
			}
		}
		if selected == "" {
			m.status = "Choose a project directory or enter an absolute path."
			return m, nil
		}
		m.items[m.cursor].Choice = deploy.PackageOverrideChoiceV1{Path: storedPath(m.workspace, selected)}
		m.input.Blur()
		m.screen = screenMain
		m.dirty = true
		m.validateItems()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	query := m.input.Value()
	m.resultCursor = 0
	if filepath.IsAbs(strings.TrimSpace(query)) {
		value := filepath.Clean(strings.TrimSpace(query))
		if info, err := os.Stat(value); err == nil && info.IsDir() {
			m.results = []string{value}
		} else {
			m.results = []string{}
		}
		return m, cmd
	}
	if len(strings.TrimSpace(query)) < 2 {
		m.results = []string{}
		return m, cmd
	}
	return m, tea.Batch(cmd, findProjects(m.projectSearchRoot(), query))
}

func (m *model) openWorkspace(returnTo screenKind) {
	m.workspaceReturn = returnTo
	m.workspaceInput = m.input.Value()
	m.screen = screenWorkspace
	m.input.SetValue(m.workspace)
	m.input.Placeholder = "Optional /absolute/workspace/root"
	m.input.Focus()
}

func (m *model) restoreWorkspaceReturn() {
	returnTo := m.workspaceReturn
	if returnTo != screenPath {
		returnTo = screenMain
	}
	m.screen = returnTo
	if returnTo == screenPath {
		m.input.SetValue(m.workspaceInput)
		m.input.Placeholder = "Filter projects, or enter an absolute directory"
		m.input.Focus()
	}
	m.workspaceReturn = screenMain
	m.workspaceInput = ""
}

func (m *model) refreshPathResults() tea.Cmd {
	if m.screen != screenPath {
		return nil
	}
	query := strings.TrimSpace(m.input.Value())
	m.resultCursor = 0
	if filepath.IsAbs(query) {
		value := filepath.Clean(query)
		if info, err := os.Stat(value); err == nil && info.IsDir() {
			m.results = []string{value}
		} else {
			m.results = []string{}
		}
		return nil
	}
	m.results = []string{}
	if len(query) < 2 {
		return nil
	}
	return findProjects(m.projectSearchRoot(), query)
}

func (m *model) updateVersion(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.input.Blur()
		m.screen = screenChoose
		return m, nil
	case "up":
		m.resultCursor = clamp(m.resultCursor-1, 0, max(0, len(m.results)-1))
		return m, nil
	case "down":
		m.resultCursor = clamp(m.resultCursor+1, 0, max(0, len(m.results)-1))
		return m, nil
	case "enter":
		if len(m.results) == 0 {
			m.status = "No matching upstream version is selected."
			return m, nil
		}
		m.items[m.cursor].Choice = deploy.PackageOverrideChoiceV1{Version: m.results[m.resultCursor]}
		m.input.Blur()
		m.screen = screenMain
		m.dirty = true
		m.validateItems()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	m.resultCursor = 0
	m.filterVersionResults()
	return m, cmd
}

func (m *model) filterVersionResults() {
	versions := m.versionCache[m.current().Package]
	query := strings.ToLower(strings.TrimSpace(m.input.Value()))
	m.results = m.results[:0]
	for _, version := range versions {
		if query == "" || strings.Contains(strings.ToLower(version), query) {
			m.results = append(m.results, version)
		}
	}
	m.resultCursor = clamp(m.resultCursor, 0, max(0, len(m.results)-1))
}

func (m *model) current() *overrideItem {
	if len(m.items) == 0 {
		return &overrideItem{}
	}
	m.cursor = clamp(m.cursor, 0, len(m.items)-1)
	return &m.items[m.cursor]
}

func (m *model) resetCurrentOverride() {
	if len(m.items) == 0 {
		return
	}
	item := m.items[m.cursor]
	if item.Explicit {
		m.items[m.cursor].Choice = deploy.PackageOverrideChoiceV1{}
	} else {
		m.items = append(m.items[:m.cursor], m.items[m.cursor+1:]...)
		m.cursor = clamp(m.cursor, 0, max(0, len(m.items)-1))
	}
	delete(m.versionPending, item.Package)
	delete(m.versionError, item.Package)
	m.dirty = true
	m.validateItems()
}

func (m *model) buildRaw() deploy.PackageOverridesV1 {
	raw := m.raw
	raw.Environment.ID = m.document.Environment.ID
	raw.Environment.Vars = cloneVars(raw.Environment.Vars)
	if m.workspace == "" {
		delete(raw.Environment.Vars, "workspace_root")
	} else {
		raw.Environment.Vars["workspace_root"] = m.workspace
	}
	raw.Environment.PackageOverrides = map[string]map[string]deploy.PackageOverrideChoiceV1{}
	for _, item := range m.items {
		if item.Choice.Path == "" && item.Choice.Version == "" {
			continue
		}
		if raw.Environment.PackageOverrides[item.Provider] == nil {
			raw.Environment.PackageOverrides[item.Provider] = map[string]deploy.PackageOverrideChoiceV1{}
		}
		raw.Environment.PackageOverrides[item.Provider][item.Package] = item.Choice
	}
	return raw
}

func (m *model) validateItems() {
	raw := m.buildRaw()
	for index := range m.items {
		m.items[index].Error = ""
		m.items[index].Notice = ""
		choice := m.items[index].Choice
		if choice.Path == "" && choice.Version == "" {
			continue
		}
		itemRaw := raw
		itemRaw.Environment.PackageOverrides = map[string]map[string]deploy.PackageOverrideChoiceV1{
			m.items[index].Provider: {
				m.items[index].Package: choice,
			},
		}
		resolved, err := deploy.ResolvePackageOverridesV1(itemRaw, m.deploymentDir, normalizeEditorPackage)
		if err != nil {
			m.items[index].Error = err.Error()
			continue
		}
		if choice.Path != "" {
			resolvedChoice := resolved.Providers[m.items[index].Provider][m.items[index].Package]
			m.items[index].Error = validateLocalProject(m.items[index].Provider, m.items[index].Package, resolvedChoice.Path)
			continue
		}
		if m.versionPending[m.items[index].Package] {
			m.items[index].Notice = "checking upstream version"
			continue
		}
		if versionErr := m.versionError[m.items[index].Package]; versionErr != "" {
			m.items[index].Notice = "not verified: " + versionErr
			continue
		}
		if versions, found := m.versionCache[m.items[index].Package]; found {
			if !contains(versions, choice.Version) {
				m.items[index].Error = "version is not present in the upstream release catalog"
			}
		}
	}
}

func normalizeEditorPackage(provider string, packageID string, choice deploy.PackageOverrideChoiceV1) (string, error) {
	if provider != "python" {
		return "", fmt.Errorf("provider %q is not supported by the v1 override editor", provider)
	}
	if choice.Path != "" && choice.Version != "" {
		return "", fmt.Errorf("select exactly one of path or version")
	}
	normalized := pythonprovider.NormalizeDistributionName(packageID)
	if err := blueprint.ValidatePythonDistributionName("Python package override", normalized); err != nil {
		return "", err
	}
	if choice.Version != "" {
		if err := pythonprovider.ValidatePackageVersionV1(choice.Version); err != nil {
			return "", err
		}
	}
	return normalized, nil
}

func validateLocalProject(provider string, packageID string, path string) string {
	if provider != "python" {
		return "local paths are not supported for this provider"
	}
	info, err := os.Stat(path)
	if err != nil {
		return "path does not exist"
	}
	if !info.IsDir() {
		return "path is not a directory"
	}
	name, err := pythonProjectName(path)
	if err != nil {
		return err.Error()
	}
	if pythonprovider.NormalizeDistributionName(name) != packageID {
		return fmt.Sprintf("project provides %q, not %q", name, packageID)
	}
	return ""
}

func pythonProjectName(dir string) (string, error) {
	content, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if err == nil {
		var document struct {
			Project struct {
				Name string `toml:"name"`
			} `toml:"project"`
			Tool struct {
				Poetry struct {
					Name string `toml:"name"`
				} `toml:"poetry"`
			} `toml:"tool"`
		}
		if err := toml.Unmarshal(content, &document); err != nil {
			return "", fmt.Errorf("pyproject.toml is invalid")
		}
		if document.Project.Name != "" {
			return document.Project.Name, nil
		}
		if document.Tool.Poetry.Name != "" {
			return document.Tool.Poetry.Name, nil
		}
	}
	setup, setupErr := os.Open(filepath.Join(dir, "setup.cfg"))
	if setupErr == nil {
		defer setup.Close()
		section := ""
		scanner := bufio.NewScanner(setup)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				section = strings.ToLower(strings.Trim(line, "[] "))
				continue
			}
			if section == "metadata" {
				key, value, found := strings.Cut(line, "=")
				if found && strings.TrimSpace(strings.ToLower(key)) == "name" && strings.TrimSpace(value) != "" {
					return strings.TrimSpace(value), nil
				}
			}
		}
	}
	return "", fmt.Errorf("could not determine the Python project name")
}

func loadVersions(ctx context.Context, catalog VersionCatalog, packageID string) tea.Cmd {
	return func() tea.Msg {
		versions, err := catalog(ctx, packageID)
		sortVersionsNewestFirst(versions)
		return versionsLoadedMsg{Package: packageID, Versions: versions, Err: err}
	}
}

func FetchPyPIVersions(ctx context.Context, packageID string) ([]string, error) {
	endpoint := "https://pypi.org/pypi/" + url.PathEscape(packageID) + "/json"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "reploy")
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("load PyPI versions: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("load PyPI versions: %s", response.Status)
	}
	var payload struct {
		Releases map[string][]json.RawMessage `json:"releases"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode PyPI versions: %w", err)
	}
	versions := make([]string, 0, len(payload.Releases))
	for version, files := range payload.Releases {
		if len(files) != 0 && pythonprovider.ValidatePackageVersionV1(version) == nil {
			versions = append(versions, version)
		}
	}
	sortVersionsNewestFirst(versions)
	return versions, nil
}

func sortVersionsNewestFirst(versions []string) {
	sort.SliceStable(versions, func(left, right int) bool {
		compared, err := pythonprovider.ComparePackageVersionsV1(versions[left], versions[right])
		if err != nil {
			return versions[left] > versions[right]
		}
		return compared > 0
	})
}

func findProjects(root string, query string) tea.Cmd {
	return func() tea.Msg {
		projects, err := matchingProjects(root, query, 100)
		return projectsLoadedMsg{Root: root, Query: query, Projects: projects, Err: err}
	}
}

func matchingProjects(root string, query string, limit int) ([]string, error) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return []string{}, nil
	}
	ignored := map[string]bool{
		".git": true, ".sl": true, ".venv": true, "node_modules": true,
		"__pycache__": true, ".mypy_cache": true, ".pytest_cache": true,
	}
	result := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && ignored[entry.Name()] {
			return filepath.SkipDir
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.Contains(strings.ToLower(filepath.ToSlash(relative)), needle) {
			if _, err := pythonProjectName(path); err == nil {
				result = append(result, path)
				if len(result) >= limit {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	sort.Strings(result)
	return result, err
}

func storedPath(workspace string, selected string) string {
	if workspace == "" {
		return filepath.Clean(selected)
	}
	relative, err := filepath.Rel(workspace, selected)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.Clean(selected)
	}
	if relative == "." {
		return "{{ workspace_root }}"
	}
	return "{{ workspace_root }}/" + filepath.ToSlash(relative)
}

func (m *model) projectSearchRoot() string {
	if m.workspace != "" {
		return m.workspace
	}
	return m.browseRoot
}

func readSidecarSnapshot(deploymentDir string) (fileSnapshot, error) {
	path, err := deploy.PackageOverridesPath(deploymentDir)
	if err != nil {
		return fileSnapshot{}, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("read package overrides snapshot: %w", err)
	}
	sum := sha256.Sum256(content)
	return fileSnapshot{Found: true, Digest: hex.EncodeToString(sum[:])}, nil
}

func saveSidecarAt(
	ctx context.Context,
	deploymentDir string,
	original fileSnapshot,
	overrides deploy.PackageOverridesV1,
) (snapshot fileSnapshot, err error) {
	operation, err := deploy.AcquireOperationLock(ctx, deploymentDir)
	if err != nil {
		return fileSnapshot{}, err
	}
	defer func() {
		err = errors.Join(err, operation.Unlock())
	}()
	current, err := readSidecarSnapshot(deploymentDir)
	if err != nil {
		return fileSnapshot{}, err
	}
	if current != original {
		return fileSnapshot{}, fmt.Errorf("%s changed while the editor was open; reopen the editor and try again", deploy.PackageOverridesFilename)
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return fileSnapshot{}, err
	}
	if !found || state.Staging == nil {
		return fileSnapshot{}, fmt.Errorf("package overrides require a staged deployment")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return fileSnapshot{}, err
	}
	if document.Environment.ID != overrides.Environment.ID {
		return fileSnapshot{}, fmt.Errorf(
			"staged environment changed from %q to %q while the editor was open; reopen the editor and try again",
			overrides.Environment.ID,
			document.Environment.ID,
		)
	}
	if err := operation.CommitPackageOverridesV1(overrides); err != nil {
		return fileSnapshot{}, err
	}
	return readSidecarSnapshot(deploymentDir)
}

func cloneVars(source map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range source {
		result[key] = value
	}
	return result
}

func choiceIndex(choice deploy.PackageOverrideChoiceV1) int {
	if choice.Path != "" {
		return 1
	}
	if choice.Version != "" {
		return 2
	}
	return 0
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func truncate(value string, width int) string {
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

func sanitizeTerminalText(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
}

func clamp(value int, low int, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func max(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
