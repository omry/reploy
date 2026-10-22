//go:build linux

package probe

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

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

const trustedStartupVerifierExecutableV1 = "/reploy-probe"

func applicationLaunchProofV1(commandLine []byte) error {
	end := bytes.IndexByte(commandLine, 0)
	if end <= 0 {
		return fmt.Errorf("application process command is unavailable")
	}
	if string(commandLine[:end]) == trustedStartupVerifierExecutableV1 {
		return fmt.Errorf("trusted startup verifier has not completed application exec")
	}
	return nil
}

func waitForApplicationLaunchV1() error {
	deadline := time.Now().Add(privateEnvironmentDeadlineV1)
	for {
		// The canonical Compose input explicitly disables init. PID 1 is
		// therefore the trusted verifier until its direct application exec.
		// cmdline remains readable after the verifier drops UID/capabilities;
		// the ptrace-gated /proc/1/exe link does not.
		process, err := os.Open("/proc/1/cmdline")
		if err != nil {
			return fmt.Errorf("read application process command: %w", err)
		}
		commandLine, readErr := io.ReadAll(io.LimitReader(process, 65536))
		closeErr := process.Close()
		if readErr != nil || closeErr != nil {
			return fmt.Errorf("read application process command")
		}
		if err := applicationLaunchProofV1(commandLine); err == nil {
			return nil
		}
		if err := waitPrivateEnvironmentV1(deadline); err != nil {
			return fmt.Errorf("application launch proof exceeded its deadline: %w", err)
		}
	}
}

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
	if plan.PrepareTemporaryHome {
		if err := prepareApplicationTemporaryHomeV1(plan.UID, plan.GID); err != nil {
			return fmt.Errorf("prepare private temporary home: %w", err)
		}
	}
	statusFile := (*os.File)(nil)
	if plan.RecordExitStatus {
		var err error
		statusFile, err = createPortableToolExitStatusFileV1()
		if err != nil {
			return err
		}
		defer statusFile.Close()
	}
	if plan.InstallRules {
		sessionNetwork, err := readApplicationSessionNetworkConfigurationV1(plan.SessionNetworkPrefixes)
		if err != nil {
			return fmt.Errorf("read application session-network prefixes: %w", err)
		}
		sessionPeerCIDRs, sessionLocalCIDRs, err := prepareApplicationSessionPeerV1(
			plan.SessionNetworkPeer, sessionNetwork.Prefixes, sessionNetwork.Peers[plan.SessionNetworkPeer],
		)
		if err != nil {
			return fmt.Errorf("prepare application session-network peer: %w", err)
		}
		if err := installApplicationNetworkPolicyV1(applicationNetworkPolicyV1{
			AllowPublic:       plan.AllowPublic,
			AllowLocal:        plan.AllowLocal,
			AllowAmbiguous:    plan.AllowAmbiguous,
			InboundTCP:        plan.InboundTCP,
			SessionPeerCIDRs:  sessionPeerCIDRs,
			SessionLocalCIDRs: sessionLocalCIDRs,
		}); err != nil {
			return fmt.Errorf("install application network policy: %w", err)
		}
	}
	environment, err := sandboxExecEnvironmentV1(plan.EnvironmentProfile, plan.ContractEnvironment, os.Environ())
	if err != nil {
		return err
	}
	if statusFile != nil {
		// The status supervisor must remain outside the application's signal
		// authority. It starts a separate helper that performs the complete
		// authority drop immediately before directly execing the application.
		return syscall.Exec(
			"/proc/self/exe",
			portableToolObservedExecArgvV1(int(statusFile.Fd()), plan, plan.Argv),
			environment,
		)
	}
	if err := dropApplicationAuthorityV1(plan.UID, plan.GID, plan.Groups); err != nil {
		return err
	}
	if plan.PrivateEnvironment {
		return launchPrivateWorkloadEnvironmentV1(plan.Argv, environment)
	}
	return verifyAndExecApplication(plan.Argv, readApplicationKernelStatus, func(argv []string) error {
		return syscall.Exec(argv[0], argv, environment)
	})
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
