//go:build linux

package controlledsession

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type asciinemaFixtureMetadataV1 struct {
	Schema  string `json:"schema"`
	Version string `json:"version"`
	Asset   string `json:"asset"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

func TestTerminalAttachmentRunsBeneathPinnedUnmodifiedAsciinemaV1(t *testing.T) {
	asciinema := os.Getenv("REPLOY_ASCIINEMA_FIXTURE")
	if asciinema == "" {
		t.Skip("set REPLOY_ASCIINEMA_FIXTURE to the pinned asciinema 3.x executable")
	}
	fixture := readAsciinemaFixtureMetadataV1(t)
	payload, err := os.ReadFile(asciinema)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if got := hex.EncodeToString(digest[:]); got != fixture.SHA256 {
		t.Fatalf("asciinema fixture SHA-256 = %s, want %s", got, fixture.SHA256)
	}
	version, err := exec.Command(asciinema, "--version").CombinedOutput()
	wantVersion := "asciinema " + fixture.Version
	if err != nil || strings.TrimSpace(string(version)) != wantVersion {
		t.Fatalf("asciinema fixture version = %q, %v; want %q", version, err, wantVersion)
	}

	temporaryHome := shortControllerBrokerTempHomeV1(t)
	sessionClient := filepath.Join(t.TempDir(), "reploy-session-client")
	linkerValue := "github.com/omry/reploy/internal/controlledsession.controllerTerminalAttachmentHomeV1=" + temporaryHome
	build := exec.Command("go", "build", "-buildvcs=false", "-ldflags", "-X "+linkerValue, "-o", sessionClient, "../../cmd/reploy-session-client")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build reploy-session-client fixture: %v\n%s", err, output)
	}

	hostListener, hostSocket := newControllerBrokerHostListenerV1(t)
	defer hostListener.Close()
	hostDone := make(chan error, 1)
	go runControllerAsciinemaTestHostV1(hostListener, hostDone)
	publicInputReader, publicInputWriter := io.Pipe()
	defer publicInputWriter.Close()
	publicOutputReader, publicOutputWriter := io.Pipe()
	defer publicOutputReader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	brokerDone := make(chan error, 1)
	go func() {
		err := RunControllerBrokerV1(ctx, ControllerBrokerOptionsV1{
			SessionSocket: hostSocket,
			TemporaryHome: temporaryHome,
			Input:         publicInputReader,
			Output:        publicOutputWriter,
		})
		_ = publicOutputWriter.CloseWithError(err)
		brokerDone <- err
	}()
	public := bufio.NewReader(publicOutputReader)
	brokerReady := readControllerBrokerJSONLineV1(t, public)
	terminalSocket, ok := brokerReady["terminal_socket"].(string)
	if brokerReady["type"] != string(ControllerStreamEventBrokerReadyV1) || !ok {
		t.Fatalf("broker-ready event = %#v", brokerReady)
	}

	cast := filepath.Join(t.TempDir(), "attachment.cast")
	command := shellQuoteTerminalFixtureV1(sessionClient) + " attach --socket " + shellQuoteTerminalFixtureV1(terminalSocket)
	record := exec.CommandContext(ctx, asciinema, "record", "--quiet", "--headless", "--window-size", "80x24", "--return", "--command", command, cast)
	var recorderStderr bytes.Buffer
	record.Stderr = &recorderStderr
	recorderDone := make(chan error, 1)
	go func() { recorderDone <- record.Run() }()

	for _, kind := range []ControllerStreamEventKindV1{
		ControllerStreamEventOpenedV1,
		ControllerStreamEventReadyV1,
		ControllerStreamEventWorkloadExitV1,
		ControllerStreamEventTerminatingV1,
		ControllerStreamEventWorkloadOutputsFinalizedV1,
	} {
		if event := readControllerBrokerJSONLineV1(t, public); event["type"] != string(kind) {
			t.Fatalf("public event = %#v, want %q", event, kind)
		}
	}
	if err := <-recorderDone; err != nil {
		t.Fatalf("asciinema record: %v\n%s", err, recorderStderr.String())
	}
	select {
	case err := <-brokerDone:
		t.Fatalf("broker exited before recorder finalization: %v", err)
	default:
	}
	if info, err := os.Stat(cast); err != nil || info.Size() == 0 {
		t.Fatalf("asciinema cast = %#v, %v", info, err)
	}
	raw := filepath.Join(t.TempDir(), "attachment.raw")
	convert := exec.CommandContext(ctx, asciinema, "convert", "--quiet", "--output-format", "raw", cast, raw)
	if output, err := convert.CombinedOutput(); err != nil {
		t.Fatalf("convert asciinema cast: %v\n%s", err, output)
	}
	content, err := os.ReadFile(raw)
	if err != nil || !bytes.HasSuffix(content, []byte("recorded output\r\n")) || bytes.Count(content, []byte("recorded output\r\n")) != 1 {
		t.Fatalf("recorded raw output = %q, %v", content, err)
	}

	writeControllerBrokerPublicRequestV1(t, publicInputWriter, ControllerStreamRequestCompleteV1, 0, 0)
	if event := readControllerBrokerJSONLineV1(t, public); event["type"] != string(ControllerStreamEventTerminatedV1) {
		t.Fatalf("terminated event = %#v", event)
	}
	writeControllerBrokerPublicRequestV1(t, publicInputWriter, ControllerStreamRequestAcknowledgeTerminatedV1, 0, 0)
	if err := <-brokerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-hostDone; err != nil {
		t.Fatal(err)
	}
}

func readAsciinemaFixtureMetadataV1(t *testing.T) asciinemaFixtureMetadataV1 {
	t.Helper()
	fixturePath := "../../testdata/controlled-session/asciinema-v3-linux-" + runtime.GOARCH + ".json"
	payload, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture asciinemaFixtureMetadataV1
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "reploy-test-fixture-v1" || fixture.Version == "" || fixture.Asset == "" || fixture.URL == "" || len(fixture.SHA256) != 64 {
		t.Fatalf("invalid asciinema fixture metadata: %#v", fixture)
	}
	return fixture
}

func runControllerAsciinemaTestHostV1(listener *net.UnixListener, done chan<- error) {
	done <- runControllerAsciinemaTestHostConnectionV1(listener)
}

func runControllerAsciinemaTestHostConnectionV1(listener *net.UnixListener) (resultErr error) {
	connection, err := listener.AcceptUnix()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, connection.Close()) }()
	code := 0
	result := ResultV1{
		Cause:                            CauseWorkloadExitV1,
		WorkloadStatus:                   ProcessStatusV1{Kind: ProcessStatusExitedV1, Code: &code},
		WorkloadOutputFinalizationStatus: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1},
		RuntimeObservationStatus:         RuntimeObservationStatusV1{Kind: RuntimeObservationMaintainedV1},
		ControllerFinalizationStatus:     ControllerFinalizationStatusV1{Kind: ControllerFinalizationCompletedV1},
		CleanupStatus:                    CleanupStatusV1{Kind: CleanupStatusSucceededV1},
		RecoveryAction:                   RecoveryNoneV1,
	}
	openingEvents := []EventV1{
		{Kind: EventOpenedV1, Opened: pointerToOpenedV1(testOpenedV1())},
		{Kind: EventReadyV1},
	}
	for _, event := range openingEvents {
		if err := WriteEventV1(connection, event); err != nil {
			return err
		}
	}
	request, err := ReadRequestV1(connection)
	if err != nil || request.Kind != RequestResizeV1 || request.Columns != 80 || request.Rows != 24 {
		return errors.Join(err, fmt.Errorf("host expected initial 80x24 resize, got %#v", request))
	}
	closingEvents := []EventV1{
		{Kind: EventOutputV1, Bytes: []byte("recorded output\r\n")},
		{Kind: EventWorkloadExitV1, WorkloadExit: &WorkloadExitV1{Status: result.WorkloadStatus}},
		{Kind: EventTerminatingV1, Terminating: &TerminatingV1{Cause: CauseWorkloadExitV1}},
		{Kind: EventWorkloadOutputsFinalizedV1, WorkloadOutputsFinalized: &WorkloadOutputsFinalizedV1{Status: WorkloadOutputFinalizationDrainedV1}},
	}
	for _, event := range closingEvents {
		if err := WriteEventV1(connection, event); err != nil {
			return err
		}
	}
requests:
	for {
		request, err = ReadRequestV1(connection)
		if err != nil {
			return err
		}
		switch request.Kind {
		case RequestResizeV1:
			if request.Columns != 80 || request.Rows != 24 {
				return fmt.Errorf("host expected 80x24 resize, got %#v", request)
			}
		case RequestCompleteV1:
			break requests
		default:
			return fmt.Errorf("host expected resize or complete request, got %#v", request)
		}
	}
	if err := WriteEventV1(connection, EventV1{Kind: EventTerminatedV1, Terminated: &result}); err != nil {
		return err
	}
	request, err = ReadRequestV1(connection)
	if err != nil || request.Kind != RequestAcknowledgeTerminatedV1 {
		return errors.Join(err, fmt.Errorf("host expected acknowledge-terminated request, got %#v", request))
	}
	return nil
}

func shellQuoteTerminalFixtureV1(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
