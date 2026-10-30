//go:build linux

package dockerdeploy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This owned same-UID peer is deliberately outside a product container. Future
// occupied-entry proposals must test proc/ptrace and capability access against
// such an existing process, not only against their newly started helper. The
// current slice qualifies refusal only; it performs no authority transition.
func TestMappedOccupiedPeerV1(t *testing.T) {
	if os.Getenv("REPLOY_TEST_OCCUPIED_PEER") != "child" {
		return
	}
	fmt.Fprintln(os.Stdout, "ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

type mappedOccupiedHarnessV1 struct {
	pid         int
	privatePath string
	descriptors []*os.File
	status      []byte
	procError   error
}

// Retain real private file/directory/socket/pidfd/high descriptors and a live
// same-UID peer. No fixture value is production evidence or a qualification
// flag. A future successful transition needs separately reviewed probes for
// proc/ptrace access and leaks; today's gate must reach none of those effects.
func newMappedOccupiedHarnessV1(t *testing.T) mappedOccupiedHarnessV1 {
	t.Helper()
	h := mappedOccupiedHarnessV1{privatePath: filepath.Join(t.TempDir(), "private")}
	if err := os.WriteFile(h.privatePath, []byte("private-entry-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	retain := func(file *os.File) {
		h.descriptors = append(h.descriptors, file)
		t.Cleanup(func() { _ = file.Close() })
	}
	for _, name := range []string{h.privatePath, filepath.Dir(h.privatePath)} {
		file, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		retain(file)
	}
	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range sockets {
		retain(os.NewFile(uintptr(fd), "private-socket"))
	}
	high, err := unix.FcntlInt(h.descriptors[0].Fd(), unix.F_DUPFD_CLOEXEC, 4096)
	if err != nil {
		t.Fatalf("high descriptor fixture unavailable (no qualification): %v", err)
	}
	retain(os.NewFile(uintptr(high), "private-high-file"))
	// testing cancels t.Context before cleanup runs. Keep the owned peer alive
	// until our cleanup closes its input, while retaining a hard reap deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 15*time.Second)
	t.Cleanup(cancel)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	peer := exec.CommandContext(ctx, self, "-test.run=^TestMappedOccupiedPeerV1$")
	peer.Env = append(os.Environ(), "REPLOY_TEST_OCCUPIED_PEER=child")
	input, err := peer.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := peer.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		if err := peer.Wait(); err != nil {
			t.Errorf("reap owned peer: %v", err)
		}
	})
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("owned peer readiness: %q %v", line, err)
	}
	h.pid = peer.Process.Pid
	pidfd, err := unix.PidfdOpen(h.pid, 0)
	if err != nil {
		t.Fatalf("pidfd fixture unavailable (no qualification): %v", err)
	}
	retain(os.NewFile(uintptr(pidfd), "private-peer-pidfd"))
	h.status, err = os.ReadFile(fmt.Sprintf("/proc/%d/status", h.pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Uid:", "Gid:", "CapPrm:", "CapEff:", "CapBnd:", "CapInh:", "CapAmb:", "TracerPid:"} {
		if !strings.Contains(string(h.status), field) {
			t.Fatalf("peer status lacks %s", field)
		}
	}
	uidLine := fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d", os.Getuid(), os.Geteuid(), os.Geteuid(), os.Geteuid())
	if !strings.Contains(string(h.status), uidLine) {
		t.Fatal("fixture peer does not share current UID")
	}
	mem, err := os.Open(fmt.Sprintf("/proc/%d/mem", h.pid))
	h.procError = err
	if err == nil {
		retain(mem)
	}
	return h
}

func TestMappedOccupiedRefusalWithLiveAdversarialSurfacesV1(t *testing.T) {
	h := newMappedOccupiedHarnessV1(t)
	t.Logf("owned same-UID peer=%d, proc-mem open=%v; no ptrace or exec qualification claimed", h.pid, h.procError)
	for _, kind := range []string{"declaration", "unresolved", "policy", "retained"} {
		state := mappedEntryStateFixtureV1(t, kind)
		for _, mode := range []mappedEntryModeV1{mappedEntryOccupiedExecV1, mappedEntryHostNativeV1} {
			_, err := runtimeDocumentForEntryModeV1(state, mode)
			if !errors.Is(err, errMappedOccupiedEntryV1) && !errors.Is(err, errMappedHostNativeEntryV1) {
				t.Fatal("adversarial occupied entry did not refuse", err)
			}
		}
		// Exercise a real public entry, with all runtime callbacks instrumented.
		// Its broad public guard must still stop before build, helpers, engine
		// create/start/attach or a fresh-container fallback can be selected.
		current, _ := runtimeCurrentBuildFixture(t)
		current.State = state
		dir := t.TempDir()
		var order []string
		err := runCurrentShellV1(t.Context(), CurrentShellRunInputV1{DeploymentDir: dir}, currentShellRunTestBackend(t, dir, current, CurrentRuntimePlanV1{}, &order))
		if err == nil || !reflect.DeepEqual(order, []string{"acquire", "store", "state"}) {
			t.Fatalf("public mapped shell reached effects: %v %v", order, err)
		}
	}
	data, err := os.ReadFile(h.privatePath)
	if err != nil || string(data) != "private-entry-canary" {
		t.Fatal("private fixture changed during refusal", err)
	}
	for _, file := range h.descriptors {
		if _, err := file.Stat(); err != nil {
			t.Fatalf("private descriptor damaged during refusal: %s: %v", file.Name(), err)
		}
	}
}
