package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRuntimeTimestampWriterColorsOnlyRuntimeTimestampFields(t *testing.T) {
	var output bytes.Buffer
	writer := newRuntimeTimestampWriter(&output, true)
	_, _ = writer.Write([]byte("2026-07-24T17:05:06.123456789+08:00 application INFO unchanged\n"))
	_ = writer.Flush()

	got := output.String()
	for _, want := range []string{
		"\x1b[2m2026-07-24\x1b[0m",
		"\x1b[36m17:05:06.123456789\x1b[0m",
		"\x1b[2m+08:00\x1b[0m",
		" application INFO unchanged\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("colored timestamp output missing %q: %q", want, got)
		}
	}
}

func TestRuntimeTimestampWriterKeepsPlainOutputByteStable(t *testing.T) {
	const input = "2026-07-24T17:05:06.123456789Z application timestamp=unchanged\npartial"
	var output bytes.Buffer
	writer := newRuntimeTimestampWriter(&output, false)
	_, _ = writer.Write([]byte(input[:35]))
	_, _ = writer.Write([]byte(input[35:]))
	_ = writer.Flush()
	if output.String() != input {
		t.Fatalf("plain timestamp output = %q, want %q", output.String(), input)
	}
}

func TestRuntimeLogColorPolicyHonorsTerminalEnvironment(t *testing.T) {
	interactive := true
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	if !runtimeLogColorEnabled(&bytes.Buffer{}, &interactive) {
		t.Fatal("interactive color-capable output did not enable color")
	}
	t.Setenv("NO_COLOR", "1")
	if runtimeLogColorEnabled(&bytes.Buffer{}, &interactive) {
		t.Fatal("NO_COLOR did not disable log timestamp color")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if runtimeLogColorEnabled(&bytes.Buffer{}, &interactive) {
		t.Fatal("TERM=dumb did not disable log timestamp color")
	}
	noninteractive := false
	t.Setenv("TERM", "xterm-256color")
	if runtimeLogColorEnabled(&bytes.Buffer{}, &noninteractive) {
		t.Fatal("redirected output enabled log timestamp color")
	}
}
