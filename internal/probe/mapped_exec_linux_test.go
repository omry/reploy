//go:build linux && (amd64 || arm64)

package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/mappedlaunch"
	"golang.org/x/sys/unix"
)

//go:noinline
func mappedReadAtDepthV1(depth, fd int) byte {
	var buffer [256]byte
	buffer[0] = 77
	if depth > 0 {
		value := mappedReadAtDepthV1(depth-1, fd)
		runtime.KeepAlive(&buffer)
		return value
	}
	mappedRawCallV1(unix.SYS_READ, uintptr(fd), uintptr(unsafe.Pointer(&buffer[0])), 1)
	return buffer[0]
}

func TestMappedRawPointerStackRelocationV1(t *testing.T) {
	var pipe [2]int
	if err := unix.Pipe(pipe[:]); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipe[0])
	defer unix.Close(pipe[1])
	for depth := range 2048 {
		if _, err := unix.Write(pipe[1], []byte{42}); err != nil {
			t.Fatal(err)
		}
		result := make(chan byte, 1)
		go func() { result <- mappedReadAtDepthV1(depth, pipe[0]) }()
		if got := <-result; got != 42 {
			t.Errorf("stack depth%d: pointer destination=%d, want42", depth, got)
		}
	}
}

func TestMappedStartupPreflightRejectsUnsupportedAuthorityV1(t *testing.T) {
	mask := uint64(0)
	good := sandboxExecPlanV1{UID: 200, GID: 201, Groups: []uint32{100, 201}, MappedCapabilities: &mask, Argv: []string{"/bin/true"}}
	if err := validateMappedStartupV1(good); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"nil", "UID", "GID", "groups sentinel", "groups unsorted", "groups duplicate", "nonzero capabilities", "private", "observed", "no argv", "relative", "unclean", "kernel ceiling"} {
		t.Run(mode, func(t *testing.T) {
			plan := good
			plan.Groups = append([]uint32(nil), good.Groups...)
			value := mask
			plan.MappedCapabilities = &value
			switch mode {
			case "nil":
				plan.MappedCapabilities = nil
			case "UID":
				plan.UID = math.MaxUint32
			case "GID":
				plan.GID = math.MaxUint32
			case "groups sentinel":
				plan.Groups = []uint32{math.MaxUint32}
			case "groups unsorted":
				plan.Groups = []uint32{201, 100}
			case "groups duplicate":
				plan.Groups = []uint32{100, 100}
			case "nonzero capabilities":
				value = 1
			case "private":
				plan.PrivateEnvironment = true
			case "observed":
				plan.RecordExitStatus = true
			case "no argv":
				plan.Argv = nil
			case "relative":
				plan.Argv = []string{"bin/true"}
			case "unclean":
				plan.Argv = []string{"/bin/../bin/true"}
			case "kernel ceiling":
				plan.UID, value = 0, uint64(1)<<63
			}
			if err := validateMappedStartupV1(plan); err == nil {
				t.Fatal("unsupported authority accepted")
			}
		})
	}
	// All conversion errors precede credential changes or thread creation.
	for _, mode := range []string{"filename", "argument", "environment"} {
		t.Run(mode, func(t *testing.T) {
			plan, environment := good, []string{"PATH=/usr/bin:/bin"}
			switch mode {
			case "filename":
				plan.Argv = []string{"/bad\x00path"}
			case "argument":
				plan.Argv = []string{"/bin/true", "bad\x00argument"}
			case "environment":
				environment = []string{"bad\x00environment"}
			}
			if err := execMappedApplicationV1(plan, environment); err == nil {
				t.Fatal("invalid exec pointer accepted")
			}
		})
	}
}

func TestMappedStandardDescriptorsV1(t *testing.T) {
	if text := os.Getenv("REPLOY_TEST_MAPPED_FD_V1"); text != "" {
		fd, err := strconv.Atoi(text)
		if err != nil {
			os.Exit(3)
		}
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
			os.Exit(4)
		}
		mask := uint64(0)
		plan := sandboxExecPlanV1{UID: 200, GID: 200, MappedCapabilities: &mask, Argv: []string{"/bin/true"}}
		if err := validateMappedStartupV1(plan); err == nil || !strings.Contains(err.Error(), "standard descriptor") {
			os.Exit(5)
		}
		os.Exit(0)
	}
	for fd := range 3 {
		command := exec.Command(os.Args[0], "-test.run=^TestMappedStandardDescriptorsV1$")
		command.Env = append(os.Environ(), fmt.Sprintf("REPLOY_TEST_MAPPED_FD_V1=%d", fd))
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("reject close-on-exec descriptor%d: %v: %s", fd, err, output)
		}
	}
}

func TestMappedKernelCapabilityLimitV1(t *testing.T) {
	for _, test := range []struct {
		text string
		mask uint64
		err  error
		pass bool
	}{
		{"", 0, errors.New("fixture read failure"), false},
		{"broken", 0, nil, false}, {"-1", 0, nil, false}, {"64", 0, nil, false},
		{"0", 2, nil, false}, {"0", 1, nil, true}, {"63", uint64(1) << 63, nil, true},
	} {
		plan := sandboxExecPlanV1{MappedCapabilities: &test.mask, Argv: []string{"/bin/true"}}
		called := false
		err := validateMappedStartupWithV1(plan, func(path string) ([]byte, error) {
			called = true
			if path != "/proc/sys/kernel/cap_last_cap" {
				t.Fatal("untrusted capability-limit path", path)
			}
			return []byte(test.text), test.err
		})
		if !called || (err == nil) != test.pass {
			t.Fatalf("limit %q mask%x pass=%v: %v", test.text, test.mask, test.pass, err)
		}
	}
}

func mappedFixtureSubIDV1(t *testing.T, kind string) uint64 {
	t.Helper()
	content, err := os.ReadFile("/etc/sub" + kind)
	if err != nil {
		t.Skipf("production mapped proof needs subordinate %ss: %v", kind, err)
	}
	uid := strconv.Itoa(os.Getuid())
	identity, err := exec.Command("id", "-un").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 3 || fields[0] != uid && fields[0] != strings.TrimSpace(string(identity)) {
			continue
		}
		base, e1 := strconv.ParseUint(fields[1], 10, 32)
		count, e2 := strconv.ParseUint(fields[2], 10, 32)
		if e1 == nil && e2 == nil && count >= 512 && base+511 < math.MaxUint32 {
			return base
		}
	}
	t.Skipf("production mapped proof needs an authorized512-ID subordinate %s range", kind)
	return 0
}

func mappedProductionBinaryV1(t *testing.T, directory string) string {
	t.Helper()
	probe := filepath.Join(directory, "reploy-probe")
	command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", probe, "../../cmd/reploy-probe")
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build exact production probe: %v: %s", err, output)
	}
	disassembly, err := exec.Command("go", "tool", "objdump", "-s", "github.com/omry/reploy/internal/probe.mappedExecTailV1", probe).CombinedOutput()
	if err != nil || !bytes.Contains(disassembly, []byte("TEXT ")) {
		t.Fatalf("inspect exact production tail: %v: %s", err, disassembly)
	}
	calls := 0
	for _, line := range strings.Split(string(disassembly), "\n") {
		if strings.Contains(line, "CALL") {
			calls++
			if !strings.Contains(line, "syscall.RawSyscall") {
				t.Fatalf("non-raw call after descriptor closure: %s", line)
			}
		}
	}
	if calls != 3 || strings.Contains(string(disassembly), "runtime.") {
		t.Fatalf("unexpected raw exec-or-exit tail: %s", disassembly)
	}
	return probe
}

func TestMappedProductionExecTailV1(t *testing.T) {
	// Compiler/tail qualification runs even without namespace/map helpers.
	_ = mappedProductionBinaryV1(t, t.TempDir())
}

func TestMappedProductionStartupV1(t *testing.T) {
	for _, name := range []string{"cc", "go", "unshare", "newuidmap", "newgidmap"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("production mapped proof prerequisite %s unavailable: %v", name, err)
		}
	}
	if output, err := exec.Command("unshare", "--user", "--map-root-user", "--net", "true").CombinedOutput(); err != nil {
		t.Skipf("production mapped proof needs disposable user/network namespaces: %v: %s", err, output)
	}
	uidBase, gidBase := mappedFixtureSubIDV1(t, "uid"), mappedFixtureSubIDV1(t, "gid")
	directory, err := os.MkdirTemp("", "reploy-mapped-startup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	// These are public fixture executables. Namespace-root must traverse the
	// directory after losing the caller's host identity. No secrets are stored.
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	probe := mappedProductionBinaryV1(t, directory)
	payload, bootstrap := filepath.Join(directory, "payload"), filepath.Join(directory, "bootstrap")
	commands := [][]string{
		{"go", "build", "-buildvcs=false", "-trimpath", "-o", payload, "testdata/mapped_startup_payload.go"},
		{"cc", "-O2", "-Wall", "-Wextra", "-Werror", "-o", bootstrap, "testdata/mapped_startup_bootstrap.c"},
	}
	for _, argv := range commands {
		command := exec.Command(argv[0], argv[1:]...)
		command.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build exact production/fixture binary %v: %v: %s", argv, err, output)
		}
	}
	t.Logf("qualified production source with %s %s/%s; fixture mode2 is not effective-policy qualification", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	for _, profile := range []string{"exact", "exact-permuted-gids", "bounded"} {
		for _, tuple := range []struct {
			uid, gid uint32
			groups   []uint32
			mask     uint64
		}{
			{0, 0, nil, 0}, {200, 201, []uint32{100, 201}, 0},
			{0, 0, nil, 0xc4}, {0, 200, []uint32{100, 201}, 0x400c1},
			{0, 0, []uint32{100}, uint64(1) << 40},
		} {
			for repetition := range 3 {
				t.Run(fmt.Sprintf("%s/uid%d/gid%d/mask%x/%d", profile, tuple.uid, tuple.gid, tuple.mask, repetition), func(t *testing.T) {
					mappedProductionCaseV1(t, bootstrap, probe, payload, directory, profile, uidBase, gidBase, tuple.uid, tuple.gid, tuple.groups, tuple.mask, "normal", 0)
				})
			}
		}
		for _, negative := range []struct {
			mode string
			uid  uint32
			gid  uint32
			code int
		}{{"deny-close", 0, 0, 125}, {"bad-exec", 0, 0, 125}, {"deny-setuid", 0, 0, 121}, {"deny-setgid", 0, 0, 121}, {"deny-groups", 0, 0, 121}, {"deny-capset", 0, 0, 121}, {"deny-prctl", 0, 0, 121}, {"deny-get-securebits", 0, 0, 122}, {"unmapped-uid", 999, 0, 121}, {"unmapped-gid", 0, 999, 121}} {
			t.Run(profile+"/"+negative.mode, func(t *testing.T) {
				mappedProductionCaseV1(t, bootstrap, probe, payload, directory, profile, uidBase, gidBase, negative.uid, negative.gid, nil, 0, negative.mode, negative.code)
			})
		}
		t.Run(profile+"/unmapped-groups", func(t *testing.T) {
			mappedProductionCaseV1(t, bootstrap, probe, payload, directory, profile, uidBase, gidBase, 0, 0, []uint32{999}, 0, "normal", 121)
		})
		t.Run(profile+"/clear-ambient", func(t *testing.T) {
			mappedProductionCaseV1(t, bootstrap, probe, payload, directory, profile, uidBase, gidBase, 0, 0, nil, 0, "ambient", 0)
		})
		t.Run(profile+"/set-nnp", func(t *testing.T) {
			status, err := os.ReadFile("/proc/self/status")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(status), "NoNewPrivs:\t0\n") {
				t.Skip("cannot remove inherited host no-new-privileges for this control")
			}
			mappedProductionCaseV1(t, bootstrap, probe, payload, directory, profile, uidBase, gidBase, 0, 0, nil, 0, "unset-nnp", 0)
		})
		t.Run(profile+"/no-filter", func(t *testing.T) {
			status, err := os.ReadFile("/proc/self/status")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(status), "Seccomp:\t0\n") {
				t.Skip("cannot remove an inherited host seccomp filter for this negative control")
			}
			mappedProductionCaseV1(t, bootstrap, probe, payload, directory, profile, uidBase, gidBase, 0, 0, nil, 0, "no-filter", 122)
		})
	}
}

func mappedProductionCaseV1(t *testing.T, bootstrap, probe, payload, directory, profile string, uidBase, gidBase uint64, uid, gid uint32, groups []uint32, mask uint64, mode string, expectedExit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	binding := mappedWaitBindingV1()
	encoded, err := canonical.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := mappedlaunch.Frame(mappedlaunch.Request{Binding: binding, Container: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	groupText := []string{}
	for _, g := range groups {
		groupText = append(groupText, strconv.FormatUint(uint64(g), 10))
	}
	canary, err := os.CreateTemp(directory, "mapped-private-canary-")
	if err != nil {
		t.Fatal(err)
	}
	defer canary.Close()
	executable := payload
	if mode == "bad-exec" {
		executable = filepath.Join(directory, "missing-executable")
	}
	args := []string{probe, fmt.Sprint(mask), mode, "/reploy-probe", mappedlaunch.Command, string(encoded), time.Now().Add(15 * time.Second).UTC().Format(time.RFC3339Nano), "sandbox-exec", fmt.Sprintf("%s%016x", mappedCapabilityArgumentV1, mask), "--uid", fmt.Sprint(uid), "--gid", fmt.Sprint(gid), "--groups", strings.Join(groupText, ","), "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", executable, "3"}
	command := exec.CommandContext(ctx, bootstrap, args...)
	command.ExtraFiles = []*os.File{canary}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	reader := bufio.NewReader(output)
	ready, err := reader.ReadString('\n')
	if err != nil || ready != "ready\n" {
		t.Fatalf("namespace readiness: %q %v", ready, err)
	}
	for _, domain := range []struct {
		kind string
		base uint64
		ids  []uint64
	}{{"uid", uidBase, []uint64{0, 100, 200}}, {"gid", gidBase, []uint64{0, 100, 200, 201}}} {
		mapArgs := []string{strconv.Itoa(command.Process.Pid)}
		if profile == "bounded" {
			mapArgs = append(mapArgs, "0", strconv.FormatUint(domain.base, 10), "512")
		} else {
			for _, id := range domain.ids {
				hostOffset := id
				if profile == "exact-permuted-gids" && domain.kind == "gid" {
					if id == 100 {
						hostOffset = 201
					} else if id == 201 {
						hostOffset = 100
					}
				}
				mapArgs = append(mapArgs, fmt.Sprint(id), fmt.Sprint(domain.base+hostOffset), "1")
			}
		}
		if out, err := exec.CommandContext(ctx, "new"+domain.kind+"map", mapArgs...).CombinedOutput(); err != nil {
			t.Fatalf("install exact %s maps: %v: %s", domain.kind, err, out)
		}
	}
	if _, err := input.Write([]byte{'G'}); err != nil {
		t.Fatal(err)
	}
	// Observe stronger peer threads in the actual production Go process while
	// it waits for admission. They must not survive the later winning exec.
	stronger, seeded := false, mode != "ambient" && mode != "unset-nnp"
	for !stronger && ctx.Err() == nil {
		processStatus, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", command.Process.Pid))
		if strings.Contains(string(processStatus), "State:\tZ") {
			_ = command.Wait()
			t.Fatalf("fixture exited before production readiness: %s", stderr.String())
		}
		entries, _ := os.ReadDir(fmt.Sprintf("/proc/%d/task", command.Process.Pid))
		if len(entries) >= 2 {
			for _, entry := range entries {
				content, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/status", command.Process.Pid, entry.Name()))
				for _, line := range strings.Split(string(content), "\n") {
					if strings.HasPrefix(line, "CapEff:") {
						bits, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
						stronger = stronger || err == nil && bits & ^mask != 0
					}
					if mode == "ambient" && strings.HasPrefix(line, "CapAmb:") {
						bits, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapAmb:")), 16, 64)
						seeded = seeded || err == nil && bits != 0
					}
					if mode == "unset-nnp" && line == "NoNewPrivs:\t0" {
						seeded = true
					}
				}
			}
		}
		if !stronger {
			time.Sleep(time.Millisecond)
		}
	}
	if !stronger || !seeded {
		t.Fatal("missing stronger production peer-thread control")
	}
	_, _ = input.Write(append(frame, []byte("mapped-app-stdin\n")...))
	_ = input.Close()
	stdout, readErr := io.ReadAll(reader)
	waitErr := command.Wait()
	if readErr != nil || command.ProcessState.ExitCode() != expectedExit {
		t.Fatalf("production transition exit=%d want%d, read=%v wait=%v stderr=%s stdout=%s", command.ProcessState.ExitCode(), expectedExit, readErr, waitErr, stderr.String(), stdout)
	}
	if expectedExit != 0 {
		if len(stdout) != 0 {
			t.Fatal("failed transition reached payload", string(stdout))
		}
		return
	}
	var observed struct {
		PID          int                 `json:"pid"`
		Threads      []map[string]string `json:"threads"`
		Securebits   uintptr             `json:"securebits"`
		PrivateLeaks []string            `json:"private_leaks"`
		Stdin        string              `json:"stdin"`
		NativeDrop   map[string]string   `json:"native_uid_drop"`
	}
	if err := json.Unmarshal(stdout, &observed); err != nil {
		t.Fatal(err, string(stdout), stderr.String())
	}
	plan := sandboxExecPlanV1{UID: uid, GID: gid, Groups: groups, MappedCapabilities: &mask}
	for _, thread := range observed.Threads {
		var status strings.Builder
		for key, value := range thread {
			fmt.Fprintf(&status, "%s: %s\n", key, value)
		}
		if err := verifyMappedKernelStatusV1([]byte(status.String()), plan); err != nil {
			t.Fatal("post-exec thread authority", thread, err)
		}
	}
	wantBits := uintptr(2 | 8 | 32 | 64 | 128)
	if mask == 0 {
		wantBits |= 1 | 4
	}
	if len(observed.Threads) == 0 || observed.PID != command.Process.Pid || observed.Securebits != wantBits || len(observed.PrivateLeaks) != 0 || observed.Stdin != "mapped-app-stdin\n" {
		t.Fatal("post-exec authority, descriptor or stdin mismatch", observed)
	}
	if uid == 0 && mask&(1<<7) != 0 {
		if observed.NativeDrop["Uid"] != "200 200 200 200" || observed.NativeDrop["CapPrm"] != "0000000000000000" || observed.NativeDrop["CapEff"] != "0000000000000000" {
			t.Fatal("native UID change retained capability authority", observed.NativeDrop)
		}
	}
}
