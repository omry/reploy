//go:build linux

package probe

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	secureNoRootV1               = 1 << 0
	secureNoRootLockedV1         = 1 << 1
	secureNoSetUIDFixupV1        = 1 << 2
	secureNoSetUIDFixupLockedV1  = 1 << 3
	secureKeepCapsLockedV1       = 1 << 5
	secureNoAmbientRaiseV1       = 1 << 6
	secureNoAmbientRaiseLockedV1 = 1 << 7
)

func sandboxAndExecApplicationV1(plan sandboxExecPlanV1) error {
	// Linux credentials, securebits, and capability bounding sets are
	// thread-scoped. Keep the trusted transition and the final exec on exactly
	// one OS thread so the Go scheduler cannot move verification onto a thread
	// that still carries the container's setup authority.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Geteuid() != 0 {
		return fmt.Errorf("trusted sandbox setup must begin as container root")
	}
	if plan.InstallRules {
		if err := installApplicationNetworkPolicyV1(applicationNetworkPolicyV1{
			AllowPublic:    plan.AllowPublic,
			AllowLocal:     plan.AllowLocal,
			AllowAmbiguous: plan.AllowAmbiguous,
			InboundTCP:     plan.InboundTCP,
		}); err != nil {
			return fmt.Errorf("install application network policy: %w", err)
		}
	}
	if err := dropApplicationAuthorityV1(plan.UID, plan.GID, plan.Groups); err != nil {
		return err
	}
	return verifyAndExecApplication(plan.Argv, readApplicationKernelStatus, execApplication)
}

func dropApplicationAuthorityV1(uid uint32, gid uint32, groups []uint32) error {
	lastContent, err := os.ReadFile("/proc/sys/kernel/cap_last_cap")
	if err != nil {
		return fmt.Errorf("read last Linux capability: %w", err)
	}
	lastCapability, err := strconv.Atoi(strings.TrimSpace(string(lastContent)))
	if err != nil {
		return fmt.Errorf("parse last Linux capability: %w", err)
	}
	for capability := 0; capability <= lastCapability; capability++ {
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, uintptr(capability), 0, 0, 0); err != nil {
			return fmt.Errorf("drop capability %d from bounding set: %w", capability, err)
		}
	}
	secureBits := uintptr(
		secureNoRootV1 | secureNoRootLockedV1 |
			secureNoSetUIDFixupV1 | secureNoSetUIDFixupLockedV1 |
			secureKeepCapsLockedV1 |
			secureNoAmbientRaiseV1 | secureNoAmbientRaiseLockedV1,
	)
	if err := unix.Prctl(unix.PR_SET_SECUREBITS, secureBits, 0, 0, 0); err != nil {
		return fmt.Errorf("lock application securebits: %w", err)
	}
	// The x/sys credential wrappers use int even though Linux uid_t/gid_t are
	// unsigned 32-bit values. Conversion here preserves the exact low 32 bits
	// on 32-bit targets; parsing and validation remain architecture-neutral.
	nativeGroups := make([]int, len(groups))
	for index, group := range groups {
		nativeGroups[index] = int(group)
	}
	if err := unix.Setgroups(nativeGroups); err != nil {
		return fmt.Errorf("set application supplementary groups: %w", err)
	}
	if err := unix.Setresgid(int(gid), int(gid), int(gid)); err != nil {
		return fmt.Errorf("set application GID %d: %w", gid, err)
	}
	if err := unix.Setresuid(int(uid), int(uid), int(uid)); err != nil {
		return fmt.Errorf("set application UID %d: %w", uid, err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set application no-new-privileges: %w", err)
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3, Pid: 0}
	data := [2]unix.CapUserData{}
	if err := unix.Capset(&header, &data[0]); err != nil {
		return fmt.Errorf("clear application capabilities: %w", err)
	}
	return nil
}
