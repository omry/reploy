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

type recorder struct {
	command *exec.Cmd
	input   io.WriteCloser
	stderr  bytes.Buffer
	cast    string
	output  synchronizedBuffer
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
	Cast                     string `json:"cast"`
	CastBytes                int64  `json:"cast_bytes"`
	BrowserScreenshot        string `json:"browser_screenshot,omitempty"`
	TerminalMarkersVerified  bool   `json:"terminal_markers_verified,omitempty"`
	RecorderFailed           bool   `json:"recorder_failed,omitempty"`
	OutputFinalizationStatus string `json:"output_finalization_status"`
	OutputFinalizationReason string `json:"output_finalization_reason,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "omegaflow conformance controller: %v\n", err)
		if outputDir := os.Getenv("REPLOY_OUTPUT_DIR"); outputDir != "" {
			_ = os.WriteFile(filepath.Join(outputDir, "controller-error.txt"), []byte(err.Error()+"\n"), 0o644)
		}
		os.Exit(1)
	}
}

func run(args []string) (resultErr error) {
	if len(args) != 1 || args[0] != "success" && args[0] != "failed-output-finalization" {
		return fmt.Errorf("usage: omegaflow-conformance-controller {success | failed-output-finalization}")
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
	cast := filepath.Join(outputDir, args[0]+".cast")
	recording, err := startRecorder(ready.TerminalSocket, cast)
	if err != nil {
		return err
	}
	defer func() {
		if recording.command.Process != nil && recording.command.ProcessState == nil {
			_ = syscall.Kill(-recording.command.Process.Pid, syscall.SIGKILL)
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
		return runSuccess(outputDir, opened, client, recording)
	}
	if err := runFailedOutputFinalization(outputDir, client, recording); err != nil {
		return err
	}
	return nil
}

func runSuccess(outputDir string, opened event, client *broker, recording *recorder) error {
	web, err := selectEndpoint(opened.Endpoints, "web")
	if err != nil {
		return err
	}
	if err := recording.writeString("printf 'OPERATION-ONE\\n'; cd /tmp; trap 'printf \\\"\\\\036INTERRUPT-DONE\\\\n\\\"' INT; printf '\\036SLEEP-ACTIVE\\n'; sleep 30; trap - INT\n"); err != nil {
		return err
	}
	if err := recording.waitFor(append([]byte{0x1e}, []byte("SLEEP-ACTIVE")...), 10*time.Second); err != nil {
		return err
	}
	if _, err := recording.input.Write([]byte{0x03}); err != nil {
		return fmt.Errorf("send terminal Ctrl-C: %w", err)
	}
	if err := recording.waitFor(append([]byte{0x1e}, []byte("INTERRUPT-DONE")...), 10*time.Second); err != nil {
		return err
	}
	if err := client.write(map[string]any{"schema": controllerStreamSchema, "type": "resize", "columns": 100, "rows": 31}); err != nil {
		return err
	}
	command := "printf 'OPERATION-TWO\\n'; printf 'CWD='; pwd; printf 'SIZE='; stty size; " +
		"mkdir -p /mnt/reploy-home/site; printf '%s\\n' '<!doctype html><title>OmegaFlow handoff</title><h1 id=proof>browser-proof</h1>' > /mnt/reploy-home/site/index.html; " +
		"python3 -m http.server 8080 --bind 0.0.0.0 --directory /mnt/reploy-home/site >/mnt/reploy-home/server.log 2>&1 & " +
		"for attempt in 1 2 3 4 5 6 7 8 9 10; do python3 -c 'import urllib.request; urllib.request.urlopen(\"http://127.0.0.1:8080/\", timeout=1).read()' >/dev/null 2>&1 && break; sleep 0.25; done; " +
		"python3 -c 'import urllib.request; urllib.request.urlopen(\"http://127.0.0.1:8080/\", timeout=1).read()' >/dev/null && printf '\\036SERVICE-STARTED\\n'\n"
	if err := recording.writeString(command); err != nil {
		return err
	}
	if err := recording.waitFor(append([]byte{0x1e}, []byte("SERVICE-STARTED")...), 10*time.Second); err != nil {
		return err
	}
	screenshot := filepath.Join(outputDir, "browser.png")
	url := fmt.Sprintf("%s://%s:%d/", web.Scheme, web.Host, web.Port)
	if err := runBrowserProof(url, screenshot); err != nil {
		return err
	}
	if err := recording.writeString("printf 'BROWSER-DONE\\n'; exit 0\n"); err != nil {
		return err
	}
	if err := recording.input.Close(); err != nil {
		return fmt.Errorf("close recorder input: %w", err)
	}
	finalization, err := client.readUntilOutputFinalization(30 * time.Second)
	if err != nil {
		return err
	}
	if finalization.status != "drained" {
		return fmt.Errorf("workload output finalization = %q: %s", finalization.status, finalization.reason)
	}
	if err := recording.command.Wait(); err != nil {
		return fmt.Errorf("asciinema success recording: %w: %s", err, recording.stderr.String())
	}
	raw := filepath.Join(outputDir, "terminal.txt")
	convert := exec.Command("asciinema", "convert", "--quiet", "--output-format", "raw", recording.cast, raw)
	if output, err := convert.CombinedOutput(); err != nil {
		return fmt.Errorf("convert success cast: %w: %s", err, output)
	}
	payload, err := os.ReadFile(raw)
	if err != nil {
		return fmt.Errorf("read converted cast: %w", err)
	}
	for _, marker := range []string{"OPERATION-ONE", "OPERATION-TWO", "CWD=/tmp", "SIZE=31 100", "SERVICE-STARTED", "BROWSER-DONE", "^C"} {
		if !bytes.Contains(payload, []byte(marker)) {
			return fmt.Errorf("converted cast is missing %q", marker)
		}
	}
	info, err := os.Stat(recording.cast)
	if err != nil {
		return err
	}
	if err := writeProof(outputDir, proof{
		Schema: "reploy-omegaflow-conformance-proof-v1", Scenario: "success",
		Cast: filepath.Base(recording.cast), CastBytes: info.Size(), BrowserScreenshot: filepath.Base(screenshot),
		TerminalMarkersVerified: true, OutputFinalizationStatus: finalization.status,
	}); err != nil {
		return err
	}
	return finishBroker(client, "drained")
}

func runFailedOutputFinalization(outputDir string, client *broker, recording *recorder) error {
	command := "cat > /mnt/reploy-home/fail.py <<'PY'\n" +
		"import os\nimport signal\nimport time\n\n" +
		"def terminate(*_):\n    time.sleep(5)\n    raise SystemExit(0)\n\n" +
		"signal.signal(signal.SIGTERM, terminate)\n" +
		"os.write(1, b'\\x1eFAILURE-READY\\n')\n" +
		"signal.pause()\nPY\n" +
		"exec python3 /mnt/reploy-home/fail.py\n"
	if err := recording.writeString(command); err != nil {
		return err
	}
	if err := recording.waitFor(append([]byte{0x1e}, []byte("FAILURE-READY")...), 10*time.Second); err != nil {
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
	fmt.Fprintln(os.Stderr, "OMEGAFLOW-CONFORMANCE-TERMINATING")
	finalization, err := client.readUntilOutputFinalization(45 * time.Second)
	if err != nil {
		return err
	}
	if finalization.status != "failed" || finalization.reason == "" {
		return fmt.Errorf("workload output finalization = %q: %s", finalization.status, finalization.reason)
	}
	if err := recording.input.Close(); err != nil {
		return fmt.Errorf("close failed recorder input: %w", err)
	}
	recorderErr := recording.command.Wait()
	if recorderErr == nil {
		return fmt.Errorf("asciinema unexpectedly reported successful attachment finalization")
	}
	payload, err := os.ReadFile(recording.cast)
	if err != nil {
		return fmt.Errorf("read partial cast: %w", err)
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' || !bytes.Contains(payload, []byte("FAILURE-READY")) {
		return fmt.Errorf("partial cast was not retained as a closed asciinema stream")
	}
	if err := writeProof(outputDir, proof{
		Schema: "reploy-omegaflow-conformance-proof-v1", Scenario: "failed-output-finalization",
		Cast: filepath.Base(recording.cast), CastBytes: int64(len(payload)), RecorderFailed: true,
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

func startRecorder(socket string, cast string) (*recorder, error) {
	if socket == "" {
		return nil, fmt.Errorf("broker did not report a terminal socket")
	}
	attachment := "reploy-session-client attach --socket " + shellQuote(socket)
	asciinema := "asciinema record --quiet --window-size 80x24 --return --command " + shellQuote(attachment) + " " + shellQuote(cast)
	command := exec.Command("script", "--quiet", "--return", "--echo", "never", "--command", asciinema, "/dev/null")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	result := &recorder{command: command, input: input, cast: cast}
	command.Stderr = &result.stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start asciinema: %w", err)
	}
	go func() { _, _ = io.Copy(&result.output, output) }()
	return result, nil
}

func (recording *recorder) writeString(value string) error {
	if _, err := io.WriteString(recording.input, value); err != nil {
		return fmt.Errorf("write recorder input: %w", err)
	}
	return nil
}

func (recording *recorder) waitFor(marker []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if recording.output.contains(marker) {
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
	command := exec.Command("node", "/opt/omegaflow/browser-proof.js", url, screenshot)
	command.Env = append(os.Environ(), "NODE_PATH=/opt/omegaflow/node_modules")
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
		return fmt.Errorf("write conformance proof: %w", err)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
