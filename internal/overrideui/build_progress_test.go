package overrideui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/omry/reploy/internal/buildprogress"
)

func TestRunBuildProgressCompletesWithoutInput(t *testing.T) {
	var output bytes.Buffer
	result, err := RunBuildProgress(BuildProgressConfig{
		Context: t.Context(),
		Input:   strings.NewReader(""),
		Output:  &output,
		Run: func(
			_ context.Context,
			progress io.Writer,
			report buildprogress.Reporter,
		) (ValidationResult, error) {
			report(buildprogress.Event{
				Phase: buildprogress.PhaseProviders, Detail: "Resolving packages",
				Completed: 1, Total: 4,
			})
			fmt.Fprintln(progress, "resolving packages")
			return ValidationResult{Build: &BuildOutcome{ImageReference: "reploy/env/example:g-test"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Canceled || result.BuildError != nil ||
		result.Validation.Build == nil || result.Validation.Build.ImageReference != "reploy/env/example:g-test" {
		t.Fatalf("build progress result = %#v", result)
	}
	if strings.Contains(output.String(), "\x1b[?1049h") {
		t.Fatalf("build progress entered the alternate screen:\n%q", output.String())
	}
}

func TestBuildProgressModelRendersCompactProgressOnlyPanel(t *testing.T) {
	indicator := spinner.New()
	indicator.Spinner = spinner.Dot
	m := &buildProgressModel{
		spinner: indicator, validating: true,
		current:     "Building Python layer",
		environment: "omegaconf-inspector",
		event: buildprogress.Event{
			Phase: buildprogress.PhaseProviders, Completed: 2, Total: 4,
		},
	}

	view := m.View()
	for _, want := range []string{
		"Building omegaconf-inspector",
		"Providers · phase 3 of 5",
		"Building Python layer",
		"2 of 4 provider operations complete",
		"Ctrl+C twice cancels",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("build progress missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"Build log", "PgUp", "save log", "edits choices"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("build progress retained %q:\n%s", unwanted, view)
		}
	}
	if lipgloss.Height(view) > 12 {
		t.Fatalf("build progress panel is not compact:\n%s", view)
	}
}

func TestBuildProgressPanelHeightDoesNotChangeBetweenPhases(t *testing.T) {
	indicator := spinner.New()
	indicator.Spinner = spinner.Dot
	m := &buildProgressModel{
		spinner: indicator, validating: true,
		current: "Preparing base image and provider plan",
		event: buildprogress.Event{
			Phase: buildprogress.PhasePrepare,
		},
	}
	initialHeight := lipgloss.Height(m.View())

	m.recordEvent(buildprogress.Event{
		Phase: buildprogress.PhaseProviders, Detail: strings.Repeat("long provider operation ", 8),
		Completed: 1, Total: 4,
	})
	if got := lipgloss.Height(m.View()); got != initialHeight {
		t.Fatalf("provider phase panel height = %d, want %d:\n%s", got, initialHeight, m.View())
	}

	m.recordEvent(buildprogress.Event{
		Phase: buildprogress.PhasePublish, Detail: "Validating and publishing final image",
	})
	if got := lipgloss.Height(m.View()); got != initialHeight {
		t.Fatalf("publish phase panel height = %d, want %d:\n%s", got, initialHeight, m.View())
	}
}

func TestBuildProgressModelQuitsOnSuccessAndFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "failure", err: errors.New("dependency conflict")},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := &buildProgressModel{validating: true}
			updated, command := m.Update(buildProgressFinishedMsg{
				result: ValidationResult{Build: &BuildOutcome{ImageReference: "reploy/env/example:g-test"}},
				err:    test.err,
			})
			m = updated.(*buildProgressModel)
			if command == nil {
				t.Fatal("completed build did not exit")
			}
			if _, ok := command().(tea.QuitMsg); !ok {
				t.Fatalf("completion message = %T", command())
			}
			if m.buildErr != test.err || m.result.Build == nil {
				t.Fatalf("completion state = %#v, %v", m.result, m.buildErr)
			}
		})
	}
}

func TestBuildProgressModelDoubleCtrlCCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	m := &buildProgressModel{validating: true, cancel: cancel}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*buildProgressModel)
	if !m.ctrlCArmed || m.canceled {
		t.Fatalf("first Ctrl-C armed/canceled = %v/%v", m.ctrlCArmed, m.canceled)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*buildProgressModel)
	if !m.canceled || m.current != "Canceling build" || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("canceled/current/context = %v/%q/%v", m.canceled, m.current, ctx.Err())
	}
}
