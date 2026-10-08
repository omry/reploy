package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const controllerStreamSchema = "reploy-controlled-session-client-v1"

type endpoint struct {
	ID     string `json:"id"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
}

type lifecycleResult struct {
	Cause          string `json:"cause"`
	WorkloadStatus struct {
		Kind   string `json:"kind"`
		Code   *int   `json:"code,omitempty"`
		Reason string `json:"reason,omitempty"`
	} `json:"workload_status"`
	WorkloadOutputFinalizationStatus struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason,omitempty"`
	} `json:"workload_output_finalization_status"`
	RuntimeObservationStatus struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason,omitempty"`
	} `json:"runtime_observation_status"`
	ControllerFinalizationStatus struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason,omitempty"`
	} `json:"controller_finalization_status"`
	CleanupStatus struct {
		Kind    string `json:"kind"`
		Message string `json:"message,omitempty"`
	} `json:"cleanup_status"`
	RecoveryAction string `json:"recovery_action"`
}

type event struct {
	Schema                                string           `json:"schema"`
	Type                                  string           `json:"type"`
	TerminalSocket                        string           `json:"terminal_socket,omitempty"`
	Operations                            []string         `json:"operations,omitempty"`
	Endpoints                             []endpoint       `json:"endpoints,omitempty"`
	Columns                               uint32           `json:"columns,omitempty"`
	Rows                                  uint32           `json:"rows,omitempty"`
	OutputFinalizationTimeoutMilliseconds uint32           `json:"output_finalization_timeout_milliseconds,omitempty"`
	Status                                json.RawMessage  `json:"status,omitempty"`
	Cause                                 string           `json:"cause,omitempty"`
	Reason                                string           `json:"reason,omitempty"`
	Code                                  string           `json:"code,omitempty"`
	Message                               string           `json:"message,omitempty"`
	Result                                *lifecycleResult `json:"result,omitempty"`
}

type broker struct {
	command *exec.Cmd
	input   io.WriteCloser
	events  *bufio.Scanner
	stderr  bytes.Buffer
	waited  bool
}

type terminalCapture struct {
	command      *exec.Cmd
	input        io.WriteCloser
	stderr       bytes.Buffer
	artifactPath string
	artifact     *os.File
	output       synchronizedBuffer
}

type synchronizedBuffer struct {
	mu      sync.Mutex
	payload []byte
}

func (buffer *synchronizedBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.payload = append(buffer.payload, payload...)
	return len(payload), nil
}

func (buffer *synchronizedBuffer) contains(marker []byte) bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return bytes.Contains(buffer.payload, marker)
}

type proof struct {
	Schema                   string `json:"schema"`
	Scenario                 string `json:"scenario"`
	Terminal                 string `json:"terminal"`
	TerminalBytes            int64  `json:"terminal_bytes"`
	BrowserScreenshot        string `json:"browser_screenshot,omitempty"`
	TerminalMarkersVerified  bool   `json:"terminal_markers_verified,omitempty"`
	AttachmentFailed         bool   `json:"attachment_failed,omitempty"`
	OutputFinalizationStatus string `json:"output_finalization_status"`
	OutputFinalizationReason string `json:"output_finalization_reason,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "reploy-contract contract controller: %v\n", err)
		if outputDir := os.Getenv("REPLOY_OUTPUT_DIR"); outputDir != "" {
			_ = os.WriteFile(filepath.Join(outputDir, "controller-error.txt"), []byte(err.Error()+"\n"), 0o644)
		}
		os.Exit(1)
	}
}

func run(args []string) (resultErr error) {
	if len(args) != 1 || args[0] != "success" && args[0] != "failed-output-finalization" {
		return fmt.Errorf("usage: reploy-contract-controller {success | failed-output-finalization}")
	}
	outputDir := os.Getenv("REPLOY_OUTPUT_DIR")
	if outputDir == "" {
		return fmt.Errorf("REPLOY_OUTPUT_DIR is required")
	}
	client, err := startBroker()
	if err != nil {
		return err
	}
	defer func() {
		if client.command.Process != nil && client.command.ProcessState == nil {
			_ = client.command.Process.Kill()
		}
		if !client.waited {
			resultErr = errors.Join(resultErr, client.wait())
		}
	}()

	ready, err := client.read("broker-ready", 10*time.Second)
	if err != nil {
		return err
	}
	artifactPath := filepath.Join(outputDir, args[0]+".raw")
	capture, err := startTerminalCapture(ready.TerminalSocket, artifactPath)
	if err != nil {
		return err
	}
	defer func() {
		if capture.command.Process != nil && capture.command.ProcessState == nil {
			_ = syscall.Kill(-capture.command.Process.Pid, syscall.SIGKILL)
			_ = capture.wait()
		}
	}()

	opened, err := client.read("opened", 10*time.Second)
	if err != nil {
		return err
	}
	if _, err := client.read("ready", 30*time.Second); err != nil {
		return err
	}
	if args[0] == "success" {
		return runSuccess(outputDir, opened, client, capture)
	}
	if err := runFailedOutputFinalization(outputDir, client, capture); err != nil {
		return err
	}
	return nil
}

func runSuccess(outputDir string, opened event, client *broker, capture *terminalCapture) error {
	web, err := selectEndpoint(opened.Endpoints, "web")
	if err != nil {
		return err
	}
	if err := capture.writeString("printf 'OPERATION-ONE\\n'; cd /tmp; trap 'printf \\\"\\\\036INTERRUPT-DONE\\\\n\\\"' INT; printf '\\036SLEEP-ACTIVE\\n'; sleep 30; trap - INT\n"); err != nil {
		return err
	}
	if err := capture.waitFor(append([]byte{0x1e}, []byte("SLEEP-ACTIVE")...), 10*time.Second); err != nil {
		return err
	}
	if _, err := capture.input.Write([]byte{0x03}); err != nil {
		return fmt.Errorf("send terminal Ctrl-C: %w", err)
	}
	if err := capture.waitFor(append([]byte{0x1e}, []byte("INTERRUPT-DONE")...), 10*time.Second); err != nil {
		return err
	}
	canonicalCommand := "printf '\\036CANONICAL-OK\\n'\n"
	if err := capture.writeString(canonicalCommand); err != nil {
		return err
	}
	if err := capture.waitFor([]byte("\x1eCANONICAL-OK"), 10*time.Second); err != nil {
		return err
	}
	if err := capture.writeString("stty raw -echo; printf '\\036RAW-ACTIVE\\n'; dd bs=1 count=4 2>/dev/null | od -An -t x1; stty sane; printf '\\036RAW-DONE\\n'\n"); err != nil {
		return err
	}
	if err := capture.waitFor([]byte("\x1eRAW-ACTIVE"), 10*time.Second); err != nil {
		return err
	}
	if _, err := capture.input.Write([]byte{0, 1, 0x7f, 0xff}); err != nil {
		return err
	}
	if err := capture.waitFor([]byte("\x1eRAW-DONE"), 10*time.Second); err != nil {
		return err
	}
	if err := capture.writeString("python3 -c 'import os; os.write(1, b\"\\x01\\x02\\x7f\\xff\" + b\"LARGE-ORDER-\" + b\"0123456789\" * 16384 + b\"-END\\n\"); os.write(1, b\"\\x1eLARGE-DONE\\n\")'\n"); err != nil {
		return err
	}
	if err := capture.waitFor([]byte("\x1eLARGE-DONE"), 10*time.Second); err != nil {
		return err
	}
	if err := client.write(map[string]any{"schema": controllerStreamSchema, "type": "resize", "columns": 100, "rows": 31}); err != nil {
		return err
	}
	// Control requests and terminal input use separate transports. Observe the
	// applied size before continuing instead of assuming cross-channel ordering.
	if err := capture.writeString("for attempt in $(seq 1 100); do size=$(stty size); [ \"$size\" = '31 100' ] && break; sleep 0.05; done; [ \"$size\" = '31 100' ] && printf '\\036SIZE=31 100\\n'\n"); err != nil {
		return err
	}
	if err := capture.waitFor([]byte("\x1eSIZE=31 100"), 10*time.Second); err != nil {
		return err
	}
	command := "printf 'OPERATION-TWO\\n'; printf 'CWD='; pwd; " +
		"mkdir -p /mnt/reploy-home/site; printf '%s\\n' '<!doctype html><title>Reploy handoff</title><h1 id=proof>browser-proof</h1>' > /mnt/reploy-home/site/index.html; " +
		"python3 -m http.server 8080 --bind 0.0.0.0 --directory /mnt/reploy-home/site >/mnt/reploy-home/server.log 2>&1 & " +
		"for attempt in 1 2 3 4 5 6 7 8 9 10; do python3 -c 'import urllib.request; urllib.request.urlopen(\"http://127.0.0.1:8080/\", timeout=1).read()' >/dev/null 2>&1 && break; sleep 0.25; done; " +
		"python3 -c 'import urllib.request; urllib.request.urlopen(\"http://127.0.0.1:8080/\", timeout=1).read()' >/dev/null && printf '\\036SERVICE-STARTED\\n'\n"
	if err := capture.writeString(command); err != nil {
		return err
	}
	if err := capture.waitFor(append([]byte{0x1e}, []byte("SERVICE-STARTED")...), 10*time.Second); err != nil {
		return err
	}
	screenshot := filepath.Join(outputDir, "browser.png")
	url := fmt.Sprintf("%s://%s:%d/", web.Scheme, web.Host, web.Port)
	if err := runBrowserProof(url, screenshot); err != nil {
		return err
	}
	if err := capture.writeString("printf 'BROWSER-DONE\\n'; exit 0\n"); err != nil {
		return err
	}
	if err := capture.input.Close(); err != nil {
		return fmt.Errorf("close terminal capture input: %w", err)
	}
	finalization, err := client.readUntilOutputFinalization(30 * time.Second)
	if err != nil {
		return err
	}
	if finalization.status != "drained" {
		return fmt.Errorf("workload output finalization = %q: %s", finalization.status, finalization.reason)
	}
	if err := capture.wait(); err != nil {
		return fmt.Errorf("direct attachment success capture: %w: %s", err, capture.stderr.String())
	}
	payload, err := os.ReadFile(capture.artifactPath)
	if err != nil {
		return fmt.Errorf("read closed terminal artifact: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "terminal.txt"), payload, 0o600); err != nil {
		return err
	}
	for _, marker := range []string{"OPERATION-ONE", "OPERATION-TWO", "CWD=/tmp", "SIZE=31 100", "SERVICE-STARTED", "BROWSER-DONE", "^C"} {
		if !bytes.Contains(payload, []byte(marker)) {
			return fmt.Errorf("closed terminal artifact is missing %q", marker)
		}
	}
	if !bytes.Contains(payload, []byte("00 01 7f ff")) || !bytes.Contains(payload, []byte{1, 2, 0x7f, 0xff}) {
		return fmt.Errorf("raw terminal input or binary output changed")
	}
	large := append([]byte("LARGE-ORDER-"), bytes.Repeat([]byte("0123456789"), 16384)...)
	large = append(large, []byte("-END")...)
	if !bytes.Contains(payload, large) {
		return fmt.Errorf("large terminal output was truncated or reordered")
	}
	if bytes.Count(payload, []byte(strings.TrimSuffix(canonicalCommand, "\n"))) != 1 || bytes.Count(payload, []byte("\x1eCANONICAL-OK")) != 1 {
		return fmt.Errorf("canonical terminal input was lost or echoed twice")
	}
	info, err := os.Stat(capture.artifactPath)
	if err != nil {
		return err
	}
	if err := writeProof(outputDir, proof{
		Schema: "reploy-controlled-session-contract-proof-v1", Scenario: "success",
		Terminal: filepath.Base(capture.artifactPath), TerminalBytes: info.Size(), BrowserScreenshot: filepath.Base(screenshot),
		TerminalMarkersVerified: true, OutputFinalizationStatus: finalization.status,
	}); err != nil {
		return err
	}
	return finishBroker(client, "drained")
}

func runFailedOutputFinalization(outputDir string, client *broker, capture *terminalCapture) error {
	command := "cat > /mnt/reploy-home/fail.py <<'PY'\n" +
		"import os\nimport signal\nimport time\n\n" +
		"def terminate(*_):\n    time.sleep(5)\n    raise SystemExit(0)\n\n" +
		"signal.signal(signal.SIGTERM, terminate)\n" +
		"os.write(1, b'\\x1eFAILURE-READY\\n')\n" +
		"signal.pause()\nPY\n" +
		"exec python3 /mnt/reploy-home/fail.py\n"
	if err := capture.writeString(command); err != nil {
		return err
	}
	if err := capture.waitFor(append([]byte{0x1e}, []byte("FAILURE-READY")...), 10*time.Second); err != nil {
		return err
	}
	if err := client.write(map[string]any{"schema": controllerStreamSchema, "type": "terminate"}); err != nil {
		return err
	}
	if err := client.readUntil("terminating", 10*time.Second); err != nil {
		return err
	}
	// The host-side test waits for this container-log marker before suspending
	// the public host process across its absolute output-finalization deadline.
	// The controller and its private channel remain live throughout the injected
	// observation fault.
	fmt.Fprintln(os.Stderr, "REPLOY-CONTRACT-TERMINATING")
	finalization, err := client.readUntilOutputFinalization(45 * time.Second)
	if err != nil {
		return err
	}
	if finalization.status != "failed" || finalization.reason == "" {
		return fmt.Errorf("workload output finalization = %q: %s", finalization.status, finalization.reason)
	}
	if err := capture.input.Close(); err != nil {
		return fmt.Errorf("close failed terminalCapture input: %w", err)
	}
	terminalCaptureErr := capture.wait()
	var attachmentExit *exec.ExitError
	if !errors.As(terminalCaptureErr, &attachmentExit) || attachmentExit.ExitCode() != 1 {
		return fmt.Errorf("direct attachment failed-finalization exit = %v, want exit 1", terminalCaptureErr)
	}
	payload, err := os.ReadFile(capture.artifactPath)
	if err != nil {
		return fmt.Errorf("read partial terminal artifact: %w", err)
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' || !bytes.Contains(payload, []byte("FAILURE-READY")) {
		return fmt.Errorf("closed partial terminal artifact was not retained")
	}
	if err := writeProof(outputDir, proof{
		Schema: "reploy-controlled-session-contract-proof-v1", Scenario: "failed-output-finalization",
		Terminal: filepath.Base(capture.artifactPath), TerminalBytes: int64(len(payload)), AttachmentFailed: true,
		OutputFinalizationStatus: finalization.status, OutputFinalizationReason: finalization.reason,
	}); err != nil {
		return err
	}
	return finishBroker(client, "failed")
}

func (client *broker) readUntil(want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		event, err := client.readAny(time.Until(deadline))
		if err != nil {
			return err
		}
		if event.Type == want {
			return nil
		}
		if event.Type != "diagnostic" && event.Type != "workload-exit" {
			return fmt.Errorf("controller event = %q while waiting for %q", event.Type, want)
		}
	}
	return fmt.Errorf("timed out waiting for controller event %q", want)
}

type outputFinalization struct {
	status string
	reason string
}

func (client *broker) readUntilOutputFinalization(timeout time.Duration) (outputFinalization, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		event, err := client.readAny(time.Until(deadline))
		if err != nil {
			return outputFinalization{}, err
		}
		switch event.Type {
		case "diagnostic", "workload-exit", "terminating":
			continue
		case "workload-outputs-finalized":
			var status string
			if err := json.Unmarshal(event.Status, &status); err != nil {
				return outputFinalization{}, fmt.Errorf("decode workload output status: %w", err)
			}
			return outputFinalization{status: status, reason: event.Reason}, nil
		case "client-error":
			return outputFinalization{}, fmt.Errorf("controlled-session client error %q: %s", event.Code, event.Message)
		default:
			return outputFinalization{}, fmt.Errorf("unexpected controller event before output finalization: %s", event.Type)
		}
	}
	return outputFinalization{}, fmt.Errorf("timed out waiting for workload output finalization")
}

func finishBroker(client *broker, wantOutputStatus string) error {
	if err := client.write(map[string]any{"schema": controllerStreamSchema, "type": "complete"}); err != nil {
		return err
	}
	terminated, err := client.read("terminated", 30*time.Second)
	if err != nil {
		return err
	}
	if terminated.Result == nil || terminated.Result.WorkloadOutputFinalizationStatus.Kind != wantOutputStatus || terminated.Result.ControllerFinalizationStatus.Kind != "completed" {
		return fmt.Errorf("authoritative terminated result is inconsistent: %#v", terminated.Result)
	}
	if err := client.write(map[string]any{"schema": controllerStreamSchema, "type": "acknowledge-terminated"}); err != nil {
		return err
	}
	if err := client.input.Close(); err != nil {
		return fmt.Errorf("close broker input: %w", err)
	}
	if err := client.wait(); err != nil {
		return fmt.Errorf("session broker: %w: %s", err, client.stderr.String())
	}
	return nil
}

func (client *broker) wait() error {
	client.waited = true
	return client.command.Wait()
}

func startBroker() (*broker, error) {
	command := exec.Command("reploy-session-client", "client")
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &broker{command: command, input: input}
	client.events = bufio.NewScanner(output)
	client.events.Buffer(make([]byte, 4096), 1<<20)
	command.Stderr = &client.stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start session broker: %w", err)
	}
	return client, nil
}

func (client *broker) read(want string, timeout time.Duration) (event, error) {
	event, err := client.readAny(timeout)
	if err != nil {
		return event, err
	}
	if event.Type != want {
		return event, fmt.Errorf("controller event = %q, want %q", event.Type, want)
	}
	return event, nil
}

func (client *broker) readAny(timeout time.Duration) (event, error) {
	type result struct {
		event event
		err   error
	}
	done := make(chan result, 1)
	go func() {
		if !client.events.Scan() {
			err := client.events.Err()
			if err == nil {
				err = io.EOF
			}
			done <- result{err: err}
			return
		}
		value, err := decodeEvent(client.events.Bytes())
		done <- result{event: value, err: err}
	}()
	select {
	case value := <-done:
		if value.err != nil {
			return event{}, fmt.Errorf("read controller event: %w: %s", value.err, client.stderr.String())
		}
		return value.event, nil
	case <-time.After(timeout):
		return event{}, fmt.Errorf("timed out waiting for controller event")
	}
}

func decodeEvent(payload []byte) (event, error) {
	if err := rejectDuplicateJSONFields(payload); err != nil {
		return event{}, err
	}
	var envelope struct {
		Schema string `json:"schema"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return event{}, err
	}
	if envelope.Schema != controllerStreamSchema {
		return event{}, fmt.Errorf("controller event schema = %q", envelope.Schema)
	}
	value := event{Schema: envelope.Schema, Type: envelope.Type}
	switch envelope.Type {
	case "broker-ready":
		var wire struct {
			Schema         string `json:"schema"`
			Type           string `json:"type"`
			TerminalSocket string `json:"terminal_socket"`
		}
		if err := decodeStrictEvent(payload, &wire); err != nil {
			return event{}, err
		}
		value.TerminalSocket = wire.TerminalSocket
	case "opened":
		var wire struct {
			Schema                                string     `json:"schema"`
			Type                                  string     `json:"type"`
			Operations                            []string   `json:"operations"`
			Endpoints                             []endpoint `json:"endpoints"`
			Columns                               uint32     `json:"columns"`
			Rows                                  uint32     `json:"rows"`
			OutputFinalizationTimeoutMilliseconds uint32     `json:"output_finalization_timeout_milliseconds"`
		}
		if err := decodeStrictEvent(payload, &wire); err != nil {
			return event{}, err
		}
		value.Operations, value.Endpoints = wire.Operations, wire.Endpoints
		value.Columns, value.Rows = wire.Columns, wire.Rows
		value.OutputFinalizationTimeoutMilliseconds = wire.OutputFinalizationTimeoutMilliseconds
	case "ready":
		if err := decodeStrictEvent(payload, &struct {
			Schema string `json:"schema"`
			Type   string `json:"type"`
		}{}); err != nil {
			return event{}, err
		}
	case "workload-exit":
		var wire struct {
			Schema string          `json:"schema"`
			Type   string          `json:"type"`
			Status json.RawMessage `json:"status"`
		}
		if err := decodeStrictEvent(payload, &wire); err != nil {
			return event{}, err
		}
		var status struct {
			Kind   string `json:"kind"`
			Code   *int   `json:"code,omitempty"`
			Reason string `json:"reason,omitempty"`
		}
		if err := decodeStrictEvent(wire.Status, &status); err != nil {
			return event{}, fmt.Errorf("decode workload-exit status: %w", err)
		}
		value.Status = wire.Status
	case "terminating":
		var wire struct {
			Schema string `json:"schema"`
			Type   string `json:"type"`
			Cause  string `json:"cause"`
		}
		if err := decodeStrictEvent(payload, &wire); err != nil {
			return event{}, err
		}
		value.Cause = wire.Cause
	case "diagnostic", "client-error":
		var wire struct {
			Schema  string `json:"schema"`
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := decodeStrictEvent(payload, &wire); err != nil {
			return event{}, err
		}
		if !validDiagnosticCode(wire.Code) {
			return event{}, fmt.Errorf("controller event code %q is invalid", wire.Code)
		}
		value.Code, value.Message = wire.Code, wire.Message
	case "workload-outputs-finalized":
		var wire struct {
			Schema string          `json:"schema"`
			Type   string          `json:"type"`
			Status json.RawMessage `json:"status"`
			Reason string          `json:"reason,omitempty"`
		}
		if err := decodeStrictEvent(payload, &wire); err != nil {
			return event{}, err
		}
		value.Status, value.Reason = wire.Status, wire.Reason
	case "terminated":
		var wire struct {
			Schema string           `json:"schema"`
			Type   string           `json:"type"`
			Result *lifecycleResult `json:"result"`
		}
		if err := decodeStrictEvent(payload, &wire); err != nil {
			return event{}, err
		}
		value.Result = wire.Result
	default:
		return event{}, fmt.Errorf("controller event type %q is unsupported", envelope.Type)
	}
	return value, nil
}

func decodeStrictEvent(payload []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("controller event must contain exactly one JSON object")
	}
	return nil
}

func rejectDuplicateJSONFields(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON contains trailing token %v", token)
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if !validJSONFieldName(key) {
				return fmt.Errorf("JSON object field %q is not lowercase ASCII snake_case", key)
			}
			if seen[key] {
				return fmt.Errorf("JSON object repeats field %q", key)
			}
			seen[key] = true
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("JSON array is not closed")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func validJSONFieldName(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' {
			continue
		}
		if index != 0 && (character == '_' || character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func validDiagnosticCode(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func (client *broker) write(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := client.input.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write controller request: %w", err)
	}
	return nil
}

func startTerminalCapture(socket string, artifactPath string) (*terminalCapture, error) {
	if socket == "" {
		return nil, fmt.Errorf("broker did not report a terminal socket")
	}
	attachment := "stty rows 24 cols 80; exec reploy-session-client attach --socket " + shellQuote(socket)
	command := exec.Command("script", "--quiet", "--return", "--echo", "never", "--command", attachment, "/dev/null")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	artifact, err := os.OpenFile(artifactPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, errors.Join(err, artifact.Close())
	}
	result := &terminalCapture{command: command, input: input, artifactPath: artifactPath, artifact: artifact}
	command.Stderr = &result.stderr
	// Cmd.Wait joins its internal output-copy goroutine before returning. Only
	// then may we close and consume the artifact or complete the broker.
	command.Stdout = io.MultiWriter(artifact, &result.output)
	if err := command.Start(); err != nil {
		return nil, errors.Join(fmt.Errorf("start direct attachment: %w", err), input.Close(), artifact.Close())
	}
	return result, nil
}

func (capture *terminalCapture) wait() error {
	waitErr := capture.command.Wait()
	if err := capture.artifact.Close(); err != nil {
		return fmt.Errorf("close terminal artifact after process result %v: %w", waitErr, err)
	}
	return waitErr
}

func (capture *terminalCapture) writeString(value string) error {
	if _, err := io.WriteString(capture.input, value); err != nil {
		return fmt.Errorf("write terminal capture input: %w", err)
	}
	return nil
}

func (capture *terminalCapture) waitFor(marker []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if capture.output.contains(marker) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for terminal action marker %q", marker)
}

func selectEndpoint(endpoints []endpoint, id string) (endpoint, error) {
	for _, candidate := range endpoints {
		if candidate.ID == id && candidate.Scheme != "" && candidate.Host != "" && candidate.Port != 0 {
			return candidate, nil
		}
	}
	return endpoint{}, fmt.Errorf("opened event did not grant endpoint %q", id)
}

func runBrowserProof(url string, screenshot string) error {
	command := exec.Command("node", "/opt/reploy-contract/browser-proof.js", url, screenshot)
	command.Env = append(os.Environ(), "NODE_PATH=/opt/reploy-contract/node_modules")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("Playwright Chromium proof: %w: %s", err, output)
	}
	return nil
}

func writeProof(outputDir string, value proof) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(filepath.Join(outputDir, value.Scenario+"-proof.json"), payload, 0o644); err != nil {
		return fmt.Errorf("write contract proof: %w", err)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
