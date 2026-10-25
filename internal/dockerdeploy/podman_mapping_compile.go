package dockerdeploy

import (
	"cmp"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/identitymapping"
)

// Compile actual host IDs through the verified engine's parent namespace.
// Work scales with intervals and their boundaries, never with individual IDs.
func compilePodmanMappingV1(data []byte, active identitymapping.Geometry, uid, gid uint32) (string, error) {
	type entry struct {
		Container uint32 `json:"container_id"`
		Host      uint32 `json:"host_id"`
		Size      uint32 `json:"size"`
	}
	var info struct {
		Host struct {
			Security   struct{ Rootless *bool }
			IDMappings struct{ UIDMap, GIDMap []entry }
		}
	}
	if json.Unmarshal(data, &info) != nil || info.Host.Security.Rootless == nil || !*info.Host.Security.Rootless {
		return "", fmt.Errorf("mapped creation requires explicit rootless Podman metadata")
	}
	parent := identitymapping.Geometry{Profile: "exact"}
	for i, entries := range [][]entry{info.Host.IDMappings.UIDMap, info.Host.IDMappings.GIDMap} {
		var values []identitymapping.Interval
		for _, e := range entries {
			values = append(values, identitymapping.Interval{ContainerStart: e.Container, HostStart: e.Host, Count: e.Size})
		}
		if i == 0 {
			parent.UIDs = values
		} else {
			parent.GIDs = values
		}
	}
	canonical, err := identitymapping.ValidateGeometry(parent)
	if err != nil || !reflect.DeepEqual(canonical, parent) {
		return "", fmt.Errorf("Podman parent maps are incomplete or noncanonical: %v", err)
	}
	active, err = identitymapping.ValidateGeometry(active)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	result.WriteString("auto:size=1")
	for i, values := range [][]identitymapping.Interval{active.UIDs, active.GIDs} {
		parents, owner, kind := parent.UIDs, uid, "uidmapping"
		if i == 1 {
			parents, owner, kind = parent.GIDs, gid, "gidmapping"
		}
		if parents[0].ContainerStart != 0 || parents[0].Count != 1 || parents[0].HostStart != owner || values[0].ContainerStart != 0 {
			return "", fmt.Errorf("mapped creation requires explicit container zero and the verified engine account")
		}
		slices.SortFunc(parents, func(a, b identitymapping.Interval) int {
			return cmp.Compare(a.HostStart, b.HostStart)
		})
		for _, value := range values {
			start, end := uint64(value.HostStart), uint64(value.HostStart)+uint64(value.Count)
			for start < end {
				j := sort.Search(len(parents), func(j int) bool { return uint64(parents[j].HostStart)+uint64(parents[j].Count) > start })
				if j == len(parents) || uint64(parents[j].HostStart) > start {
					return "", fmt.Errorf("active %s interval is outside the verified parent map", kind)
				}
				p := parents[j]
				stop := min(end, uint64(p.HostStart)+uint64(p.Count))
				container := uint64(value.ContainerStart) + start - uint64(value.HostStart)
				translated := uint64(p.ContainerStart) + start - uint64(p.HostStart)
				result.WriteString("," + kind + "=" + strconv.FormatUint(container, 10) + ":" + strconv.FormatUint(translated, 10) + ":" + strconv.FormatUint(stop-start, 10))
				start = stop
			}
		}
	}
	return result.String(), nil
}
