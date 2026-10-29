//go:build linux && (amd64 || arm64)

package probe

import (
	"fmt"
	"math"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func validateMappedStartupV1(plan sandboxExecPlanV1) error {
	return validateMappedStartupWithV1(plan, os.ReadFile)
}

func validateMappedStartupWithV1(plan sandboxExecPlanV1, readFile func(string) ([]byte, error)) error {
	if plan.MappedCapabilities == nil || plan.UID == math.MaxUint32 || plan.GID == math.MaxUint32 ||
		plan.UID != 0 && *plan.MappedCapabilities != 0 || plan.PrivateEnvironment || plan.RecordExitStatus {
		return fmt.Errorf("mapped credential tuple is not qualified")
	}
	for i, group := range plan.Groups {
		if group == math.MaxUint32 || i > 0 && plan.Groups[i-1] >= group {
			return fmt.Errorf("mapped groups must be exact, sorted and unique")
		}
	}
	if len(plan.Argv) == 0 || !path.IsAbs(plan.Argv[0]) || path.Clean(plan.Argv[0]) != plan.Argv[0] {
		return fmt.Errorf("mapped executable must be a normalized absolute path")
	}
	content, err := readFile("/proc/sys/kernel/cap_last_cap")
	if err != nil {
		return fmt.Errorf("read mapped capability limit: %w", err)
	}
	last, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || last < 0 || last >= 64 || last < 63 && *plan.MappedCapabilities>>(last+1) != 0 {
		return fmt.Errorf("mapped capability ceiling is outside the kernel range")
	}
	for fd := 0; fd < 3; fd++ {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC != 0 {
			return fmt.Errorf("mapped standard descriptor %d is unavailable at exec", fd)
		}
	}
	return nil
}

func execMappedApplicationV1(plan sandboxExecPlanV1, environment []string) error {
	// Prepare every pointer while the shared Go descriptor table is intact.
	filename, err := syscall.BytePtrFromString(plan.Argv[0])
	if err != nil {
		return err
	}
	argv, err := syscall.SlicePtrFromStrings(plan.Argv)
	if err != nil {
		return err
	}
	envp, err := syscall.SlicePtrFromStrings(environment)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if unix.Gettid() == os.Getpid() {
		// Keep the leader pinned and trusted while a different locked thread
		// performs the transition. Exec must destroy this stronger peer too.
		result := make(chan error, 1)
		go func() { result <- execMappedApplicationV1(plan, environment) }()
		return <-result
	}
	mask := *plan.MappedCapabilities
	// Kernel support was checked before sandbox preparation. Dropping all
	// unwanted bits, including future ones, cannot grant authority.
	for capability := 0; capability < 64; capability++ {
		if mask&(uint64(1)<<capability) == 0 {
			_, _, errno := syscall.RawSyscall6(unix.SYS_PRCTL, unix.PR_CAPBSET_DROP, uintptr(capability), 0, 0, 0, 0)
			if errno != 0 && errno != unix.EINVAL {
				mappedTransitionFailureV1(121)
			}
		}
	}
	bits := uintptr(secureNoRootLockedV1 | secureNoSetUIDFixupLockedV1 |
		secureKeepCapsLockedV1 | secureNoAmbientRaiseV1 | secureNoAmbientRaiseLockedV1)
	if mask == 0 {
		bits |= secureNoRootV1 | secureNoSetUIDFixupV1
	}
	// Nonempty root roles retain Linux's native UID-change capability fixup.
	// Locking that fixup off would let native workers retain the master's caps.
	mappedRawCallV1(unix.SYS_PRCTL, unix.PR_SET_SECUREBITS, bits, 0)
	mappedRawCallV1(unix.SYS_PRCTL, unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0)
	mappedRawCallV1(unix.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0)
	var groups unsafe.Pointer
	if len(plan.Groups) > 0 {
		groups = unsafe.Pointer(&plan.Groups[0])
	}
	mappedRawCallV1(unix.SYS_SETGROUPS, uintptr(len(plan.Groups)), uintptr(groups), 0)
	mappedRawCallV1(unix.SYS_SETRESGID, uintptr(plan.GID), uintptr(plan.GID), uintptr(plan.GID))
	mappedRawCallV1(unix.SYS_SETRESUID, uintptr(plan.UID), uintptr(plan.UID), uintptr(plan.UID))
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	data := [2]unix.CapUserData{
		{Effective: uint32(mask), Permitted: uint32(mask)},
		{Effective: uint32(mask >> 32), Permitted: uint32(mask >> 32)},
	}
	mappedRawCallV1(unix.SYS_CAPSET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0)
	actualBits, _, errno := syscall.RawSyscall6(unix.SYS_PRCTL, unix.PR_GET_SECUREBITS, 0, 0, 0, 0, 0)
	if errno != 0 || actualBits != bits {
		mappedTransitionFailureV1(122)
	}
	content, err := readApplicationKernelStatus()
	if err != nil || verifyMappedKernelStatusV1(content, plan) != nil {
		mappedTransitionFailureV1(122)
	}
	mappedExecTailV1(filename, unsafe.Pointer(&argv[0]), unsafe.Pointer(&envp[0]))
	runtime.KeepAlive(argv)
	runtime.KeepAlive(envp)
	runtime.KeepAlive(plan.Groups)
	return fmt.Errorf("mapped exec tail returned unexpectedly")
}

// Pointer conversions must stay in this call's argument list. The directive
// keeps their objects on the heap and alive across any wrapper stack growth.
//
//go:uintptrescapes
func mappedRawCallV1(number, a, b, c uintptr) {
	if _, _, errno := syscall.RawSyscall6(number, a, b, c, 0, 0, 0); errno != 0 {
		mappedTransitionFailureV1(121)
	}
}

//go:nosplit
//go:norace
func mappedTransitionFailureV1(code uintptr) {
	syscall.RawSyscall(unix.SYS_EXIT_GROUP, code, 0, 0)
	for {
	}
}

// No Go work, argument conversion, logging or returning error path is allowed
// after detaching and closing the descriptor table. Constants come from the
// target architecture's UAPI, never another architecture's fixture numbers.
//
//go:nosplit
//go:norace
func mappedExecTailV1(filename *byte, argv, envp unsafe.Pointer) {
	_, _, errno := syscall.RawSyscall6(unix.SYS_CLOSE_RANGE, 3, uintptr(^uint32(0)), unix.CLOSE_RANGE_UNSHARE, 0, 0, 0)
	if errno == 0 {
		syscall.RawSyscall(unix.SYS_EXECVE, uintptr(unsafe.Pointer(filename)), uintptr(argv), uintptr(envp))
	}
	mappedTransitionFailureV1(125)
}
