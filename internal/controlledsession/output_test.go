package controlledsession

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPTYOutputPumpV1DeliversOrderedBoundedOutputBeforeDrained(t *testing.T) {
	reader, writer := io.Pipe()
	payload := bytes.Repeat([]byte("ordered-terminal-output\x00"), 5000)
	var delivered bytes.Buffer
	var chunkCount int
	pump, err := StartPTYOutputPumpV1(reader, func(_ context.Context, chunk []byte) error {
		if len(chunk) > ptyOutputChunkSizeV1 {
			t.Fatalf("output chunk length = %d", len(chunk))
		}
		chunkCount++
		_, _ = delivered.Write(chunk)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	writeErr := make(chan error, 1)
	go func() {
		_, err := writer.Write(payload)
		writeErr <- errors.Join(err, writer.Close())
	}()

	result, err := pump.Finalize(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("write PTY output: %v", err)
	}
	if result.Status != (WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}) || result.Err != nil {
		t.Fatalf("Finalize() = %#v", result)
	}
	if chunkCount < 2 {
		t.Fatalf("chunk count = %d, want multiple bounded chunks", chunkCount)
	}
	if !bytes.Equal(delivered.Bytes(), payload) {
		t.Fatal("delivered output did not preserve exact byte order")
	}
}

func TestPTYOutputPumpV1TimeoutCancelsBackpressureAndClosesSource(t *testing.T) {
	reader, writer := io.Pipe()
	emitterEntered := make(chan struct{})
	var calls atomic.Int32
	pump, err := StartPTYOutputPumpV1(reader, func(ctx context.Context, _ []byte) error {
		if calls.Add(1) == 1 {
			close(emitterEntered)
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	writeErr := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("blocked"))
		writeErr <- err
	}()
	<-emitterEntered

	result, err := pump.Finalize(time.Now().Add(20 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != failedPTYOutputStatusV1(ptyOutputTimeoutReasonV1) || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("Finalize() = %#v", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("emitter calls after finalization = %d", calls.Load())
	}
	if _, err := writer.Write([]byte("late")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after finalization error = %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("initial write error = %v", err)
	}
	_ = writer.Close()
}

func TestPTYOutputPumpV1DeliveryFailureIsExplicitAndTerminal(t *testing.T) {
	reader, writer := io.Pipe()
	deliveryErr := errors.New("controller stream unavailable")
	var calls atomic.Int32
	pump, err := StartPTYOutputPumpV1(reader, func(_ context.Context, _ []byte) error {
		calls.Add(1)
		return deliveryErr
	})
	if err != nil {
		t.Fatal(err)
	}

	writeErr := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("undeliverable"))
		writeErr <- err
	}()
	select {
	case <-pump.Done():
	case <-time.After(time.Second):
		t.Fatal("output pump did not expose its delivery failure to the supervisor")
	}
	result, err := pump.Finalize(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != failedPTYOutputStatusV1(ptyOutputDeliveryFailureReasonV1) || !errors.Is(result.Err, deliveryErr) {
		t.Fatalf("Finalize() = %#v", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("emitter calls = %d", calls.Load())
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("initial write error = %v", err)
	}
	if _, err := writer.Write([]byte("late")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after delivery failure error = %v", err)
	}
	_ = writer.Close()
}

func TestPTYOutputPumpV1DeliversBytesReturnedWithReadFailure(t *testing.T) {
	readErr := errors.New("PTY read failed")
	source := &singleReadCloserV1{bytes: []byte("last bytes"), err: readErr}
	var delivered []byte
	pump, err := StartPTYOutputPumpV1(source, func(_ context.Context, chunk []byte) error {
		delivered = append(delivered, chunk...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := pump.Finalize(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if string(delivered) != "last bytes" {
		t.Fatalf("delivered = %q", delivered)
	}
	if result.Status != failedPTYOutputStatusV1(ptyOutputReadFailureReasonV1) || !errors.Is(result.Err, readErr) {
		t.Fatalf("Finalize() = %#v", result)
	}
	if source.closeCount.Load() != 1 {
		t.Fatalf("source close count = %d", source.closeCount.Load())
	}
}

func TestPTYOutputPumpV1DeliversBytesReturnedWithEOF(t *testing.T) {
	source := &singleReadCloserV1{bytes: []byte("last bytes"), err: io.EOF}
	var delivered []byte
	pump, err := StartPTYOutputPumpV1(source, func(_ context.Context, chunk []byte) error {
		delivered = append(delivered, chunk...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := pump.Finalize(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if string(delivered) != "last bytes" {
		t.Fatalf("delivered = %q", delivered)
	}
	if result.Status != (WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}) || result.Err != nil {
		t.Fatalf("Finalize() = %#v", result)
	}
	if source.closeCount.Load() != 1 {
		t.Fatalf("source close count = %d", source.closeCount.Load())
	}
}

func TestPTYOutputPumpV1RejectsRepeatedEmptyReads(t *testing.T) {
	source := &emptyReadCloserV1{}
	var emitterCalls atomic.Int32
	unexpectedEmitErr := errors.New("emitter called for an empty read")
	pump, err := StartPTYOutputPumpV1(source, func(context.Context, []byte) error {
		emitterCalls.Add(1)
		return unexpectedEmitErr
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := pump.Finalize(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != failedPTYOutputStatusV1(ptyOutputReadFailureReasonV1) || !errors.Is(result.Err, io.ErrNoProgress) {
		t.Fatalf("Finalize() = %#v", result)
	}
	if emitterCalls.Load() != 0 || errors.Is(result.Err, unexpectedEmitErr) {
		t.Fatalf("emitter calls = %d; Finalize() = %#v", emitterCalls.Load(), result)
	}
	if source.readCount.Load() != maxConsecutiveEmptyReadsV1 {
		t.Fatalf("source read count = %d", source.readCount.Load())
	}
	if source.closeCount.Load() != 1 {
		t.Fatalf("source close count = %d", source.closeCount.Load())
	}
}

func TestPTYOutputPumpV1FinalizationIsImmutableAcrossCallers(t *testing.T) {
	source := io.NopCloser(bytes.NewReader([]byte("done")))
	pump, err := StartPTYOutputPumpV1(source, func(_ context.Context, _ []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	const callers = 16
	results := make(chan PTYOutputFinalizationV1, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := pump.Finalize(time.Now().Add(time.Second))
			if err != nil {
				t.Errorf("Finalize() error = %v", err)
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	for result := range results {
		if result.Status != (WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1}) || result.Err != nil {
			t.Fatalf("Finalize() = %#v", result)
		}
	}
}

func TestPTYOutputPumpV1ResultSatisfiesLifecycleBarrier(t *testing.T) {
	machine := activatedMachineV1(t)
	code := 0
	observeWorkloadExitV1(t, machine, code)

	var events []EventV1
	pump, err := StartPTYOutputPumpV1(io.NopCloser(bytes.NewReader([]byte("terminal"))), func(_ context.Context, chunk []byte) error {
		event := EventV1{Kind: EventOutputV1, Bytes: append([]byte(nil), chunk...)}
		if err := ValidateEventV1(event); err != nil {
			return err
		}
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := pump.Finalize(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.Err != nil {
		t.Fatalf("Finalize() error = %v", result.Err)
	}
	transition := observeOutputsFinalizedV1(t, machine, result.Status)
	if len(events) != 1 || string(events[0].Bytes) != "terminal" {
		t.Fatalf("output events = %#v", events)
	}
	if transition.AwaitingWorkloadOutputFinalization || !transition.AwaitingControllerFinalization {
		t.Fatalf("output finalization transition = %#v", transition)
	}
}

func TestStartPTYOutputPumpV1RejectsInvalidConfiguration(t *testing.T) {
	validSource := func() io.ReadCloser { return io.NopCloser(bytes.NewReader(nil)) }
	validEmitter := func(context.Context, []byte) error { return nil }
	tests := []struct {
		name    string
		source  io.ReadCloser
		emitter PTYOutputEmitterV1
	}{
		{name: "missing source", emitter: validEmitter},
		{name: "missing emitter", source: validSource()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := StartPTYOutputPumpV1(test.source, test.emitter); err == nil {
				t.Fatal("StartPTYOutputPumpV1() succeeded")
			}
		})
	}
}

func TestPTYOutputPumpV1RequiresAbsoluteFinalizationDeadline(t *testing.T) {
	pump, err := StartPTYOutputPumpV1(io.NopCloser(bytes.NewReader(nil)), func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pump.Finalize(time.Time{}); err == nil {
		t.Fatal("Finalize() accepted a zero deadline")
	}
	result, err := pump.Finalize(time.Now().Add(time.Second))
	if err != nil || result.Status.Kind != WorkloadOutputFinalizationDrainedV1 {
		t.Fatalf("Finalize(valid deadline) = %#v, %v", result, err)
	}
}

func TestPTYOutputPumpV1DeadlineIncludesEarlierWorkloadShutdown(t *testing.T) {
	reader, writer := io.Pipe()
	pump, err := StartPTYOutputPumpV1(reader, func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	// A deadline consumed by workload shutdown must not be reset when draining
	// finally begins.
	deadline := time.Now().Add(-time.Second)
	started := time.Now()
	result, err := pump.Finalize(deadline)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 250*time.Millisecond {
		t.Fatalf("Finalize() reset the termination deadline; elapsed = %s", elapsed)
	}
	if result.Status != failedPTYOutputStatusV1(ptyOutputTimeoutReasonV1) {
		t.Fatalf("Finalize() = %#v", result)
	}
	_ = writer.Close()
}

func TestPTYOutputPumpV1RejectsCompletionAfterDeadlineWithoutTimerRace(t *testing.T) {
	source := &singleReadCloserV1{bytes: []byte("late"), err: io.EOF}
	pump, err := StartPTYOutputPumpV1(source, func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(-time.Second)
	awaitV1(t, time.Second, func() bool { return source.closeCount.Load() == 1 })

	result, err := pump.Finalize(deadline)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != failedPTYOutputStatusV1(ptyOutputTimeoutReasonV1) || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("Finalize() = %#v", result)
	}
}

func TestPTYOutputPumpV1ChargesSourceClosureAgainstDeadline(t *testing.T) {
	readErr := errors.New("PTY read failed")
	closeEntered := make(chan struct{})
	allowClose := make(chan struct{})
	source := &blockingCloseReaderV1{
		ReadCloser: &singleReadCloserV1{err: readErr},
		entered:    closeEntered,
		allow:      allowClose,
	}
	pump, err := StartPTYOutputPumpV1(source, func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-closeEntered

	resultReady := make(chan PTYOutputFinalizationV1, 1)
	go func() {
		result, finalizeErr := pump.Finalize(time.Now().Add(-time.Second))
		if finalizeErr != nil {
			t.Errorf("Finalize() error = %v", finalizeErr)
		}
		resultReady <- result
	}()
	close(allowClose)
	result := <-resultReady
	if result.Status != failedPTYOutputStatusV1(ptyOutputTimeoutReasonV1) ||
		!errors.Is(result.Err, context.DeadlineExceeded) ||
		!errors.Is(result.Err, readErr) {
		t.Fatalf("Finalize() = %#v", result)
	}
}

type singleReadCloserV1 struct {
	bytes      []byte
	err        error
	read       atomic.Bool
	closeCount atomic.Int32
}

type emptyReadCloserV1 struct {
	readCount  atomic.Int32
	closeCount atomic.Int32
}

func (source *emptyReadCloserV1) Read([]byte) (int, error) {
	source.readCount.Add(1)
	return 0, nil
}

func (source *emptyReadCloserV1) Close() error {
	source.closeCount.Add(1)
	return nil
}

func (source *singleReadCloserV1) Read(buffer []byte) (int, error) {
	if !source.read.CompareAndSwap(false, true) {
		return 0, io.EOF
	}
	return copy(buffer, source.bytes), source.err
}

func (source *singleReadCloserV1) Close() error {
	source.closeCount.Add(1)
	return nil
}

func TestPTYOutputPumpV1CloseFailureCannotReportDrained(t *testing.T) {
	closeErr := errors.New("close failed")
	source := &singleReadCloserV1{err: io.EOF}
	pump, err := StartPTYOutputPumpV1(&closeErrorReaderV1{ReadCloser: source, err: closeErr}, func(_ context.Context, _ []byte) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := pump.Finalize(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != failedPTYOutputStatusV1(ptyOutputClosureFailureReasonV1) || !errors.Is(result.Err, closeErr) {
		t.Fatalf("Finalize() = %#v", result)
	}
}

type closeErrorReaderV1 struct {
	io.ReadCloser
	err error
}

type blockingCloseReaderV1 struct {
	io.ReadCloser
	entered chan struct{}
	allow   chan struct{}
}

func (reader *blockingCloseReaderV1) Close() error {
	close(reader.entered)
	<-reader.allow
	return reader.ReadCloser.Close()
}

func (reader *closeErrorReaderV1) Close() error {
	return fmt.Errorf("synthetic close: %w", reader.err)
}

func awaitV1(t *testing.T, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(time.Millisecond)
	}
}
