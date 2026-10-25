package dockerdeploy

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/identitymapping"
)

func mappedGeometryFixtureV1(profile string) identitymapping.Geometry {
	if profile == "bounded" {
		return identitymapping.Geometry{Profile: profile, UIDs: []identitymapping.Interval{{HostStart: 100000, Count: 4096}}, GIDs: []identitymapping.Interval{{HostStart: 100000, Count: 4096}}}
	}
	return identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{{HostStart: 100000, Count: 1}, {ContainerStart: 100, HostStart: 100001, Count: 1}, {ContainerStart: 1001, HostStart: 100002, Count: 1}}, GIDs: []identitymapping.Interval{{HostStart: 100000, Count: 1}, {ContainerStart: 5, HostStart: 100001, Count: 1}, {ContainerStart: 100, HostStart: 100002, Count: 1}, {ContainerStart: 2001, HostStart: 100003, Count: 1}, {ContainerStart: 3001, HostStart: 100004, Count: 2}}}
}

func TestCompilePodmanMappingExactAndBoundedV1(t *testing.T) {
	info := podmanSameIDInfo(1000, 1000)
	for _, test := range []struct{ profile, want string }{
		{"exact", "auto:size=1,uidmapping=0:1:1,uidmapping=100:2:1,uidmapping=1001:3:1,gidmapping=0:1:1,gidmapping=5:2:1,gidmapping=100:3:1,gidmapping=2001:4:1,gidmapping=3001:5:2"},
		{"bounded", "auto:size=1,uidmapping=0:1:4096,gidmapping=0:1:4096"},
	} {
		t.Run(test.profile, func(t *testing.T) {
			active := mappedGeometryFixtureV1(test.profile)
			before, _ := json.Marshal(active)
			got, err := compilePodmanMappingV1(info, active, 1000, 1000)
			if err != nil || got != test.want {
				t.Fatalf("%q: %v", got, err)
			}
			after, _ := json.Marshal(active)
			if string(before) != string(after) {
				t.Fatal("compiler mutated caller geometry")
			}
		})
	}
}

func TestCompilePodmanMappingSplitsAtParentBoundariesV1(t *testing.T) {
	data := strings.ReplaceAll(string(podmanSameIDInfo(1000, 1000)), `{"container_id":1,"host_id":100000,"size":65536}`, `{"container_id":1,"host_id":100004,"size":20},{"container_id":40,"host_id":100000,"size":4}`)
	active := identitymapping.Geometry{Profile: "bounded", UIDs: []identitymapping.Interval{{HostStart: 100002, Count: 5}}, GIDs: []identitymapping.Interval{{HostStart: 100002, Count: 5}}}
	got, err := compilePodmanMappingV1([]byte(data), active, 1000, 1000)
	want := "auto:size=1,uidmapping=0:42:2,uidmapping=2:1:3,gidmapping=0:42:2,gidmapping=2:1:3"
	if err != nil || got != want {
		t.Fatalf("%q: %v", got, err)
	}
}

func TestCompilePodmanMappingRejectsIncompleteOrAmbiguousEvidenceV1(t *testing.T) {
	info := string(podmanSameIDInfo(1000, 1000))
	for _, test := range []struct{ name, data string }{
		{"malformed", "{"}, {"missing mode", "{}"},
		{"rootful", strings.Replace(info, `"rootless":true`, `"rootless":false`, 1)},
		{"wrong account", strings.Replace(info, `"host_id":1000`, `"host_id":2000`, 1)},
		{"wrong group", strings.ReplaceAll(info, `"host_id":1000`, `"host_id":1001`)},
		{"zero size", strings.Replace(info, `"size":65536`, `"size":0`, 1)},
		{"missing zero", strings.Replace(info, `{"container_id":0,"host_id":1000,"size":1},`, "", 1)},
		{"root size", strings.Replace(info, `"host_id":1000,"size":1`, `"host_id":1000,"size":2`, 1)},
		{"container overlap", strings.Replace(info, `"container_id":1`, `"container_id":0`, 1)},
		{"host overlap", strings.Replace(info, `"host_id":100000`, `"host_id":1000`, 1)},
		{"overflow", strings.Replace(info, `"size":65536`, `"size":4294967295`, 1)},
		{"negative", strings.Replace(info, `"host_id":100000`, `"host_id":-1`, 1)},
		{"out of range", strings.Replace(info, `"host_id":100000`, `"host_id":4294967296`, 1)},
		{"uncovered tail", strings.ReplaceAll(info, `"size":65536`, `"size":2`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := compilePodmanMappingV1([]byte(test.data), mappedGeometryFixtureV1("exact"), 1000, 1000); err == nil {
				t.Fatal("accepted invalid parent evidence")
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*identitymapping.Geometry)
	}{
		{"missing UID zero", func(g *identitymapping.Geometry) { g.UIDs = g.UIDs[1:] }},
		{"missing GID zero", func(g *identitymapping.Geometry) { g.GIDs = g.GIDs[1:] }},
		{"host root", func(g *identitymapping.Geometry) { g.UIDs[0].HostStart = 0 }},
		{"uncovered host", func(g *identitymapping.Geometry) { g.UIDs[0].HostStart = 200000 }},
		{"invalid count", func(g *identitymapping.Geometry) { g.GIDs[0].Count = 0 }},
		{"sentinel", func(g *identitymapping.Geometry) { g.GIDs[0].HostStart = math.MaxUint32 }},
		{"container overlap", func(g *identitymapping.Geometry) { g.UIDs[1].ContainerStart = 0 }},
		{"profile", func(g *identitymapping.Geometry) { g.Profile = "keep-id" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := mappedGeometryFixtureV1("exact")
			test.mutate(&g)
			if _, err := compilePodmanMappingV1([]byte(info), g, 1000, 1000); err == nil {
				t.Fatal("accepted invalid active geometry")
			}
		})
	}
}
