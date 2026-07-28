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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/pelletier/go-toml/v2"
)

type VersionCatalog func(context.Context, string) ([]string, error)
type ValidationRunner func(context.Context, io.Writer) (ValidationResult, error)

var setupPyNamePattern = regexp.MustCompile(
	`(?m)^[\t ]*name[\t ]*=[\t ]*["']([A-Za-z0-9][A-Za-z0-9_.-]*)["'][\t ]*,?[\t ]*(?:#.*)?$`,
)

type DiscoveredPackage struct {
	Provider string
	Package  string
}

type ValidationResult struct {
	Packages []DiscoveredPackage
	Unused   []DiscoveredPackage
	Build    *BuildOutcome
	Warnings []string
}

type BuildOutcome struct {
	Environment    string
	ImageReference string
	Elapsed        time.Duration
	Reused         bool
	Republished    bool
}

type Config struct {
	Context       context.Context
	DeploymentDir string
	Document      blueprint.Document
	Overlay       deploy.RequestOverlayV1
	Platform      blueprint.Platform
	Input         io.Reader
	Output        io.Writer
	WorkingDir    string
	Versions      VersionCatalog
	Validate      ValidationRunner
	Validated     bool
	Discovered    []DiscoveredPackage
	Unused        []DiscoveredPackage
	AutoValidate  bool
	ExitOnValid   bool
}

type Result struct {
	Validated bool
	Canceled  bool
}

type fileSnapshot struct {
	Found  bool
	Digest string
}

type stagingSnapshot struct {
	BlueprintDigest canonical.Digest
	OverlayDigest   canonical.Digest
	Platform        blueprint.Platform
}

type editorSnapshot struct {
	Sidecar fileSnapshot
	Staging stagingSnapshot
}

type overrideItem struct {
	Provider        string
	Package         string
	Addition        bool
	Explicit        bool
	Discovered      bool
	Sources         []string
	Choice          deploy.PackageOverrideChoiceV1
	Error           string
	Notice          string
	DiscoveryNotice string
}

type screenKind int

const ctrlCExitStatus = "Press Ctrl+C again to exit without saving."
const ctrlCValidationExitStatus = "Press Ctrl+C again to cancel and exit."

const (
	screenMain screenKind = iota
	screenChoose
	screenAdd
	screenWorkspace
	screenPath
	screenVersion
	screenPreview
	screenExit
	screenBase
	screenBaseReference
)

type versionsLoadedMsg struct {
	Package  string
	Versions []string
	Err      error
}

type projectsLoadedMsg struct {
	Root     string
	Projects []string
	Err      error
}

type savedMsg struct {
	Snapshot  editorSnapshot
	Err       error
	ExitAfter bool
}

type validatedMsg struct {
	ID          uint64
	Snapshot    editorSnapshot
	Result      ValidationResult
	Saved       bool
	StartStep   string
	ProgressLog string
	Err         error
	ExitAfter   bool
}

type validationProgressMsg struct {
	ID     uint64
	Step   string
	Closed bool
}

type model struct {
	ctx           context.Context
	deploymentDir string
	document      blueprint.Document
	raw           deploy.PackageOverridesV1
	original      editorSnapshot
	save          func(context.Context, editorSnapshot, deploy.PackageOverridesV1) (editorSnapshot, error)
	versions      VersionCatalog
	validate      ValidationRunner

	screen               screenKind
	width                int
	height               int
	cursor               int
	optionCursor         int
	input                textinput.Model
	items                []overrideItem
	workspace            string
	workspaceResolved    string
	workspaceReturn      screenKind
	workspaceInput       string
	browseRoot           string
	projectCatalog       []string
	projectCatalogRoot   string
	projectCatalogBusy   bool
	results              []string
	resultCursor         int
	baseImage            string
	versionCache         map[string][]string
	versionError         map[string]string
	versionPending       map[string]bool
	status               string
	dirty                bool
	ctrlCArmed           bool
	exitCursor           int
	exitReturn           screenKind
	exitError            string
	validated            bool
	validating           bool
	validationID         uint64
	validationSpinner    spinner.Model
	validationCurrent    string
	validationCompleted  []string
	completedSteps       map[string]struct{}
	loggedSteps          map[string]struct{}
	validationError      string
	validationProgress   <-chan string
	validationVisible    bool
	validationViewport   viewport.Model
	validationLog        string
	validationSavePrompt bool
	validationSaveInput  textinput.Model
	validationSaveError  string
	validationSavedPath  string
	validatedPackages    map[string]struct{}
	unusedOverrides      []DiscoveredPackage
	autoValidate         bool
	exitOnValid          bool
	validationCancel     context.CancelFunc
	canceled             bool
}

var (
	titleStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	goodStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	badStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	focusStyle       = lipgloss.NewStyle().Background(lipgloss.Color("25")).Foreground(lipgloss.Color("255"))
	directStyle      = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	discoveredStyle  = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	scrollTrackStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	scrollThumbStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	panelStyle       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("220")).Padding(1, 2)
)

func Run(config Config) error {
	_, err := RunWithResult(config)
	return err
}

func RunWithResult(config Config) (Result, error) {
	m, err := newModel(config)
	if err != nil {
		return Result{}, err
	}
	options := []tea.ProgramOption{tea.WithContext(m.ctx), tea.WithAltScreen()}
	if config.Input != nil {
		options = append(options, tea.WithInput(config.Input))
	}
	if config.Output != nil {
		options = append(options, tea.WithOutput(config.Output))
	}
	final, err := tea.NewProgram(m, options...).Run()
	if err != nil {
		return Result{}, err
	}
	finalModel, ok := final.(*model)
	if !ok {
		return Result{}, fmt.Errorf("package override editor returned unexpected model %T", final)
	}
	return Result{
		Validated: finalModel.validated && !finalModel.dirty,
		Canceled:  finalModel.canceled,
	}, nil
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
	overlay := config.Overlay
	if overlay.Schema == "" && overlay.SelectedOptions == nil && overlay.DirectPackages == nil {
		overlay = deploy.EmptyRequestOverlayV1()
	}
	if _, err := deploy.RequestOverlayDigestV1(overlay); err != nil {
		return nil, fmt.Errorf("package override editor request overlay: %w", err)
	}
	platform := config.Platform
	if platform.Canonical == "" && len(config.Document.Blueprint.Compatibility.Platforms) == 1 {
		platform = config.Document.Blueprint.Compatibility.Platforms[0]
	}
	expectedStaging, err := stagingSnapshotFor(config.Document, overlay, platform)
	if err != nil {
		return nil, fmt.Errorf("package override editor staging identity: %w", err)
	}
	snapshot, raw, found, err := readEditorOpenState(config.Context, deploymentDir)
	if err != nil {
		return nil, err
	}
	if snapshot.Staging != expectedStaging {
		return nil, fmt.Errorf("staged deployment changed while the editor was opening; reopen the editor and try again")
	}
	if !found {
		raw = deploy.EmptyPackageOverridesV1(config.Document.Environment.ID)
	}
	if raw.Environment.ID != config.Document.Environment.ID {
		return nil, fmt.Errorf("package overrides target environment %q, want %q", raw.Environment.ID, config.Document.Environment.ID)
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
	workspaceResolved := ""
	if value, ok := raw.Environment.Vars["workspace_root"].(string); ok && value != "" {
		workspace = value
		workspaceResolved, err = deploy.ResolvePackageOverrideWorkspaceRootV1(value)
		if err != nil {
			return nil, fmt.Errorf("resolve package override editor workspace root: %w", err)
		}
	}
	items, err := editorItems(config.Document, overlay, raw)
	if err != nil {
		return nil, err
	}
	input := textinput.New()
	input.CharLimit = 512
	input.Width = 72
	validationSpinner := spinner.New()
	validationSpinner.Spinner = spinner.Dot
	validationSpinner.Style = warnStyle
	validationViewport := viewport.New(68, 10)
	validationSaveInput := textinput.New()
	validationSaveInput.CharLimit = 1024
	validationSaveInput.Width = 68
	m := &model{
		ctx: config.Context, deploymentDir: deploymentDir, document: config.Document,
		raw: raw, original: snapshot,
		save: func(ctx context.Context, original editorSnapshot, overrides deploy.PackageOverridesV1) (editorSnapshot, error) {
			return saveSidecarAt(ctx, deploymentDir, original, overrides)
		},
		versions: config.Versions,
		validate: config.Validate, validated: config.Validated,
		screen: screenMain, input: input, items: items, workspace: workspace,
		workspaceResolved: workspaceResolved, browseRoot: filepath.Clean(workingDir),
		versionCache: map[string][]string{}, versionError: map[string]string{},
		versionPending: map[string]bool{}, validatedPackages: map[string]struct{}{},
		validationSpinner: validationSpinner, validationViewport: validationViewport,
		validationSaveInput: validationSaveInput,
		autoValidate:        config.AutoValidate, exitOnValid: config.ExitOnValid,
	}
	if raw.Environment.Base != nil {
		m.baseImage = raw.Environment.Base.Image
	}
	if len(m.items) == 0 {
		m.cursor = -1
	}
	for _, item := range config.Discovered {
		m.validatedPackages[item.Provider+"\x00"+item.Package] = struct{}{}
	}
	m.unusedOverrides = append([]DiscoveredPackage{}, config.Unused...)
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
	for applicationName, application := range document.Environment.Applications {
		if application.Packages.Python == nil {
			continue
		}
		for _, requirement := range application.Packages.Python.Requirements {
			distribution, err := pythonprovider.RequirementDistributionName(requirement)
			if err != nil {
				return nil, fmt.Errorf("identify Python requirement %q: %w", requirement, err)
			}
			addExplicit(distribution, pythonRequirementSource(requirement))
		}
		for _, selected := range overlay.SelectedOptions {
			if selected.Application != applicationName {
				continue
			}
			option := application.Options[selected.Option]
			if option.Packages.Python == nil {
				continue
			}
			for _, requirement := range option.Packages.Python.Requirements {
				distribution, err := pythonprovider.RequirementDistributionName(requirement)
				if err != nil {
					return nil, fmt.Errorf("identify Python option requirement %q: %w", requirement, err)
				}
				addExplicit(distribution, pythonRequirementSource(requirement))
			}
		}
	}
	for _, direct := range overlay.DirectPackages {
		if direct.Package.Schema != pythonprovider.PackageRequestSchemaV1 {
			continue
		}
		requirement, ok := direct.Package.Value["requirement"].(string)
		if !ok {
			return nil, fmt.Errorf("identify direct Python requirement for component %q: requirement must be a string", direct.Contribution)
		}
		distribution, err := pythonprovider.RequirementDistributionName(requirement)
		if err != nil {
			return nil, fmt.Errorf("identify direct Python requirement %q: %w", requirement, err)
		}
		addExplicit(distribution, pythonRequirementSource(requirement))
	}
	for provider, requirements := range raw.Environment.PackageAdditions {
		for _, requirement := range requirements {
			key := provider + "\x00" + requirement
			byKey[key] = overrideItem{
				Provider: provider, Package: requirement, Addition: true, Explicit: true,
			}
		}
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
	sortOverrideItems(items)
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
	if m.autoValidate {
		_, command := m.startValidation(m.exitOnValid)
		if command != nil {
			commands = append(commands, command)
		}
		m.autoValidate = false
	}
	return tea.Batch(commands...)
}

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(24, min(72, msg.Width-12))
		m.validationSaveInput.Width = max(24, min(90, msg.Width-12))
		m.syncValidationViewport()
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
		if m.screen == screenPath && msg.Root == m.projectSearchRoot() {
			m.projectCatalog = msg.Projects
			m.projectCatalogRoot = msg.Root
			m.projectCatalogBusy = false
			m.filterProjectResults()
			if msg.Err != nil {
				m.status = msg.Err.Error()
			}
		}
		return m, nil
	case savedMsg:
		if msg.Err != nil {
			if msg.ExitAfter {
				m.exitError = "Save failed: " + msg.Err.Error()
			} else {
				m.status = "Save failed: " + msg.Err.Error()
			}
		} else {
			m.original = msg.Snapshot
			m.raw = m.buildRaw()
			m.dirty = false
			m.status = "Saved " + deploy.PackageOverridesFilename
			if msg.ExitAfter {
				return m, tea.Quit
			}
		}
		return m, nil
	case validationProgressMsg:
		if msg.ID != m.validationID || !m.validating {
			return m, nil
		}
		if msg.Closed {
			return m, nil
		}
		m.recordValidationStep(msg.Step)
		return m, waitForValidationProgress(msg.ID, m.validationProgress)
	case spinner.TickMsg:
		if !m.validating {
			return m, nil
		}
		var command tea.Cmd
		m.validationSpinner, command = m.validationSpinner.Update(msg)
		return m, command
	case validatedMsg:
		if msg.ID != m.validationID {
			return m, nil
		}
		m.validating = false
		m.validationCancel = nil
		m.ctrlCArmed = false
		if m.canceled {
			return m, tea.Quit
		}
		m.validationLog = msg.StartStep + "\n" + msg.ProgressLog
		if msg.Saved {
			m.original = msg.Snapshot
			m.raw = m.buildRaw()
			m.dirty = false
		}
		if msg.Err != nil {
			m.invalidateValidation()
			m.validationError = msg.Err.Error()
			m.appendValidationLog("ERROR\n" + msg.Err.Error())
			m.status = ""
		} else {
			m.validated = true
			m.validatedPackages = map[string]struct{}{}
			for _, item := range msg.Result.Packages {
				m.validatedPackages[item.Provider+"\x00"+item.Package] = struct{}{}
			}
			m.unusedOverrides = append([]DiscoveredPackage{}, msg.Result.Unused...)
			m.validateItems()
			m.status = "Validated choices. The cached trial build is ready for reploy build."
			m.validationError = ""
			m.appendValidationLog("Validation succeeded.")
			if msg.ExitAfter {
				return m, tea.Quit
			}
		}
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *model) View() string {
	if m.validationVisible {
		return m.viewValidationOverlay()
	}
	content := ""
	switch m.screen {
	case screenMain:
		content = m.viewMain()
	case screenChoose:
		content = m.viewChoose()
	case screenAdd:
		content = m.viewInputDialog(
			"Add package",
			"Python package name creates a source override · os:PACKAGE adds a native OS package · Enter applies · Esc cancels",
		)
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
			content = panelStyle.Render(badStyle.Render(sanitizeTerminalBlock(err.Error())))
		} else {
			content = panelStyle.Render(
				titleStyle.Render(deploy.PackageOverridesFilename) +
					"\n\n" + sanitizeTerminalBlock(string(encoded)) + "\nEsc returns",
			)
		}
	case screenExit:
		content = m.viewExit()
	case screenBase:
		content = m.viewBaseChoice()
	case screenBaseReference:
		content = m.viewInputDialog(
			"Exact base image name",
			"Enter applies · for example ubuntu:24.04 or python:3.13-slim · Esc cancels",
		)
	}
	if m.status != "" {
		content += "\n" + mutedStyle.Render(sanitizeTerminalText(m.status))
	}
	return content
}

func (m *model) viewValidationOverlay() string {
	if m.validationSavePrompt {
		return m.viewValidationSavePrompt()
	}
	var body strings.Builder
	title := "Validating package choices"
	inProgress := "Validation in progress"
	failed := "Validation failed"
	succeeded := "Validation succeeded"
	logTitle := "Validation log"
	body.WriteString(titleStyle.Render(title))
	body.WriteString("\n\n")
	switch {
	case m.validating:
		body.WriteString(m.validationSpinner.View() + " " + inProgress)
		body.WriteString("\n" + wrapWords(m.validationCurrent, m.validationViewport.Width))
		if len(m.validationCompleted) != 0 {
			completed := fmt.Sprintf("%d steps complete", len(m.validationCompleted))
			if len(m.validationCompleted) == 1 {
				completed = "1 step complete"
			}
			body.WriteString("\n" + mutedStyle.Render(completed))
		}
	case m.validationError != "":
		body.WriteString(badStyle.Render("× " + failed))
	default:
		body.WriteString(goodStyle.Render("✓ " + succeeded))
	}
	body.WriteString("\n\n")
	body.WriteString(mutedStyle.Render(logTitle))
	body.WriteString("\n")
	body.WriteString(m.renderValidationLogPanel())
	body.WriteString("\n")
	footer := "↑/↓ scroll · PgUp/PgDn"
	if m.validating {
		if m.ctrlCArmed {
			footer += " · " + ctrlCValidationExitStatus
		} else {
			footer += " · Ctrl+C twice exits"
		}
	} else {
		footer += " · S save log · Enter or Esc closes"
		if m.ctrlCArmed {
			footer += " · Press Ctrl+C again to exit."
		}
	}
	body.WriteString(mutedStyle.Render(footer))
	if m.validationSavedPath != "" {
		body.WriteString("\n" + goodStyle.Render("Saved log to "+sanitizeTerminalText(m.validationSavedPath)))
	}
	dialog := panelStyle.Width(m.panelWidth()).Render(body.String())
	if m.width <= 0 || m.height <= 0 {
		return dialog
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *model) renderValidationLogPanel() string {
	contentLines := strings.Split(m.validationViewport.View(), "\n")
	scrollLines := strings.Split(m.validationScrollbar(), "\n")
	lines := make([]string, 0, m.validationViewport.Height+2)
	fixedWidth := lipgloss.NewStyle().
		Width(m.validationViewport.Width).
		MaxWidth(m.validationViewport.Width)
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	lines = append(lines, borderStyle.Render("┌"+strings.Repeat("─", m.validationViewport.Width+2)+"┐"))
	for index := range m.validationViewport.Height {
		content := ""
		if index < len(contentLines) {
			content = contentLines[index]
		}
		scrollbar := ""
		if index < len(scrollLines) {
			scrollbar = scrollLines[index]
		}
		lines = append(
			lines,
			borderStyle.Render("│")+" "+fixedWidth.Render(content)+" "+scrollbar,
		)
	}
	lines = append(lines, borderStyle.Render("└"+strings.Repeat("─", m.validationViewport.Width+2)+"┘"))
	return strings.Join(lines, "\n")
}

func (m *model) validationScrollbar() string {
	height := m.validationViewport.Height
	if height <= 0 {
		return ""
	}
	content := wrapValidationLog(m.validationLog, m.validationViewport.Width)
	lineCount := 0
	if content != "" {
		lineCount = strings.Count(content, "\n") + 1
	}
	thumbHeight := height
	if lineCount > height {
		thumbHeight = max(1, height*height/lineCount)
	}
	thumbStart := 0
	if available := height - thumbHeight; available > 0 {
		thumbStart = int(m.validationViewport.ScrollPercent()*float64(available) + 0.5)
	}
	lines := make([]string, height)
	for index := range lines {
		if index >= thumbStart && index < thumbStart+thumbHeight {
			lines[index] = scrollThumbStyle.Render("█")
		} else {
			lines[index] = scrollTrackStyle.Render("│")
		}
	}
	return strings.Join(lines, "\n")
}

func (m *model) viewValidationSavePrompt() string {
	var body strings.Builder
	body.WriteString(titleStyle.Render("Save validation log"))
	body.WriteString("\n\n")
	body.WriteString(m.validationSaveInput.View())
	body.WriteString("\n\n")
	body.WriteString(mutedStyle.Render(
		"Relative paths use " + sanitizeTerminalText(m.browseRoot) + ". Existing files are not overwritten.",
	))
	if m.validationSaveError != "" {
		body.WriteString("\n\n" + badStyle.Render(sanitizeTerminalText(m.validationSaveError)))
	}
	body.WriteString("\n\nEnter saves · Esc cancels")
	dialog := panelStyle.Width(m.dialogWidth()).Render(body.String())
	if m.width <= 0 || m.height <= 0 {
		return dialog
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m *model) appendValidationLog(value string) {
	value = sanitizeTerminalBlock(value)
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return
	}
	if m.validationLog != "" && !strings.HasSuffix(m.validationLog, "\n") {
		m.validationLog += "\n"
	}
	m.validationLog += value + "\n"
	m.syncValidationViewport()
}

func (m *model) syncValidationViewport() {
	atBottom := m.validationViewport.AtBottom()
	m.validationViewport.Width = max(20, m.panelWidth()-12)
	if m.height <= 0 {
		m.validationViewport.Height = 10
	} else {
		m.validationViewport.Height = max(5, m.height-16)
	}
	m.validationViewport.SetContent(wrapValidationLog(m.validationLog, m.validationViewport.Width))
	if atBottom {
		m.validationViewport.GotoBottom()
	}
}

func wrapValidationLog(value string, width int) string {
	value = strings.TrimSuffix(value, "\n")
	if value == "" || width <= 0 {
		return value
	}
	value = strings.ReplaceAll(value, "\t", "    ")
	value = strings.ReplaceAll(value, "\r", "")
	var wrapped []string
	for _, line := range strings.Split(value, "\n") {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		var current strings.Builder
		currentWidth := 0
		for _, character := range line {
			characterWidth := lipgloss.Width(string(character))
			if currentWidth != 0 && currentWidth+characterWidth > width {
				wrapped = append(wrapped, current.String())
				current.Reset()
				currentWidth = 0
			}
			current.WriteRune(character)
			currentWidth += characterWidth
		}
		wrapped = append(wrapped, current.String())
	}
	return strings.Join(wrapped, "\n")
}

func resolveValidationLogPath(base string, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("enter a log file path")
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve current user's home directory: %w", err)
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	return filepath.Clean(value), nil
}

func writeNewValidationLog(path string, content string) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("log file already exists: %s", path)
	}
	if err != nil {
		return fmt.Errorf("create validation log %s: %w", path, err)
	}
	complete := false
	defer func() {
		closeErr := file.Close()
		if !complete {
			_ = os.Remove(path)
		}
		err = errors.Join(err, closeErr)
	}()
	if _, err := io.WriteString(file, content); err != nil {
		return fmt.Errorf("write validation log %s: %w", path, err)
	}
	complete = true
	return nil
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
	body.WriteString("\n")
	switch {
	case m.validating:
		body.WriteString(warnStyle.Render("Validating choices with a trial build…"))
	case m.validated && !m.dirty:
		body.WriteString(goodStyle.Render("Validated"))
	default:
		body.WriteString(mutedStyle.Render("Not validated"))
	}
	for _, item := range m.unusedOverrides {
		body.WriteString("\n")
		body.WriteString(warnStyle.Render("Unused override: " + item.Provider + " / " + item.Package))
	}
	body.WriteString("\n\n")
	compact := m.width > 0 && m.width < 90
	const providerWidth = 10
	const packageWidth = 28
	sourceWidth := max(34, contentWidth-(2+providerWidth+1+packageWidth+1+1+12))
	if !compact {
		body.WriteString(fmt.Sprintf(
			"  %-*s %-*s %-*s %s\n",
			providerWidth, "Provider",
			packageWidth, "Package",
			sourceWidth, "Source",
			"Status",
		))
	}
	baseSource := "blueprint · " + m.blueprintBaseImage()
	if m.baseImage != "" {
		baseSource = "override · " + m.baseImage
	}
	baseStatus := "—"
	if m.validated && !m.dirty {
		baseStatus = goodStyle.Render("valid")
	}
	baseLine := ""
	if compact {
		baseLine = fmt.Sprintf("  base / Base image [explicit]\n  %s · %s",
			truncate(sanitizeTerminalText(baseSource), max(8, contentWidth/2)),
			truncate(baseStatus, max(6, contentWidth/2-3)),
		)
	} else {
		baseLine = fmt.Sprintf("  %-*s %-*s %-*s %s",
			providerWidth, "base",
			packageWidth, "Base image",
			sourceWidth, truncate(sanitizeTerminalText(baseSource), sourceWidth),
			baseStatus,
		)
	}
	if m.cursor == -1 {
		baseLine = focusStyle.Render(baseLine)
	} else {
		baseLine = directStyle.Render(baseLine)
	}
	body.WriteString(baseLine)
	body.WriteByte('\n')
	if len(m.items) == 0 {
		body.WriteString(mutedStyle.Render("  No package roots or overrides. Press A to add an override."))
		body.WriteString("\n")
	}
	for index, item := range m.items {
		source := "not selected"
		if item.Addition {
			source = "native OS package"
		} else if len(item.Sources) != 0 {
			source = strings.Join(item.Sources, ", ")
		} else if item.Provider == "python" && (item.Explicit || item.Discovered) {
			source = "PyPI"
		} else if item.Discovered {
			source = item.Provider + " provider"
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
		} else if m.validated && !m.dirty {
			if m.isUnusedOverride(item) {
				status = warnStyle.Render("unused")
			} else {
				status = goodStyle.Render("valid")
			}
		} else if item.Notice != "" {
			status = warnStyle.Render(sanitizeTerminalText(item.Notice))
		} else if item.Addition {
			status = mutedStyle.Render("requested")
		} else if item.Choice.Path != "" {
			status = mutedStyle.Render("source found")
		} else if item.Choice.Version != "" {
			status = mutedStyle.Render("available")
		}
		line := ""
		if compact {
			classification := "override-only"
			if item.Explicit {
				classification = "explicit"
			} else if item.Discovered {
				classification = "direct"
			}
			packageWidth := max(8, contentWidth-lipgloss.Width(item.Provider)-lipgloss.Width(classification)-8)
			line = fmt.Sprintf("  %s / %s [%s]\n  %s · %s",
				item.Provider,
				truncate(item.Package, packageWidth),
				classification,
				truncate(source, max(8, contentWidth/2)),
				truncate(status, max(6, contentWidth/2-3)),
			)
		} else {
			line = fmt.Sprintf(
				"  %-*s %-*s %-*s %s",
				providerWidth, item.Provider,
				packageWidth, truncate(item.Package, packageWidth),
				sourceWidth, truncate(source, sourceWidth),
				status,
			)
		}
		if index == m.cursor {
			line = focusStyle.Render(line)
		} else if item.Explicit {
			line = directStyle.Render(line)
		} else if item.Discovered {
			line = discoveredStyle.Render(line)
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	body.WriteString("\n")
	body.WriteString(mutedStyle.Render("Dark rows are explicit choices; lighter package rows are direct dependencies."))
	body.WriteString("\n")
	body.WriteString("↑/↓ select · Enter edit · A add package · D reset · V validate · W workspace · P preview · Ctrl+S save · Q exit")
	if m.dirty {
		body.WriteString("\n" + goodStyle.Render("Unsaved changes"))
	}
	return panelStyle.Width(m.panelWidth()).Render(body.String())
}

func (m *model) isUnusedOverride(item overrideItem) bool {
	if item.Choice == (deploy.PackageOverrideChoiceV1{}) {
		return false
	}
	for _, unused := range m.unusedOverrides {
		if unused.Provider == item.Provider && unused.Package == item.Package {
			return true
		}
	}
	return false
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

func (m *model) viewBaseChoice() string {
	options := []string{"From blueprint", "Exact image name"}
	var body strings.Builder
	body.WriteString(titleStyle.Render("Base image"))
	body.WriteString("\n\n")
	body.WriteString(mutedStyle.Render("Blueprint: " + sanitizeTerminalText(m.blueprintBaseImage())))
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

func (m *model) viewExit() string {
	options := []string{
		"Validate and exit",
		"Save without validation and exit",
		"Back to editor",
	}
	if !m.dirty {
		options[1] = "Exit without validation"
	}
	var body strings.Builder
	body.WriteString(titleStyle.Render("Exit development overrides"))
	body.WriteString("\n\n")
	body.WriteString("Current choices have not been validated.")
	body.WriteString("\n\n")
	for index, option := range options {
		line := "  " + option
		if index == m.exitCursor {
			line = focusStyle.Render(line)
		}
		body.WriteString(line + "\n")
	}
	if m.exitError != "" {
		body.WriteString("\n" + badStyle.Render(sanitizeTerminalText(m.exitError)))
	}
	body.WriteString("\n" + mutedStyle.Render("↑/↓ choose · Enter confirms · Esc returns"))
	return panelStyle.Width(m.dialogWidth()).Render(body.String())
}

func (m *model) viewPath() string {
	workspace := "not set; selected paths are stored as absolute paths"
	if m.workspace != "" {
		workspace = sanitizeTerminalText(m.workspace) + "; paths inside it use {{ workspace_root }}"
	}
	return m.viewResults(
		"Find a local Python project for "+m.current().Package,
		"Workspace root: "+workspace+"\nSearch root: "+sanitizeTerminalText(m.projectSearchRoot())+". Enter a relative path inside it or an absolute directory outside it.",
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
		empty := "  No matches"
		if m.screen == screenPath && m.projectCatalogBusy {
			empty = "  Finding projects…"
		}
		body.WriteString(mutedStyle.Render(empty))
		body.WriteByte('\n')
	} else {
		limit := min(len(m.results), max(4, m.height-14))
		start := clamp(m.resultCursor-limit/2, 0, max(0, len(m.results)-limit))
		for index := start; index < start+limit; index++ {
			label := m.results[index]
			if m.screen == screenPath && m.workspaceResolved != "" {
				if relative, err := filepath.Rel(m.workspaceResolved, label); err == nil &&
					relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					label = filepath.ToSlash(relative)
				}
			}
			label = sanitizeTerminalText(label)
			line := "  " + truncate(label, max(12, m.panelWidth()-8))
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
	return max(28, min(132, m.width-2))
}

func (m *model) dialogWidth() int {
	if m.width <= 0 {
		return 72
	}
	return max(28, min(78, m.width-2))
}

func (m *model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.validationVisible {
		return m.updateValidationOverlay(key)
	}
	if key.String() == "ctrl+c" {
		if m.ctrlCArmed {
			if m.validating {
				m.canceled = true
				if m.validationCancel != nil {
					m.validationCancel()
				}
				m.validationCurrent = "Canceling validation"
				return m, nil
			}
			return m, tea.Quit
		}
		m.ctrlCArmed = true
		m.status = ctrlCExitStatus
		return m, nil
	}
	if m.ctrlCArmed {
		m.ctrlCArmed = false
		if m.status == ctrlCExitStatus {
			m.status = ""
		}
	}
	if key.String() == "ctrl+s" {
		return m.startSave(false)
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
		if key.String() == "esc" || key.String() == "enter" || key.String() == "p" || key.String() == "P" {
			m.screen = screenMain
		}
	case screenExit:
		return m.updateExit(key)
	case screenBase:
		return m.updateBaseChoice(key)
	case screenBaseReference:
		return m.updateBaseReference(key)
	}
	return m, nil
}

func (m *model) requestExit() (tea.Model, tea.Cmd) {
	if m.validated && !m.dirty {
		return m, tea.Quit
	}
	if m.screen == screenExit {
		return m, nil
	}
	m.exitReturn = m.screen
	m.exitCursor = 0
	m.exitError = ""
	m.screen = screenExit
	m.input.Blur()
	return m, nil
}

func (m *model) updateExit(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.screen = m.exitReturn
		m.exitError = ""
		return m, nil
	case "up":
		m.exitCursor = clamp(m.exitCursor-1, 0, 2)
	case "down":
		m.exitCursor = clamp(m.exitCursor+1, 0, 2)
	case "enter":
		switch m.exitCursor {
		case 0:
			m.screen = m.exitReturn
			return m.startValidation(true)
		case 1:
			if !m.dirty {
				return m, tea.Quit
			}
			return m.startSave(true)
		case 2:
			m.screen = m.exitReturn
			m.exitError = ""
		}
	}
	return m, nil
}

func (m *model) startSave(exitAfter bool) (tea.Model, tea.Cmd) {
	raw := m.buildRaw()
	if err := deploy.ValidatePackageOverridesV1(raw); err != nil {
		m.reportSaveError(exitAfter, "Cannot save: "+err.Error())
		return m, nil
	}
	m.validateItems()
	for _, item := range m.items {
		if item.Error != "" {
			m.reportSaveError(exitAfter, "Cannot save while an override is invalid.")
			return m, nil
		}
	}
	return m, func() tea.Msg {
		snapshot, err := m.save(m.ctx, m.original, raw)
		return savedMsg{Snapshot: snapshot, Err: err, ExitAfter: exitAfter}
	}
}

func (m *model) reportSaveError(exitAfter bool, message string) {
	if exitAfter {
		m.screen = screenExit
		m.exitError = message
		return
	}
	m.status = message
}

func (m *model) updateValidationOverlay(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.validationSavePrompt {
		switch key.String() {
		case "esc":
			m.validationSavePrompt = false
			m.validationSaveError = ""
			m.validationSaveInput.Blur()
			return m, nil
		case "enter":
			path, err := resolveValidationLogPath(m.browseRoot, m.validationSaveInput.Value())
			if err == nil {
				err = writeNewValidationLog(path, m.validationLog)
			}
			if err != nil {
				m.validationSaveError = err.Error()
				return m, nil
			}
			m.validationSavePrompt = false
			m.validationSaveError = ""
			m.validationSavedPath = path
			m.validationSaveInput.Blur()
			return m, nil
		}
		var command tea.Cmd
		m.validationSaveInput, command = m.validationSaveInput.Update(key)
		return m, command
	}

	if key.String() == "ctrl+c" {
		if m.ctrlCArmed {
			if m.validating {
				m.canceled = true
				if m.validationCancel != nil {
					m.validationCancel()
				}
				m.validationCurrent = "Canceling validation"
				return m, nil
			}
			return m, tea.Quit
		}
		m.ctrlCArmed = true
		return m, nil
	}
	if m.ctrlCArmed {
		m.ctrlCArmed = false
	}
	if !m.validating {
		switch key.String() {
		case "enter", "esc":
			m.validationVisible = false
			m.validationCurrent = ""
			m.validationCompleted = nil
			m.validationError = ""
			m.validationSavedPath = ""
			return m, nil
		case "s", "S":
			m.validationSavePrompt = true
			m.validationSaveError = ""
			m.validationSaveInput.SetValue("reploy-validation.log")
			m.validationSaveInput.CursorEnd()
			return m, m.validationSaveInput.Focus()
		}
	}

	var command tea.Cmd
	m.validationViewport, command = m.validationViewport.Update(key)
	return m, command
}

func (m *model) updateMain(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "up":
		m.cursor = clamp(m.cursor-1, -1, max(-1, len(m.items)-1))
	case "down":
		m.cursor = clamp(m.cursor+1, -1, max(-1, len(m.items)-1))
	case "enter", "e", "E":
		if m.cursor == -1 {
			m.screen = screenBase
			m.optionCursor = baseChoiceIndex(m.baseImage)
		} else if len(m.items) != 0 {
			if m.current().Addition {
				m.status = "OS package additions use their exact native package name; press D to remove."
				return m, nil
			}
			m.screen = screenChoose
			m.optionCursor = choiceIndex(m.current().Choice)
		}
	case "a", "A":
		m.screen = screenAdd
		m.input.SetValue("")
		m.input.Placeholder = "Python package, or os:default-jre-headless"
		m.input.Focus()
	case "d", "D", "backspace", "delete":
		if m.cursor == -1 {
			m.setBaseImage("")
		} else if len(m.items) != 0 {
			m.resetCurrentOverride()
		}
	case "w", "W":
		m.openWorkspace(screenMain)
	case "p", "P":
		m.screen = screenPreview
	case "v", "V":
		return m.startValidation(m.exitOnValid)
	case "q", "Q":
		return m.requestExit()
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
			m.input.SetValue(m.editablePath(m.current().Choice.Path))
			m.input.CursorEnd()
			m.input.Placeholder = "Filter projects, or enter a directory"
			m.input.Focus()
			return m, m.refreshPathResults()
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

func (m *model) updateBaseChoice(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.screen = screenMain
	case "up", "left":
		m.optionCursor = clamp(m.optionCursor-1, 0, 1)
	case "down", "right":
		m.optionCursor = clamp(m.optionCursor+1, 0, 1)
	case "enter":
		switch m.optionCursor {
		case 0:
			m.setBaseImage("")
			m.screen = screenMain
		case 1:
			m.screen = screenBaseReference
			m.input.SetValue(m.baseImage)
			m.input.CursorEnd()
			m.input.Placeholder = "python:3.13-slim"
			m.input.Focus()
		}
	}
	return m, nil
}

func (m *model) updateBaseReference(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.input.Blur()
		m.screen = screenBase
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.input.Value())
		if err := deploy.ValidateBaseImageReferenceV1(value); err != nil {
			m.status = "Invalid base image reference: " + err.Error()
			return m, nil
		}
		m.setBaseImage(value)
		m.input.Blur()
		m.screen = screenMain
		return m, nil
	}
	var command tea.Cmd
	m.input, command = m.input.Update(key)
	return m, command
}

func (m *model) setBaseImage(image string) {
	if m.baseImage == image {
		return
	}
	m.baseImage = image
	m.dirty = true
	m.invalidateValidation()
}

func (m *model) blueprintBaseImage() string {
	if m.document.Environment.Base.Image == "" {
		return "not declared"
	}
	return m.document.Environment.Base.Image
}

func (m *model) updateAdd(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.input.Blur()
		m.screen = screenMain
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.input.Value())
		if provider, requirement, found := strings.Cut(value, ":"); found {
			normalized, err := deploy.NormalizePackageAdditionV1(provider, requirement)
			if err != nil {
				m.status = err.Error()
				return m, nil
			}
			for index := range m.items {
				if m.items[index].Addition &&
					m.items[index].Provider == provider &&
					m.items[index].Package == normalized {
					m.cursor = index
					m.input.Blur()
					m.screen = screenMain
					return m, nil
				}
			}
			m.items = append(m.items, overrideItem{
				Provider: provider, Package: normalized, Addition: true, Explicit: true,
			})
			sortOverrideItems(m.items)
			for index := range m.items {
				if m.items[index].Addition &&
					m.items[index].Provider == provider &&
					m.items[index].Package == normalized {
					m.cursor = index
					break
				}
			}
			m.dirty = true
			m.invalidateValidation()
			m.validateItems()
			m.input.Blur()
			m.screen = screenMain
			return m, nil
		}
		packageID := pythonprovider.NormalizeDistributionName(value)
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
		sortOverrideItems(m.items)
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
			m.workspaceResolved = ""
			m.input.Blur()
			m.restoreWorkspaceReturn()
			m.dirty = true
			m.invalidateValidation()
			m.validateItems()
			return m, m.refreshPathResults()
		}
		value, err := deploy.ResolvePackageOverrideWorkspaceRootV1(rawValue)
		if err != nil {
			m.status = "Workspace must be an existing directory given as an absolute path or ~/path."
			return m, nil
		}
		info, err := os.Stat(value)
		if err != nil || !info.IsDir() {
			m.status = "Workspace must be an existing directory given as an absolute path or ~/path."
			return m, nil
		}
		m.workspace = rawValue
		m.workspaceResolved = value
		m.input.Blur()
		m.restoreWorkspaceReturn()
		m.dirty = true
		m.invalidateValidation()
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
			} else if candidate, ok := relativeProjectCandidate(m.projectSearchRoot(), value); ok {
				if info, err := os.Stat(candidate); err == nil && info.IsDir() {
					selected = candidate
				}
			}
		}
		if selected == "" {
			m.status = "Choose a project directory, enter a path relative to the workspace, or enter an absolute path."
			return m, nil
		}
		m.items[m.cursor].Choice = deploy.PackageOverrideChoiceV1{Path: storedPath(m.workspaceResolved, selected)}
		m.input.Blur()
		m.screen = screenMain
		m.dirty = true
		m.invalidateValidation()
		m.validateItems()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	m.filterProjectResults()
	return m, cmd
}

func (m *model) filterProjectResults() {
	query := strings.TrimSpace(m.input.Value())
	m.resultCursor = 0
	if filepath.IsAbs(query) {
		value := filepath.Clean(query)
		if info, err := os.Stat(value); err == nil && info.IsDir() {
			m.results = []string{value}
		} else {
			m.results = []string{}
		}
		return
	}
	if candidate, ok := relativeProjectCandidate(m.projectSearchRoot(), query); ok {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			if _, err := pythonProjectName(candidate); err == nil {
				m.results = []string{candidate}
				return
			}
			if projects, err := matchingProjects(candidate); err == nil && len(projects) != 0 {
				m.results = projects
				return
			}
		}
	}
	if m.projectCatalogRoot != m.projectSearchRoot() {
		m.results = []string{}
		return
	}
	needle := strings.ToLower(query)
	m.results = make([]string, 0, len(m.projectCatalog))
	for _, project := range m.projectCatalog {
		relative, err := filepath.Rel(m.projectSearchRoot(), project)
		if err == nil && strings.Contains(strings.ToLower(filepath.ToSlash(relative)), needle) {
			m.results = append(m.results, project)
		}
	}
}

func (m *model) openWorkspace(returnTo screenKind) {
	m.workspaceReturn = returnTo
	m.workspaceInput = m.input.Value()
	m.screen = screenWorkspace
	m.input.SetValue(m.workspace)
	m.input.CursorEnd()
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
		m.input.Placeholder = "Filter projects, or enter a directory"
		m.input.Focus()
	}
	m.workspaceReturn = screenMain
	m.workspaceInput = ""
}

func (m *model) refreshPathResults() tea.Cmd {
	if m.screen != screenPath {
		return nil
	}
	root := m.projectSearchRoot()
	m.projectCatalog = nil
	m.projectCatalogRoot = root
	m.projectCatalogBusy = true
	m.results = []string{}
	m.resultCursor = 0
	return findProjects(root)
}

func (m *model) editablePath(path string) string {
	const workspaceVariable = "{{ workspace_root }}"
	if m.workspaceResolved == "" {
		return path
	}
	if path == workspaceVariable {
		return "."
	}
	if strings.HasPrefix(path, workspaceVariable+"/") {
		return strings.TrimPrefix(path, workspaceVariable+"/")
	}
	return path
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
		m.invalidateValidation()
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
	if len(m.items) == 0 || m.cursor < 0 {
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
	if item.Addition {
		m.items = append(m.items[:m.cursor], m.items[m.cursor+1:]...)
		m.cursor = clamp(m.cursor, 0, max(0, len(m.items)-1))
	} else if item.Explicit {
		m.items[m.cursor].Choice = deploy.PackageOverrideChoiceV1{}
	} else {
		m.items = append(m.items[:m.cursor], m.items[m.cursor+1:]...)
		m.cursor = clamp(m.cursor, 0, max(0, len(m.items)-1))
	}
	delete(m.versionPending, item.Package)
	delete(m.versionError, item.Package)
	m.dirty = true
	m.invalidateValidation()
	m.validateItems()
}

func (m *model) buildRaw() deploy.PackageOverridesV1 {
	raw := m.raw
	raw.Environment.ID = m.document.Environment.ID
	raw.Environment.Vars = cloneVars(raw.Environment.Vars)
	raw.Environment.Base = nil
	if m.baseImage != "" {
		raw.Environment.Base = &deploy.BaseImageOverrideV1{Image: m.baseImage}
	}
	if m.workspace == "" {
		delete(raw.Environment.Vars, "workspace_root")
	} else {
		raw.Environment.Vars["workspace_root"] = m.workspace
	}
	raw.Environment.PackageAdditions = map[string][]string{}
	raw.Environment.PackageOverrides = map[string]map[string]deploy.PackageOverrideChoiceV1{}
	for _, item := range m.items {
		if item.Addition {
			raw.Environment.PackageAdditions[item.Provider] = append(
				raw.Environment.PackageAdditions[item.Provider], item.Package,
			)
			continue
		}
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

func (m *model) invalidateValidation() {
	m.validated = false
	m.validatedPackages = map[string]struct{}{}
	m.unusedOverrides = nil
}

func (m *model) validateItems() {
	m.refreshDiscoveredItems()
	raw := m.buildRaw()
	for index := range m.items {
		m.items[index].Error = ""
		m.items[index].Notice = ""
		if m.items[index].Addition {
			if _, err := deploy.NormalizePackageAdditionV1(m.items[index].Provider, m.items[index].Package); err != nil {
				m.items[index].Error = err.Error()
			} else if !m.validated || m.dirty {
				m.items[index].Notice = "requested"
			}
			continue
		}
		choice := m.items[index].Choice
		if choice.Path == "" && choice.Version == "" {
			m.items[index].Notice = m.items[index].DiscoveryNotice
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
			if m.items[index].Error == "" {
				m.items[index].Notice = m.items[index].DiscoveryNotice
			}
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
		if m.items[index].Notice == "" {
			m.items[index].Notice = m.items[index].DiscoveryNotice
		}
	}
}

func (m *model) refreshDiscoveredItems() {
	discovered := map[string]struct{}{}
	discoveredSources := map[string][]string{}
	for key := range m.validatedPackages {
		discovered[key] = struct{}{}
	}
	notices := map[string]string{}
	raw := m.buildRaw()
	for _, item := range m.items {
		if !item.Explicit || item.Provider != "python" || item.Choice.Path == "" {
			continue
		}
		itemRaw := raw
		itemRaw.Environment.PackageOverrides = map[string]map[string]deploy.PackageOverrideChoiceV1{
			item.Provider: {item.Package: item.Choice},
		}
		resolved, err := deploy.ResolvePackageOverridesV1(itemRaw, m.deploymentDir, normalizeEditorPackage)
		if err != nil {
			continue
		}
		path := resolved.Providers[item.Provider][item.Package].Path
		dependencies, available, err := pythonProjectDirectDependencies(path)
		switch {
		case err != nil:
			notices[item.Provider+"\x00"+item.Package] = "direct dependencies unavailable: " + err.Error()
		case !available:
			notices[item.Provider+"\x00"+item.Package] = "direct dependencies require a trial build"
		default:
			for _, requirement := range dependencies {
				distribution, err := pythonprovider.RequirementDistributionName(requirement)
				if err != nil {
					continue
				}
				key := "python\x00" + distribution
				discovered[key] = struct{}{}
				source := pythonRequirementSource(requirement)
				if !contains(discoveredSources[key], source) {
					discoveredSources[key] = append(discoveredSources[key], source)
					sort.Strings(discoveredSources[key])
				}
			}
		}
	}
	items := make([]overrideItem, 0, len(m.items)+len(discovered))
	seen := map[string]struct{}{}
	for _, item := range m.items {
		key := item.Provider + "\x00" + item.Package
		item.DiscoveryNotice = notices[key]
		_, isDiscovered := discovered[key]
		if item.Explicit {
			item.Discovered = false
		} else if isDiscovered {
			item.Discovered = true
			if sources := discoveredSources[key]; len(sources) != 0 {
				item.Sources = append([]string{}, sources...)
			}
		} else if item.Discovered {
			if item.Choice == (deploy.PackageOverrideChoiceV1{}) {
				continue
			}
			item.Discovered = false
		}
		items = append(items, item)
		seen[key] = struct{}{}
	}
	for key := range discovered {
		if _, found := seen[key]; found {
			continue
		}
		provider, packageID, _ := strings.Cut(key, "\x00")
		items = append(items, overrideItem{
			Provider: provider, Package: packageID, Discovered: true,
			Sources: append([]string{}, discoveredSources[key]...),
		})
	}
	sortOverrideItems(items)
	m.items = items
	m.cursor = clamp(m.cursor, -1, max(-1, len(m.items)-1))
}

func sortOverrideItems(items []overrideItem) {
	sort.Slice(items, func(left, right int) bool {
		if items[left].Explicit != items[right].Explicit {
			return items[left].Explicit
		}
		if items[left].Discovered != items[right].Discovered {
			return items[left].Discovered
		}
		if items[left].Provider != items[right].Provider {
			return items[left].Provider < items[right].Provider
		}
		return items[left].Package < items[right].Package
	})
}

func (m *model) startValidation(exitAfter bool) (tea.Model, tea.Cmd) {
	if m.validate == nil {
		m.reportValidationStartError(exitAfter, "Choice validation is unavailable.")
		return m, nil
	}
	raw := m.buildRaw()
	if err := deploy.ValidatePackageOverridesV1(raw); err != nil {
		m.reportValidationStartError(exitAfter, "Cannot validate: "+err.Error())
		return m, nil
	}
	if !m.autoValidate {
		m.validateItems()
		for _, item := range m.items {
			if item.Error != "" {
				m.reportValidationStartError(exitAfter, "Cannot validate while an override is invalid.")
				return m, nil
			}
		}
	}
	m.validating = true
	m.validationVisible = true
	m.validationError = ""
	m.validationCompleted = nil
	saveChoices := m.dirty
	startStep := "Validating current choices"
	if saveChoices {
		startStep = "Saving choices"
	}
	m.validationCurrent = startStep
	m.validationLog = ""
	m.completedSteps = map[string]struct{}{}
	m.loggedSteps = map[string]struct{}{startStep: {}}
	m.validationSavedPath = ""
	m.validationSaveError = ""
	m.validationSavePrompt = false
	m.appendValidationLog(startStep)
	m.validationID++
	validationID := m.validationID
	progress := make(chan string, 64)
	m.validationProgress = progress
	m.status = ""
	validationCtx, cancel := context.WithCancel(m.ctx)
	m.validationCancel = cancel
	run := func() tea.Msg {
		defer cancel()
		writer := &validationProgressWriter{events: progress}
		snapshot := m.original
		saved := true
		var err error
		if saveChoices {
			snapshot, err = m.save(validationCtx, m.original, raw)
			saved = err == nil
		}
		result := ValidationResult{}
		if err == nil {
			result, err = m.validate(validationCtx, writer)
		}
		if saved {
			current, snapshotErr := readEditorSnapshot(validationCtx, m.deploymentDir)
			var currentErr error
			switch {
			case snapshotErr != nil:
				currentErr = fmt.Errorf("verify validated choices remained current: %w", snapshotErr)
			case current != snapshot:
				currentErr = fmt.Errorf(
					"the staged deployment or %s changed during validation; reopen the editor and try again",
					deploy.PackageOverridesFilename,
				)
			}
			if currentErr != nil {
				err = errors.Join(err, currentErr)
				saved = false
			}
		}
		writer.flush()
		close(progress)
		return validatedMsg{
			ID: validationID, Snapshot: snapshot, Result: result, Saved: saved,
			StartStep: startStep, ProgressLog: writer.log.String(), Err: err, ExitAfter: exitAfter,
		}
	}
	return m, tea.Batch(run, waitForValidationProgress(validationID, progress), m.validationSpinner.Tick)
}

func (m *model) reportValidationStartError(exitAfter bool, message string) {
	if exitAfter {
		m.screen = screenExit
		m.exitError = message
		return
	}
	m.status = message
}

func (m *model) recordValidationStep(step string) {
	step = upperFirst(strings.TrimSpace(step))
	if step == "" || step == m.validationCurrent {
		return
	}
	if m.validationCurrent != "" {
		if _, found := m.completedSteps[m.validationCurrent]; !found {
			m.validationCompleted = append(m.validationCompleted, m.validationCurrent)
			m.completedSteps[m.validationCurrent] = struct{}{}
		}
	}
	m.validationCurrent = step
	if _, found := m.loggedSteps[step]; !found {
		m.appendValidationLog(step)
		m.loggedSteps[step] = struct{}{}
	}
}

func waitForValidationProgress(id uint64, progress <-chan string) tea.Cmd {
	return func() tea.Msg {
		step, found := <-progress
		return validationProgressMsg{ID: id, Step: step, Closed: !found}
	}
}

type validationProgressWriter struct {
	mutex   sync.Mutex
	pending string
	events  chan<- string
	log     strings.Builder
	seen    map[string]struct{}
}

func (writer *validationProgressWriter) Write(content []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	writer.pending += string(content)
	for {
		index := strings.IndexByte(writer.pending, '\n')
		if index < 0 {
			break
		}
		writer.emitLocked(writer.pending[:index])
		writer.pending = writer.pending[index+1:]
	}
	return len(content), nil
}

func (writer *validationProgressWriter) flush() {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	writer.emitLocked(writer.pending)
	writer.pending = ""
}

func (writer *validationProgressWriter) emitLocked(step string) {
	step = sanitizeTerminalText(strings.TrimSpace(step))
	if step != "" {
		if writer.seen == nil {
			writer.seen = map[string]struct{}{}
		}
		if _, found := writer.seen[step]; !found {
			writer.log.WriteString(step)
			writer.log.WriteByte('\n')
			writer.seen[step] = struct{}{}
		}
		select {
		case writer.events <- step:
		default:
		}
	}
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

func sanitizeTerminalBlock(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' {
			return character
		}
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
}

func upperFirst(value string) string {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func wrapWords(value string, width int) string {
	if width <= 0 {
		return value
	}
	words := strings.Fields(value)
	if len(words) == 0 {
		return ""
	}
	var result strings.Builder
	lineWidth := 0
	for _, word := range words {
		wordWidth := lipgloss.Width(word)
		if lineWidth != 0 && lineWidth+1+wordWidth > width {
			result.WriteByte('\n')
			lineWidth = 0
		} else if lineWidth != 0 {
			result.WriteByte(' ')
			lineWidth++
		}
		result.WriteString(word)
		lineWidth += wordWidth
	}
	return result.String()
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
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read setup.cfg: %w", err)
		}
	}
	if content, err := os.ReadFile(filepath.Join(dir, "setup.py")); err == nil {
		if match := setupPyNamePattern.FindSubmatch(content); match != nil {
			return string(match[1]), nil
		}
	}
	return "", fmt.Errorf("could not determine the Python project name")
}

func pythonProjectDirectDependencies(dir string) ([]string, bool, error) {
	content, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if err != nil {
		return nil, false, nil
	}
	var document struct {
		Project *struct {
			Dependencies []string `toml:"dependencies"`
			Dynamic      []string `toml:"dynamic"`
		} `toml:"project"`
	}
	if err := toml.Unmarshal(content, &document); err != nil {
		return nil, false, fmt.Errorf("pyproject.toml is invalid")
	}
	if document.Project == nil {
		return nil, false, nil
	}
	for _, field := range document.Project.Dynamic {
		if strings.EqualFold(strings.TrimSpace(field), "dependencies") {
			return nil, false, nil
		}
	}
	seen := map[string]struct{}{}
	dependencies := []string{}
	for _, requirement := range document.Project.Dependencies {
		distribution, err := pythonprovider.RequirementDistributionName(requirement)
		if err != nil {
			return nil, false, fmt.Errorf("invalid project dependency %q", requirement)
		}
		if _, found := seen[distribution]; found {
			continue
		}
		seen[distribution] = struct{}{}
		dependencies = append(dependencies, requirement)
	}
	sort.Slice(dependencies, func(left, right int) bool {
		leftName, _ := pythonprovider.RequirementDistributionName(dependencies[left])
		rightName, _ := pythonprovider.RequirementDistributionName(dependencies[right])
		return leftName < rightName
	})
	return dependencies, true, nil
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

func findProjects(root string) tea.Cmd {
	return func() tea.Msg {
		projects, err := matchingProjects(root)
		return projectsLoadedMsg{Root: root, Projects: projects, Err: err}
	}
}

func matchingProjects(root string) ([]string, error) {
	ignored := map[string]bool{
		".git": true, ".sl": true, ".venv": true, "node_modules": true,
		"__pycache__": true, ".mypy_cache": true, ".pytest_cache": true,
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, entry := range entries {
		if !entry.IsDir() || ignored[entry.Name()] {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if _, err := pythonProjectName(path); err == nil {
			result = append(result, path)
		}
	}
	return result, nil
}

func relativeProjectCandidate(root string, value string) (string, bool) {
	if value == "" || root == "" || filepath.IsAbs(value) {
		return "", false
	}
	candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(value)))
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return candidate, true
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
	if m.workspaceResolved != "" {
		return m.workspaceResolved
	}
	return m.browseRoot
}

func sidecarSnapshotForContent(content []byte) fileSnapshot {
	sum := sha256.Sum256(content)
	return fileSnapshot{Found: true, Digest: hex.EncodeToString(sum[:])}
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
	return sidecarSnapshotForContent(content), nil
}

var commitEditorPackageOverridesV1 = func(
	operation *deploy.OperationLock,
	overrides deploy.PackageOverridesV1,
) error {
	return operation.CommitPackageOverridesV1(overrides)
}

func stagingSnapshotFor(
	document blueprint.Document,
	overlay deploy.RequestOverlayV1,
	platform blueprint.Platform,
) (stagingSnapshot, error) {
	blueprintDigest, err := blueprint.DocumentDigestV1(document)
	if err != nil {
		return stagingSnapshot{}, err
	}
	overlayDigest, err := deploy.RequestOverlayDigestV1(overlay)
	if err != nil {
		return stagingSnapshot{}, err
	}
	if err := blueprint.ValidateSelectedPlatform(document, platform); err != nil {
		return stagingSnapshot{}, err
	}
	return stagingSnapshot{
		BlueprintDigest: blueprintDigest,
		OverlayDigest:   overlayDigest,
		Platform:        platform,
	}, nil
}

func stagingSnapshotFromState(state deploy.StateV1) (stagingSnapshot, error) {
	if state.Staging == nil {
		return stagingSnapshot{}, fmt.Errorf("package overrides require a staged deployment")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return stagingSnapshot{}, err
	}
	return stagingSnapshotFor(document, state.Overlay, state.Platform)
}

func readEditorOpenState(
	ctx context.Context,
	deploymentDir string,
) (snapshot editorSnapshot, overrides deploy.PackageOverridesV1, found bool, err error) {
	operation, err := deploy.AcquireOperationLock(ctx, deploymentDir)
	if err != nil {
		return editorSnapshot{}, deploy.PackageOverridesV1{}, false, err
	}
	defer func() {
		err = errors.Join(err, operation.Unlock())
	}()
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return editorSnapshot{}, deploy.PackageOverridesV1{}, false, err
	}
	if !found {
		return editorSnapshot{}, deploy.PackageOverridesV1{}, false, fmt.Errorf("package overrides require a staged deployment")
	}
	staging, err := stagingSnapshotFromState(state)
	if err != nil {
		return editorSnapshot{}, deploy.PackageOverridesV1{}, false, err
	}
	before, err := readSidecarSnapshot(deploymentDir)
	if err != nil {
		return editorSnapshot{}, deploy.PackageOverridesV1{}, false, err
	}
	overrides, found, err = deploy.ReadPackageOverridesV1(deploymentDir)
	if err != nil {
		return editorSnapshot{}, deploy.PackageOverridesV1{}, false, err
	}
	after, err := readSidecarSnapshot(deploymentDir)
	if err != nil {
		return editorSnapshot{}, deploy.PackageOverridesV1{}, false, err
	}
	if before != after {
		return editorSnapshot{}, deploy.PackageOverridesV1{}, false, fmt.Errorf(
			"%s changed while the editor was opening; reopen the editor and try again",
			deploy.PackageOverridesFilename,
		)
	}
	return editorSnapshot{Sidecar: after, Staging: staging}, overrides, found, nil
}

func readEditorSnapshot(ctx context.Context, deploymentDir string) (editorSnapshot, error) {
	snapshot, _, _, err := readEditorOpenState(ctx, deploymentDir)
	return snapshot, err
}

func saveSidecarAt(
	ctx context.Context,
	deploymentDir string,
	original editorSnapshot,
	overrides deploy.PackageOverridesV1,
) (snapshot editorSnapshot, err error) {
	operation, err := deploy.AcquireOperationLock(ctx, deploymentDir)
	if err != nil {
		return editorSnapshot{}, err
	}
	defer func() {
		err = errors.Join(err, operation.Unlock())
	}()
	currentSidecar, err := readSidecarSnapshot(deploymentDir)
	if err != nil {
		return editorSnapshot{}, err
	}
	if currentSidecar != original.Sidecar {
		return editorSnapshot{}, fmt.Errorf("%s changed while the editor was open; reopen the editor and try again", deploy.PackageOverridesFilename)
	}
	state, found, err := operation.ReadStateV1()
	if err != nil {
		return editorSnapshot{}, err
	}
	if !found || state.Staging == nil {
		return editorSnapshot{}, fmt.Errorf("package overrides require a staged deployment")
	}
	currentStaging, err := stagingSnapshotFromState(state)
	if err != nil {
		return editorSnapshot{}, err
	}
	if currentStaging != original.Staging {
		return editorSnapshot{}, fmt.Errorf("staged deployment changed while the editor was open; reopen the editor and try again")
	}
	document, err := blueprint.DecodeResolvedDocumentV1(state.Blueprint)
	if err != nil {
		return editorSnapshot{}, err
	}
	if document.Environment.ID != overrides.Environment.ID {
		return editorSnapshot{}, fmt.Errorf(
			"staged environment changed from %q to %q while the editor was open; reopen the editor and try again",
			overrides.Environment.ID,
			document.Environment.ID,
		)
	}
	content, err := deploy.EncodePackageOverridesV1(overrides)
	if err != nil {
		return editorSnapshot{}, err
	}
	if err := commitEditorPackageOverridesV1(operation, overrides); err != nil {
		return editorSnapshot{}, err
	}
	return editorSnapshot{
		Sidecar: sidecarSnapshotForContent(content),
		Staging: currentStaging,
	}, nil
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

func baseChoiceIndex(image string) int {
	if image == "" {
		return 0
	}
	return 1
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
