package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/identitymapping"
)

type managedStorageEvidenceFixtureV1 struct {
	*mappingOperationEvidenceV1
	observe func(context.Context, StateV1) error
	calls   int
}

func (e *managedStorageEvidenceFixtureV1) ObserveManagedStorage(ctx context.Context, state StateV1) error {
	e.calls++
	if e.observe != nil {
		return e.observe(ctx, state)
	}
	return ctx.Err()
}

func managedStorageFixtureV1(t *testing.T) (string, IdentityMappingOperationsV1, StateV1, *managedStorageEvidenceFixtureV1) {
	t.Helper()
	name, ops, state, original := operationFixtureV1(t, false, false)
	original.handles = []string{"owned-data"}
	var err error
	state, err = ops.Configure(t.Context(), state, operationGeometryV1(100000), "initial-ownership")
	if err == nil {
		state, err = ops.BeginEffects(t.Context(), state, "initial-ownership")
	}
	original.effects = true
	if err == nil {
		state, err = ops.Recover(t.Context(), state, "initial-ownership", false)
	}
	if err == nil {
		state, err = ops.UpgradeNativeMapping(t.Context(), state)
	}
	if err != nil {
		t.Fatal(err)
	}
	e := &managedStorageEvidenceFixtureV1{mappingOperationEvidenceV1: original}
	ops.Evidence = e
	return name, ops, state, e
}

func managedStorageRequestFixtureV1() ManagedStorageOperationV1 {
	root := ManagedStorageNodeV1{Device: "1", Inode: "2", Mount: "3", UID: "100000", GID: "200000", Mode: "16872"}
	mode := uint32(0o600)
	return ManagedStorageOperationV1{Action: "prepare", Resources: []ManagedStorageResourceV1{{ID: "owned-data", Root: root,
		Declarations: ManagedStorageDeclarationsV1([]blueprint.ManagedMountObject{{Path: ".", Directory: true, Identity: blueprint.MountMetadataIdentity{UID: 0, GID: 0}}, {Path: "file", Identity: blueprint.MountMetadataIdentity{UID: 1, GID: 1, Permissions: &mode}}})}},
		Staging: []ManagedStorageNodeV1{{Device: "1", Inode: "4", Mount: "3", UID: "100000", GID: "200000", Mode: "16832"}}}
}

func managedStorageResultFixtureV1(state StateV1) []ManagedStorageResourceV1 {
	resources := cloneMappingStateV1(state).IdentityMapping.PendingOperation.Storage.Resources
	for i := range resources {
		r := &resources[i]
		r.Objects = []ManagedStorageObjectV1{{Path: ".", Identity: r.Root}, {Path: "file", Identity: ManagedStorageNodeV1{Device: "1", Inode: "5", Mount: "3", UID: "100001", GID: "200001", Mode: "33152"}}}
	}
	return resources
}

func TestManagedStoragePreparationAndExactAttachmentV1(t *testing.T) {
	_, ops, original, evidence := managedStorageFixtureV1(t)
	request := managedStorageRequestFixtureV1()
	wire, _ := json.Marshal(request)
	intent, err := ops.PrepareManagedStorage(t.Context(), original, operationGeometryV1(100000), "prepare", request)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(request)
	if !bytes.Equal(wire, after) {
		t.Fatal("preparation mutated caller-owned slices or pointers")
	}
	effects, err := ops.BeginEffects(t.Context(), intent, "prepare")
	if err != nil {
		t.Fatal(err)
	}
	objects := managedStorageResultFixtureV1(effects)
	verified, err := ops.RecordManagedStorage(t.Context(), effects, "prepare", objects)
	if err != nil {
		t.Fatal(err)
	}
	objects[0].Root.Inode = "99"
	prepared, err := ops.Recover(t.Context(), verified, "prepare", false)
	if err != nil || prepared.IdentityMapping.PendingOperation != nil || len(prepared.IdentityMapping.ManagedStorage) != 1 {
		t.Fatalf("prepared ownership was not committed: %v", err)
	}
	if prepared.IdentityMapping.ManagedStorage[0].Root.Inode != "2" || prepared.IdentityMapping.NativeProcess != nil || prepared.IdentityMapping.AdmittedGeneration != nil ||
		!reflect.DeepEqual(prepared.IdentityMapping.RetainedStorage, original.IdentityMapping.RetainedStorage) {
		t.Fatal("resource completion altered provenance, reservations or native admission")
	}
	attach := ManagedStorageOperationV1{Action: "attach", Resources: prepared.IdentityMapping.ManagedStorage, Attachment: &ManagedStorageAttachmentV1{Engine: "owned-engine", Process: "owned-instance"}}
	intent, err = ops.PrepareManagedStorage(t.Context(), prepared, operationGeometryV1(100000), "attach", attach)
	if err == nil {
		effects, err = ops.BeginEffects(t.Context(), intent, "attach")
	}
	if err != nil {
		t.Fatal(err)
	}
	result := cloneMappingStateV1(effects).IdentityMapping.PendingOperation.Storage.Resources
	result[0].Attachment = attach.Attachment
	// An actual attachment exists by this point. Result recording must verify
	// that positive result, rather than run the pre-attachment absence gate.
	evidence.observe = func(_ context.Context, state StateV1) error {
		if state.IdentityMapping.PendingOperation.Phase != "verified" || !reflect.DeepEqual(state.IdentityMapping.PendingOperation.Storage.Resources[0].Attachment, attach.Attachment) {
			return errors.New("exact attached instance is not the proposed verified result")
		}
		return nil
	}
	verified, err = ops.RecordManagedStorage(t.Context(), effects, "attach", result)
	if err == nil {
		prepared, err = ops.Recover(t.Context(), verified, "attach", false)
	}
	if err != nil || !reflect.DeepEqual(prepared.IdentityMapping.ManagedStorage[0].Attachment, attach.Attachment) {
		t.Fatalf("exact attachment did not commit: %v", err)
	}
	request.Resources[0] = prepared.IdentityMapping.ManagedStorage[0]
	request.Resources[0].Attachment = nil
	if _, err := ops.PrepareManagedStorage(t.Context(), prepared, operationGeometryV1(100000), "prepare-attached", request); err == nil {
		t.Fatal("recorded attachment permitted preparation")
	}
}

func TestManagedStorageRefusesUnknownOwnershipAndStaleInputsBeforeEffectsV1(t *testing.T) {
	for _, defect := range []string{"observer", "unknown", "foreign-id", "staging-alias", "operator-owner", "changed-map", "cancelled"} {
		t.Run(defect, func(t *testing.T) {
			name, ops, state, e := managedStorageFixtureV1(t)
			before, _ := os.ReadFile(name)
			request, active, ctx := managedStorageRequestFixtureV1(), operationGeometryV1(100000), t.Context()
			switch defect {
			case "observer":
				ops.Evidence = e.mappingOperationEvidenceV1
			case "unknown":
				e.observe = func(context.Context, StateV1) error { return errors.New("attachment/ownership inventory unavailable") }
			case "foreign-id":
				request.Resources[0].ID = "foreign-name-with-matching-uid"
			case "staging-alias":
				request.Staging[0] = request.Resources[0].Root
			case "operator-owner":
				request.Resources[0].Root.UID = "1000"
			case "changed-map":
				active.UIDs[0].HostStart++
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if _, err := ops.PrepareManagedStorage(ctx, state, active, "refused", request); err == nil {
				t.Fatal("unproved resource authority was accepted")
			}
			after, _ := os.ReadFile(name)
			if !bytes.Equal(before, after) {
				t.Fatal("refused preparation changed durable state")
			}
		})
	}
}

func TestManagedStorageConcurrentIntentsAndLostObservationsV1(t *testing.T) {
	name, ops, original, e := managedStorageFixtureV1(t)
	var group sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := ops.PrepareManagedStorage(t.Context(), original, operationGeometryV1(100000), id, managedStorageRequestFixtureV1())
			results <- err
		}()
	}
	group.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent operation successes=%d", success)
	}
	intent := readOperationStateV1(t, ops)
	op := intent.IdentityMapping.PendingOperation.OperationID
	effects, err := ops.BeginEffects(t.Context(), intent, op)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(name)
	e.observe = func(context.Context, StateV1) error { return errors.New("lost exact observations") }
	if _, err := ops.ObserveEffects(t.Context(), effects, operationGeometryV1(100000), op); err == nil {
		t.Fatal("lost observations authorized a physical effect")
	}
	if _, err := ops.Recover(t.Context(), effects, op, false); err == nil {
		t.Fatal("process exit/API success without a result cleared inhibition")
	}
	if _, err := ops.Recover(t.Context(), effects, op, true); err == nil {
		t.Fatal("effects-pending storage was cancelled")
	}
	after, _ := os.ReadFile(name)
	if !bytes.Equal(before, after) {
		t.Fatal("unknown effects changed resource ownership or reservations")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	if competing, err := AcquireExistingOperationLock(ctx, filepath.Dir(filepath.Dir(name))); err == nil {
		competing.Unlock()
		t.Fatal("another attachment/preparation operation acquired the held installation lock")
	}
}

func TestManagedStorageCrashRetainsPendingOwnershipV1(t *testing.T) {
	const childKey = "REPLOY_TEST_STORAGE_CRASH_STATE"
	if name := os.Getenv(childKey); name != "" {
		lock, err := AcquireExistingOperationLock(t.Context(), filepath.Dir(filepath.Dir(name)))
		if err != nil {
			t.Fatal(err)
		}
		ops := IdentityMappingOperationsV1{Operation: lock, Evidence: &managedStorageEvidenceFixtureV1{mappingOperationEvidenceV1: &mappingOperationEvidenceV1{handles: []string{"owned-data"}}},
			hostCheck: func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error) {
				return identitymapping.HostIdentity{Username: "operator", UID: 1000, GID: 1000}, nil
			}}
		state := readOperationStateV1(t, ops)
		if _, err := ops.BeginEffects(t.Context(), state, "crash"); err != nil {
			t.Fatal(err)
		}
		os.Exit(73) // No Unlock or test cleanup: the process loses its kernel owner.
	}
	name, ops, original, _ := managedStorageFixtureV1(t)
	intent, err := ops.PrepareManagedStorage(t.Context(), original, operationGeometryV1(100000), "crash", managedStorageRequestFixtureV1())
	if err != nil {
		t.Fatal(err)
	}
	if err := ops.Operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestManagedStorageCrashRetainsPendingOwnershipV1$", "-test.count=1")
	command.Env = append(os.Environ(), childKey+"="+name)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("crash child: %v\n%s", err, output)
	}
	lock, err := AcquireExistingOperationLock(t.Context(), filepath.Dir(filepath.Dir(name)))
	if err != nil {
		t.Fatal("kernel lock did not retire", err)
	}
	defer lock.Unlock()
	ops.Operation = lock
	state := readOperationStateV1(t, ops)
	p := state.IdentityMapping.PendingOperation
	if p == nil || p.Phase != "effects-pending" || !p.Inhibited || !reflect.DeepEqual(p.Storage, intent.IdentityMapping.PendingOperation.Storage) || !reflect.DeepEqual(state.IdentityMapping.RetainedStorage, original.IdentityMapping.RetainedStorage) {
		t.Fatal("crash lost exact intent or reservations")
	}
	before, _ := os.ReadFile(name)
	for _, cancel := range []bool{false, true} {
		if _, err := ops.Recover(t.Context(), state, "crash", cancel); err == nil {
			t.Fatal("process retirement alone released resource inhibition")
		}
	}
	after, _ := os.ReadFile(name)
	if !bytes.Equal(before, after) {
		t.Fatal("unknown crash outcome changed durable ownership")
	}
}

func TestManagedStorageCancellationRequiresCurrentTypedObservationV1(t *testing.T) {
	name, ops, original, e := managedStorageFixtureV1(t)
	intent, err := ops.PrepareManagedStorage(t.Context(), original, operationGeometryV1(100000), "unused", managedStorageRequestFixtureV1())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(name)
	ops.Evidence = e.mappingOperationEvidenceV1
	e.effects = false // A generic never-used success is insufficient for storage.
	if _, err := ops.Recover(t.Context(), intent, "unused", true); err == nil {
		t.Fatal("generic cancellation bypassed exact storage observations")
	}
	after, _ := os.ReadFile(name)
	if !bytes.Equal(before, after) {
		t.Fatal("unknown cancellation changed state")
	}
	ops.Evidence = e
	state, err := ops.Recover(t.Context(), intent, "unused", true)
	if err != nil || state.IdentityMapping.PendingOperation != nil || !reflect.DeepEqual(state.IdentityMapping.RetainedStorage, original.IdentityMapping.RetainedStorage) {
		t.Fatal("proved unused cancellation lost reservations", err)
	}
}

func TestManagedStorageResultRequiresCompleteCurrentProofV1(t *testing.T) {
	for _, defect := range []string{"partial", "authority", "unknown-result", "lost-completion"} {
		t.Run(defect, func(t *testing.T) {
			name, ops, original, e := managedStorageFixtureV1(t)
			intent, err := ops.PrepareManagedStorage(t.Context(), original, operationGeometryV1(100000), "result", managedStorageRequestFixtureV1())
			if err != nil {
				t.Fatal(err)
			}
			effects, err := ops.BeginEffects(t.Context(), intent, "result")
			if err != nil {
				t.Fatal(err)
			}
			resources := managedStorageResultFixtureV1(effects)
			if defect == "partial" {
				resources = nil
			}
			if defect == "authority" {
				resources[0].Root.Inode = "99"
			}
			if defect == "unknown-result" {
				e.observe = func(context.Context, StateV1) error { return errors.New("result observation unavailable") }
			}
			before, _ := os.ReadFile(name)
			verified, err := ops.RecordManagedStorage(t.Context(), effects, "result", resources)
			if defect == "lost-completion" {
				if err != nil {
					t.Fatal(err)
				}
				before, _ = os.ReadFile(name)
				e.observe = func(context.Context, StateV1) error { return errors.New("completion observation unavailable") }
				_, err = ops.Recover(t.Context(), verified, "result", false)
			}
			if err == nil {
				t.Fatal("incomplete or unknown result committed")
			}
			after, _ := os.ReadFile(name)
			if !bytes.Equal(before, after) {
				t.Fatal("refusal changed durable authority")
			}
		})
	}
}

func TestManagedStoragePunctuationAndUnicodePathsV1(t *testing.T) {
	for _, path := range []string{" file", "!file", "-file", "配置", "\ufffd"} {
		t.Run(path, func(t *testing.T) {
			_, ops, original, _ := managedStorageFixtureV1(t)
			request := managedStorageRequestFixtureV1()
			request.Resources[0].Declarations[1].Path = path
			intent, err := ops.PrepareManagedStorage(t.Context(), original, operationGeometryV1(100000), "path", request)
			if err != nil {
				t.Fatal(err)
			}
			effects, err := ops.BeginEffects(t.Context(), intent, "path")
			if err != nil {
				t.Fatal(err)
			}
			result := managedStorageResultFixtureV1(effects)
			result[0].Objects[1].Path = path
			sort.Slice(result[0].Objects, func(a, b int) bool { return result[0].Objects[a].Path < result[0].Objects[b].Path })
			verified, err := ops.RecordManagedStorage(t.Context(), effects, "path", result)
			if err != nil {
				t.Fatal("valid complete inventory rejected", err)
			}
			prepared, err := ops.Recover(t.Context(), verified, "path", false)
			if err != nil {
				t.Fatal(err)
			}
			content, err := EncodeStateV1(prepared)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeStateV1(content)
			if err != nil || !reflect.DeepEqual(decoded, prepared) || decoded.IdentityMapping.ManagedStorage[0].Declarations[1].Path != path {
				t.Fatal("exact path did not survive preparation and canonical round trip", err)
			}
			for _, defect := range []string{"missing-root", "changed-root", "duplicate", "unsorted"} {
				t.Run(defect, func(t *testing.T) {
					bad := cloneMappingStateV1(prepared)
					r := &bad.IdentityMapping.ManagedStorage[0]
					root := 0
					if r.Objects[root].Path != "." {
						root = 1
					}
					switch defect {
					case "missing-root":
						r.Objects = append(r.Objects[:root], r.Objects[root+1:]...)
					case "changed-root":
						r.Objects[root].Identity.Inode = "999"
					case "duplicate":
						r.Objects[root] = r.Objects[1-root]
					case "unsorted":
						r.Objects[0], r.Objects[1] = r.Objects[1], r.Objects[0]
					}
					if _, err := EncodeStateV1(bad); err == nil {
						t.Fatal("invalid root or inventory accepted")
					}
				})
			}
		})
	}
}

func TestManagedStorageRejectsInvalidUTF8BeforeStateWriteV1(t *testing.T) {
	for _, boundary := range []string{"intent", "result"} {
		t.Run(boundary, func(t *testing.T) {
			name, ops, original, _ := managedStorageFixtureV1(t)
			request := managedStorageRequestFixtureV1()
			request.Resources[0].Declarations[1].Path = "\ufffd"
			before, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "intent" {
				request.Resources[0].Declarations[1].Path = string([]byte{0xff})
				_, err = ops.PrepareManagedStorage(t.Context(), original, operationGeometryV1(100000), "utf8", request)
			} else {
				intent, prepareErr := ops.PrepareManagedStorage(t.Context(), original, operationGeometryV1(100000), "utf8", request)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				effects, beginErr := ops.BeginEffects(t.Context(), intent, "utf8")
				if beginErr != nil {
					t.Fatal(beginErr)
				}
				before, err = os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				result := managedStorageResultFixtureV1(effects)
				result[0].Objects[1].Path = string([]byte{0xff})
				_, err = ops.RecordManagedStorage(t.Context(), effects, "utf8", result)
			}
			if err == nil {
				t.Fatal("invalid UTF-8 was accepted through lossy detachment")
			}
			after, err := os.ReadFile(name)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("refused input changed durable state", err)
			}
		})
	}
}
