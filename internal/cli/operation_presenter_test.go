package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestOperationPresenterInteractiveOwnsProgressAndCompletesBeforeResult(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	interactive := true
	var output bytes.Buffer
	presenter := newOperationPresenter(operationPresenterOptions{
		Name:                "building environment",
		ProgressOutput:      &output,
		ResultOutput:        &output,
		InteractiveOverride: &interactive,
	})

	fmt.Fprintln(presenter.Progress(), "resolving Python packages")
	fmt.Fprintln(presenter.ChildOutput(), "raw Docker build output")
	presenter.Warn("base image uses a deprecated instruction")
	presenter.Warn("base image uses a deprecated instruction")
	if err := presenter.Success(func(result io.Writer) {
		fmt.Fprintln(result, "Built environment image.")
	}); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, want := range []string{
		"\x1b[?25l",
		"building environment: resolving Python packages",
		"building environment... done",
		"reploy warning: base image uses a deprecated instruction",
		"Built environment image.",
		"\x1b[?25h",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("interactive output missing %q:\n%q", want, got)
		}
	}
	if strings.Contains(got, "raw Docker build output") {
		t.Fatalf("successful child output leaked into normal output:\n%q", got)
	}
	if strings.Count(got, "reploy warning:") != 1 {
		t.Fatalf("warning was not deduplicated:\n%q", got)
	}
	if strings.Index(got, "Built environment image.") < strings.Index(got, "building environment... done") {
		t.Fatalf("result appeared before progress completion:\n%q", got)
	}
	if captured := presenter.CapturedChildOutput(); captured != "raw Docker build output\n" {
		t.Fatalf("captured child output = %q", captured)
	}
}

func TestOperationPresenterVerboseUsesDurableStepLines(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	interactive := true
	var output bytes.Buffer
	presenter := newOperationPresenter(operationPresenterOptions{
		Name:                "installing app",
		ProgressOutput:      &output,
		ResultOutput:        &output,
		Verbose:             true,
		InteractiveOverride: &interactive,
	})

	presenter.Step("planning managed paths")
	presenter.Step("starting service")
	if err := presenter.Success(func(result io.Writer) {
		fmt.Fprintln(result, "Installed app.")
	}); err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"installing app: planning managed paths",
		"installing app: starting service",
		"installing app: done",
		"Installed app.",
		"",
	}, "\n")
	if got := output.String(); got != want {
		t.Fatalf("verbose output = %q, want %q", got, want)
	}
}

func TestOperationPresenterRedirectedAndDumbOutputIsPlain(t *testing.T) {
	for _, test := range []struct {
		name        string
		term        string
		interactive *bool
	}{
		{name: "redirected", term: "xterm-256color"},
		{name: "dumb terminal", term: "dumb", interactive: boolPointer(true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TERM", test.term)
			var output bytes.Buffer
			presenter := newOperationPresenter(operationPresenterOptions{
				Name:                "uninstalling app",
				ProgressOutput:      &output,
				ResultOutput:        &output,
				InteractiveOverride: test.interactive,
			})
			presenter.Step("removing runtime resources")
			if err := presenter.Success(nil); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "\r") {
				t.Fatalf("plain output contains terminal controls: %q", output.String())
			}
		})
	}
}

func TestOperationPresenterFailureRetainsStepAndTranslatedDiagnostic(t *testing.T) {
	var output bytes.Buffer
	presenter := newOperationPresenter(operationPresenterOptions{
		Name:           "installing app",
		ProgressOutput: &output,
		ResultOutput:   &output,
	})
	presenter.Step("starting service")
	fmt.Fprintln(presenter.ChildOutput(), "container exited with status 1")

	if err := presenter.Failure("service failed its readiness check"); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	if !strings.Contains(got, "installing app: starting service: failed\n") {
		t.Fatalf("failed step not retained:\n%s", got)
	}
	if !strings.Contains(got, "service failed its readiness check\n") {
		t.Fatalf("translated diagnostic missing:\n%s", got)
	}
	if strings.Contains(got, "container exited with status 1") {
		t.Fatalf("raw child diagnostic leaked into normal failure output:\n%s", got)
	}
}

func TestOperationPresenterRejectsSecondCompletion(t *testing.T) {
	var output bytes.Buffer
	presenter := newOperationPresenter(operationPresenterOptions{
		Name: "building environment", ProgressOutput: &output, ResultOutput: &output,
	})
	if err := presenter.Success(nil); err != nil {
		t.Fatal(err)
	}
	if err := presenter.Failure("late failure"); err == nil {
		t.Fatal("second completion succeeded")
	}
	if strings.Contains(output.String(), "late failure") {
		t.Fatalf("second completion emitted output:\n%s", output.String())
	}
}

func TestOperationPresenterNoColorKeepsText(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	interactive := true
	render := func(noColor bool) string {
		t.Helper()
		if noColor {
			t.Setenv("NO_COLOR", "1")
		} else {
			t.Setenv("NO_COLOR", "")
			t.Setenv("REPLOY_COLOR", "always")
		}
		var output bytes.Buffer
		presenter := newOperationPresenter(operationPresenterOptions{
			Name:                "building environment",
			ProgressOutput:      &output,
			ResultOutput:        &output,
			InteractiveOverride: &interactive,
		})
		presenter.Step("creating image")
		if err := presenter.Success(nil); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}

	colored := render(false)
	plain := render(true)
	if !strings.Contains(colored, "\x1b[32m") {
		t.Fatalf("interactive colored output has no success color: %q", colored)
	}
	if strings.Contains(plain, "\x1b[32m") {
		t.Fatalf("NO_COLOR output contains success color: %q", plain)
	}
	if stripOperationColors(colored) != plain {
		t.Fatalf("NO_COLOR changed content:\ncolored: %q\nplain:   %q", colored, plain)
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func stripOperationColors(value string) string {
	for _, sequence := range []string{"\x1b[31m", "\x1b[32m", "\x1b[33m", "\x1b[0m"} {
		value = strings.ReplaceAll(value, sequence, "")
	}
	return value
}
