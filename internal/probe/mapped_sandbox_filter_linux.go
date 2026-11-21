//go:build linux && (amd64 || arm64)

package probe

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/omry/reploy/internal/mappedlaunch"
	"golang.org/x/sys/unix"
)

// This fixed boundary is installed only by the generation-verified startup,
// before its release wait. TSYNC covers existing Go threads; future threads
// inherit it. The qualified engine's capability-conditioned default-deny
// policy remains required. Neither filter counts nor an engine pathname alone
// identify that combined policy.
func mappedEscapeFilterV1() []unix.SockFilter {
	architecture := uint32(unix.AUDIT_ARCH_X86_64)
	if runtime.GOARCH == "arm64" {
		architecture = unix.AUDIT_ARCH_AARCH64
	}
	load := uint16(unix.BPF_LD | unix.BPF_W | unix.BPF_ABS)
	equal := uint16(unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K)
	ret := uint16(unix.BPF_RET | unix.BPF_K)
	deny := uint32(unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM))
	program := []unix.SockFilter{
		{Code: load, K: 4}, // seccomp_data.arch
		{Code: equal, K: architecture, Jt: 1},
		{Code: ret, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: load}, // seccomp_data.nr
	}
	if runtime.GOARCH == "amd64" {
		program = append(program, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, K: 0x40000000, Jf: 1}, unix.SockFilter{Code: ret, K: deny})
	}
	program = append(program, unix.SockFilter{Code: equal, K: unix.SYS_CLONE3, Jf: 1}, unix.SockFilter{Code: ret, K: unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)})
	for _, number := range []uint32{unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT,
		unix.SYS_FSOPEN, unix.SYS_FSCONFIG, unix.SYS_FSMOUNT, unix.SYS_FSPICK,
		unix.SYS_OPEN_TREE, unix.SYS_MOVE_MOUNT, unix.SYS_MOUNT_SETATTR,
		unix.SYS_SETNS, unix.SYS_UNSHARE, unix.SYS_OPEN_BY_HANDLE_AT, unix.SYS_NAME_TO_HANDLE_AT} {
		program = append(program, unix.SockFilter{Code: equal, K: number, Jf: 1}, unix.SockFilter{Code: ret, K: deny})
	}
	return append(program,
		unix.SockFilter{Code: equal, K: unix.SYS_CLONE, Jf: 3},
		unix.SockFilter{Code: load, K: 16}, // native clone flags, argument zero
		unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, K: mappedlaunch.CloneNamespaceMask, Jf: 1},
		unix.SockFilter{Code: ret, K: deny},
		unix.SockFilter{Code: ret, K: unix.SECCOMP_RET_ALLOW})
}

func installMappedEscapeFilterV1() error {
	status, err := readApplicationKernelStatus()
	if err != nil {
		return err
	}
	if err := verifyMappedEngineFilterV1(status); err != nil {
		return err
	}
	nnp, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || nnp != 1 {
		return fmt.Errorf("mapped filter requires effective no-new-privileges: %v", err)
	}
	code := mappedEscapeFilterV1()
	program := unix.SockFprog{Len: uint16(len(code)), Filter: &code[0]}
	result, _, errno := syscall.RawSyscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&program)))
	runtime.KeepAlive(code)
	// With TSYNC, a positive result names an unsynchronized thread. It is not
	// success, even when errno is zero.
	if errno != 0 || result != 0 {
		return fmt.Errorf("mapped filter did not synchronize all threads: result=%d errno=%v", result, errno)
	}
	return nil
}

func verifyMappedEngineFilterV1(status []byte) error {
	wanted := map[string]string{"Seccomp": "2", "Seccomp_filters": "1"}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(status), "\n") {
		key, value, found := strings.Cut(line, ":")
		if expected, required := wanted[key]; found && required {
			if seen[key] || strings.TrimSpace(value) != expected {
				return fmt.Errorf("mapped startup requires the qualified engine's existing single filter")
			}
			seen[key] = true
		}
	}
	if len(seen) != len(wanted) {
		return fmt.Errorf("mapped startup lacks complete engine filter observations")
	}
	return nil
}
