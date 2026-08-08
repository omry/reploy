package controlledsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	ptyOutputChunkSizeV1       = 32 * 1024
	maxConsecutiveEmptyReadsV1 = 100
)

const (
	ptyOutputReadFailureReasonV1     = "workload PTY output read failed"
	ptyOutputDeliveryFailureReasonV1 = "workload PTY output delivery failed"
	ptyOutputClosureFailureReasonV1  = "workload PTY output closure failed"
	ptyOutputTimeoutReasonV1         = "workload PTY output finalization timed out"
)

// PTYOutputEmitterV1 delivers one ordered output chunk. It must return only
// after the chunk has been consumed through the controller's flow-control
// window, and it must stop promptly when ctx is canceled.
type PTYOutputEmitterV1 func(ctx context.Context, bytes []byte) error

// PTYOutputFinalizationV1 separates the safe, protocol-visible status from
// the detailed host diagnostic. Err is never sent to the controller directly.
type PTYOutputFinalizationV1 struct {
	Status WorkloadOutputFinalizationStatusV1
	Err    error
}

// PTYOutputPumpV1 owns one workload PTY output source. It reads and emits one
// bounded chunk at a time, so the emitter itself is the bounded flow-control
// window and no unbounded output queue can form.
type PTYOutputPumpV1 struct {
	source io.ReadCloser
	emit   PTYOutputEmitterV1

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	closeOnce sync.Once
	closeErr  error

	resultMu  sync.Mutex
	result    *PTYOutputFinalizationV1
	stoppedAt time.Time

	finalizeOnce sync.Once
	finalized    PTYOutputFinalizationV1
}

// StartPTYOutputPumpV1 begins forwarding workload PTY output immediately.
// Closing source must return promptly and permanently unblock an in-progress
// Read; this is the trusted PTY adapter contract that makes finalization
// bounded.
func StartPTYOutputPumpV1(
	source io.ReadCloser,
	emit PTYOutputEmitterV1,
) (*PTYOutputPumpV1, error) {
	if source == nil {
		return nil, fmt.Errorf("start controlled-session PTY output pump: source is required")
	}
	if emit == nil {
		return nil, fmt.Errorf("start controlled-session PTY output pump: emitter is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pump := &PTYOutputPumpV1{
		source: source,
		emit:   emit,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go pump.run()
	return pump, nil
}

// Done closes when PTY reading and delivery have stopped. It is an observation
// signal for the host supervisor, not a successful lifecycle outcome: callers
// must still establish termination, observe the workload, and call Finalize.
func (pump *PTYOutputPumpV1) Done() <-chan struct{} {
	return pump.done
}

// Finalize waits for all previously read bytes to be delivered and for the
// source to close. deadline is the absolute host-owned deadline established
// when termination began; time already spent stopping the workload is not
// reset here. If draining cannot complete by that deadline, Finalize cancels
// the outstanding delivery, forcibly closes the source, and reports a failed
// output outcome. Repeated calls return the same immutable result.
func (pump *PTYOutputPumpV1) Finalize(deadline time.Time) (PTYOutputFinalizationV1, error) {
	if deadline.IsZero() {
		return PTYOutputFinalizationV1{}, fmt.Errorf("finalize controlled-session PTY output: absolute deadline is required")
	}
	pump.finalizeOnce.Do(func() {
		pump.finalized = pump.finalize(deadline)
	})
	return pump.finalized, nil
}

func (pump *PTYOutputPumpV1) run() {
	defer func() {
		pump.resultMu.Lock()
		pump.stoppedAt = time.Now()
		pump.resultMu.Unlock()
		close(pump.done)
	}()
	defer pump.cancel()

	buffer := make([]byte, ptyOutputChunkSizeV1)
	emptyReads := 0
	for {
		count, readErr := pump.source.Read(buffer)
		if count > 0 {
			emptyReads = 0
			chunk := append([]byte(nil), buffer[:count]...)
			if emitErr := pump.emit(pump.ctx, chunk); emitErr != nil {
				pump.failAndClose(ptyOutputDeliveryFailureReasonV1, fmt.Errorf("deliver controlled-session PTY output: %w", emitErr))
				return
			}
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= maxConsecutiveEmptyReadsV1 {
				pump.failAndClose(ptyOutputReadFailureReasonV1, fmt.Errorf("read controlled-session PTY output: %w", io.ErrNoProgress))
				return
			}
		}

		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			if _, closeErr := pump.closeSource(); closeErr != nil {
				pump.latchResult(PTYOutputFinalizationV1{
					Status: failedPTYOutputStatusV1(ptyOutputClosureFailureReasonV1),
					Err:    fmt.Errorf("close drained controlled-session PTY output: %w", closeErr),
				})
			} else {
				pump.latchResult(PTYOutputFinalizationV1{
					Status: WorkloadOutputFinalizationStatusV1{Kind: WorkloadOutputFinalizationDrainedV1},
				})
			}
			return
		}
		pump.failAndClose(ptyOutputReadFailureReasonV1, fmt.Errorf("read controlled-session PTY output: %w", readErr))
		return
	}
}

func (pump *PTYOutputPumpV1) finalize(deadline time.Time) PTYOutputFinalizationV1 {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case <-pump.done:
		return pump.resultForDeadline(deadline)
	case <-timer.C:
	}

	// The result latch decides the deadline/EOF boundary. If the reader already
	// established a terminal result, do not rewrite it merely because its done
	// notification was racing the timer.
	timedOut := pump.latchResult(PTYOutputFinalizationV1{
		Status: failedPTYOutputStatusV1(ptyOutputTimeoutReasonV1),
		Err:    context.DeadlineExceeded,
	})
	if timedOut {
		pump.cancel()
		if closed, closeErr := pump.closeSource(); closed && closeErr != nil {
			pump.appendResultError(fmt.Errorf("close timed-out controlled-session PTY output: %w", closeErr))
		}
	}
	<-pump.done
	return pump.resultForDeadline(deadline)
}

func (pump *PTYOutputPumpV1) failAndClose(reason string, failure error) {
	pump.latchResult(PTYOutputFinalizationV1{
		Status: failedPTYOutputStatusV1(reason),
		Err:    failure,
	})
	pump.cancel()
	if closed, closeErr := pump.closeSource(); closed && closeErr != nil {
		pump.appendResultError(fmt.Errorf("close failed controlled-session PTY output: %w", closeErr))
	}
}

func (pump *PTYOutputPumpV1) closeSource() (bool, error) {
	closed := false
	pump.closeOnce.Do(func() {
		closed = true
		pump.closeErr = pump.source.Close()
	})
	return closed, pump.closeErr
}

func (pump *PTYOutputPumpV1) latchResult(result PTYOutputFinalizationV1) bool {
	pump.resultMu.Lock()
	defer pump.resultMu.Unlock()
	if pump.result != nil {
		return false
	}
	pump.result = &result
	return true
}

func (pump *PTYOutputPumpV1) appendResultError(err error) {
	if err == nil {
		return
	}
	pump.resultMu.Lock()
	defer pump.resultMu.Unlock()
	if pump.result == nil {
		panic("controlled-session PTY output diagnostic precedes its terminal result")
	}
	pump.result.Err = errors.Join(pump.result.Err, err)
}

func (pump *PTYOutputPumpV1) resultForDeadline(deadline time.Time) PTYOutputFinalizationV1 {
	pump.resultMu.Lock()
	defer pump.resultMu.Unlock()
	if pump.result == nil {
		panic("controlled-session PTY output pump stopped without a terminal result")
	}
	if pump.stoppedAt.IsZero() {
		panic("controlled-session PTY output result inspected before the pump stopped")
	}
	if !pump.stoppedAt.After(deadline) {
		return *pump.result
	}
	return PTYOutputFinalizationV1{
		Status: failedPTYOutputStatusV1(ptyOutputTimeoutReasonV1),
		Err:    errors.Join(context.DeadlineExceeded, pump.result.Err),
	}
}

func failedPTYOutputStatusV1(reason string) WorkloadOutputFinalizationStatusV1 {
	return WorkloadOutputFinalizationStatusV1{
		Kind:   WorkloadOutputFinalizationFailedV1,
		Reason: reason,
	}
}
