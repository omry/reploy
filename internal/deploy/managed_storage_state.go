package deploy

import (
	"fmt"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/identitymapping"
)

// ManagedStorageNodeV1 is an observed Linux object, never a path or UID-based
// ownership claim. The trusted owner also retains its descriptor provenance.
type ManagedStorageNodeV1 struct {
	Device string `json:"device"`
	Inode  string `json:"inode"`
	Mount  string `json:"mount"`
	UID    string `json:"uid"`
	GID    string `json:"gid"`
	Mode   string `json:"mode"`
}

// ManagedStorageDeclarationV1 uses the existing canonical state decimal format.
type ManagedStorageDeclarationV1 struct {
	Path        string  `json:"path"`
	Directory   bool    `json:"directory"`
	UID         string  `json:"uid"`
	GID         string  `json:"gid"`
	Permissions *string `json:"permissions"`
}

func ManagedStorageDeclarationsV1(objects []blueprint.ManagedMountObject) []ManagedStorageDeclarationV1 {
	result := make([]ManagedStorageDeclarationV1, len(objects))
	for i, object := range objects {
		result[i] = ManagedStorageDeclarationV1{Path: object.Path, Directory: object.Directory, UID: strconv.FormatUint(uint64(object.Identity.UID), 10), GID: strconv.FormatUint(uint64(object.Identity.GID), 10)}
		if object.Identity.Permissions != nil {
			value := strconv.FormatUint(uint64(*object.Identity.Permissions), 10)
			result[i].Permissions = &value
		}
	}
	return result
}

// ManagedMountObjects returns validated numeric creation intent for F1.3.
func (r ManagedStorageResourceV1) ManagedMountObjects() ([]blueprint.ManagedMountObject, error) {
	result := make([]blueprint.ManagedMountObject, len(r.Declarations))
	for i, d := range r.Declarations {
		if !validMappingDecimalV1(d.UID, 32, false) || !validMappingDecimalV1(d.GID, 32, false) || d.Permissions != nil && (!validMappingDecimalV1(*d.Permissions, 32, false) || mappingDecimalV1(*d.Permissions) > 0o7777) {
			return nil, fmt.Errorf("managed declaration requires canonical numeric metadata")
		}
		result[i] = blueprint.ManagedMountObject{Path: d.Path, Directory: d.Directory, Identity: blueprint.MountMetadataIdentity{UID: uint32(mappingDecimalV1(d.UID)), GID: uint32(mappingDecimalV1(d.GID))}}
		if d.Permissions != nil {
			value := uint32(mappingDecimalV1(*d.Permissions))
			result[i].Identity.Permissions = &value
		}
	}
	return result, nil
}

type ManagedStorageObjectV1 struct {
	Path     string               `json:"path"`
	Identity ManagedStorageNodeV1 `json:"identity"`
}

type ManagedStorageAttachmentV1 struct {
	Engine  string `json:"engine"`
	Process string `json:"process"`
}

// ManagedStorageResourceV1 extends the existing retained-storage ownership.
// Losing observations never removes this record or releases its reservation.
type ManagedStorageResourceV1 struct {
	ID           string                        `json:"id"`
	Root         ManagedStorageNodeV1          `json:"root"`
	Declarations []ManagedStorageDeclarationV1 `json:"declarations"`
	Objects      []ManagedStorageObjectV1      `json:"objects"`
	Generation   *EnvironmentGenerationState   `json:"generation"`
	Active       IdentityMappingGeometryV1     `json:"active"`
	Attachment   *ManagedStorageAttachmentV1   `json:"attachment"`
}

type ManagedStorageOperationV1 struct {
	Action     string                      `json:"action"`
	Active     IdentityMappingGeometryV1   `json:"active"`
	Resources  []ManagedStorageResourceV1  `json:"resources"`
	Staging    []ManagedStorageNodeV1      `json:"staging"`
	Attachment *ManagedStorageAttachmentV1 `json:"attachment"`
}

func validManagedStorageNodeV1(n ManagedStorageNodeV1) bool {
	kind := mappingDecimalV1(n.Mode) & 0o170000
	return validMappingDecimalV1(n.Device, 64, false) && validMappingDecimalV1(n.Inode, 64, true) &&
		validMappingDecimalV1(n.Mount, 64, true) && validMappingDecimalV1(n.UID, 32, false) && validMappingDecimalV1(n.GID, 32, false) && validMappingDecimalV1(n.Mode, 32, false) && mappingDecimalV1(n.Mode)&^uint64(0o177777) == 0 && (kind == 0o040000 || kind == 0o100000)
}

func managedStorageNodeKeyV1(n ManagedStorageNodeV1) string {
	return n.Device + ":" + n.Inode + ":" + n.Mount
}

func validateManagedStorageResourcesV1(m *IdentityMappingStateV1, resources []ManagedStorageResourceV1, prepared bool) error {
	if len(resources) > 64 {
		return fmt.Errorf("managed storage resource inventory exceeds its bound")
	}
	seenRoots, total := map[string]bool{}, 0
	previous := ""
	for _, r := range resources {
		key := managedStorageNodeKeyV1(r.Root)
		if !safeRecoveryIdentity(r.ID) || r.ID <= previous || !slices.Contains(m.RetainedStorage, r.ID) ||
			!validManagedStorageNodeV1(r.Root) || mappingDecimalV1(r.Root.Mode)&0o170000 != 0o040000 || seenRoots[key] || r.Generation == nil {
			return fmt.Errorf("managed storage requires unique ordered retained owners and observed roots")
		}
		previous, seenRoots[key] = r.ID, true
		if err := ValidateEnvironmentGenerationState(*r.Generation); err != nil {
			return err
		}
		if err := requireCanonicalMappingGeometryV1(r.Active); err != nil {
			return err
		}
		if r.Active.Profile != m.Profile || !managedStorageSubsetV1(r.Active, m.geometry()) {
			return fmt.Errorf("managed storage geometry lacks retained ownership")
		}
		if !managedStorageNodeMappedV1(r.Root, r.Active) {
			return fmt.Errorf("managed root ownership is outside its selected geometry")
		}
		if r.Attachment != nil && (!safeRecoveryIdentity(r.Attachment.Engine) || !safeRecoveryIdentity(r.Attachment.Process)) {
			return fmt.Errorf("managed storage attachment requires exact owned instance identities")
		}
		if _, err := r.ManagedMountObjects(); err != nil {
			return err
		}
		declarations := map[string]bool{}
		if len(r.Declarations) == 0 || len(r.Declarations) > 4096 || r.Declarations[0].Path != "." || !r.Declarations[0].Directory {
			return fmt.Errorf("managed storage requires its frozen declared object manifest")
		}
		for _, declaration := range r.Declarations {
			n := declaration.Path
			if n == "" || path.IsAbs(n) || path.Clean(n) != n || n == ".." || strings.HasPrefix(n, "../") || strings.ContainsAny(n, "\\\x00\r\n\t") ||
				declarations[n] {
				return fmt.Errorf("managed storage declaration is invalid or repeated")
			}
			declarations[n] = true
		}
		if prepared && len(r.Objects) != len(r.Declarations) {
			return fmt.Errorf("prepared storage requires its complete recorded object inventory")
		}
		seen, last := map[string]bool{}, ""
		for _, object := range r.Objects {
			n := object.Path
			key := managedStorageNodeKeyV1(object.Identity)
			if n == "" || n <= last || prepared && !declarations[n] || path.IsAbs(n) || path.Clean(n) != n || n == ".." || strings.HasPrefix(n, "../") ||
				strings.ContainsAny(n, "\\\x00\r\n\t") || !validManagedStorageNodeV1(object.Identity) || !managedStorageNodeMappedV1(object.Identity, r.Active) || seen[key] ||
				object.Identity.Mount != r.Root.Mount || object.Identity.Device != r.Root.Device ||
				n == "." && !reflect.DeepEqual(object.Identity, r.Root) {
				return fmt.Errorf("managed storage object provenance is invalid or aliased")
			}
			last, seen[key] = n, true
		}
		total += len(r.Objects)
		if total > 4096 {
			return fmt.Errorf("managed storage object inventory exceeds its bound")
		}
	}
	return nil
}

func managedStorageSubsetV1(active, retained IdentityMappingGeometryV1) bool {
	union, err := identitymapping.RetainGeometry(active.numeric(), geometryPointerV1(retained.numeric()))
	return err == nil && sameExecutionGeometryV1(union, retained.numeric())
}

func managedStorageNodeMappedV1(n ManagedStorageNodeV1, geometry IdentityMappingGeometryV1) bool {
	g := geometry.numeric()
	for i, rows := range [][]identitymapping.Interval{g.UIDs, g.GIDs} {
		id := []uint64{mappingDecimalV1(n.UID), mappingDecimalV1(n.GID)}[i]
		if !slices.ContainsFunc(rows, func(r identitymapping.Interval) bool {
			return id >= uint64(r.HostStart) && id < uint64(r.HostStart)+uint64(r.Count)
		}) {
			return false
		}
	}
	return true
}

func validateManagedStorageStateV1(state StateV1) error {
	m := state.IdentityMapping
	p := m.PendingOperation
	if p != nil && p.Kind == "managed-storage" && p.Storage == nil {
		return fmt.Errorf("managed storage operation requires its typed selection")
	}
	if len(m.ManagedStorage) == 0 && (p == nil || p.Storage == nil) {
		return nil
	}
	if m.Schema != IdentityMappingStateSchemaV2 {
		return fmt.Errorf("managed storage requires native mapping ownership")
	}
	if err := validateManagedStorageResourcesV1(m, m.ManagedStorage, true); err != nil {
		return err
	}
	if p == nil || p.Storage == nil {
		return nil
	}
	s := p.Storage
	if p.Kind != "managed-storage" || p.Native != nil || p.ExpectedRevision != m.Revision ||
		(s.Action != "prepare" && s.Action != "attach") || len(s.Resources) == 0 ||
		!reflect.DeepEqual(p.ExpectedGeneration, state.Current) || !managedStorageSubsetV1(s.Active, p.Candidate) {
		return fmt.Errorf("managed storage requires its exact inhibited operation and selected geometry")
	}
	if err := requireCanonicalMappingGeometryV1(s.Active); err != nil {
		return err
	}
	if err := validateManagedStorageResourcesV1(m, s.Resources, p.Phase == "verified" || s.Action == "attach"); err != nil {
		return err
	}
	ids := make([]string, len(s.Resources))
	for i, r := range s.Resources {
		ids[i] = r.ID
		if !reflect.DeepEqual(r.Generation, state.Current) || !reflect.DeepEqual(r.Active, s.Active) ||
			s.Action == "prepare" && r.Attachment != nil || s.Action == "attach" && p.Phase != "verified" && r.Attachment != nil ||
			s.Action == "attach" && p.Phase == "verified" && !reflect.DeepEqual(r.Attachment, s.Attachment) {
			return fmt.Errorf("managed storage selection differs from its captured generation or geometry")
		}
	}
	if !reflect.DeepEqual(ids, p.OwnedObjects) || s.Action == "prepare" && (len(s.Staging) != len(ids) || s.Attachment != nil) ||
		s.Action == "attach" && (len(s.Staging) != 0 || s.Attachment == nil || !safeRecoveryIdentity(s.Attachment.Engine) || !safeRecoveryIdentity(s.Attachment.Process)) {
		return fmt.Errorf("managed storage selection lacks its exact handles or action binding")
	}
	seen := map[string]bool{}
	for _, r := range s.Resources {
		seen[managedStorageNodeKeyV1(r.Root)] = true
	}
	for i, n := range s.Staging {
		key := managedStorageNodeKeyV1(n)
		if !validManagedStorageNodeV1(n) || mappingDecimalV1(n.Mode)&0o177777 != 0o040700 || n.Mount != s.Resources[i].Root.Mount ||
			!managedStorageNodeMappedV1(n, s.Active) || mappingDecimalV1(n.UID) != uint64(s.Active.numeric().UIDs[0].HostStart) || seen[key] {
			return fmt.Errorf("managed staging requires distinct private held directories on the selected mount")
		}
		seen[key] = true
	}
	return nil
}
