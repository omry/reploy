package deploy

import (
	"bytes"
	"reflect"
	"testing"
)

func TestManagedStorageStateCanonicalBindingsV1(t *testing.T) {
	_, ops, original, _ := managedStorageFixtureV1(t)
	intent, err := ops.PrepareManagedStorage(t.Context(), original, operationGeometryV1(100000), "codec", managedStorageRequestFixtureV1())
	if err != nil {
		t.Fatal(err)
	}
	content, err := EncodeStateV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeStateV1(content)
	if err != nil || !reflect.DeepEqual(decoded, intent) {
		t.Fatal("storage intent did not survive its canonical codec", err)
	}
	if !bytes.Contains(content, []byte(`"uid":"100000"`)) {
		t.Fatal("numeric metadata is not canonical decimal state")
	}
	mutations := map[string]func(*StateV1){
		"legacy schema": func(s *StateV1) { s.IdentityMapping.Schema = IdentityMappingStateSchemaV1 },
		"revision":      func(s *StateV1) { s.IdentityMapping.PendingOperation.ExpectedRevision = "1" },
		"generation": func(s *StateV1) {
			s.IdentityMapping.PendingOperation.Storage.Resources[0].Generation.Reference = "reploy/env/demo:foreign"
		},
		"kind":           func(s *StateV1) { s.IdentityMapping.PendingOperation.Kind = "configure" },
		"inhibition":     func(s *StateV1) { s.IdentityMapping.PendingOperation.Inhibited = false },
		"owned handles":  func(s *StateV1) { s.IdentityMapping.PendingOperation.OwnedObjects = []string{"other"} },
		"retained owner": func(s *StateV1) { s.IdentityMapping.RetainedStorage = nil },
		"root inode":     func(s *StateV1) { s.IdentityMapping.PendingOperation.Storage.Resources[0].Root.Inode = "02" },
		"root type":      func(s *StateV1) { s.IdentityMapping.PendingOperation.Storage.Resources[0].Root.Mode = "33152" },
		"unmapped owner": func(s *StateV1) { s.IdentityMapping.PendingOperation.Storage.Resources[0].Root.UID = "1000" },
		"unknown action": func(s *StateV1) { s.IdentityMapping.PendingOperation.Storage.Action = "repair" },
		"staging alias": func(s *StateV1) {
			s.IdentityMapping.PendingOperation.Storage.Staging[0] = s.IdentityMapping.PendingOperation.Storage.Resources[0].Root
		},
		"staging mount":       func(s *StateV1) { s.IdentityMapping.PendingOperation.Storage.Staging[0].Mount = "4" },
		"staging permissions": func(s *StateV1) { s.IdentityMapping.PendingOperation.Storage.Staging[0].Mode = "16877" },
		"missing stage":       func(s *StateV1) { s.IdentityMapping.PendingOperation.Storage.Staging = nil },
		"prepare attachment": func(s *StateV1) {
			s.IdentityMapping.PendingOperation.Storage.Attachment = &ManagedStorageAttachmentV1{Engine: "engine", Process: "instance"}
		},
		"declaration traversal": func(s *StateV1) {
			s.IdentityMapping.PendingOperation.Storage.Resources[0].Declarations[1].Path = "../foreign"
		},
		"declaration alias": func(s *StateV1) { s.IdentityMapping.PendingOperation.Storage.Resources[0].Declarations[1].Path = "." },
		"declaration UID overflow": func(s *StateV1) {
			s.IdentityMapping.PendingOperation.Storage.Resources[0].Declarations[1].UID = "4294967296"
		},
		"declaration permission overflow": func(s *StateV1) {
			value := "4096"
			s.IdentityMapping.PendingOperation.Storage.Resources[0].Declarations[1].Permissions = &value
		},
		"partial verified result": func(s *StateV1) { s.IdentityMapping.PendingOperation.Phase = "verified" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			state := cloneMappingStateV1(intent)
			mutate(&state)
			if _, err := EncodeStateV1(state); err == nil {
				t.Fatal("invalid storage authority encoded")
			}
		})
	}
	verified := cloneMappingStateV1(intent)
	verified.IdentityMapping.PendingOperation.Phase = "verified"
	verified.IdentityMapping.PendingOperation.Storage.Resources = managedStorageResultFixtureV1(intent)
	if _, err := EncodeStateV1(verified); err != nil {
		t.Fatal("complete verified result rejected", err)
	}
	for _, defect := range []string{"inode-alias", "mount-crossing", "undeclared", "root-change", "unsorted"} {
		t.Run(defect, func(t *testing.T) {
			state := cloneMappingStateV1(verified)
			r := &state.IdentityMapping.PendingOperation.Storage.Resources[0]
			switch defect {
			case "inode-alias":
				r.Objects[1].Identity.Inode = r.Root.Inode
			case "mount-crossing":
				r.Objects[1].Identity.Mount = "4"
			case "undeclared":
				r.Objects[1].Path = "foreign"
			case "root-change":
				r.Objects[0].Identity.Mode = "16877"
			case "unsorted":
				r.Objects[0], r.Objects[1] = r.Objects[1], r.Objects[0]
			}
			if _, err := EncodeStateV1(state); err == nil {
				t.Fatal("invalid verified provenance encoded")
			}
		})
	}
}

func TestManagedStorageKindRequiresTypedPayloadEvenInLegacyStateV1(t *testing.T) {
	_, ops, original, _ := operationFixtureV1(t, false, false)
	intent, err := ops.Configure(t.Context(), original, operationGeometryV1(100000), "malformed-storage")
	if err != nil {
		t.Fatal(err)
	}
	intent.IdentityMapping.PendingOperation.Kind = "managed-storage"
	if _, err := EncodeStateV1(intent); err == nil {
		t.Fatal("reserved managed-storage kind accepted without its typed payload")
	}
}
