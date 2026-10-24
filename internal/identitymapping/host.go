// Package identitymapping checks explicit, deployment-private subordinate IDs.
// A successful host check is a fresh observation, never execution admission.
package identitymapping

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Interval maps Count consecutive container IDs to consecutive host IDs.
type Interval struct {
	ContainerStart uint32 `json:"container_start"`
	HostStart      uint32 `json:"host_start"`
	Count          uint32 `json:"count"`
}

// HostIdentity identifies the operator whose existing delegation was checked.
// It does not identify a container engine or service-manager account.
type HostIdentity struct {
	Username string
	UID, GID uint32
}

type hostBackend struct {
	uid, euid, gid, egid int
	sudo                 bool
	read                 func(context.Context, string) (string, error)
	run                  func(context.Context, string, ...string) (string, error)
}

type span struct{ start, count uint64 }

func (s span) end() uint64          { return s.start + s.count }
func (s span) overlaps(t span) bool { return s.start < t.end() && t.start < s.end() }

// ValidateInterval rejects overflow and the POSIX unchanged-ID sentinel.
func ValidateInterval(value Interval) error {
	if value.Count == 0 || uint64(value.ContainerStart)+uint64(value.Count) > math.MaxUint32 ||
		uint64(value.HostStart)+uint64(value.Count) > math.MaxUint32 {
		return fmt.Errorf("mapping interval must contain usable unsigned 32-bit IDs")
	}
	return nil
}

func hostSpans(values []Interval) ([]span, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("mapping requires nonempty UID and GID intervals")
	}
	result := make([]span, 0, len(values))
	for _, value := range values {
		if err := ValidateInterval(value); err != nil {
			return nil, err
		}
		if value.HostStart == 0 {
			return nil, fmt.Errorf("mapping cannot include host root")
		}
		result = append(result, span{uint64(value.HostStart), uint64(value.Count)})
	}
	sortSpans(result)
	for i := 1; i < len(result); i++ {
		if result[i-1].overlaps(result[i]) {
			return nil, fmt.Errorf("mapping host intervals overlap")
		}
	}
	return result, nil
}

func sortSpans(values []span) {
	slices.SortFunc(values, func(a, b span) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		if a.count < b.count {
			return -1
		}
		if a.count > b.count {
			return 1
		}
		return 0
	})
}

func number(value string) (uint32, error) {
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil || n == math.MaxUint32 || strconv.FormatUint(n, 10) != value {
		return 0, fmt.Errorf("host inventory contains a noncanonical or unusable ID")
	}
	return uint32(n), nil
}

func inventoryLines(content string) ([]string, error) {
	if strings.ContainsAny(content, "\x00\r") || content != "" && !strings.HasSuffix(content, "\n") {
		return nil, fmt.Errorf("host inventory is malformed or incomplete")
	}
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func filesOnly(content string) error {
	lines, err := inventoryLines(content)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, line := range lines {
		line = strings.SplitN(line, "#", 2)[0]
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if key != "passwd" && key != "group" && key != "subid" && key != "initgroups" {
			continue
		}
		if !ok || seen[key] || strings.TrimSpace(value) != "files" {
			return fmt.Errorf("identity mapping requires complete files-backed %s inventory", key)
		}
		seen[key] = true
	}
	if !seen["passwd"] || !seen["group"] {
		return fmt.Errorf("identity mapping cannot prove complete passwd/group sources")
	}
	return nil
}

func account(line string) (HostIdentity, error) {
	f := strings.Split(line, ":")
	if len(f) != 7 || f[0] == "" || strings.ContainsAny(f[0], " \t\\") || strings.HasPrefix(f[0], "-") || strings.HasPrefix(f[0], "+") || strings.ContainsAny(line, "\n\x00") {
		return HostIdentity{}, fmt.Errorf("host passwd record is malformed")
	}
	uid, e1 := number(f[2])
	gid, e2 := number(f[3])
	if e1 != nil || e2 != nil {
		return HostIdentity{}, fmt.Errorf("host passwd IDs are invalid")
	}
	return HostIdentity{f[0], uid, gid}, nil
}

func delegated(content string, caller HostIdentity) ([]span, []span, []string, error) {
	lines, err := inventoryLines(content)
	if err != nil {
		return nil, nil, nil, err
	}
	var own, foreign []span
	var ownOwners []string
	for _, line := range lines {
		f := strings.Split(line, ":")
		if len(f) != 3 || f[0] == "" {
			return nil, nil, nil, fmt.Errorf("subordinate delegation record is malformed")
		}
		start, e1 := number(f[1])
		count, e2 := number(f[2])
		if e1 != nil || e2 != nil || count == 0 || uint64(start)+uint64(count) > math.MaxUint32 {
			return nil, nil, nil, fmt.Errorf("subordinate delegation range is invalid")
		}
		s := span{uint64(start), uint64(count)}
		if f[0] == caller.Username || f[0] == strconv.FormatUint(uint64(caller.UID), 10) {
			own = append(own, s)
			ownOwners = append(ownOwners, f[0])
		} else {
			foreign = append(foreign, s)
		}
	}
	sortSpans(own)
	if len(own) == 0 {
		return nil, nil, nil, fmt.Errorf("operator has no subordinate delegation")
	}
	for i := 1; i < len(own); i++ {
		if own[i-1].overlaps(own[i]) {
			return nil, nil, nil, fmt.Errorf("operator delegation is ambiguous")
		}
	}
	return own, foreign, ownOwners, nil
}

func checkDelegation(proposed, own, foreign []span) error {
	for _, wanted := range proposed {
		next := wanted.start
		for _, available := range own {
			if available.start <= next && available.end() > next {
				next = available.end()
			}
		}
		if next < wanted.end() {
			return fmt.Errorf("mapping lies outside operator delegation")
		}
		for _, other := range foreign {
			if wanted.overlaps(other) {
				return fmt.Errorf("mapping overlaps another principal's delegation")
			}
		}
	}
	return nil
}

type ownerPrincipal struct {
	name string
	id   uint32
}

type ownerInventory struct {
	byName map[string]ownerPrincipal
	byID   map[uint32][]ownerPrincipal
}

func addOwnerPrincipal(inventory *ownerInventory, principal ownerPrincipal) error {
	if _, exists := inventory.byName[principal.name]; exists {
		return fmt.Errorf("host principal names are ambiguous")
	}
	inventory.byName[principal.name] = principal
	inventory.byID[principal.id] = append(inventory.byID[principal.id], principal)
	return nil
}

func passwdOwnerInventory(content string) (ownerInventory, error) {
	lines, err := inventoryLines(content)
	if err != nil {
		return ownerInventory{}, err
	}
	inventory := ownerInventory{byName: map[string]ownerPrincipal{}, byID: map[uint32][]ownerPrincipal{}}
	for _, line := range lines {
		entry, err := account(line)
		if err != nil {
			return ownerInventory{}, err
		}
		if err := addOwnerPrincipal(&inventory, ownerPrincipal{entry.Username, entry.UID}); err != nil {
			return ownerInventory{}, err
		}
	}
	return inventory, nil
}

func numericOwnerID(value string) (uint32, bool) {
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil || n == math.MaxUint32 {
		return 0, false
	}
	return uint32(n), true
}

func checkOwnerTokens(owners []string, callerName string, callerID uint32, inventory ownerInventory) error {
	for _, owner := range owners {
		if principal, ok := inventory.byName[owner]; ok &&
			(principal.name != callerName || principal.id != callerID) {
			return fmt.Errorf("subordinate owner token is foreign or ambiguous")
		}
		numeric, ok := numericOwnerID(owner)
		if !ok {
			continue
		}
		for _, principal := range inventory.byID[numeric] {
			if principal.name != callerName || principal.id != callerID {
				return fmt.Errorf("subordinate owner token is foreign or ambiguous")
			}
		}
	}
	return nil
}

func activeRanges(content string, caller HostIdentity) ([]span, error) {
	lines, err := inventoryLines(content)
	if err != nil {
		return nil, err
	}
	var result []span
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) != 4 || f[0] != strconv.Itoa(i)+":" ||
			(f[1] != caller.Username && f[1] != strconv.FormatUint(uint64(caller.UID), 10)) {
			return nil, fmt.Errorf("active subordinate delegation output is malformed")
		}
		start, e1 := number(f[2])
		count, e2 := number(f[3])
		if e1 != nil || e2 != nil || count == 0 || uint64(start)+uint64(count) > math.MaxUint32 {
			return nil, fmt.Errorf("active subordinate delegation range is invalid")
		}
		result = append(result, span{uint64(start), uint64(count)})
	}
	sortSpans(result)
	return result, nil
}

func collides(id uint32, ranges []span) bool {
	for _, r := range ranges {
		if uint64(id) >= r.start && uint64(id) < r.end() {
			return true
		}
	}
	return false
}

func checkPrincipals(passwd, group string, caller HostIdentity, uids, gids []span) error {
	lines, err := inventoryLines(passwd)
	if err != nil {
		return err
	}
	found := 0
	names := map[string]bool{}
	for _, line := range lines {
		entry, err := account(line)
		if err != nil {
			return err
		}
		if names[entry.Username] {
			return fmt.Errorf("host passwd names are ambiguous")
		}
		names[entry.Username] = true
		if entry.UID == caller.UID {
			found++
			if entry != caller {
				return fmt.Errorf("operator account changed or has an alias")
			}
		}
		if collides(entry.UID, uids) || collides(entry.GID, gids) {
			return fmt.Errorf("mapping aliases a host passwd UID or primary GID")
		}
	}
	if found != 1 {
		return fmt.Errorf("operator account is absent or ambiguous")
	}
	lines, err = inventoryLines(group)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return fmt.Errorf("host group inventory is incomplete")
	}
	names = map[string]bool{}
	for _, line := range lines {
		f := strings.Split(line, ":")
		if len(f) != 4 || f[0] == "" || names[f[0]] {
			return fmt.Errorf("host group inventory is malformed or ambiguous")
		}
		names[f[0]] = true
		gid, err := number(f[2])
		if err != nil {
			return err
		}
		if collides(gid, gids) {
			return fmt.Errorf("mapping aliases a host group GID")
		}
	}
	return nil
}

func checkHost(ctx context.Context, b hostBackend, ownerUID, ownerGID int, uids, gids []Interval) (HostIdentity, error) {
	if ctx == nil {
		return HostIdentity{}, fmt.Errorf("host check requires a context")
	}
	if err := ctx.Err(); err != nil {
		return HostIdentity{}, err
	}
	u, err := hostSpans(uids)
	if err != nil {
		return HostIdentity{}, err
	}
	g, err := hostSpans(gids)
	if err != nil {
		return HostIdentity{}, err
	}
	if b.uid <= 0 || b.gid <= 0 || b.uid != b.euid || b.gid != b.egid || b.sudo || b.uid != ownerUID || b.gid != ownerGID {
		return HostIdentity{}, fmt.Errorf("identity mapping requires the same non-root caller and deployment owner without sudo substitution")
	}
	snapshot := map[string]string{}
	read := func(path string) (string, error) {
		v, err := b.read(ctx, path)
		if err == nil {
			snapshot[path] = v
		}
		return v, err
	}
	nss, err := read("/etc/nsswitch.conf")
	if err != nil {
		return HostIdentity{}, err
	}
	if err := filesOnly(nss); err != nil {
		return HostIdentity{}, err
	}
	lookup, err := b.run(ctx, "getent", "passwd", strconv.Itoa(b.uid))
	if err != nil {
		return HostIdentity{}, err
	}
	lines, err := inventoryLines(lookup)
	if err != nil || len(lines) != 1 {
		return HostIdentity{}, fmt.Errorf("cannot resolve one canonical operator account")
	}
	caller, err := account(lines[0])
	if err != nil || uint64(caller.UID) != uint64(b.uid) || uint64(caller.GID) != uint64(b.gid) {
		return HostIdentity{}, fmt.Errorf("canonical operator account differs from caller IDs")
	}
	var uidOwners, gidOwners []string
	for i, kind := range []string{"uid", "gid"} {
		text, err := read("/etc/sub" + kind)
		if err != nil {
			return HostIdentity{}, err
		}
		own, foreign, owners, err := delegated(text, caller)
		if err != nil {
			return HostIdentity{}, err
		}
		proposed := u
		if i == 1 {
			proposed = g
		}
		if err := checkDelegation(proposed, own, foreign); err != nil {
			return HostIdentity{}, err
		}
		if i == 0 {
			uidOwners = owners
		} else {
			gidOwners = owners
		}
		args := []string{caller.Username}
		if i == 1 {
			args = []string{"-g", caller.Username}
		}
		output, err := b.run(ctx, "getsubids", args...)
		if err != nil {
			return HostIdentity{}, err
		}
		active, err := activeRanges(output, caller)
		if err != nil || !slices.Equal(own, active) {
			return HostIdentity{}, fmt.Errorf("files and active subordinate %s delegation disagree", kind)
		}
	}
	passwd, err := read("/etc/passwd")
	if err != nil {
		return HostIdentity{}, err
	}
	group, err := read("/etc/group")
	if err != nil {
		return HostIdentity{}, err
	}
	passwdInventory, err := passwdOwnerInventory(passwd)
	if err != nil {
		return HostIdentity{}, err
	}
	if err := checkOwnerTokens(uidOwners, caller.Username, caller.UID, passwdInventory); err != nil {
		return HostIdentity{}, err
	}
	if err := checkOwnerTokens(gidOwners, caller.Username, caller.UID, passwdInventory); err != nil {
		return HostIdentity{}, err
	}
	if err := checkPrincipals(passwd, group, caller, u, g); err != nil {
		return HostIdentity{}, err
	}
	for path, before := range snapshot {
		after, err := b.read(ctx, path)
		if err != nil {
			return HostIdentity{}, err
		}
		if after != before {
			return HostIdentity{}, fmt.Errorf("host authority changed during identity check")
		}
	}
	if err := ctx.Err(); err != nil {
		return HostIdentity{}, err
	}
	return caller, nil
}
