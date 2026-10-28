package deploy

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/identitymapping"
)

func TestNativeMappingV1ClosedSchemaAndProvenance(t *testing.T) {
	_, ops, empty, evidence, candidate := nativeMappingFixtureV1(t)
	intent, err := ops.PrepareNativeExecution(t.Context(), empty, candidate, "schema", nativeBindingV1("schema"))
	if err != nil {
		t.Fatal(err)
	}
	effects, err := ops.BeginEffects(t.Context(), intent, "schema")
	if err != nil {
		t.Fatal(err)
	}
	evidence.effects = true
	admitted, err := ops.AdmitNativeExecution(t.Context(), effects, "schema")
	if err != nil {
		t.Fatal(err)
	}
	for name, entry := range map[string]struct {
		source StateV1
		mutate func(*IdentityMappingStateV1)
	}{
		"model":               {empty, func(m *IdentityMappingStateV1) { m.ProcessModel = "supervisor-v1" }},
		"supervisor":          {empty, func(m *IdentityMappingStateV1) { v := "supervisor"; m.Supervisor = &v }},
		"missing accepted":    {empty, func(m *IdentityMappingStateV1) { m.AcceptedUIDs = nil }},
		"null accepted":       {empty, func(m *IdentityMappingStateV1) { *m.AcceptedUIDs = nil }},
		"one accepted domain": {admitted, func(m *IdentityMappingStateV1) { *m.AcceptedGIDs = []IdentityMappingIntervalV1{} }},
		"empty admitted":      {empty, func(m *IdentityMappingStateV1) { m.AdmittedGeneration = admitted.Current }},
		"empty engine":        {empty, func(m *IdentityMappingStateV1) { v := "engine"; m.Engine = &v }},
		"empty process":       {empty, func(m *IdentityMappingStateV1) { v := "process"; m.NativeProcess = &v }},
		"missing process":     {admitted, func(m *IdentityMappingStateV1) { m.NativeProcess = nil }},
		"unsafe process":      {admitted, func(m *IdentityMappingStateV1) { v := "-unsafe"; m.NativeProcess = &v }},
		"missing generation":  {admitted, func(m *IdentityMappingStateV1) { m.AdmittedGeneration = nil }},
		"invalid accepted":    {admitted, func(m *IdentityMappingStateV1) { (*m.AcceptedUIDs)[0].Count = "0" }},
		"unreserved accepted": {admitted, func(m *IdentityMappingStateV1) { (*m.AcceptedUIDs)[0].HostStart = "999999" }},
		"unaccepted dormant": {admitted, func(m *IdentityMappingStateV1) {
			m.PendingOperation = nil
			m.DormantUIDs = []IdentityMappingIntervalV1{{ContainerStart: "600", HostStart: "100600", Count: "1"}}
			m.UIDAssignments = append(m.UIDAssignments, m.DormantUIDs...)
		}},
		"all dormant": {admitted, func(m *IdentityMappingStateV1) {
			m.PendingOperation = nil
			m.DormantUIDs = *m.AcceptedUIDs
			m.DormantGIDs = *m.AcceptedGIDs
		}},
		"foreign kind":               {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.Kind = "future" }},
		"configure projection":       {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.Kind = "configure" }},
		"missing active":             {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.Native = nil }},
		"invalid active":             {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.Native.Active.UIDs[0].Count = "0" }},
		"active outside candidate":   {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.Native.Active.UIDs[0].HostStart = "999999" }},
		"missing pending generation": {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.ExpectedGeneration = nil }},
		"stale pending revision":     {intent, func(m *IdentityMappingStateV1) { m.Revision = "99" }},
		"unknown native phase":       {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.Phase = "verified" }},
		"unsafe instance":            {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.Native.Process = "-invalid" }},
		"unsafe engine":              {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.Native.Engine = "-invalid" }},
		"uncommitted release":        {intent, func(m *IdentityMappingStateV1) { m.PendingOperation.Phase = "release-attempted" }},
		"wrong release process":      {admitted, func(m *IdentityMappingStateV1) { m.PendingOperation.Native.Process = "different" }},
		"wrong release engine":       {admitted, func(m *IdentityMappingStateV1) { m.PendingOperation.Native.Engine = "different" }},
		"wrong release generation": {admitted, func(m *IdentityMappingStateV1) {
			g := *m.PendingOperation.ExpectedGeneration
			g.Reference += "-different"
			m.PendingOperation.ExpectedGeneration = &g
		}},
		"wrong admitted active": {admitted, func(m *IdentityMappingStateV1) {
			m.PendingOperation.Native.Active.UIDs = m.PendingOperation.Native.Active.UIDs[:1]
		}},
		"legacy native fields": {empty, func(m *IdentityMappingStateV1) { m.Schema = IdentityMappingStateSchemaV1 }},
		"legacy native journal": {intent, func(m *IdentityMappingStateV1) {
			m.Schema = IdentityMappingStateSchemaV1
			m.ProcessModel = ""
			m.AcceptedUIDs = nil
			m.AcceptedGIDs = nil
		}},
	} {
		t.Run(name, func(t *testing.T) {
			s := cloneMappingStateV1(entry.source)
			entry.mutate(s.IdentityMapping)
			if _, err := EncodeStateV1(s); err == nil {
				t.Fatal("invalid native authority encoded")
			}
		})
	}
	for _, state := range []StateV1{empty, intent, effects, admitted} {
		content, err := EncodeStateV1(state)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeStateV1(content)
		if err != nil || !reflect.DeepEqual(got, state) {
			t.Fatalf("native canonical roundtrip: %v", err)
		}
		if state.IdentityMapping.acceptedGeometry() == nil && !bytes.Contains(content, []byte(`"accepted_uids":[]`)) {
			t.Fatal("empty P was not explicit")
		}
	}
	content, err := EncodeStateV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{`"native":null`, `"native":{"unexpected":true}`} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(content, &fields); err != nil {
			t.Fatal(err)
		}
		// Exercise the real closed decoder with an unknown nested field.
		bad := bytes.Replace(content, []byte(`"native":{`), []byte(`"native":{"unexpected":true,`), 1)
		if replacement == `"native":null` {
			bad = bytes.Replace(content, []byte(`"accepted_uids":[]`), []byte(`"accepted_uids":null`), 1)
		}
		if _, err := DecodeStateV1(bad); err == nil {
			t.Fatal("native decoder accepted missing/unknown authority")
		}
	}
}

func TestNativeMappingV1ExactProjectionAndNeverAdmittedSurplus(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "reserved-only", true: "prior-admission"}[accepted], func(t *testing.T) {
			path, ops, state, evidence, candidate := nativeMappingFixtureV1(t)
			extra := executionGeometryV1([]uint32{700}, []uint32{700})
			all, err := identitymapping.RetainGeometry(candidate, &extra)
			if err != nil {
				t.Fatal(err)
			}
			wire := mappingGeometryStateV1(all)
			m := state.IdentityMapping
			m.UIDAssignments, m.GIDAssignments = wire.UIDs, wire.GIDs
			if accepted {
				u, g := append([]IdentityMappingIntervalV1(nil), wire.UIDs...), append([]IdentityMappingIntervalV1(nil), wire.GIDs...)
				m.AcceptedUIDs, m.AcceptedGIDs = &u, &g
				engine, process := "old-engine", "old-process"
				m.Engine, m.NativeProcess, m.AdmittedGeneration = &engine, &process, state.Current
				d := mappingGeometryStateV1(extra)
				m.DormantUIDs, m.DormantGIDs = d.UIDs, d.GIDs
			}
			writeMappingFixtureV1(t, path, state)
			intent, err := ops.PrepareNativeExecution(t.Context(), state, all, "project", nativeBindingV1("project"))
			if !accepted {
				if err == nil {
					t.Fatal("never-admitted surplus authorized by R")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !sameExecutionGeometryV1(intent.IdentityMapping.PendingOperation.Native.Active.numeric(), candidate) {
				t.Fatal("native active map retained dormant rows")
			}
			effects, err := ops.BeginEffects(t.Context(), intent, "project")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ops.ObserveEffects(t.Context(), effects, all, "project"); err == nil {
				t.Fatal("native observer accepted C instead of A")
			}
			if _, err := ops.ObserveEffects(t.Context(), effects, candidate, "project"); err != nil {
				t.Fatal(err)
			}
			evidence.effects = true
			admitted, err := ops.AdmitNativeExecution(t.Context(), effects, "project")
			if err != nil {
				t.Fatal(err)
			}
			if !sameExecutionGeometryV1(*admitted.IdentityMapping.acceptedGeometry(), all) || !reflect.DeepEqual(admitted.IdentityMapping.DormantUIDs, m.DormantUIDs) {
				t.Fatal("projected admission lost prior P or dormancy")
			}
		})
	}
}

func TestNativeMappingV1LegacyBytesAndUncheckedWritesRemainClosed(t *testing.T) {
	path, ops, state, _ := operationFixtureV1(t, false, false)
	legacy, err := EncodeStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"accepted_uids", "accepted_gids", "process_model", "native_process", "native"} {
		if bytes.Contains(legacy, []byte(`"`+key+`":`)) {
			t.Fatal("legacy canonical bytes gained native fields")
		}
	}
	state.IdentityMapping = nil
	writeMappingFixtureV1(t, path, state)
	for _, mutate := range []func(*IdentityMappingStateV1){
		func(m *IdentityMappingStateV1) { m.Schema = IdentityMappingStateSchemaV2 },
		func(m *IdentityMappingStateV1) { m.ProcessModel = IdentityMappingNativeProcessModelV1 },
		func(m *IdentityMappingStateV1) { v := []IdentityMappingIntervalV1{}; m.AcceptedUIDs = &v },
		func(m *IdentityMappingStateV1) { v := []IdentityMappingIntervalV1{}; m.AcceptedGIDs = &v },
		func(m *IdentityMappingStateV1) { v := "process"; m.NativeProcess = &v },
	} {
		next := IdentityMappingStateV1{Schema: IdentityMappingStateSchemaV1, Profile: "exact", OperatorUID: "1000", OperatorGID: "1000"}
		mutate(&next)
		if _, err := ops.Operation.CommitIdentityMappingV1(state.Current, "", 0, next); err == nil {
			t.Fatal("generic first-configuration writer admitted native fields")
		}
	}
	stored := readOperationStateV1(t, ops)
	if stored.IdentityMapping != nil {
		t.Fatal("rejected generic writes changed state")
	}
	if _, err := ops.UpgradeNativeMapping(t.Context(), stored); err == nil {
		t.Fatal("unconfigured owner acquired native state")
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeMappingV1BoundedAdmissionKeepsWholeRange(t *testing.T) {
	path, ops, state, evidence, _ := nativeMappingFixtureV1(t)
	state.IdentityMapping.Profile = "bounded"
	writeMappingFixtureV1(t, path, state)
	geometry := identitymapping.Geometry{Profile: "bounded", UIDs: []identitymapping.Interval{{ContainerStart: 0, HostStart: 100000, Count: 512}}, GIDs: []identitymapping.Interval{{ContainerStart: 0, HostStart: 200000, Count: 512}}}
	effects := nativeEffectsV1(t, ops, state, geometry, evidence, "bounded")
	admitted, err := ops.AdmitNativeExecution(t.Context(), effects, "bounded")
	if err != nil {
		t.Fatal(err)
	}
	if !sameExecutionGeometryV1(*admitted.IdentityMapping.acceptedGeometry(), geometry) || len(admitted.IdentityMapping.DormantUIDs)+len(admitted.IdentityMapping.DormantGIDs) != 0 {
		t.Fatal("bounded admission projected or fabricated dormancy")
	}
	for _, change := range []string{"partial-accepted", "dormant"} {
		t.Run(change, func(t *testing.T) {
			s := cloneMappingStateV1(admitted)
			s.IdentityMapping.PendingOperation = nil
			if change == "partial-accepted" {
				(*s.IdentityMapping.AcceptedUIDs)[0].Count = "511"
			} else {
				s.IdentityMapping.DormantUIDs = []IdentityMappingIntervalV1{{ContainerStart: "511", HostStart: "100511", Count: "1"}}
			}
			if _, err := EncodeStateV1(s); err == nil {
				t.Fatal("bounded accepted authority changed")
			}
		})
	}
}
