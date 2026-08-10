package deploy

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/omry/reploy/internal/canonical"
)

const LiveRunQueueSchemaV1 = "live-run-queue-v1"

type LiveRunKindV1 string

const (
	LiveRunKindAppV1     LiveRunKindV1 = "app"
	LiveRunKindShellV1   LiveRunKindV1 = "shell"
	LiveRunKindControlV1 LiveRunKindV1 = "control"
)

type ControlOperationV1 string

const (
	ControlOperationUpV1        ControlOperationV1 = "up"
	ControlOperationStopV1      ControlOperationV1 = "stop"
	ControlOperationRestartV1   ControlOperationV1 = "restart"
	ControlOperationInstallV1   ControlOperationV1 = "install"
	ControlOperationUninstallV1 ControlOperationV1 = "uninstall"
	ControlOperationStageV1     ControlOperationV1 = "stage"
)

type LiveRunStatusV1 string

const (
	LiveRunStatusActiveV1  LiveRunStatusV1 = "active"
	LiveRunStatusWaitingV1 LiveRunStatusV1 = "waiting"
	LiveRunStatusReadyV1   LiveRunStatusV1 = "ready"
)

type LiveRunV1 struct {
	ID                  string          `json:"id"`
	Kind                LiveRunKindV1   `json:"kind"`
	Name                string          `json:"name"`
	GenerationReference string          `json:"generation_reference"`
	BootSession         string          `json:"boot_session,omitempty"`
	Status              LiveRunStatusV1 `json:"status"`
	Exclusive           bool            `json:"exclusive"`
	WritableMount       string          `json:"writable_mount,omitempty"`
	WritablePaths       []string        `json:"writable_paths,omitempty"`
	Container           string          `json:"container,omitempty"`
}

type LiveRunQueueV1 struct {
	Schema  string                      `json:"schema"`
	Runs    []LiveRunV1                 `json:"runs"`
	Cleanup []LiveRunContainerCleanupV1 `json:"cleanup,omitempty"`
}

type LiveRunRecoveryReasonV1 string

const (
	LiveRunRecoveryAbandonedOwnerV1 LiveRunRecoveryReasonV1 = "abandoned-owner"
	LiveRunRecoveryPriorSessionV1   LiveRunRecoveryReasonV1 = "prior-session"
	LiveRunRecoveryLegacyEntryV1    LiveRunRecoveryReasonV1 = "legacy-entry"
	LiveRunRecoveryCleanupFailedV1  LiveRunRecoveryReasonV1 = "cleanup-failed"
)

type LiveRunContainerCleanupV1 struct {
	Container string                  `json:"container"`
	RunID     string                  `json:"run_id"`
	Kind      LiveRunKindV1           `json:"kind"`
	Name      string                  `json:"name"`
	Reason    LiveRunRecoveryReasonV1 `json:"reason"`
}

type RecoveredLiveRunV1 struct {
	Run    LiveRunV1
	Reason LiveRunRecoveryReasonV1
}

type LiveRunRecoveryV1 struct {
	Removed []RecoveredLiveRunV1
}

type ControlMarkerV1 struct {
	ID                  string
	Operation           ControlOperationV1
	GenerationReference string
	BootSession         string
	Status              LiveRunStatusV1
}

var ErrLiveRunConflict = errors.New("another run must finish first")

var liveRunIDPatternV1 = regexp.MustCompile(`^run-[0-9a-f]{16}$`)
var controlMarkerIDPatternV1 = regexp.MustCompile(`^control-[0-9a-f]{16}$`)

func NewLiveRunQueueV1() LiveRunQueueV1 {
	return LiveRunQueueV1{Schema: LiveRunQueueSchemaV1, Runs: []LiveRunV1{}}
}

func NewLiveRunIDV1() (string, error) {
	return newOpaqueAdmissionIDV1(rand.Reader, "run", "run ID")
}

func newLiveRunIDV1(random io.Reader) (string, error) {
	return newOpaqueAdmissionIDV1(random, "run", "run ID")
}

func NewControlMarkerIDV1() (string, error) {
	return newOpaqueAdmissionIDV1(rand.Reader, "control", "control marker ID")
}

func newControlMarkerIDV1(random io.Reader) (string, error) {
	return newOpaqueAdmissionIDV1(random, "control", "control marker ID")
}

func newOpaqueAdmissionIDV1(random io.Reader, prefix string, label string) (string, error) {
	if random == nil {
		return "", fmt.Errorf("create %s requires randomness", label)
	}
	var value [8]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return "", fmt.Errorf("create %s: %w", label, err)
	}
	return fmt.Sprintf("%s-%x", prefix, value), nil
}

func EncodeLiveRunQueueV1(queue LiveRunQueueV1) ([]byte, error) {
	if err := ValidateLiveRunQueueV1(queue); err != nil {
		return nil, fmt.Errorf("encode live run queue: %w", err)
	}
	content, err := canonical.Marshal(queue)
	if err != nil {
		return nil, fmt.Errorf("encode live run queue: %w", err)
	}
	return content, nil
}

func DecodeLiveRunQueueV1(content []byte) (LiveRunQueueV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var queue LiveRunQueueV1
	if err := decoder.Decode(&queue); err != nil {
		return LiveRunQueueV1{}, fmt.Errorf("decode live run queue: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return LiveRunQueueV1{}, fmt.Errorf("live run queue contains trailing JSON")
		}
		return LiveRunQueueV1{}, fmt.Errorf("decode live run queue trailer: %w", err)
	}
	if err := ValidateLiveRunQueueV1(queue); err != nil {
		return LiveRunQueueV1{}, fmt.Errorf("validate live run queue: %w", err)
	}
	canonicalContent, err := canonical.Marshal(queue)
	if err != nil {
		return LiveRunQueueV1{}, err
	}
	if !bytes.Equal(content, canonicalContent) {
		return LiveRunQueueV1{}, fmt.Errorf("live run queue is not canonical JSON")
	}
	return queue, nil
}

func ValidateLiveRunIDV1(id string) error {
	if !liveRunIDPatternV1.MatchString(id) {
		return fmt.Errorf("run ID must use run- followed by 16 lowercase hexadecimal characters")
	}
	return nil
}

func ValidateControlMarkerIDV1(id string) error {
	if !controlMarkerIDPatternV1.MatchString(id) {
		return fmt.Errorf("control marker ID must use control- followed by 16 lowercase hexadecimal characters")
	}
	return nil
}

func ValidateLiveRunQueueV1(queue LiveRunQueueV1) error {
	if queue.Schema != LiveRunQueueSchemaV1 {
		return fmt.Errorf("live run queue schema must be %q", LiveRunQueueSchemaV1)
	}
	if queue.Runs == nil {
		return fmt.Errorf("live run queue runs must use an array")
	}
	for index, cleanup := range queue.Cleanup {
		if err := validateLiveRunContainerCleanupV1(cleanup); err != nil {
			return fmt.Errorf("live run queue cleanup entry %d: %w", index, err)
		}
		if index > 0 && queue.Cleanup[index-1].Container >= cleanup.Container {
			return fmt.Errorf("live run queue cleanup entries must be sorted and unique by container")
		}
	}
	seen := map[string]bool{}
	waitingSeen := false
	runnableCount := 0
	runnableExclusive := false
	readyCount := 0
	for index, run := range queue.Runs {
		if err := validateLiveQueueEntryV1(run); err != nil {
			return fmt.Errorf("live run queue entry %d: %w", index, err)
		}
		if seen[run.ID] {
			return fmt.Errorf("live run queue repeats run ID %q", run.ID)
		}
		seen[run.ID] = true
		switch run.Status {
		case LiveRunStatusActiveV1:
			if waitingSeen {
				return fmt.Errorf("live run queue has runnable entry %q after a waiting entry", run.ID)
			}
			if readyCount != 0 {
				return fmt.Errorf("live run queue has active entry %q after a ready reservation", run.ID)
			}
			runnableCount++
			runnableExclusive = runnableExclusive || run.Exclusive
		case LiveRunStatusReadyV1:
			if waitingSeen {
				return fmt.Errorf("live run queue has runnable entry %q after a waiting entry", run.ID)
			}
			runnableCount++
			runnableExclusive = runnableExclusive || run.Exclusive
			readyCount++
		case LiveRunStatusWaitingV1:
			waitingSeen = true
		}
	}
	if runnableExclusive && runnableCount != 1 {
		return fmt.Errorf("exclusive live run must be the only runnable entry")
	}
	if readyCount > 1 {
		return fmt.Errorf("live run queue may reserve only one ready entry")
	}
	firstWaiting := firstWaitingLiveRunV1(queue.Runs)
	if firstWaiting == nil {
		return nil
	}
	if runnableCount == 0 || (readyCount == 0 && !runnableExclusive && !firstWaiting.Exclusive) {
		return fmt.Errorf("live run queue leaves run %q waiting although it can start", firstWaiting.ID)
	}
	return nil
}

func validateLiveRunContainerCleanupV1(cleanup LiveRunContainerCleanupV1) error {
	if !safeRecoveryIdentity(cleanup.Container) {
		return fmt.Errorf("cleanup container must be nonempty safe text")
	}
	if err := ValidateLiveRunIDV1(cleanup.RunID); err != nil {
		return fmt.Errorf("cleanup run ID: %w", err)
	}
	if cleanup.Kind != LiveRunKindAppV1 && cleanup.Kind != LiveRunKindShellV1 {
		return fmt.Errorf("cleanup kind must be app or shell")
	}
	if !safeRecoveryIdentity(cleanup.Name) {
		return fmt.Errorf("cleanup name must be nonempty safe text")
	}
	switch cleanup.Reason {
	case LiveRunRecoveryAbandonedOwnerV1,
		LiveRunRecoveryPriorSessionV1,
		LiveRunRecoveryLegacyEntryV1,
		LiveRunRecoveryCleanupFailedV1:
		return nil
	default:
		return fmt.Errorf("cleanup reason is invalid")
	}
}

func validateLiveRunV1(run LiveRunV1) error {
	if err := ValidateLiveRunIDV1(run.ID); err != nil {
		return err
	}
	if run.Kind != LiveRunKindAppV1 && run.Kind != LiveRunKindShellV1 {
		return fmt.Errorf("live run kind must be app or shell")
	}
	if !safeRecoveryIdentity(run.Name) {
		return fmt.Errorf("live run name must be nonempty safe text")
	}
	if !safeRecoveryIdentity(run.GenerationReference) {
		return fmt.Errorf("live run generation reference must be nonempty safe text")
	}
	if run.BootSession != "" {
		if err := validateBootSessionIDV1(run.BootSession); err != nil {
			return err
		}
	}
	if run.Status != LiveRunStatusActiveV1 && run.Status != LiveRunStatusReadyV1 && run.Status != LiveRunStatusWaitingV1 {
		return fmt.Errorf("live run status must be active, ready, or waiting")
	}
	if !run.Exclusive && (run.WritableMount != "" || len(run.WritablePaths) != 0) {
		return fmt.Errorf("concurrent live run must not name a writable mount conflict")
	}
	for index, path := range run.WritablePaths {
		if !safeRecoveryIdentity(path) {
			return fmt.Errorf("live run writable path must be nonempty safe text")
		}
		if index > 0 && run.WritablePaths[index-1] >= path {
			return fmt.Errorf("live run writable paths must be sorted and unique")
		}
	}
	if run.Container != "" && !safeRecoveryIdentity(run.Container) {
		return fmt.Errorf("live run container must be safe text")
	}
	if run.Status != LiveRunStatusActiveV1 && run.Container != "" {
		return fmt.Errorf("pending live run must not name a container")
	}
	return nil
}

func validateLiveQueueEntryV1(entry LiveRunV1) error {
	if entry.Kind != LiveRunKindControlV1 {
		return validateLiveRunV1(entry)
	}
	if err := ValidateControlMarkerIDV1(entry.ID); err != nil {
		return err
	}
	if !validControlOperationV1(ControlOperationV1(entry.Name)) {
		return fmt.Errorf("control marker operation must be up, stop, restart, install, uninstall, or stage")
	}
	if !safeRecoveryIdentity(entry.GenerationReference) {
		return fmt.Errorf("control marker generation reference must be nonempty safe text")
	}
	if entry.BootSession != "" {
		if err := validateBootSessionIDV1(entry.BootSession); err != nil {
			return err
		}
	}
	if entry.Status != LiveRunStatusActiveV1 && entry.Status != LiveRunStatusWaitingV1 && entry.Status != LiveRunStatusReadyV1 {
		return fmt.Errorf("control marker status must be active, ready, or waiting")
	}
	if !entry.Exclusive {
		return fmt.Errorf("control marker must be exclusive")
	}
	if entry.WritableMount != "" || len(entry.WritablePaths) != 0 || entry.Container != "" {
		return fmt.Errorf("control marker must not name a writable mount or container")
	}
	return nil
}

func validControlOperationV1(operation ControlOperationV1) bool {
	switch operation {
	case ControlOperationUpV1, ControlOperationStopV1, ControlOperationRestartV1, ControlOperationInstallV1, ControlOperationUninstallV1, ControlOperationStageV1:
		return true
	default:
		return false
	}
}

func AdmitLiveRunV1(queue LiveRunQueueV1, candidate LiveRunV1, wait bool) (LiveRunQueueV1, LiveRunStatusV1, error) {
	if err := ValidateLiveRunQueueV1(queue); err != nil {
		return LiveRunQueueV1{}, "", err
	}
	if candidate.Status != "" {
		return LiveRunQueueV1{}, "", fmt.Errorf("live run candidate status must be empty")
	}
	candidate.Status = LiveRunStatusWaitingV1
	if err := validateLiveRunV1(candidate); err != nil {
		return LiveRunQueueV1{}, "", err
	}
	return admitLiveQueueEntryV1(queue, candidate, wait, "live run")
}

func AdmitControlMarkerV1(queue LiveRunQueueV1, candidate ControlMarkerV1, wait bool) (LiveRunQueueV1, LiveRunStatusV1, error) {
	if err := ValidateLiveRunQueueV1(queue); err != nil {
		return LiveRunQueueV1{}, "", err
	}
	if candidate.Status != "" {
		return LiveRunQueueV1{}, "", fmt.Errorf("control marker candidate status must be empty")
	}
	entry := LiveRunV1{
		ID: candidate.ID, Kind: LiveRunKindControlV1, Name: string(candidate.Operation),
		GenerationReference: candidate.GenerationReference, BootSession: candidate.BootSession, Exclusive: true,
	}
	entry.Status = LiveRunStatusWaitingV1
	if err := validateLiveQueueEntryV1(entry); err != nil {
		return LiveRunQueueV1{}, "", err
	}
	return admitLiveQueueEntryV1(queue, entry, wait, "control marker")
}

func admitLiveQueueEntryV1(queue LiveRunQueueV1, candidate LiveRunV1, wait bool, label string) (LiveRunQueueV1, LiveRunStatusV1, error) {
	for _, entry := range queue.Runs {
		if entry.ID == candidate.ID {
			return LiveRunQueueV1{}, "", fmt.Errorf("%s ID %q already exists", label, candidate.ID)
		}
	}
	result := cloneLiveRunQueueV1(queue)
	if liveRunCanStartNowV1(result, candidate) {
		candidate.Status = LiveRunStatusActiveV1
		result.Runs = append(result.Runs, candidate)
		return result, candidate.Status, nil
	}
	if !wait {
		return queue, "", ErrLiveRunConflict
	}
	result.Runs = append(result.Runs, candidate)
	return result, candidate.Status, nil
}

func RemoveLiveRunV1(queue LiveRunQueueV1, id string) (LiveRunQueueV1, bool, error) {
	if err := ValidateLiveRunQueueV1(queue); err != nil {
		return LiveRunQueueV1{}, false, err
	}
	if err := ValidateLiveRunIDV1(id); err != nil {
		return LiveRunQueueV1{}, false, err
	}
	return removeLiveQueueEntryV1(queue, id, LiveRunKindAppV1, LiveRunKindShellV1)
}

func RemoveControlMarkerV1(queue LiveRunQueueV1, id string) (LiveRunQueueV1, bool, error) {
	if err := ValidateLiveRunQueueV1(queue); err != nil {
		return LiveRunQueueV1{}, false, err
	}
	if err := ValidateControlMarkerIDV1(id); err != nil {
		return LiveRunQueueV1{}, false, err
	}
	return removeLiveQueueEntryV1(queue, id, LiveRunKindControlV1)
}

func ActivateReadyLiveRunV1(queue LiveRunQueueV1, id string) (LiveRunQueueV1, error) {
	if err := ValidateLiveRunQueueV1(queue); err != nil {
		return LiveRunQueueV1{}, err
	}
	if err := ValidateLiveRunIDV1(id); err != nil {
		return LiveRunQueueV1{}, err
	}
	result := cloneLiveRunQueueV1(queue)
	for index := range result.Runs {
		run := &result.Runs[index]
		if run.ID != id || run.Kind == LiveRunKindControlV1 {
			continue
		}
		if run.Status != LiveRunStatusReadyV1 {
			return LiveRunQueueV1{}, fmt.Errorf("live run %q is not ready", id)
		}
		run.Status = LiveRunStatusActiveV1
		promoteLiveRunsV1(&result)
		if err := ValidateLiveRunQueueV1(result); err != nil {
			return LiveRunQueueV1{}, fmt.Errorf("validate queue after activating live run: %w", err)
		}
		return result, nil
	}
	return LiveRunQueueV1{}, fmt.Errorf("live run %q is not outstanding", id)
}

func CancelWaitingLiveRunsV1(queue LiveRunQueueV1) (LiveRunQueueV1, []LiveRunV1, error) {
	if err := ValidateLiveRunQueueV1(queue); err != nil {
		return LiveRunQueueV1{}, nil, err
	}
	result := cloneLiveRunQueueV1(queue)
	retained := make([]LiveRunV1, 0, len(result.Runs))
	canceled := []LiveRunV1{}
	for _, entry := range result.Runs {
		if entry.Kind != LiveRunKindControlV1 && entry.Status != LiveRunStatusActiveV1 {
			canceled = append(canceled, entry)
			continue
		}
		retained = append(retained, entry)
	}
	result.Runs = retained
	promoteLiveRunsV1(&result)
	if err := ValidateLiveRunQueueV1(result); err != nil {
		return LiveRunQueueV1{}, nil, fmt.Errorf("validate queue after canceling waiting live runs: %w", err)
	}
	return result, canceled, nil
}

func removeLiveQueueEntryV1(queue LiveRunQueueV1, id string, kinds ...LiveRunKindV1) (LiveRunQueueV1, bool, error) {
	result := cloneLiveRunQueueV1(queue)
	for index, entry := range result.Runs {
		if entry.ID != id || !liveRunKindInV1(entry.Kind, kinds) {
			continue
		}
		result.Runs = append(result.Runs[:index], result.Runs[index+1:]...)
		promoteLiveRunsV1(&result)
		return result, true, nil
	}
	return result, false, nil
}

func liveRunKindInV1(kind LiveRunKindV1, allowed []LiveRunKindV1) bool {
	for _, candidate := range allowed {
		if kind == candidate {
			return true
		}
	}
	return false
}

func ControlMarkersV1(queue LiveRunQueueV1) []ControlMarkerV1 {
	markers := []ControlMarkerV1{}
	for _, entry := range queue.Runs {
		if entry.Kind == LiveRunKindControlV1 {
			markers = append(markers, ControlMarkerV1{
				ID: entry.ID, Operation: ControlOperationV1(entry.Name),
				GenerationReference: entry.GenerationReference, BootSession: entry.BootSession, Status: entry.Status,
			})
		}
	}
	return markers
}

func cloneLiveRunQueueV1(queue LiveRunQueueV1) LiveRunQueueV1 {
	var cleanup []LiveRunContainerCleanupV1
	if queue.Cleanup != nil {
		cleanup = append([]LiveRunContainerCleanupV1{}, queue.Cleanup...)
	}
	return LiveRunQueueV1{
		Schema:  queue.Schema,
		Runs:    append([]LiveRunV1{}, queue.Runs...),
		Cleanup: cleanup,
	}
}

func liveRunCanStartNowV1(queue LiveRunQueueV1, candidate LiveRunV1) bool {
	activeCount := 0
	for _, run := range queue.Runs {
		if run.Status != LiveRunStatusActiveV1 {
			return false
		}
		activeCount++
		if run.Exclusive {
			return false
		}
	}
	return !candidate.Exclusive || activeCount == 0
}

func promoteLiveRunsV1(queue *LiveRunQueueV1) {
	runnableCount := 0
	runnableExclusive := false
	readyCount := 0
	firstWaiting := -1
	for index, run := range queue.Runs {
		if run.Status == LiveRunStatusWaitingV1 {
			firstWaiting = index
			break
		}
		runnableCount++
		runnableExclusive = runnableExclusive || run.Exclusive
		if run.Status == LiveRunStatusReadyV1 {
			readyCount++
		}
	}
	if firstWaiting < 0 || runnableExclusive || readyCount != 0 {
		return
	}
	if queue.Runs[firstWaiting].Exclusive {
		if runnableCount == 0 {
			queue.Runs[firstWaiting].Status = LiveRunStatusReadyV1
		}
		return
	}
	queue.Runs[firstWaiting].Status = LiveRunStatusReadyV1
}

func firstWaitingLiveRunV1(runs []LiveRunV1) *LiveRunV1 {
	for index := range runs {
		if runs[index].Status == LiveRunStatusWaitingV1 {
			return &runs[index]
		}
	}
	return nil
}
