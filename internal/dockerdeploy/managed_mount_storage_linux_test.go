//go:build linux

package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"golang.org/x/sys/unix"
)

// This fixture tests descriptor observation with ordinary operator-owned files.
// It does not qualify subordinate authority or supply an engine restart fence.
func managedMountStorageObservationFixtureV1(t *testing.T) (deploy.StateV1, *managedMountStorageEvidenceV1) {
	t.Helper()
	dir, _ := stageProviderBuildRunState(t, false)
	lock, err := deploy.AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	state, found, err := lock.ReadStateV1()
	if err != nil || !found {
		t.Fatal(err)
	}
	state.Staging = &deploy.StagingStateV1{Schema: deploy.StagingStateSchemaV1}
	state.BlueprintSource = "private descriptor observation fixture"
	digest := canonical.Digest("sha256:" + strings.Repeat("a", 64))
	state.Current = &deploy.EnvironmentGenerationState{Reference: "reploy/env/demo:g-storage-fixture", ImageDigest: digest, RootFSSubject: digest, BuildLockDigest: digest, Platform: state.Platform, RuntimePolicyDigest: digest}
	rootPath, stagingPath := filepath.Join(dir, "root"), filepath.Join(dir, "staging")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stagingPath, 0o700); err != nil {
		t.Fatal(err)
	}
	root, stage := managedMountSetupHeldV1(t, rootPath), managedMountSetupHeldV1(t, stagingPath)
	rootID, err := managedMountStorageNodeV1(int(root.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	stageID, err := managedMountStorageNodeV1(int(stage.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	empty := []deploy.IdentityMappingIntervalV1{}
	geometry := deploy.IdentityMappingGeometryV1{Profile: "exact", UIDs: []deploy.IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: rootID.UID, Count: "1"}}, GIDs: []deploy.IdentityMappingIntervalV1{{ContainerStart: "0", HostStart: rootID.GID, Count: "1"}}}
	state.IdentityMapping = &deploy.IdentityMappingStateV1{Schema: deploy.IdentityMappingStateSchemaV2, InstallationID: "storage-fixture", OwnerDir: dir, Revision: "1", OperatorUID: "1000", OperatorGID: "1000", Profile: "exact", UIDAssignments: geometry.UIDs, GIDAssignments: geometry.GIDs, ProcessModel: deploy.IdentityMappingNativeProcessModelV1, AcceptedUIDs: &empty, AcceptedGIDs: &empty, RetainedStorage: []string{"resource"}}
	mode := uint32(0o700)
	r := deploy.ManagedStorageResourceV1{ID: "resource", Root: rootID, Generation: state.Current, Active: geometry, Declarations: deploy.ManagedStorageDeclarationsV1([]blueprint.ManagedMountObject{{Path: ".", Directory: true, Identity: blueprint.MountMetadataIdentity{Permissions: &mode}}})}
	state.IdentityMapping.PendingOperation = &deploy.IdentityMappingOperationV1{OperationID: "prepare", Kind: "managed-storage", ExpectedRevision: "1", ExpectedGeneration: state.Current, Candidate: geometry, OwnedObjects: []string{"resource"}, Phase: "effects-pending", Inhibited: true, Storage: &deploy.ManagedStorageOperationV1{Action: "prepare", Active: geometry, Resources: []deploy.ManagedStorageResourceV1{r}, Staging: []deploy.ManagedStorageNodeV1{stageID}}}
	e := &managedMountStorageEvidenceV1{handles: map[string]*managedMountStorageHandlesV1{"resource": {root: root, staging: stage}}, observe: func(context.Context, deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
		return map[string][]deploy.ManagedStorageAttachmentV1{"resource": {}}, nil
	}}
	if err := e.ObserveManagedStorage(t.Context(), state); err != nil {
		t.Fatal("positive descriptor fixture", err)
	}
	return state, e
}

func TestManagedMountStorageObservationRefusesLostAuthorityV1(t *testing.T) {
	for _, defect := range []string{"observer", "inventory", "foreign-id", "attached", "unknown", "root-descriptor", "staging-descriptor", "metadata-drift", "unmapped-declaration", "missing-object", "helper-retirement", "callback-mutation", "cancelled"} {
		t.Run(defect, func(t *testing.T) {
			state, e := managedMountStorageObservationFixtureV1(t)
			ctx := t.Context()
			s := state.IdentityMapping.PendingOperation.Storage
			switch defect {
			case "observer":
				e.observe = nil
			case "inventory":
				e.observe = func(context.Context, deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
					return map[string][]deploy.ManagedStorageAttachmentV1{}, nil
				}
			case "foreign-id":
				e.observe = func(context.Context, deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
					return map[string][]deploy.ManagedStorageAttachmentV1{"other": {}}, nil
				}
			case "attached":
				e.observe = func(context.Context, deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
					return map[string][]deploy.ManagedStorageAttachmentV1{"resource": {{Engine: "engine", Process: "instance"}}}, nil
				}
			case "unknown":
				e.observe = func(context.Context, deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
					return nil, errors.New("restart owner unknown")
				}
			case "root-descriptor":
				e.handles["resource"].root = e.handles["resource"].staging
			case "staging-descriptor":
				e.handles["resource"].staging = nil
			case "metadata-drift":
				if err := os.Chmod(e.handles["resource"].root.Name(), 0o750); err != nil {
					t.Fatal(err)
				}
			case "unmapped-declaration":
				s.Resources[0].Declarations[0].UID = "1"
			case "missing-object":
				s.Resources[0].Objects = []deploy.ManagedStorageObjectV1{{Path: ".", Identity: s.Resources[0].Root}}
			case "helper-retirement":
				state.IdentityMapping.PendingOperation.Phase = "verified"
				s.Resources[0].Objects = []deploy.ManagedStorageObjectV1{{Path: ".", Identity: s.Resources[0].Root}}
				e.handles["resource"].recorded = map[string]*os.File{".": e.handles["resource"].root}
			case "callback-mutation":
				e.observe = func(_ context.Context, copy deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
					copy.IdentityMapping.PendingOperation.Storage.Resources[0].Root.Inode = "999"
					return map[string][]deploy.ManagedStorageAttachmentV1{"resource": {}}, nil
				}
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			err := e.ObserveManagedStorage(ctx, state)
			if defect == "callback-mutation" {
				if err != nil || s.Resources[0].Root.Inode == "999" {
					t.Fatal("observer mutated captured proof", err)
				}
				return
			}
			if err == nil {
				t.Fatal("unproved storage authorized")
			}
		})
	}
}

func TestManagedMountStorageExactAttachmentObservationV1(t *testing.T) {
	state, e := managedMountStorageObservationFixtureV1(t)
	p := state.IdentityMapping.PendingOperation
	s := p.Storage
	h := e.handles["resource"]
	s.Resources[0].Objects = []deploy.ManagedStorageObjectV1{{Path: ".", Identity: s.Resources[0].Root}}
	h.recorded = map[string]*os.File{".": h.root}
	s.Action, s.Staging, s.Attachment = "attach", nil, &deploy.ManagedStorageAttachmentV1{Engine: "owned-engine", Process: "owned-instance"}
	if err := e.ObserveManagedStorage(t.Context(), state); err != nil {
		t.Fatal("unattached precondition", err)
	}
	p.Phase = "verified"
	s.Resources[0].Attachment = s.Attachment
	for _, actual := range [][]deploy.ManagedStorageAttachmentV1{nil, {{Engine: "owned-engine", Process: "foreign-instance"}}, {{Engine: "foreign-engine", Process: "owned-instance"}}, {*s.Attachment, *s.Attachment}, {*s.Attachment}} {
		e.observe = func(context.Context, deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
			return map[string][]deploy.ManagedStorageAttachmentV1{"resource": actual}, nil
		}
		err := e.ObserveManagedStorage(t.Context(), state)
		if (err == nil) != (len(actual) == 1 && actual[0] == *s.Attachment) {
			t.Fatalf("exact attachment=%v err=%v", actual, err)
		}
	}
	for _, phase := range []string{"intent", "effects-pending", "verified"} {
		p.Phase = phase
		if phase != "verified" {
			s.Resources[0].Attachment = nil
		}
		if phase != "intent" && e.Unused(t.Context(), state) == nil {
			t.Fatal("effects allowed intent cancellation")
		}
	}
}

func TestManagedMountStorageObserverCannotAuthorizeOtherOperationsV1(t *testing.T) {
	state, e := managedMountStorageObservationFixtureV1(t)
	geometry, err := state.IdentityMapping.PendingOperation.Storage.Active.Geometry()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Verify(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	state.IdentityMapping.PendingOperation.Phase = "intent"
	if err := e.Unused(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if ids, err := e.Plan(t.Context(), state, geometry); err != nil || !reflect.DeepEqual(ids, []string{"resource"}) {
		t.Fatal("lost exact owned selection", err)
	}
	state.IdentityMapping.PendingOperation = nil
	if e.Verify(t.Context(), state) == nil || e.Unused(t.Context(), state) == nil {
		t.Fatal("resource observer authorized a different operation")
	}
	if _, err := e.Plan(t.Context(), state, geometry); err == nil {
		t.Fatal("resource observer planned a different operation")
	}
	for _, owner := range []*managedMountStorageEvidenceV1{nil, e} {
		if _, result, err := prepareRecordedManagedMountStorageV1(t.Context(), deploy.IdentityMappingOperationsV1{}, state, "foreign", owner); err == nil || result != nil {
			t.Fatal("unowned preparation started")
		}
	}
}

func TestManagedMountStorageNativeTransactionV1(t *testing.T) {
	dir := os.Getenv("REPLOY_TEST_MANAGED_SETUP_ROOT")
	if dir == "" {
		t.Skip("requires disposable files-only subordinate-authority guest")
	}
	ops, state := managedMountSetupOwnerV1(t)
	var err error
	state, err = ops.Recover(t.Context(), state, "private-setup", false)
	if err == nil {
		state, err = ops.UpgradeNativeMapping(t.Context(), state)
	}
	if err != nil {
		t.Fatal(err)
	}
	root, stage := managedMountSetupHeldV1(t, filepath.Join(dir, "root")), managedMountSetupHeldV1(t, filepath.Join(dir, "staging"))
	rootID, err := managedMountStorageNodeV1(int(root.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	stageID, err := managedMountStorageNodeV1(int(stage.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	protected, err := os.ReadFile(filepath.Join(dir, "protected"))
	if err != nil {
		t.Fatal(err)
	}
	const id = "private-setup-fixture"
	e := &managedMountStorageEvidenceV1{handles: map[string]*managedMountStorageHandlesV1{id: {root: root, staging: stage}}, observe: func(context.Context, deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
		return map[string][]deploy.ManagedStorageAttachmentV1{id: {}}, nil
	}}
	refuseResult := os.Getenv("REPLOY_TEST_MANAGED_STORAGE_RESULT_REFUSAL") == "1"
	var returnedFDs []int
	var retainedFDs []int
	if refuseResult {
		e.observe = func(_ context.Context, observed deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
			if observed.IdentityMapping.PendingOperation.Phase == "verified" {
				for _, object := range e.handles[id].prepared {
					returnedFDs = append(returnedFDs, object.FD)
					fd, err := unix.FcntlInt(uintptr(object.FD), unix.F_DUPFD_CLOEXEC, 0)
					if err != nil {
						return nil, err
					}
					retainedFDs = append(retainedFDs, fd)
					t.Cleanup(func() { unix.Close(fd) })
				}
				return nil, errors.New("fresh result observation lost")
			}
			return map[string][]deploy.ManagedStorageAttachmentV1{id: {}}, nil
		}
	}
	ops.Evidence = e
	declarations := managedMountSetupDeclarationsV1()
	file := declarations[2]
	file.Path = "!file"
	declarations, err = orderManagedMountObjectsV1(append(declarations, file))
	if err != nil {
		t.Fatal(err)
	}
	request := deploy.ManagedStorageOperationV1{Action: "prepare", Resources: []deploy.ManagedStorageResourceV1{{ID: id, Root: rootID, Declarations: deploy.ManagedStorageDeclarationsV1(declarations)}}, Staging: []deploy.ManagedStorageNodeV1{stageID}}
	intent, err := ops.PrepareManagedStorage(t.Context(), state, managedMountSetupGeometryV1(), "storage", request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ops.PrepareManagedStorage(t.Context(), state, managedMountSetupGeometryV1(), "competing", request); err == nil {
		t.Fatal("competing stale intent authorized")
	}
	effects, err := ops.BeginEffects(t.Context(), intent, "storage")
	if err != nil {
		t.Fatal(err)
	}
	committed, objects, err := prepareRecordedManagedMountStorageV1(t.Context(), ops, effects, "storage", e)
	if refuseResult {
		if err == nil || objects != nil || len(returnedFDs) != len(declarations) || e.handles[id].prepared != nil {
			t.Fatal("unknown result escaped as a prepared capability", err)
		}
		for _, fd := range returnedFDs {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Fatal("failed result leaked a returned descriptor", fd, err)
			}
		}
		actual, found, err := ops.Operation.ReadStateV1()
		if err != nil || !found || !reflect.DeepEqual(actual, effects) {
			t.Fatal("unknown result cleared inhibition or ownership", err)
		}
		for _, fd := range retainedFDs {
			var stat unix.Stat_t
			if err := unix.Fstat(fd, &stat); err != nil || stat.Nlink == 0 {
				t.Fatal("inert result inode was deleted", err)
			}
		}
		after, err := os.ReadFile(filepath.Join(dir, "protected"))
		if err != nil || string(after) != string(protected) {
			t.Fatal("unrelated target changed", err)
		}
		if _, err := ops.Recover(t.Context(), actual, "storage", false); err == nil {
			t.Fatal("successful helper exit alone completed the journal")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, resource := range objects {
			for _, object := range resource {
				unix.Close(object.FD)
			}
		}
	}()
	if committed.IdentityMapping.PendingOperation != nil || len(committed.IdentityMapping.ManagedStorage) != 1 || committed.IdentityMapping.NativeProcess != nil || committed.IdentityMapping.AdmittedGeneration != nil {
		t.Fatal("preparation lost ownership or granted execution")
	}
	r := committed.IdentityMapping.ManagedStorage[0]
	if len(r.Objects) != len(declarations) || r.Objects[0].Path != "!file" || !reflect.DeepEqual(r.Root, rootID) || r.Attachment != nil {
		t.Fatal("incorrect committed resource inventory")
	}
	for _, object := range r.Objects {
		actual, err := managedMountStorageNodeV1(objects[id][object.Path].FD)
		if err != nil || actual != object.Identity {
			t.Fatal("returned descriptors differ from durable ownership", err)
		}
		if (object.Path == "private/data" || object.Path == "!file") && actual.UID != strconv.Itoa(100200) {
			t.Fatal("recipient ownership changed")
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, "protected"))
	if err != nil || string(after) != string(protected) {
		t.Fatal("unrelated target changed", err)
	}
}

func TestManagedMountStorageObservationFreezesCallerStateV1(t *testing.T) {
	state, e := managedMountStorageObservationFixtureV1(t)
	inode := state.IdentityMapping.PendingOperation.Storage.Resources[0].Root.Inode
	e.observe = func(_ context.Context, observed deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
		if observed.IdentityMapping.PendingOperation.Storage.Resources[0].Root.Inode != inode {
			t.Fatal("observer did not receive the frozen input")
		}
		state.IdentityMapping.PendingOperation.Storage.Resources[0].Root.Inode = "999"
		observed.IdentityMapping.PendingOperation.Storage.Resources[0].Root.Inode = "998"
		return map[string][]deploy.ManagedStorageAttachmentV1{"resource": {}}, nil
	}
	if err := e.ObserveManagedStorage(t.Context(), state); err != nil {
		t.Fatal("callback changed private proof inputs", err)
	}
	if err := e.ObserveManagedStorage(nil, state); err == nil {
		t.Fatal("nil context authorized observation")
	}
}

func managedMountStorageFDCountsV1(t *testing.T, objects map[string]preparedManagedMountObjectV1) map[string]int {
	t.Helper()
	files, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for name, object := range objects {
		var expected unix.Stat_t
		if err := unix.Fstat(object.FD, &expected); err != nil {
			t.Fatal("caller descriptor closed", err)
		}
		for _, file := range files {
			fd, err := strconv.Atoi(file.Name())
			var actual unix.Stat_t
			if err == nil && unix.Fstat(fd, &actual) == nil && actual.Dev == expected.Dev && actual.Ino == expected.Ino {
				counts[name]++
			}
		}
	}
	return counts
}

func TestManagedMountStorageInspectionBorrowsDescriptorsV1(t *testing.T) {
	state, e := managedMountStorageObservationFixtureV1(t)
	root := e.handles["resource"].root
	objects := map[string]preparedManagedMountObjectV1{".": {FD: int(root.Fd())}}
	before := managedMountStorageFDCountsV1(t, objects)
	geometry, err := state.IdentityMapping.PendingOperation.Storage.Active.Geometry()
	if err != nil {
		t.Fatal(err)
	}
	refused := errors.New("current authorization lost")
	err = inspectManagedMountStoragePathsV1(t.Context(), geometry, root, nil, map[string]int{".": int(root.Fd())}, func(context.Context) error { return refused })
	if !errors.Is(err, refused) || !reflect.DeepEqual(before, managedMountStorageFDCountsV1(t, objects)) {
		t.Fatal("inspection closed caller descriptors or leaked temporary duplicates", err)
	}
}

func TestManagedMountStorageNativePathBindingV1(t *testing.T) {
	dir, mode := os.Getenv("REPLOY_TEST_MANAGED_SETUP_ROOT"), os.Getenv("REPLOY_TEST_MANAGED_STORAGE_PATH_MODE")
	if dir == "" {
		t.Skip("requires disposable files-only subordinate-authority guest")
	}
	ops, state := managedMountSetupOwnerV1(t)
	var err error
	state, err = ops.Recover(t.Context(), state, "private-setup", false)
	if err == nil {
		state, err = ops.UpgradeNativeMapping(t.Context(), state)
	}
	if err != nil {
		t.Fatal(err)
	}
	root, stage := managedMountSetupHeldV1(t, filepath.Join(dir, "root")), managedMountSetupHeldV1(t, filepath.Join(dir, "staging"))
	rootID, err := managedMountStorageNodeV1(int(root.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	stageID, err := managedMountStorageNodeV1(int(stage.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	const id = "private-setup-fixture"
	observe := func(_ context.Context, observed deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
		p := observed.IdentityMapping.PendingOperation
		attachments := []deploy.ManagedStorageAttachmentV1{}
		if p.Storage.Action == "attach" && p.Phase == "verified" {
			attachments = append(attachments, *p.Storage.Attachment)
		}
		return map[string][]deploy.ManagedStorageAttachmentV1{id: attachments}, nil
	}
	e := &managedMountStorageEvidenceV1{handles: map[string]*managedMountStorageHandlesV1{id: {root: root, staging: stage}}, observe: observe}
	ops.Evidence = e
	request := deploy.ManagedStorageOperationV1{Action: "prepare", Resources: []deploy.ManagedStorageResourceV1{{ID: id, Root: rootID, Declarations: deploy.ManagedStorageDeclarationsV1(managedMountSetupDeclarationsV1())}}, Staging: []deploy.ManagedStorageNodeV1{stageID}}
	intent, err := ops.PrepareManagedStorage(t.Context(), state, managedMountSetupGeometryV1(), "storage", request)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := ops.BeginEffects(t.Context(), intent, "storage")
	if err != nil {
		t.Fatal(err)
	}
	committed, prepared, err := prepareRecordedManagedMountStorageV1(t.Context(), ops, effects, "storage", e)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, resource := range prepared {
			for _, object := range resource {
				unix.Close(object.FD)
			}
		}
	}()
	resource := committed.IdentityMapping.ManagedStorage[0]
	if mode == "reprepare" {
		// Model the existing owner's transfer of completed capabilities into
		// borrowed retained handles before a later preparation operation.
		h := e.handles[id]
		h.recorded = map[string]*os.File{}
		for name, object := range prepared[id] {
			fd, err := unix.FcntlInt(uintptr(object.FD), unix.F_DUPFD_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			file := os.NewFile(uintptr(fd), "retained-owner")
			t.Cleanup(func() { file.Close() })
			h.recorded[name] = file
		}
		h.prepared = nil
		permissions := uint32(0o600)
		declarations := append(managedMountSetupDeclarationsV1(), blueprint.ManagedMountObject{Path: "private/new", Identity: blueprint.MountMetadataIdentity{UID: 201, GID: 300, Permissions: &permissions}})
		declarations, err = orderManagedMountObjectsV1(declarations)
		if err != nil {
			t.Fatal(err)
		}
		resource.Declarations = deploy.ManagedStorageDeclarationsV1(declarations)
		request.Resources = []deploy.ManagedStorageResourceV1{resource}
		intent, err = ops.PrepareManagedStorage(t.Context(), committed, managedMountSetupGeometryV1(), "reprepare", request)
		if err == nil {
			effects, err = ops.BeginEffects(t.Context(), intent, "reprepare")
		}
		if err != nil {
			t.Fatal("new absent path was incorrectly required during retained observation", err)
		}
		next, result, err := prepareRecordedManagedMountStorageV1(t.Context(), ops, effects, "reprepare", e)
		if err != nil {
			t.Fatal(err)
		}
		for _, objects := range result {
			for _, object := range objects {
				defer unix.Close(object.FD)
			}
		}
		if next.IdentityMapping.PendingOperation != nil || len(result[id]) != 4 || len(next.IdentityMapping.ManagedStorage[0].Objects) != 4 {
			t.Fatal("incomplete retained-inventory preparation")
		}
		return
	}
	attachment := &deploy.ManagedStorageAttachmentV1{Engine: "owned-engine", Process: "owned-instance"}
	request = deploy.ManagedStorageOperationV1{Action: "attach", Resources: []deploy.ManagedStorageResourceV1{resource}, Attachment: attachment}
	intent, err = ops.PrepareManagedStorage(t.Context(), committed, managedMountSetupGeometryV1(), "attach", request)
	if err == nil {
		effects, err = ops.BeginEffects(t.Context(), intent, "attach")
	}
	if err != nil {
		t.Fatal("exact retained path failed attachment admission", err)
	}
	oracle := mode == "replacement" || mode == "missing" || mode == "symlink" || mode == "type" || mode == "mount"
	if oracle {
		if err := os.WriteFile(filepath.Join(dir, "replace-ready"), []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Minute)
		for {
			if _, err := os.Stat(filepath.Join(dir, "replace-done")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("native mutation oracle did not finish")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	calls := 0
	if mode == "authorization" {
		e.observe = func(ctx context.Context, observed deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error) {
			calls++
			if observed.IdentityMapping.PendingOperation.OperationID != "attach" {
				t.Fatal("helper authorization did not receive detached frozen input")
			}
			result, err := observe(ctx, observed)
			observed.IdentityMapping.PendingOperation.OperationID = "observer-mutated-copy"
			if calls == 3 {
				return nil, errors.New("fresh ownership lost before helper release")
			}
			return result, err
		}
	}
	beforeFDs := managedMountStorageFDCountsV1(t, prepared[id])
	err = e.ObserveManagedStorage(t.Context(), effects)
	refusal := oracle || mode == "authorization"
	if (err != nil) != refusal || mode == "authorization" && calls != 3 {
		t.Fatal("incorrect pre-attachment current-path/authorization result", mode, calls, err)
	}
	resources := append([]deploy.ManagedStorageResourceV1(nil), effects.IdentityMapping.PendingOperation.Storage.Resources...)
	resources[0].Attachment = attachment
	calls = 0
	verified, err := ops.RecordManagedStorage(t.Context(), effects, "attach", resources)
	if (err != nil) != refusal || mode == "authorization" && calls != 3 {
		t.Fatal("incorrect exact attached-result current-path/authorization result", mode, calls, err)
	}
	if !reflect.DeepEqual(beforeFDs, managedMountStorageFDCountsV1(t, prepared[id])) {
		t.Fatal("observation closed caller FDs or leaked inspection descriptors")
	}
	actual, found, readErr := ops.Operation.ReadStateV1()
	if readErr != nil || !found {
		t.Fatal(readErr)
	}
	if refusal {
		if !reflect.DeepEqual(actual, effects) {
			t.Fatal("refusal changed durable inhibition, reservations or retained ownership")
		}
		return
	}
	completed, err := ops.Recover(t.Context(), verified, "attach", false)
	if err != nil || completed.IdentityMapping.PendingOperation != nil || completed.IdentityMapping.NativeProcess != nil || completed.IdentityMapping.AdmittedGeneration != nil || completed.IdentityMapping.ManagedStorage[0].Attachment == nil {
		t.Fatal("exact attachment failed or granted execution admission", err)
	}
}
