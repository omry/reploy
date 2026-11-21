//go:build linux && (amd64 || arm64)

package probe

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/omry/reploy/internal/mappedlaunch"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

func TestMappedEscapeFilterPolicyV1(t *testing.T) {
	code := mappedEscapeFilterV1()
	raw := make([]bpf.RawInstruction, len(code))
	for i, instruction := range code {
		raw[i] = bpf.RawInstruction{Op: instruction.Code, Jt: instruction.Jt, Jf: instruction.Jf, K: instruction.K}
	}
	decoded, complete := bpf.Disassemble(raw)
	if !complete {
		t.Fatal("fixed policy contains an undecodable instruction")
	}
	vm, err := bpf.NewVM(decoded)
	if err != nil {
		t.Fatal(err)
	}
	arch := uint32(unix.AUDIT_ARCH_X86_64)
	if runtime.GOARCH == "arm64" {
		arch = unix.AUDIT_ARCH_AARCH64
	}
	evaluate := func(number, architecture, flags uint32) uint32 {
		t.Helper()
		data := make([]byte, 64)
		// The packet VM uses network order for ABS loads. Encode the same
		// numeric fields in that order here; native installation is checked in
		// the subprocess test and the separate qualified-engine fixture.
		binary.BigEndian.PutUint32(data, number)
		binary.BigEndian.PutUint32(data[4:], architecture)
		binary.BigEndian.PutUint32(data[16:], flags)
		result, err := vm.Run(data)
		if err != nil {
			t.Fatal(err)
		}
		return uint32(result)
	}
	deny := uint32(unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM))
	for _, number := range []uint32{unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT,
		unix.SYS_FSOPEN, unix.SYS_FSCONFIG, unix.SYS_FSMOUNT, unix.SYS_FSPICK,
		unix.SYS_OPEN_TREE, unix.SYS_MOVE_MOUNT, unix.SYS_MOUNT_SETATTR,
		unix.SYS_SETNS, unix.SYS_UNSHARE, unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_NAME_TO_HANDLE_AT} {
		if got := evaluate(number, arch, 0); got != deny {
			t.Fatalf("escape syscall %d returned %x", number, got)
		}
	}
	for bit := range 32 {
		flag := uint32(1) << bit
		want := uint32(unix.SECCOMP_RET_ALLOW)
		if flag&mappedlaunch.CloneNamespaceMask != 0 {
			want = deny
		}
		if got := evaluate(unix.SYS_CLONE, arch, flag); got != want {
			t.Fatalf("clone flag %x returned %x, want %x", flag, got, want)
		}
	}
	for _, flags := range []uint32{0, uint32(unix.SIGCHLD), unix.CLONE_VM | unix.CLONE_FS | unix.CLONE_FILES | unix.CLONE_SIGHAND | unix.CLONE_THREAD | unix.CLONE_SYSVSEM} {
		if got := evaluate(unix.SYS_CLONE, arch, flags); got != unix.SECCOMP_RET_ALLOW {
			t.Fatalf("ordinary clone flags %x were blocked", flags)
		}
	}
	if evaluate(unix.SYS_CLONE3, arch, 0) != unix.SECCOMP_RET_ERRNO|uint32(unix.ENOSYS) ||
		evaluate(unix.SYS_READ, arch, 0) != unix.SECCOMP_RET_ALLOW ||
		evaluate(unix.SYS_READ, arch^1, 0) != unix.SECCOMP_RET_KILL_PROCESS {
		t.Fatal("clone3, ordinary syscall, or foreign ABI boundary changed")
	}
	if runtime.GOARCH == "amd64" && evaluate(unix.SYS_READ|0x40000000, arch, 0) != deny {
		t.Fatal("x32 ABI bypassed the native boundary")
	}
}

func TestMappedEscapeFilterNativeV1(t *testing.T) {
	if os.Getenv("REPLOY_F312_FILTER_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestMappedEscapeFilterNativeV1$", "-test.v")
		command.Env = append(os.Environ(), "REPLOY_F312_FILTER_CHILD=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("disposable fixed-filter process: %v\n%s", err, output)
		}
		if !strings.Contains(string(output), "all actual threads have the additional filter") {
			t.Fatalf("missing native observation: %s", output)
		}
		return
	}
	count := func(data []byte) int {
		for _, line := range strings.Split(string(data), "\n") {
			if value, found := strings.CutPrefix(line, "Seccomp_filters:"); found {
				n, _ := strconv.Atoi(strings.TrimSpace(value))
				return n
			}
		}
		return -1
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	before, err := os.ReadFile("/proc/self/status")
	if err != nil || count(before) < 0 {
		t.Fatalf("missing actual filter count: %v", err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if count(before) == 0 {
		if err := installMappedEscapeFilterV1(); err == nil {
			t.Fatal("supplementary filter substituted for a missing engine filter")
		}
		// The unit process has no engine. Seed an inert fixture filter only
		// to test the extra installation; this is not engine qualification.
		code := []unix.SockFilter{{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW}}
		program := unix.SockFprog{Len: 1, Filter: &code[0]}
		result, _, errno := syscall.RawSyscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&program)))
		if errno != 0 || result != 0 {
			t.Fatalf("install unit baseline: %d %v", result, errno)
		}
		before, err = os.ReadFile("/proc/self/status")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := installMappedEscapeFilterV1(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join("/proc/self/task", entry.Name(), "status"))
		if err != nil || count(data) != count(before)+1 {
			t.Fatalf("actual thread %s lacks the synchronized filter: %v\n%s", entry.Name(), err, data)
		}
	}
	if _, _, errno := syscall.RawSyscall(unix.SYS_UNSHARE, 0, 0, 0); errno != unix.EPERM {
		t.Fatalf("actual unshare refusal: %v", errno)
	}
	if _, _, errno := syscall.RawSyscall(unix.SYS_CLONE3, 0, 0, 0); errno != unix.ENOSYS {
		t.Fatalf("actual clone3 fallback: %v", errno)
	}
	if unix.Getpid() <= 0 {
		t.Fatal("ordinary syscalls no longer work")
	}
	// A structurally invalid filter must fail, rather than becoming an
	// installation success. This is a disposable process, never the test host.
	bad := unix.SockFprog{}
	result, _, errno := syscall.RawSyscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&bad)))
	if errno == 0 || result == 0 {
		t.Fatalf("invalid installation returned success: %d %v", result, errno)
	}
	fmt.Println("all actual threads have the additional filter")
}

func TestMappedEngineFilterRequiredV1(t *testing.T) {
	if err := verifyMappedEngineFilterV1([]byte("Seccomp:\t2\nSeccomp_filters:\t1\n")); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{
		"Seccomp: 0\nSeccomp_filters: 0\n", "Seccomp: 2\nSeccomp_filters: 0\n",
		"Seccomp: 2\nSeccomp_filters: 2\n", "Seccomp: 2\n", "Seccomp_filters: 1\n",
		"Seccomp: 2\nSeccomp: 2\nSeccomp_filters: 1\n", "Seccomp: 2\nSeccomp_filters: 01\n",
	} {
		if err := verifyMappedEngineFilterV1([]byte(status)); err == nil {
			t.Fatalf("unverified engine baseline accepted: %q", status)
		}
	}
}
