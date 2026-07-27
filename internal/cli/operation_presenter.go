package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
)

type operationPresenterOptions struct {
	Name           string
	ProgressOutput io.Writer
	ResultOutput   io.Writer
	Verbose        bool

	InteractiveOverride *bool
}

type operationPresenter struct {
	name           string
	progressOutput io.Writer
	resultOutput   io.Writer
	animated       bool
	color          bool
	currentStep    string
	childOutput    synchronizedBuffer
	warnings       []string
	warningSet     map[string]struct{}
	completed      bool
	update         func(string)
	finish         func(bool, string)
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(content []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(content)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func newOperationPresenter(options operationPresenterOptions) *operationPresenter {
	name := strings.TrimSpace(options.Name)
	if name == "" {
		name = "working"
	}
	progressOutput := options.ProgressOutput
	if progressOutput == nil {
		progressOutput = io.Discard
	}
	resultOutput := options.ResultOutput
	if resultOutput == nil {
		resultOutput = io.Discard
	}
	interactive := operationOutputIsInteractive(progressOutput)
	if options.InteractiveOverride != nil {
		interactive = *options.InteractiveOverride
	}
	if envBool("CI") || strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		interactive = false
	}

	presenter := &operationPresenter{
		name:           name,
		progressOutput: progressOutput,
		resultOutput:   resultOutput,
		animated:       interactive && !options.Verbose,
		color:          operationColorEnabled(interactive),
		warningSet:     map[string]struct{}{},
	}
	if presenter.animated {
		presenter.update, presenter.finish = startOperationAnimation(progressOutput, name, presenter.color)
	}
	return presenter
}

func (presenter *operationPresenter) Step(name string) {
	if presenter == nil || presenter.completed {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || name == presenter.currentStep {
		return
	}
	presenter.currentStep = name
	if presenter.animated {
		presenter.update(name)
		return
	}
	fmt.Fprintf(presenter.progressOutput, "%s: %s\n", presenter.name, name)
}

func (presenter *operationPresenter) Progress() io.Writer {
	if presenter == nil {
		return io.Discard
	}
	return progressWriter{write: presenter.Step}
}

func (presenter *operationPresenter) ChildOutput() io.Writer {
	if presenter == nil {
		return io.Discard
	}
	return &presenter.childOutput
}

func (presenter *operationPresenter) CapturedChildOutput() string {
	if presenter == nil {
		return ""
	}
	return presenter.childOutput.String()
}

func (presenter *operationPresenter) Warn(message string) {
	if presenter == nil || presenter.completed {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	if _, found := presenter.warningSet[message]; found {
		return
	}
	presenter.warningSet[message] = struct{}{}
	presenter.warnings = append(presenter.warnings, message)
}

func (presenter *operationPresenter) Success(result func(io.Writer)) error {
	if err := presenter.complete(true); err != nil {
		return err
	}
	if result != nil {
		result(presenter.resultOutput)
	}
	return nil
}

func (presenter *operationPresenter) Failure(diagnostic string) error {
	if err := presenter.complete(false); err != nil {
		return err
	}
	if diagnostic = strings.TrimSpace(diagnostic); diagnostic != "" {
		fmt.Fprintln(presenter.progressOutput, diagnostic)
	}
	return nil
}

func (presenter *operationPresenter) complete(success bool) error {
	if presenter == nil {
		return fmt.Errorf("operation presenter is nil")
	}
	if presenter.completed {
		return fmt.Errorf("operation %q already completed", presenter.name)
	}
	presenter.completed = true
	if presenter.animated {
		presenter.finish(success, presenter.currentStep)
	} else if success {
		fmt.Fprintf(presenter.progressOutput, "%s: %s\n", presenter.name, operationStatusText("done", presenter.color, "32"))
	} else if presenter.currentStep == "" {
		fmt.Fprintf(presenter.progressOutput, "%s: %s\n", presenter.name, operationStatusText("failed", presenter.color, "31"))
	} else {
		fmt.Fprintf(presenter.progressOutput, "%s: %s: %s\n", presenter.name, presenter.currentStep, operationStatusText("failed", presenter.color, "31"))
	}
	for _, warning := range presenter.warnings {
		fmt.Fprintf(presenter.progressOutput, "reploy %s: %s\n", operationStatusText("warning", presenter.color, "33"), warning)
	}
	return nil
}

func operationOutputIsInteractive(output io.Writer) bool {
	file, ok := output.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

func operationColorEnabled(interactive bool) bool {
	if !interactive || strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("REPLOY_COLOR"))) {
	case "never":
		return false
	case "always":
		return true
	default:
		return true
	}
}

func operationStatusText(text string, color bool, code string) string {
	if !color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

type operationAnimationUpdate struct {
	step    string
	applied chan struct{}
}

type operationAnimationCompletion struct {
	success bool
	step    string
}

func startOperationAnimation(output io.Writer, name string, color bool) (func(string), func(bool, string)) {
	updates := make(chan operationAnimationUpdate)
	completion := make(chan operationAnimationCompletion, 1)
	finished := make(chan struct{})
	go func() {
		const hideCursor = "\x1b[?25l"
		const showCursor = "\x1b[?25h"
		frames := []string{"|", "/", "-", "\\"}
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		index := 0
		current := name
		lastLen := 0
		fmt.Fprint(output, hideCursor)
		render := func() {
			line := fmt.Sprintf("\r%s %s", current, frames[index])
			if len(line) < lastLen {
				line += strings.Repeat(" ", lastLen-len(line))
			}
			fmt.Fprint(output, line)
			lastLen = len(line)
		}
		render()
		for {
			select {
			case update := <-updates:
				current = name + ": " + update.step
				render()
				close(update.applied)
			case result := <-completion:
				finalLabel := name
				statusText := "done"
				statusColor := "32"
				if !result.success {
					statusText = "failed"
					statusColor = "31"
					if result.step != "" {
						finalLabel += ": " + result.step
					}
				}
				visibleLine := "\r" + finalLabel + "... " + statusText
				line := "\r" + finalLabel + "... " + operationStatusText(statusText, color, statusColor)
				if len(visibleLine) < lastLen {
					line += strings.Repeat(" ", lastLen-len(visibleLine))
				}
				fmt.Fprintln(output, line+showCursor)
				close(finished)
				return
			case <-ticker.C:
				index = (index + 1) % len(frames)
				render()
			}
		}
	}()
	return func(step string) {
			applied := make(chan struct{})
			updates <- operationAnimationUpdate{step: step, applied: applied}
			<-applied
		}, func(success bool, step string) {
			completion <- operationAnimationCompletion{success: success, step: step}
			<-finished
		}
}
