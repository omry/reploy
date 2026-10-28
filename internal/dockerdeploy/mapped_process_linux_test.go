//go:build linux

package dockerdeploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omry/reploy/internal/identitymapping"
	"golang.org/x/sys/unix"
)

// This filesystem fixture exercises the observer's complete comparisons. It is
// not kernel namespace provenance or a production qualification receipt.
func mappedProcessFixtureV1(t *testing.T) (*mappedProcessV1, string) {
	t.Helper()
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fields := append([]string{"S"}, make([]string, 19)...)
	for i := 1; i < len(fields); i++ {
		fields[i] = "0"
	}
	fields[19] = "12345"
	write("stat", "123 (trusted ) waiter) "+strings.Join(fields, " "))
	write("uid_map", fmt.Sprintf("0 %d 1\n200 100200 1\n", os.Getuid()))
	write("gid_map", fmt.Sprintf("0 %d 1\n200 100200 1\n", os.Getgid()))
	write("cmdline", "/reploy-probe\x00mapped-wait-v1\x00")
	for _, name := range []string{"ns", "root"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"user", "pid", "mnt", "net", "ipc"} {
		if err := os.Symlink(name+":[12345]", filepath.Join(root, "ns", name)); err != nil {
			t.Fatal(err)
		}
	}
	write("exe", "synthetic trusted executable")
	if err := os.Link(filepath.Join(root, "exe"), filepath.Join(root, "root/reploy-probe")); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	p := &mappedProcessV1{directory: directory, pidfd: fd, active: identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{{HostStart: uint32(os.Getuid()), Count: 1}, {ContainerStart: 200, HostStart: 100200, Count: 1}}, GIDs: []identitymapping.Interval{{HostStart: uint32(os.Getgid()), Count: 1}, {ContainerStart: 200, HostStart: 100200, Count: 1}}}, argv: []byte("/reploy-probe\x00mapped-wait-v1\x00")}
	t.Cleanup(p.close)
	p.initial, err = p.observe()
	if err != nil {
		t.Fatal("observer fixture", err)
	}
	return p, root
}

func TestMappedProcessChecksEveryObservationV1(t *testing.T) {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		t.Skip("mapped host observations require non-root owner")
	}
	for _, mode := range []string{"unchanged", "start", "missing stat", "exited stat", "map extra", "map missing", "map overflow", "map zero", "map parse error", "missing map", "oversize map", "uid owner", "gid owner", "no zero", "namespace", "ipc namespace", "missing namespace", "missing ipc namespace", "changed ipc namespace", "wrong executable", "missing executable", "command", "missing command", "dead pin", "closed directory"} {
		t.Run(mode, func(t *testing.T) {
			p, root := mappedProcessFixtureV1(t)
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "start":
				data, _ := os.ReadFile(filepath.Join(root, "stat"))
				write("stat", strings.Replace(string(data), "12345", "54321", 1))
			case "missing stat":
				_ = os.Remove(filepath.Join(root, "stat"))
			case "exited stat":
				write("stat", "123 (dead) Z")
			case "map extra":
				write("uid_map", fmt.Sprintf("0 %d 1\n200 100200 1\n300 100300 1\n", os.Getuid()))
			case "map missing":
				write("gid_map", fmt.Sprintf("0 %d 1\n", os.Getgid()))
			case "map overflow":
				write("uid_map", "0 4294967295 2\n")
			case "map zero":
				write("uid_map", "0 100000 0\n")
			case "map parse error":
				write("uid_map", "0 100000\n")
			case "missing map":
				_ = os.Remove(filepath.Join(root, "gid_map"))
			case "oversize map":
				write("uid_map", strings.Repeat("x", (64<<10)+1))
			case "uid owner":
				p.active.UIDs[0].HostStart++
				write("uid_map", fmt.Sprintf("0 %d 1\n200 100200 1\n", p.active.UIDs[0].HostStart))
			case "gid owner":
				p.active.GIDs[0].HostStart++
				write("gid_map", fmt.Sprintf("0 %d 1\n200 100200 1\n", p.active.GIDs[0].HostStart))
			case "no zero":
				p.active.UIDs[0].ContainerStart = 1
				write("uid_map", fmt.Sprintf("1 %d 1\n200 100200 1\n", os.Getuid()))
			case "namespace":
				_ = os.Remove(filepath.Join(root, "ns/net"))
				target, _ := os.Readlink("/proc/self/ns/net")
				_ = os.Symlink(target, filepath.Join(root, "ns/net"))
			case "ipc namespace":
				_ = os.Remove(filepath.Join(root, "ns/ipc"))
				target, _ := os.Readlink("/proc/self/ns/ipc")
				_ = os.Symlink(target, filepath.Join(root, "ns/ipc"))
			case "missing namespace":
				_ = os.Remove(filepath.Join(root, "ns/pid"))
			case "missing ipc namespace":
				_ = os.Remove(filepath.Join(root, "ns/ipc"))
			case "changed ipc namespace":
				_ = os.Remove(filepath.Join(root, "ns/ipc"))
				if err := os.Symlink("ipc:[11111]", filepath.Join(root, "ns/ipc")); err != nil {
					t.Fatal(err)
				}
				initial, err := p.observe()
				if err != nil {
					t.Fatal(err)
				}
				p.initial = initial
				if err := os.Remove(filepath.Join(root, "ns/ipc")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("ipc:[22222]", filepath.Join(root, "ns/ipc")); err != nil {
					t.Fatal(err)
				}
			case "wrong executable":
				_ = os.Remove(filepath.Join(root, "root/reploy-probe"))
				write("root/reploy-probe", "substitution")
			case "missing executable":
				_ = os.Remove(filepath.Join(root, "exe"))
			case "command":
				write("cmdline", "/reploy-probe\x00sandbox-exec\x00")
			case "missing command":
				_ = os.Remove(filepath.Join(root, "cmdline"))
			case "dead pin":
				_ = unix.Close(p.pidfd)
			case "closed directory":
				_ = p.directory.Close()
			}
			err := p.fresh()
			if (err == nil) != (mode == "unchanged") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}

func TestMappedProcessChangesDuringObservationV1(t *testing.T) {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		t.Skip("mapped host observations require non-root owner")
	}
	for _, mode := range []string{"start changed", "process pin lost"} {
		t.Run(mode, func(t *testing.T) {
			p, root := mappedProcessFixtureV1(t)
			name := filepath.Join(root, "cmdline")
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(name, 0600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- p.fresh() }()
			// Opening the FIFO writer synchronizes after the first stat/maps/ns
			// reads and before the final stat/liveness fences.
			writer, err := os.OpenFile(name, os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "start changed" {
				data, err := os.ReadFile(filepath.Join(root, "stat"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "stat"), bytes.Replace(data, []byte("12345"), []byte("54321"), 1), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				_ = unix.Close(p.pidfd)
			}
			_, err = writer.Write(p.argv)
			_ = writer.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err == nil {
				t.Fatal("process changed during observation accepted")
			}
		})
	}
}

func TestMappedKernelParsersRejectIncompleteAndOverflowV1(t *testing.T) {
	for _, s := range []string{"", "0 1", "0 1 1 extra", "-1 100000 1", "0 4294967296 1", "0 1 nope"} {
		if _, err := mappedKernelRowsV1([]byte(s)); err == nil {
			t.Fatalf("map %q accepted", s)
		}
	}
	rows, err := mappedKernelRowsV1([]byte("   0 100000 1\n200 100200 1\n"))
	if err != nil || len(rows) != 2 || rows[1].ContainerStart != 200 {
		t.Fatalf("complete rows: %+v %v", rows, err)
	}
	for _, s := range []string{"", "123 missing", "123 (x) Z", "123 (x) X", "123 (x) S"} {
		if _, err := mappedProcessStartV1([]byte(s)); err == nil {
			t.Fatalf("stat %q accepted", s)
		}
	}
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "S"
	for _, start := range []string{"0", "bad", "18446744073709551616"} {
		fields[19] = start
		if _, err := mappedProcessStartV1([]byte("1 (a b)) " + strings.Join(fields, " "))); err == nil {
			t.Fatalf("start %q accepted", start)
		}
	}
	fields[19] = "18446744073709551615"
	if start, err := mappedProcessStartV1([]byte("1 (a b)) " + strings.Join(fields, " "))); err != nil || start != fields[19] {
		t.Fatalf("start identity: %q %v", start, err)
	}
}

func TestMappedProcessPinRefusesUnqualifiedHostProcessV1(t *testing.T) {
	active := mappedGeometryFixtureV1("exact")
	for _, pid := range []int{0, -1, 2147483647, os.Getpid()} {
		if p, err := pinMappedProcessV1(pid, active, []string{"/reploy-probe"}); err == nil {
			p.close()
			t.Fatalf("unqualified PID %d accepted", pid)
		}
	}
	if _, err := pinMappedProcessV1(os.Getpid(), identitymapping.Geometry{}, nil); err == nil {
		t.Fatal("invalid plan pinned")
	}
	// A real pidfd is polled above; fake /proc fixture identities never enter
	// the production pin constructor or satisfy this host-process negative.
}

func TestMappedKernelOwnedProcessHelperV1(t *testing.T) {
	if os.Getenv("REPLOY_TEST_MAPPED_PROCESS") != "child" {
		return
	}
	var release [1]byte
	if _, err := io.ReadFull(os.Stdin, release[:]); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

// A disposable namespace/chroot fixture checks the real pidfd, proc directory,
// maps, executable and namespace readers. Its self-map is not a product mapping
// or F proof; it neither uses an engine nor runs application code.
func TestMappedKernelOwnedProcessObservationV1(t *testing.T) {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		t.Skip("requires non-root disposable namespace owner")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	copyFile := func(source, target string) {
		t.Helper()
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(self, filepath.Join(root, "reploy-probe"))
	// A cgo-linked test executable needs its existing loader/libraries. Copy
	// only absolute dependencies reported for this exact fixture executable.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, lddErr := exec.CommandContext(ctx, "ldd", self).CombinedOutput()
	if lddErr != nil && !bytes.Contains(output, []byte("not a dynamic executable")) && !bytes.Contains(output, []byte("statically linked")) {
		t.Fatalf("inspect fixture dependencies: %v %s", lddErr, output)
	}
	for _, field := range strings.Fields(string(output)) {
		if filepath.IsAbs(field) {
			copyFile(field, filepath.Join(root, strings.TrimPrefix(field, "/")))
		}
	}
	cmd := exec.CommandContext(ctx, "/reploy-probe", "-test.run=^TestMappedKernelOwnedProcessHelperV1$")
	cmd.Env = append(os.Environ(), "REPLOY_TEST_MAPPED_PROCESS=child")
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: root, Cloneflags: unix.CLONE_NEWUSER | unix.CLONE_NEWNS | unix.CLONE_NEWPID | unix.CLONE_NEWNET | unix.CLONE_NEWIPC, UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}}, GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}}, GidMappingsEnableSetgroups: false, Credential: &syscall.Credential{Uid: 0, Gid: 0}}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EINVAL) {
			t.Skipf("private kernel-observation fixture unavailable; no qualification: %v", err)
		}
		t.Fatalf("start private kernel-observation fixture: %v %s", err, stderr.String())
	}
	reaped := false
	defer func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	active := identitymapping.Geometry{Profile: "exact", UIDs: []identitymapping.Interval{{HostStart: uint32(os.Getuid()), Count: 1}}, GIDs: []identitymapping.Interval{{HostStart: uint32(os.Getgid()), Count: 1}}}
	p, err := pinMappedProcessV1(cmd.Process.Pid, active, cmd.Args)
	if err != nil {
		t.Fatalf("pin real isolated waiter: %v %s", err, stderr.String())
	}
	defer p.close()
	if err := p.fresh(); err != nil {
		t.Fatal("refresh real isolated waiter", err)
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	err = cmd.Wait()
	reaped = true
	if err != nil {
		t.Fatalf("retire fixture: %v %s", err, stderr.String())
	}
	if err := p.fresh(); err == nil {
		t.Fatal("retired process retained liveness")
	}
}
