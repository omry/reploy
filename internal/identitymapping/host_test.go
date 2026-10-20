package identitymapping

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

const operatorRecord = "alice:x:1000:1000:Alice:/home/alice:/bin/sh"

type hostFixture struct {
	files                        map[string]string
	reads, commands              []string
	lookup, activeUID, activeGID string
	readError, runError          error
	changePath                   string
	b                            hostBackend
}

func newHostFixture() *hostFixture {
	f := &hostFixture{files: map[string]string{
		"/etc/nsswitch.conf": "passwd: files\ngroup: files\nsubid: files\nnetgroup: nis\n",
		"/etc/subuid":        "alice:100000:1000000000\nbob:2000000000:1000\n",
		"/etc/subgid":        "alice:100000:1000000000\nbob:2000000000:1000\n",
		"/etc/passwd":        "root:x:0:0:root:/root:/bin/sh\n" + operatorRecord + "\ndaemon:x:1:1:daemon:/sbin:/bin/false\n",
		"/etc/group":         "root:x:0:\nalice:x:1000:\ndaemon:x:1:\n",
	}, lookup: operatorRecord + "\n", activeUID: "0: alice 100000 1000000000\n", activeGID: "0: alice 100000 1000000000\n"}
	f.b = hostBackend{uid: 1000, euid: 1000, gid: 1000, egid: 1000}
	f.b.read = func(ctx context.Context, path string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		f.reads = append(f.reads, path)
		if f.readError != nil {
			return "", f.readError
		}
		v, ok := f.files[path]
		if !ok {
			return "", fmt.Errorf("missing fixture file")
		}
		if path == f.changePath && len(f.reads) > 5 {
			return v + "# changed\n", nil
		}
		return v, nil
	}
	f.b.run = func(ctx context.Context, name string, args ...string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		f.commands = append(f.commands, strings.Join(append([]string{name}, args...), " "))
		if f.runError != nil {
			return "", f.runError
		}
		switch name {
		case "getent":
			return f.lookup, nil
		case "getsubids":
			if len(args) == 2 {
				return f.activeGID, nil
			}
			return f.activeUID, nil
		}
		return "", fmt.Errorf("unexpected command")
	}
	return f
}

func smallMap() []Interval { return []Interval{{ContainerStart: 0, HostStart: 100010, Count: 64}} }
func (f *hostFixture) check(u, g []Interval) (HostIdentity, error) {
	return checkHost(context.Background(), f.b, 1000, 1000, u, g)
}

func TestHostPreflightReadsCompleteAuthorityWithoutMutation(t *testing.T) {
	f := newHostFixture()
	before := cloneFiles(f.files)
	got, err := f.check(smallMap(), smallMap())
	if err != nil {
		t.Fatal(err)
	}
	if got != (HostIdentity{"alice", 1000, 1000}) {
		t.Fatalf("identity=%+v", got)
	}
	if !reflect.DeepEqual(before, f.files) {
		t.Fatal("authority files changed")
	}
	if len(f.commands) != 3 {
		t.Fatalf("commands=%v", f.commands)
	}
	for _, path := range []string{"/etc/nsswitch.conf", "/etc/subuid", "/etc/subgid", "/etc/passwd", "/etc/group"} {
		count := 0
		for _, p := range f.reads {
			if p == path {
				count++
			}
		}
		if count != 2 {
			t.Fatalf("%s reads=%d", path, count)
		}
	}
}

func cloneFiles(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestHostPreflightCallerAndScopeCannotUseEnvironmentIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*hostFixture)
	}{
		{"root", func(f *hostFixture) { f.b.uid = 0; f.b.euid = 0 }},
		{"root group", func(f *hostFixture) { f.b.gid = 0; f.b.egid = 0 }},
		{"effective uid", func(f *hostFixture) { f.b.euid = 1001 }},
		{"effective gid", func(f *hostFixture) { f.b.egid = 1001 }},
		{"sudo substitution", func(f *hostFixture) { f.b.sudo = true }},
		{"negative ids", func(f *hostFixture) { f.b.uid = -1; f.b.euid = -1 }},
		{"foreign canonical user", func(f *hostFixture) { f.lookup = strings.ReplaceAll(operatorRecord, "1000", "1001") + "\n" }},
		{"canonical group mismatch", func(f *hostFixture) { f.lookup = "alice:x:1000:1001:Alice:/home/alice:/bin/sh\n" }},
		{"missing canonical account", func(f *hostFixture) { f.lookup = "" }},
		{"ambiguous canonical account", func(f *hostFixture) { f.lookup += f.lookup }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHostFixture()
			tc.change(f)
			if _, err := f.check(smallMap(), smallMap()); err == nil {
				t.Fatal("accepted invalid caller")
			}
		})
	}
	f := newHostFixture()
	if _, err := checkHost(context.Background(), f.b, 1001, 1000, smallMap(), smallMap()); err == nil {
		t.Fatal("accepted foreign owner UID")
	}
	if _, err := checkHost(context.Background(), f.b, 1000, 1001, smallMap(), smallMap()); err == nil {
		t.Fatal("accepted foreign owner GID")
	}
}

func TestHostPreflightGeometryRejectsBeforeAnyQueries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []Interval
	}{
		{"empty", nil},
		{"zero count", []Interval{{HostStart: 100010}}},
		{"host root", []Interval{{Count: 1}}},
		{"host sentinel", []Interval{{HostStart: math.MaxUint32, Count: 1}}},
		{"container sentinel", []Interval{{HostStart: 100010, ContainerStart: math.MaxUint32, Count: 1}}},
		{"host overflow", []Interval{{HostStart: math.MaxUint32 - 1, Count: 2}}},
		{"container overflow", []Interval{{HostStart: 100010, ContainerStart: math.MaxUint32 - 1, Count: 2}}},
		{"overlap", []Interval{{HostStart: 100010, Count: 20}, {HostStart: 100020, Count: 20}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, gids := range []bool{false, true} {
				f := newHostFixture()
				u, g := smallMap(), smallMap()
				if gids {
					g = tc.values
				} else {
					u = tc.values
				}
				if _, err := f.check(u, g); err == nil {
					t.Fatal("accepted invalid geometry")
				}
				if len(f.reads)+len(f.commands) != 0 {
					t.Fatal("queried host for invalid geometry")
				}
			}
		})
	}
}

func TestHostPreflightDelegationRejectsBeforePrincipalInventory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*hostFixture)
	}{
		{"outside", func(f *hostFixture) { f.files["/etc/subuid"] = "alice:100000:1\n" }},
		{"foreign uid", func(f *hostFixture) { f.files["/etc/subuid"] += "bob:100020:1\n" }},
		{"foreign gid", func(f *hostFixture) { f.files["/etc/subgid"] += "bob:100020:1\n" }},
		{"no own uid", func(f *hostFixture) { f.files["/etc/subuid"] = "bob:100000:1000\n" }},
		{"no own gid", func(f *hostFixture) { f.files["/etc/subgid"] = "bob:100000:1000\n" }},
		{"malformed", func(f *hostFixture) { f.files["/etc/subuid"] = "alice:100000\n" }},
		{"incomplete", func(f *hostFixture) { f.files["/etc/subuid"] = "alice:100000:1000" }},
		{"zero", func(f *hostFixture) { f.files["/etc/subuid"] = "alice:100000:0\n" }},
		{"overflow", func(f *hostFixture) { f.files["/etc/subuid"] = "alice:4294967294:2\n" }},
		{"ambiguous", func(f *hostFixture) { f.files["/etc/subuid"] += "1000:100000:10\n" }},
		{"active uid disagreement", func(f *hostFixture) { f.activeUID = "0: alice 100000 999\n" }},
		{"active gid disagreement", func(f *hostFixture) { f.activeGID = "0: alice 100000 999\n" }},
		{"active unavailable", func(f *hostFixture) { f.activeUID = "" }},
		{"active malformed", func(f *hostFixture) { f.activeUID = "alice 100000 1000000000\n" }},
		{"active wrong owner", func(f *hostFixture) { f.activeUID = "0: bob 100000 1000000000\n" }},
		{"active wrong index", func(f *hostFixture) { f.activeUID = "1: alice 100000 1000000000\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHostFixture()
			tc.change(f)
			if _, err := f.check(smallMap(), smallMap()); err == nil {
				t.Fatal("accepted invalid delegation")
			}
			for _, p := range f.reads {
				if p == "/etc/passwd" || p == "/etc/group" {
					t.Fatal("read principal inventory before rejecting delegation")
				}
			}
		})
	}
}

func TestHostPreflightRequiresCompleteSources(t *testing.T) {
	for _, nss := range []string{
		"", "passwd: files\n", "group: files\n", "passwd: files systemd\ngroup: files\n",
		"passwd: sss files\ngroup: files\n", "passwd: files\ngroup: ldap\n",
		"passwd: files\ngroup: files\nsubid: sss\n", "passwd: files\ngroup: files\nsubid: files ldap\n",
		"passwd: files\npasswd: files\ngroup: files\n", "passwd: files\ngroup: files\nsubid: files\nsubid: files\n",
		"passwd: files\ngroup: files\ninitgroups: sss\n",
		"passwd: files\ngroup: files\ninitgroups: files systemd\n",
		"passwd: files\ngroup: files\ninitgroups: files\ninitgroups: files\n",
		"passwd: files [SUCCESS=return]\ngroup: files\n", "passwd: files\\\ngroup: files\n",
	} {
		f := newHostFixture()
		f.files["/etc/nsswitch.conf"] = nss
		if _, err := f.check(smallMap(), smallMap()); err == nil {
			t.Fatalf("accepted incomplete sources %q", nss)
		}
		if len(f.commands) != 0 {
			t.Fatal("queried unsupported account source")
		}
	}
	f := newHostFixture()
	f.files["/etc/nsswitch.conf"] = "passwd: files # local\ngroup: files\ninitgroups: files\n"
	if _, err := f.check(smallMap(), smallMap()); err != nil {
		t.Fatal("files default subid rejected:", err)
	}
}

func TestHostPreflightRejectsAllPrincipalCollisionDomains(t *testing.T) {
	for _, tc := range []struct{ name, path, line string }{
		{"passwd uid", "/etc/passwd", "service:x:100010:700:service:/:/bin/false\n"},
		{"passwd primary gid", "/etc/passwd", "service:x:700:100010:service:/:/bin/false\n"},
		{"group gid", "/etc/group", "service:x:100010:\n"},
		{"range last uid", "/etc/passwd", "service:x:100073:700:service:/:/bin/false\n"},
		{"range last gid", "/etc/group", "service:x:100073:\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHostFixture()
			f.files[tc.path] += tc.line
			if _, err := f.check(smallMap(), smallMap()); err == nil {
				t.Fatal("accepted host principal collision")
			}
		})
	}
	f := newHostFixture()
	f.files["/etc/passwd"] += "service:x:100074:100074:service:/:/bin/false\n"
	if _, err := f.check(smallMap(), smallMap()); err != nil {
		t.Fatal("adjacent principal rejected:", err)
	}
}

func TestHostPreflightRejectsMissingMalformedAndChangedInventory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*hostFixture)
	}{
		{"read failure", func(f *hostFixture) { f.readError = errors.New("read denied") }},
		{"command failure", func(f *hostFixture) { f.runError = errors.New("provider unavailable") }},
		{"passwd malformed", func(f *hostFixture) { f.files["/etc/passwd"] += "bad\n" }},
		{"passwd absent", func(f *hostFixture) { f.files["/etc/passwd"] = "" }},
		{"operator absent", func(f *hostFixture) { f.files["/etc/passwd"] = "root:x:0:0:root:/root:/bin/sh\n" }},
		{"operator alias", func(f *hostFixture) { f.files["/etc/passwd"] += "alias:x:1000:1000::/:/bin/sh\n" }},
		{"duplicate name", func(f *hostFixture) { f.files["/etc/passwd"] += operatorRecord + "\n" }},
		{"group empty", func(f *hostFixture) { f.files["/etc/group"] = "" }},
		{"group invalid", func(f *hostFixture) { f.files["/etc/group"] += "bad:x:no:\n" }},
		{"group duplicate", func(f *hostFixture) { f.files["/etc/group"] += "alice:x:1000:\n" }},
		{"passwd changed", func(f *hostFixture) { f.changePath = "/etc/passwd" }},
		{"delegation changed", func(f *hostFixture) { f.changePath = "/etc/subuid" }},
		{"nss changed", func(f *hostFixture) { f.changePath = "/etc/nsswitch.conf" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHostFixture()
			tc.change(f)
			if _, err := f.check(smallMap(), smallMap()); err == nil {
				t.Fatal("accepted unproved inventory")
			}
		})
	}
}

func TestHostPreflightLargeRangesHaveConstantQueryWork(t *testing.T) {
	var smallReads, smallCommands int
	for _, count := range []uint32{1, 64, 1000000000} {
		f := newHostFixture()
		values := []Interval{{HostStart: 100000, Count: count}}
		if _, err := f.check(values, values); err != nil {
			t.Fatal(err)
		}
		if smallReads == 0 {
			smallReads = len(f.reads)
			smallCommands = len(f.commands)
		}
		if len(f.reads) != smallReads || len(f.commands) != smallCommands {
			t.Fatalf("work grew for count%d", count)
		}
	}
}

func TestHostPreflightCancellationAndNumericDelegation(t *testing.T) {
	f := newHostFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checkHost(ctx, f.b, 1000, 1000, smallMap(), smallMap()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if len(f.reads)+len(f.commands) != 0 {
		t.Fatal("queried cancelled check")
	}
	if _, err := checkHost(nil, f.b, 1000, 1000, smallMap(), smallMap()); err == nil {
		t.Fatal("accepted nil context")
	}
	f = newHostFixture()
	f.files["/etc/subuid"] = strings.ReplaceAll(f.files["/etc/subuid"], "alice:", "1000:")
	f.activeUID = strings.ReplaceAll(f.activeUID, "alice ", "1000 ")
	if _, err := f.check(smallMap(), smallMap()); err != nil {
		t.Fatal(err)
	}
}

func numericCallerFixture() *hostFixture {
	f := newHostFixture()
	f.files["/etc/passwd"] = "root:x:0:0:root:/root:/bin/sh\n2000:x:1000:1000:Numeric:/home/numeric:/bin/sh\n"
	f.files["/etc/group"] = "root:x:0:\n2000:x:1000:\n"
	f.files["/etc/subuid"] = "2000:100000:1000000000\n"
	f.files["/etc/subgid"] = "2000:100000:1000000000\n"
	f.lookup = "2000:x:1000:1000:Numeric:/home/numeric:/bin/sh\n"
	f.activeUID = "0: 2000 100000 1000000000\n"
	f.activeGID = "0: 2000 100000 1000000000\n"
	return f
}

func TestHostPreflightRejectsForeignNumericOwnerAliases(t *testing.T) {
	for _, tc := range []struct {
		name string
		add  func(*hostFixture)
	}{
		{"uid", func(f *hostFixture) {
			f.files["/etc/passwd"] += "bob:x:2000:3000:Bob:/home/bob:/bin/sh\n"
		}},
		{"gid", func(f *hostFixture) {
			f.files["/etc/passwd"] += "bob:x:2000:3000:Bob:/home/bob:/bin/sh\n"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := numericCallerFixture()
			tc.add(f)
			if _, err := f.check(smallMap(), smallMap()); err == nil {
				t.Fatal("accepted foreign numeric owner alias")
			}
		})
	}
}

func TestHostPreflightRejectsInverseForeignNumericNameAliases(t *testing.T) {
	for _, tc := range []struct {
		name string
		add  func(*hostFixture)
	}{
		{"uid", func(f *hostFixture) {
			f.files["/etc/passwd"] += "1000:x:2000:2000:NumericAlias:/home/alias:/bin/sh\n"
			f.files["/etc/subuid"] = "1000:100000:1000000000\n"
			f.activeUID = "0: 1000 100000 1000000000\n"
		}},
		{"gid", func(f *hostFixture) {
			f.files["/etc/passwd"] += "1000:x:2000:3000:NumericAlias:/home/alias:/bin/sh\n"
			f.files["/etc/subgid"] = "1000:100000:1000000000\n"
			f.activeGID = "0: 1000 100000 1000000000\n"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHostFixture()
			tc.add(f)
			if _, err := f.check(smallMap(), smallMap()); err == nil {
				t.Fatal("accepted inverse foreign numeric name alias")
			}
		})
	}
}

func TestHostPreflightRejectsPasswdOnlyNumericSubgidAlias(t *testing.T) {
	f := newHostFixture()
	f.files["/etc/passwd"] += "1000:x:2000:3000:NumericAlias:/home/alias:/bin/sh\n"
	f.files["/etc/group"] = "root:x:0:\nalice:x:1000:\nforeignprimary:x:3000:\n"
	f.files["/etc/subgid"] = "1000:100000:1000000000\n"
	f.activeGID = "0: 1000 100000 1000000000\n"
	if _, err := f.check(smallMap(), smallMap()); err == nil {
		t.Fatal("accepted passwd-only numeric subgid alias")
	}
}

func TestHostPreflightAcceptsUserRecipientWithDifferentPrimaryGID(t *testing.T) {
	f := newHostFixture()
	f.b.gid, f.b.egid = 3000, 3000
	f.lookup = "alice:x:1000:3000:Alice:/home/alice:/bin/sh\n"
	f.files["/etc/passwd"] = "root:x:0:0:root:/root:/bin/sh\nalice:x:1000:3000:Alice:/home/alice:/bin/sh\ndaemon:x:1:1:daemon:/sbin:/bin/false\n"
	f.files["/etc/group"] = "root:x:0:\nalice:x:2000:\n1000:x:2000:\nstaff:x:3000:\n"
	if _, err := checkHost(context.Background(), f.b, 1000, 3000, smallMap(), smallMap()); err != nil {
		t.Fatal("rejected valid user recipient with unrelated groups:", err)
	}
}

func TestHostPreflightPreservesUnambiguousNumericNames(t *testing.T) {
	f := numericCallerFixture()
	if _, err := f.check(smallMap(), smallMap()); err != nil {
		t.Fatal("rejected unambiguous numeric name:", err)
	}

	f = numericCallerFixture()
	f.b.uid, f.b.euid, f.b.gid, f.b.egid = 2000, 2000, 2000, 2000
	f.lookup = "2000:x:2000:2000:Numeric:/home/numeric:/bin/sh\n"
	f.files["/etc/passwd"] = "root:x:0:0:root:/root:/bin/sh\n2000:x:2000:2000:Numeric:/home/numeric:/bin/sh\n"
	f.files["/etc/group"] = "root:x:0:\n2000:x:2000:\n"
	if _, err := checkHost(context.Background(), f.b, 2000, 2000, smallMap(), smallMap()); err != nil {
		t.Fatal("rejected aligned numeric name:", err)
	}
}

func TestHostPreflightAdjacentDelegationUnionAndIndependentDomains(t *testing.T) {
	f := newHostFixture()
	f.files["/etc/subuid"] = "alice:100000:32\nalice:100032:32\n"
	f.activeUID = "0: alice 100032 32\n1: alice 100000 32\n"
	u := []Interval{{HostStart: 100000, Count: 64}}
	if _, err := f.check(u, smallMap()); err != nil {
		t.Fatal(err)
	}
	f = newHostFixture()
	f.files["/etc/subuid"] = "alice:100000:31\nalice:100032:32\n"
	if _, err := f.check(u, smallMap()); err == nil {
		t.Fatal("accepted gap")
	}
}
