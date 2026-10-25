package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/omry/reploy/internal/identitymapping"
)

func TestMappingObserveEffectsBindsExactPendingAuthorityV1(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "staged", true: "installed"}[installed], func(t *testing.T) {
			path, ops, original, evidence := operationFixtureV1(t, installed, !installed)
			active := operationGeometryV1(100000)
			intent, err := ops.Configure(t.Context(), original, active, "create-one")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ops.ObserveEffects(t.Context(), intent, active, "create-one"); err == nil {
				t.Fatal("intent produced dispatch authority")
			}
			pending, err := ops.BeginEffects(t.Context(), intent, "create-one")
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ops.ObserveEffects(t.Context(), pending, active, "create-one")
			if err != nil || !reflect.DeepEqual(got, pending) {
				t.Fatalf("exact pending observation: %v", err)
			}
			if _, err := ops.ObserveEffects(t.Context(), pending, active, "another-operation"); err == nil {
				t.Fatal("foreign pending operation accepted")
			}
			if _, err := ops.ObserveEffects(t.Context(), intent, active, "create-one"); err == nil {
				t.Fatal("old intent revision accepted")
			}
			if _, err := ops.ObserveEffects(t.Context(), pending, operationGeometryV1(110000), "create-one"); err == nil {
				t.Fatal("different active candidate accepted")
			}
			stale := cloneMappingStateV1(pending)
			stale.IdentityMapping.OwnerDir += "-foreign"
			if _, err := ops.ObserveEffects(t.Context(), stale, active, "create-one"); err == nil {
				t.Fatal("foreign owner accepted")
			}
			stale = cloneMappingStateV1(pending)
			stale.Current.Reference = "another-generation"
			if _, err := ops.ObserveEffects(t.Context(), stale, active, "create-one"); err == nil {
				t.Fatal("different generation accepted")
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := ops.ObserveEffects(cancelled, pending, active, "create-one"); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled admission: %v", err)
			}
			if _, err := ops.ObserveEffects(nil, pending, active, "create-one"); err == nil {
				t.Fatal("missing context accepted")
			}
			originalCheck := ops.hostCheck
			ops.hostCheck = func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error) {
				return identitymapping.HostIdentity{}, errors.New("host inventory unavailable")
			}
			if _, err := ops.ObserveEffects(t.Context(), pending, active, "create-one"); err == nil {
				t.Fatal("stale host evidence reused")
			}
			ops.hostCheck = originalCheck
			got.IdentityMapping.PendingOperation.Phase = "verified"
			if _, err := ops.ObserveEffects(t.Context(), pending, active, "create-one"); err != nil {
				t.Fatalf("returned state aliases retained authority: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) || evidence.verified != 0 {
				t.Fatal("observation wrote state or invoked recovery")
			}
			if err := ops.Operation.Unlock(); err != nil {
				t.Fatal(err)
			}
			if _, err := ops.ObserveEffects(t.Context(), pending, active, "create-one"); err == nil {
				t.Fatal("released lock accepted")
			}
		})
	}
	if _, err := (IdentityMappingOperationsV1{}).ObserveEffects(t.Context(), StateV1{}, identitymapping.Geometry{}, "none"); err == nil {
		t.Fatal("missing lock accepted")
	}
}

func TestMappingObserveEffectsDoesNotActivateRetainedReservationsV1(t *testing.T) {
	for _, reactivate := range []bool{false, true} {
		t.Run(fmt.Sprint(reactivate), func(t *testing.T) {
			path, ops, state, _ := operationFixtureV1(t, false, false)
			protected := operationGeometryV1(100000)
			stored := mappingGeometryStateV1(protected)
			m := state.IdentityMapping
			m.UIDAssignments, m.GIDAssignments = stored.UIDs, stored.GIDs
			m.AdmittedGeneration = state.Current
			engine, supervisor := "fixture-engine", "fixture-supervisor"
			m.Engine, m.Supervisor = &engine, &supervisor
			m.DormantUIDs = []IdentityMappingIntervalV1{{ContainerStart: "5", HostStart: "100005", Count: "1"}}
			m.DormantGIDs = []IdentityMappingIntervalV1{{ContainerStart: "5", HostStart: "200005", Count: "1"}}
			writeMappingFixtureV1(t, path, state)
			active := identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{{HostStart: 100000, Count: 1}}, GIDs: []identitymapping.Interval{{HostStart: 200000, Count: 1}}}
			if reactivate {
				active.UIDs = append(active.UIDs, identitymapping.Interval{ContainerStart: 5, HostStart: 100005, Count: 1})
				active.GIDs = append(active.GIDs, identitymapping.Interval{ContainerStart: 5, HostStart: 200005, Count: 1})
			}
			intent, err := ops.Configure(t.Context(), state, active, "active-only")
			if err != nil {
				t.Fatal(err)
			}
			pending, err := ops.BeginEffects(t.Context(), intent, "active-only")
			if err != nil {
				t.Fatal(err)
			}
			status, err := ops.Status()
			if err != nil || status.Reservations == nil || reflect.DeepEqual(active, *status.Reservations) {
				t.Fatal("fixture lost protected reservations", err)
			}
			if _, err := ops.ObserveEffects(t.Context(), pending, active, "active-only"); err != nil {
				t.Fatal("exact active input rejected", err)
			}
			if _, err := ops.ObserveEffects(t.Context(), pending, *status.Reservations, "active-only"); err == nil {
				t.Fatal("retained union substituted for selected active mappings")
			}
			if !reflect.DeepEqual(pending.IdentityMapping.DormantUIDs, m.DormantUIDs) || !reflect.DeepEqual(pending.IdentityMapping.DormantGIDs, m.DormantGIDs) {
				t.Fatal("create observation changed dormant reservations")
			}
		})
	}
}
