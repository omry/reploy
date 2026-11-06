//go:build linux

package dockerdeploy

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"golang.org/x/sys/unix"
)

type managedMountStorageHandlesV1 struct {
	root, staging *os.File
	recorded      map[string]*os.File
	prepared      map[string]preparedManagedMountObjectV1
}

// The existing trusted owner supplies complete fresh resource/engine/fence
// observations. It must return every selected ID, including explicit empty
// inventories for unattached resources. Missing/unknown provenance, attachment
// inventory or restart inhibition is an error, never an empty-success shortcut.
// Callbacks execute only trusted host inspection, never image/application code.
type managedMountStorageEvidenceV1 struct {
	handles map[string]*managedMountStorageHandlesV1
	observe func(context.Context, deploy.StateV1) (map[string][]deploy.ManagedStorageAttachmentV1, error)
}

func storageModeV1(value string) uint32 { n, _ := strconv.ParseUint(value, 10, 32); return uint32(n) }

func managedMountStorageNodeV1(fd int) (deploy.ManagedStorageNodeV1, error) {
	n, err := managedMountObjectIdentityForFDV1(fd)
	return deploy.ManagedStorageNodeV1{Device: strconv.FormatUint(n.Device, 10), Inode: strconv.FormatUint(n.Inode, 10), Mount: strconv.FormatUint(n.Mount, 10), UID: strconv.FormatUint(uint64(n.UID), 10), GID: strconv.FormatUint(uint64(n.GID), 10), Mode: strconv.FormatUint(uint64(n.Mode), 10)}, err
}

func (e *managedMountStorageEvidenceV1) ObserveManagedStorage(ctx context.Context, state deploy.StateV1) error {
	if ctx == nil || ctx.Err() != nil || e == nil || e.observe == nil || state.IdentityMapping == nil || state.IdentityMapping.PendingOperation == nil || state.IdentityMapping.PendingOperation.Storage == nil {
		return fmt.Errorf("managed storage requires its exact trusted observer and pending selection")
	}
	// Freeze every proof input before invoking the trusted owner. Each fresh
	// authorization receives a separate detached copy and cannot recurse through
	// the journal writer or operation lock.
	content, err := deploy.EncodeStateV1(state)
	if err != nil {
		return err
	}
	state, err = deploy.DecodeStateV1(content)
	if err != nil {
		return err
	}
	p := state.IdentityMapping.PendingOperation
	s := p.Storage
	active, err := s.Active.Geometry()
	if err != nil {
		return err
	}
	observe := e.observe
	authorize := func(ctx context.Context) error {
		observedState, err := deploy.DecodeStateV1(content)
		if err != nil || ctx.Err() != nil {
			return fmt.Errorf("managed storage authorization is no longer current: %v", err)
		}
		attachments, err := observe(ctx, observedState)
		if err != nil || len(attachments) != len(s.Resources) {
			return fmt.Errorf("managed resource observations are incomplete: %v", err)
		}
		for _, r := range s.Resources {
			actual, complete := attachments[r.ID]
			wanted := []deploy.ManagedStorageAttachmentV1{}
			if s.Action == "attach" && p.Phase == "verified" {
				wanted = append(wanted, *s.Attachment)
			}
			if !complete || len(actual) != len(wanted) || len(actual) == 1 && actual[0] != wanted[0] {
				return fmt.Errorf("managed resource attachment differs from its exact operation")
			}
		}
		return ctx.Err()
	}
	if err := authorize(ctx); err != nil {
		return err
	}
	for i, r := range s.Resources {
		h := e.handles[r.ID]
		if h == nil || h.root == nil {
			return fmt.Errorf("managed resource lacks its selected held ownership")
		}
		root, err := managedMountStorageNodeV1(int(h.root.Fd()))
		if err != nil || root != r.Root {
			return fmt.Errorf("managed root descriptor changed: %v", err)
		}
		declarationsInput, err := r.ManagedMountObjects()
		if err != nil {
			return err
		}
		ordered, err := orderManagedMountObjectsV1(declarationsInput)
		if err != nil || !reflect.DeepEqual(ordered, declarationsInput) {
			return fmt.Errorf("managed resource declaration is not frozen canonical input: %v", err)
		}
		if s.Action == "prepare" {
			if h.staging == nil {
				return fmt.Errorf("managed preparation lacks its held staging descriptor")
			}
			staging, err := managedMountStorageNodeV1(int(h.staging.Fd()))
			if err != nil || staging != s.Staging[i] {
				return fmt.Errorf("managed staging descriptor changed: %v", err)
			}
		}
		declarations := map[string]blueprint.ManagedMountObject{}
		for _, declaration := range declarationsInput {
			declarations[declaration.Path] = declaration
			if _, ok := managedMountSetupHostIDV1(declaration.Identity.UID, active.UIDs); !ok {
				return fmt.Errorf("managed declaration has an unmapped UID")
			}
			if _, ok := managedMountSetupHostIDV1(declaration.Identity.GID, active.GIDs); !ok {
				return fmt.Errorf("managed declaration has an unmapped GID")
			}
		}
		if s.Action == "prepare" && p.Phase == "verified" && len(h.prepared) != len(r.Objects) {
			return fmt.Errorf("managed preparation lacks its retired-helper result")
		}
		retained := map[string]int{}
		for _, object := range r.Objects {
			fd := -1
			if f := h.recorded[object.Path]; f != nil {
				fd = int(f.Fd())
			}
			if prepared, ok := h.prepared[object.Path]; ok {
				fd = prepared.FD
			}
			retained[object.Path] = fd
			identity, err := managedMountStorageNodeV1(fd)
			if err != nil || identity != object.Identity {
				return fmt.Errorf("managed object descriptor provenance changed: %v", err)
			}
			if declaration, present := declarations[object.Path]; present {
				uid, _ := managedMountSetupHostIDV1(declaration.Identity.UID, active.UIDs)
				gid, _ := managedMountSetupHostIDV1(declaration.Identity.GID, active.GIDs)
				kind := uint32(unix.S_IFREG)
				if declaration.Directory {
					kind = unix.S_IFDIR
				}
				if identity.UID != strconv.FormatUint(uint64(uid), 10) || identity.GID != strconv.FormatUint(uint64(gid), 10) || storageModeV1(identity.Mode)&unix.S_IFMT != kind ||
					declaration.Identity.Permissions != nil && storageModeV1(identity.Mode)&0o7777 != *declaration.Identity.Permissions {
					return fmt.Errorf("managed object differs from its frozen declared metadata")
				}
			}
		}
		// Dot is already the held root capability. Every retained non-root path
		// needs the accepted subordinate-credential current-path proof.
		if len(r.Objects) != 0 && !(len(r.Objects) == 1 && r.Objects[0].Path == ".") {
			inspection := r
			if s.Action == "prepare" && p.Phase != "verified" {
				found := false
				for _, prior := range state.IdentityMapping.ManagedStorage {
					if prior.ID == r.ID && reflect.DeepEqual(prior.Root, r.Root) && reflect.DeepEqual(prior.Objects, r.Objects) {
						inspection, found = prior, true
					}
				}
				if !found {
					return fmt.Errorf("preparation lacks its complete prior retained inventory")
				}
			}
			objects, err := inspection.ManagedMountObjects()
			if err != nil {
				return err
			}
			if err := inspectManagedMountStoragePathsV1(ctx, active, h.root, objects, retained, authorize); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func inspectManagedMountStoragePathsV1(ctx context.Context, active identitymapping.Geometry, root *os.File, objects []blueprint.ManagedMountObject, retained map[string]int, authorize func(context.Context) error) error {
	recorded := map[string]*os.File{}
	defer func() {
		for _, file := range recorded {
			file.Close()
		}
	}()
	for name, fd := range retained {
		duplicate, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			return err
		}
		recorded[name] = os.NewFile(uintptr(duplicate), "managed-storage-inspection")
	}
	observed, err := inspectManagedMountSetupV1(ctx, active, root, objects, recorded, authorize)
	for _, object := range observed {
		unix.Close(object.FD)
	}
	return err
}

func (e *managedMountStorageEvidenceV1) Unused(ctx context.Context, state deploy.StateV1) error {
	if state.IdentityMapping != nil && state.IdentityMapping.PendingOperation != nil && state.IdentityMapping.PendingOperation.Storage != nil {
		if state.IdentityMapping.PendingOperation.Phase != "intent" {
			return fmt.Errorf("managed storage effects cannot be cancelled")
		}
		return e.ObserveManagedStorage(ctx, state)
	}
	return fmt.Errorf("resource observer cannot cancel a non-storage operation")
}

func (e *managedMountStorageEvidenceV1) Verify(ctx context.Context, state deploy.StateV1) error {
	if state.IdentityMapping != nil && state.IdentityMapping.PendingOperation != nil && state.IdentityMapping.PendingOperation.Storage != nil {
		return e.ObserveManagedStorage(ctx, state)
	}
	return fmt.Errorf("resource observer cannot verify a non-storage operation")
}

func (e *managedMountStorageEvidenceV1) Plan(ctx context.Context, state deploy.StateV1, active identitymapping.Geometry) ([]string, error) {
	if state.IdentityMapping != nil && state.IdentityMapping.PendingOperation != nil && state.IdentityMapping.PendingOperation.Storage != nil {
		if err := e.ObserveManagedStorage(ctx, state); err != nil {
			return nil, err
		}
		return append([]string(nil), state.IdentityMapping.PendingOperation.OwnedObjects...), nil
	}
	return nil, fmt.Errorf("resource observer cannot authorize a non-storage operation")
}

// prepareRecordedManagedMountStorageV1 borrows every descriptor. Only returned
// prepared descriptors transfer to the caller, after all helpers have retired
// and the existing journal commits. Failure closes them and preserves inhibition.
func prepareRecordedManagedMountStorageV1(ctx context.Context, ops deploy.IdentityMappingOperationsV1, expected deploy.StateV1, operation string, evidence *managedMountStorageEvidenceV1) (state deploy.StateV1, result map[string]map[string]preparedManagedMountObjectV1, err error) {
	state = expected
	if evidence == nil || ops.Evidence != evidence || expected.IdentityMapping == nil || expected.IdentityMapping.PendingOperation == nil ||
		expected.IdentityMapping.PendingOperation.Storage == nil || expected.IdentityMapping.PendingOperation.Storage.Action != "prepare" {
		return state, nil, fmt.Errorf("managed preparation requires its recorded resource owner")
	}
	s := expected.IdentityMapping.PendingOperation.Storage
	active, err := s.Active.Geometry()
	if err != nil {
		return state, nil, err
	}
	result = map[string]map[string]preparedManagedMountObjectV1{}
	defer func() {
		if err != nil {
			for id, objects := range result {
				for _, object := range objects {
					unix.Close(object.FD)
				}
				evidence.handles[id].prepared = nil
			}
			result = nil
		}
	}()
	resources := append([]deploy.ManagedStorageResourceV1(nil), s.Resources...)
	for i, r := range s.Resources {
		if _, err = ops.ObserveEffects(ctx, expected, active, operation); err != nil {
			return state, result, err
		}
		h := evidence.handles[r.ID]
		recorded := map[string]*os.File{".": h.root}
		for name, file := range h.recorded {
			recorded[name] = file
		}
		declarations, failure := r.ManagedMountObjects()
		if failure != nil {
			return state, result, failure
		}
		objects, failure := prepareManagedMountSetupV1(ctx, ops, expected, active, operation, h.root, h.staging, declarations, recorded)
		if failure != nil {
			return state, result, failure
		}
		result[r.ID], h.prepared = objects, objects
		resources[i].Objects = []deploy.ManagedStorageObjectV1{}
		for name, object := range objects {
			identity, failure := managedMountStorageNodeV1(object.FD)
			if failure != nil {
				return state, result, failure
			}
			resources[i].Objects = append(resources[i].Objects, deploy.ManagedStorageObjectV1{Path: name, Identity: identity})
		}
		sort.Slice(resources[i].Objects, func(a, b int) bool { return resources[i].Objects[a].Path < resources[i].Objects[b].Path })
	}
	state, err = ops.RecordManagedStorage(ctx, expected, operation, resources)
	if err == nil {
		state, err = ops.Recover(ctx, state, operation, false)
	}
	return state, result, err
}
