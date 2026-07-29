package overrideui

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/omry/reploy/internal/buildprogress"
)

type BuildRunner func(context.Context, io.Writer, buildprogress.Reporter) (ValidationResult, error)

type BuildProgressConfig struct {
	Context context.Context
	Input   io.Reader
	Output  io.Writer
	Run     BuildRunner
}

type BuildProgressResult struct {
	Validation ValidationResult
	BuildError error
	Canceled   bool
}

type buildProgressFinishedMsg struct {
	result ValidationResult
	err    error
}

type buildProgressStepMsg struct {
	step   string
	closed bool
}

type buildProgressEventMsg struct {
	event  buildprogress.Event
	closed bool
}

type buildProgressModel struct {
	ctx         context.Context
	run         BuildRunner
	width       int
	height      int
	spinner     spinner.Model
	environment string
	current     string
	event       buildprogress.Event
	progress    <-chan string
	events      <-chan buildprogress.Event
	cancel      context.CancelFunc
	validating  bool
	ctrlCArmed  bool
	canceled    bool
	result      ValidationResult
	buildErr    error
}

func RunBuildProgress(config BuildProgressConfig) (BuildProgressResult, error) {
	if config.Context == nil {
		config.Context = context.Background()
	}
	if err := config.Context.Err(); err != nil {
		return BuildProgressResult{}, err
	}
	if config.Run == nil {
		return BuildProgressResult{}, fmt.Errorf("build progress runner is required")
	}
	indicator := spinner.New()
	indicator.Spinner = spinner.Dot
	indicator.Style = warnStyle
	model := &buildProgressModel{
		ctx: config.Context, run: config.Run, spinner: indicator,
		current: "Inspecting staged inputs and build cache",
		event: buildprogress.Event{
			Phase:  buildprogress.PhaseInspect,
			Detail: "Inspecting staged inputs and build cache",
		},
	}
	options := []tea.ProgramOption{tea.WithContext(config.Context)}
	if config.Input != nil {
		options = append(options, tea.WithInput(config.Input))
	}
	if config.Output != nil {
		options = append(options, tea.WithOutput(config.Output))
	}
	final, err := tea.NewProgram(model, options...).Run()
	if err != nil {
		return BuildProgressResult{}, err
	}
	finalModel, ok := final.(*buildProgressModel)
	if !ok {
		return BuildProgressResult{}, fmt.Errorf("build progress UI returned unexpected model %T", final)
	}
	return BuildProgressResult{
		Validation: finalModel.result,
		BuildError: finalModel.buildErr,
		Canceled:   finalModel.canceled,
	}, nil
}

func (m *buildProgressModel) Init() tea.Cmd {
	m.validating = true
	progress := make(chan string, 64)
	events := make(chan buildprogress.Event, 64)
	m.progress = progress
	m.events = events
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	run := func() tea.Msg {
		defer cancel()
		writer := &validationProgressWriter{events: progress}
		reporter := func(event buildprogress.Event) {
			select {
			case events <- event:
			default:
			}
		}
		result, err := m.run(ctx, writer, reporter)
		writer.flush()
		close(progress)
		close(events)
		return buildProgressFinishedMsg{result: result, err: err}
	}
	return tea.Batch(run, waitForBuildProgress(progress), waitForBuildProgressEvent(events), m.spinner.Tick)
}

func (m *buildProgressModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case buildProgressStepMsg:
		if !m.validating || msg.closed {
			return m, nil
		}
		m.recordStep(msg.step)
		return m, waitForBuildProgress(m.progress)
	case buildProgressEventMsg:
		if !m.validating || msg.closed {
			return m, nil
		}
		m.recordEvent(msg.event)
		return m, waitForBuildProgressEvent(m.events)
	case spinner.TickMsg:
		if !m.validating {
			return m, nil
		}
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(msg)
		return m, command
	case buildProgressFinishedMsg:
		m.validating = false
		m.cancel = nil
		m.result = msg.result
		m.buildErr = msg.err
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() != "ctrl+c" {
			m.ctrlCArmed = false
			return m, nil
		}
		if !m.ctrlCArmed {
			m.ctrlCArmed = true
			return m, nil
		}
		m.canceled = true
		m.current = "Canceling build"
		if m.cancel != nil {
			m.cancel()
		}
		return m, nil
	}
	return m, nil
}

func (m *buildProgressModel) View() string {
	var body string
	title := "Building environment"
	if m.environment != "" {
		title = "Building " + m.environment
	}
	body += titleStyle.Render(title)
	body += "\n" + m.spinner.View() + " " + m.phaseLabel()
	body += "\n" + m.progressBar()
	body += "\n" + truncate(m.current, max(24, m.panelWidth()-6))
	operationStatus := " "
	if m.event.Total > 1 {
		operationStatus = fmt.Sprintf(
			"%d of %d provider operations complete",
			min(m.event.Completed, m.event.Total), m.event.Total,
		)
	}
	body += "\n" + mutedStyle.Render(operationStatus)
	body += "\n"
	if m.ctrlCArmed {
		body += mutedStyle.Render("Press Ctrl+C again to cancel and exit.")
	} else {
		body += mutedStyle.Render("Ctrl+C twice cancels")
	}
	// Bubble Tea's inline renderer erases the cursor's final line when it
	// shuts down. Keep the cursor on a blank line so that graceful completion
	// preserves the panel's bottom border and subsequent command output starts
	// below it.
	return panelStyle.Width(m.panelWidth()).Render(body) + "\n"
}

func (m *buildProgressModel) panelWidth() int {
	if m.width <= 0 {
		return 72
	}
	return max(28, min(78, m.width-2))
}

func (m *buildProgressModel) recordStep(step string) {
	step = upperFirst(step)
	if step == "" || step == m.current {
		return
	}
	m.current = step
}

func (m *buildProgressModel) recordEvent(event buildprogress.Event) {
	if event.Phase >= buildprogress.PhaseCount {
		return
	}
	if event.Completed < 0 {
		event.Completed = 0
	}
	if event.Total < 0 {
		event.Total = 0
	}
	if event.Total > 0 && event.Completed > event.Total {
		event.Completed = event.Total
	}
	if event.Environment != "" {
		m.environment = event.Environment
	}
	m.event = event
	if detail := upperFirst(event.Detail); detail != "" {
		m.current = detail
	}
}

func (m *buildProgressModel) phaseLabel() string {
	names := [...]string{"Inspect", "Prepare", "Providers", "Assemble", "Publish"}
	index := int(m.event.Phase)
	if index < 0 || index >= len(names) {
		index = 0
	}
	return fmt.Sprintf("%s · phase %d of %d", names[index], index+1, buildprogress.PhaseCount)
}

func (m *buildProgressModel) progressBar() string {
	width := min(36, max(12, m.panelWidth()-10))
	completed := float64(m.event.Phase)
	if m.event.Total > 0 {
		completed += float64(m.event.Completed) / float64(m.event.Total)
	}
	fraction := math.Min(1, math.Max(0, completed/float64(buildprogress.PhaseCount)))
	filled := min(width, int(math.Floor(fraction*float64(width))))
	return warnStyle.Render(strings.Repeat("━", filled)) +
		mutedStyle.Render(strings.Repeat("─", width-filled))
}

func waitForBuildProgress(progress <-chan string) tea.Cmd {
	return func() tea.Msg {
		step, found := <-progress
		return buildProgressStepMsg{step: step, closed: !found}
	}
}

func waitForBuildProgressEvent(events <-chan buildprogress.Event) tea.Cmd {
	return func() tea.Msg {
		event, found := <-events
		return buildProgressEventMsg{event: event, closed: !found}
	}
}

var _ tea.Model = (*buildProgressModel)(nil)
