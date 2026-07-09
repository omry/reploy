package dockerdeploy

import (
	"bufio"
	"strings"
	"time"
)

const (
	runtimeLogEventPrefix      = "reploy:event "
	runtimeLogSnippetTail      = "200"
	runtimeLogSnippetLineLimit = 20
)

var runtimeLogSinceTime = func() time.Time {
	return time.Now().UTC()
}

func runtimeActionUsesStartupLogSnippet(action string) bool {
	return action == "up"
}

type runtimeStartupLogDiagnostics struct {
	Snippet string
	Failure string
}

func runtimeStartupLogDiagnosticsFor(dir string, since time.Time, dockerPreflightTimeout time.Duration) runtimeStartupLogDiagnostics {
	if since.IsZero() {
		return runtimeStartupLogDiagnostics{}
	}
	spec, err := RuntimeCommandWithOptions(dir, "logs", RuntimeCommandOptions{
		Since: since.UTC().Format(time.RFC3339Nano),
		Tail:  runtimeLogSnippetTail,
	})
	if err != nil {
		return runtimeStartupLogDiagnostics{}
	}
	output, err := runTestCommandOutput(spec, RunOptions{DockerPreflightTimeout: dockerPreflightTimeout})
	if err != nil && strings.TrimSpace(string(output)) == "" {
		return runtimeStartupLogDiagnostics{}
	}
	return extractRuntimeStartupLogDiagnostics(string(output))
}

func runtimeStartupLogSnippet(dir string, since time.Time, dockerPreflightTimeout time.Duration) string {
	return runtimeStartupLogDiagnosticsFor(dir, since, dockerPreflightTimeout).Snippet
}

func extractRuntimeStartupLogSnippet(logs string) string {
	return extractRuntimeStartupLogDiagnostics(logs).Snippet
}

func extractRuntimeStartupLogDiagnostics(logs string) runtimeStartupLogDiagnostics {
	diagnostics := runtimeStartupLogDiagnostics{}
	var lines []string
	capturing := false
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		line := scanner.Text()
		event, ok := parseRuntimeLogEvent(line)
		if ok {
			if failure := runtimeLogEventFailure(event); failure != "" {
				diagnostics.Failure = failure
			}
			switch event.Event {
			case "start":
				capturing = true
			case "end":
				capturing = false
			}
			continue
		}
		if !capturing {
			continue
		}
		if cleaned := cleanRuntimeLogSnippetLine(line); cleaned != "" {
			lines = append(lines, cleaned)
		}
	}
	if len(lines) == 0 {
		return diagnostics
	}
	if len(lines) > runtimeLogSnippetLineLimit {
		lines = lines[len(lines)-runtimeLogSnippetLineLimit:]
	}
	diagnostics.Snippet = strings.Join(lines, "\n")
	return diagnostics
}

type runtimeLogEvent struct {
	Phase  string
	Event  string
	Fields map[string]string
}

func parseRuntimeLogEvent(line string) (runtimeLogEvent, bool) {
	index := strings.Index(line, runtimeLogEventPrefix)
	if index == -1 {
		return runtimeLogEvent{}, false
	}
	fields := strings.Fields(line[index+len(runtimeLogEventPrefix):])
	event := runtimeLogEvent{Fields: map[string]string{}}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		event.Fields[key] = value
		switch key {
		case "phase":
			event.Phase = value
		case "event":
			event.Event = value
		}
	}
	return event, event.Phase != "" && event.Event != ""
}

func runtimeLogEventFailure(event runtimeLogEvent) string {
	if event.Phase != "config-check" || event.Event != "end" || event.Fields["status"] != "failed" {
		return ""
	}
	if exitCode := strings.TrimSpace(event.Fields["exit"]); exitCode != "" {
		return "config check failed (exit code " + exitCode + ")"
	}
	return "config check failed"
}

func cleanRuntimeLogSnippetLine(line string) string {
	line = strings.TrimSpace(line)
	if _, after, ok := strings.Cut(line, " | "); ok {
		line = strings.TrimSpace(after)
	}
	fields := strings.Fields(line)
	if len(fields) > 1 && strings.Contains(fields[0], "T") && strings.HasSuffix(fields[0], "Z") {
		line = strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	}
	return line
}
