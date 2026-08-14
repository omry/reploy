//go:build linux

// Command probe is a disposable Linux identity-boundary test fixture. It is
// intentionally not linked into Reploy and exposes no product API.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	supervisorUID   = 100
	supervisorGID   = 100
	declaredUID     = 1001
	declaredGID     = 2001
	declaredGroup1  = 3001
	declaredGroup2  = 3002
	undeclaredUID   = 1999
	undeclaredGID   = 2999
	undeclaredGroup = 3999
	runtimeGID      = 5
	outsideID       = 70000

	socketPath  = "/run/reploy-multiidentity-probe.sock"
	servicePort = 33441

	canaryFDsEnv          = "REPLOY_PROBE_CANARY_FDS"
	closeRangeVerifiedEnv = "REPLOY_PROBE_CLOSE_RANGE_VERIFIED"
)

var supervisorMemory byte = 0x5a

type result struct {
	Test     string         `json:"test"`
	Pass     bool           `json:"pass"`
	Expected string         `json:"expected,omitempty"`
	Actual   string         `json:"actual,omitempty"`
	Error    string         `json:"error,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

type request struct {
	Action    string `json:"action"`
	Value     int    `json:"value,omitempty"`
	Token     string `json:"token,omitempty"`
	TargetPID int    `json:"target_pid,omitempty"`
}

type response struct {
	Pass     bool            `json:"pass"`
	Rejected bool            `json:"rejected,omitempty"`
	Error    string          `json:"error,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

func main() {
	if len(os.Args) == 1 || os.Args[1] == "supervisor" {
		must(runSupervisor())
		return
	}
	var err error
	switch os.Args[1] {
	case "request":
		err = runRequest(os.Args[2:])
	case "child-launch":
		err = runChildLaunch(os.Args[2:])
	case "app-report":
		err = runAppReport(os.Args[2:])
	case "privilege-target":
		err = runPrivilegeTarget()
	case "raw-matrix":
		err = runRawMatrix(os.Args[2:])
	case "raw-attempt":
		err = runRawAttempt(os.Args[2:])
	case "cap-report":
		err = emitCapabilityReport("no_capability_default")
	case "seccomp-control":
		err = runSeccompControl()
	case "service-server":
		err = runServiceServer(os.Args[2:])
	case "cross-probe":
		err = runCrossProbe(os.Args[2:])
	case "write-private":
		err = writePrivate(os.Args[2:])
	case "stamp-filecap":
		err = stampFileCapability(os.Args[2:])
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	must(err)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func emit(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func statusFields(pid int) (map[string]string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{
		"Uid": true, "Gid": true, "Groups": true,
		"CapInh": true, "CapPrm": true, "CapEff": true,
		"CapBnd": true, "CapAmb": true, "NoNewPrivs": true,
		"Seccomp": true,
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && wanted[key] {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out, nil
}

func capMask(caps ...int) uint32 {
	var mask uint32
	for _, cap := range caps {
		if cap < 32 {
			mask |= 1 << uint(cap)
		}
	}
	return mask
}

func setSupervisorCapabilities() error {
	data := [2]unix.CapUserData{{
		Effective:   capMask(unix.CAP_SETGID, unix.CAP_SETUID, unix.CAP_SETPCAP),
		Permitted:   capMask(unix.CAP_SETGID, unix.CAP_SETUID, unix.CAP_SETPCAP),
		Inheritable: capMask(unix.CAP_SETGID, unix.CAP_SETUID, unix.CAP_SETPCAP),
	}}
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	return unix.Capset(&hdr, &data[0])
}

func becomeSupervisor() error {
	if err := unix.Prctl(unix.PR_SET_KEEPCAPS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("keep supervisor capabilities: %w", err)
	}
	if err := syscall.Setgroups([]int{supervisorGID}); err != nil {
		return fmt.Errorf("set supervisor groups: %w", err)
	}
	if err := syscall.Setresgid(supervisorGID, supervisorGID, supervisorGID); err != nil {
		return fmt.Errorf("set supervisor gids: %w", err)
	}
	if err := syscall.Setresuid(supervisorUID, supervisorUID, supervisorUID); err != nil {
		return fmt.Errorf("set supervisor uids: %w", err)
	}
	if err := setSupervisorCapabilities(); err != nil {
		return fmt.Errorf("set supervisor capabilities: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("make supervisor non-dumpable: %w", err)
	}
	return nil
}

type canaries struct {
	files []*os.File
	fds   []int
}

func openCanaries() (*canaries, error) {
	c := &canaries{}
	add := func(fd int, name string) error {
		if fd < 0 {
			return fmt.Errorf("open %s canary", name)
		}
		_, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0)
		if err != nil {
			unix.Close(fd)
			return fmt.Errorf("clear close-on-exec for %s canary: %w", name, err)
		}
		c.fds = append(c.fds, fd)
		return nil
	}
	f, err := os.OpenFile("/probe-state/supervisor-private", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	c.files = append(c.files, f)
	if err := add(int(f.Fd()), "private file"); err != nil {
		return nil, err
	}
	sp, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	for _, fd := range sp {
		if err := add(fd, "socket"); err != nil {
			return nil, err
		}
	}
	var pipe [2]int
	if err := unix.Pipe(pipe[:]); err != nil {
		return nil, err
	}
	for _, fd := range pipe {
		if err := add(fd, "control pipe"); err != nil {
			return nil, err
		}
	}
	pidfd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return nil, err
	}
	if err := add(pidfd, "pidfd"); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *canaries) close() {
	seen := map[int]bool{}
	for _, f := range c.files {
		seen[int(f.Fd())] = true
		_ = f.Close()
	}
	for _, fd := range c.fds {
		if !seen[fd] {
			_ = unix.Close(fd)
		}
	}
}

func runSupervisor() error {
	// Linux capabilities are per-thread. Keep all privileged child-launch
	// operations on the one thread whose narrowly allowlisted capability set is
	// established below; request handling is intentionally serial.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := becomeSupervisor(); err != nil {
		return err
	}
	canaries, err := openCanaries()
	if err != nil {
		return fmt.Errorf("open supervisor canaries: %w", err)
	}
	defer canaries.close()
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on control socket: %w", err)
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return err
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		func() {
			defer conn.Close()
			var req request
			if err := json.NewDecoder(io.LimitReader(conn, 64<<10)).Decode(&req); err != nil {
				_ = json.NewEncoder(conn).Encode(response{Error: err.Error()})
				return
			}
			resp := handleRequest(req, canaries)
			_ = json.NewEncoder(conn).Encode(resp)
		}()
	}
}

func rawResponse(v any) response {
	b, err := json.Marshal(v)
	if err != nil {
		return response{Error: err.Error()}
	}
	return response{Pass: true, Data: b}
}

func handleRequest(req request, canaries *canaries) response {
	switch req.Action {
	case "ready":
		return rawResponse(map[string]any{"ready": true, "pid": os.Getpid()})
	case "supervisor-report":
		status, err := statusFields(os.Getpid())
		if err != nil {
			return response{Error: err.Error()}
		}
		dumpable, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
		if err != nil {
			return response{Error: err.Error()}
		}
		sid, err := unix.Getsid(0)
		if err != nil {
			return response{Error: err.Error()}
		}
		return rawResponse(map[string]any{
			"status": status, "dumpable": dumpable, "sid": sid,
			"canary_fds":     canaries.fds,
			"memory_address": fmt.Sprintf("%x", uintptr(unsafe.Pointer(&supervisorMemory))),
		})
	case "app-report":
		return launchCaptured(canaries, "/probe", "app-report", strconv.Itoa(os.Getpid()),
			fmt.Sprintf("%x", uintptr(unsafe.Pointer(&supervisorMemory))))
	case "write-private":
		return launchCaptured(canaries, "/probe", "write-private", "/probe-state/app-private")
	case "setid-target":
		return launchCaptured(canaries, "/probe-setid", "privilege-target")
	case "filecap-target":
		return launchFileCapabilityTarget(canaries)
	case "start-services":
		if req.Token == "" {
			return response{Error: "missing service token"}
		}
		cmd, err := launchCommand(canaries, "/probe", "service-server", req.Token)
		if err != nil {
			return response{Error: err.Error()}
		}
		defer closeExtraFiles(cmd)
		logFile, err := os.OpenFile("/probe-state/service.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return response{Error: err.Error()}
		}
		cmd.Stdout, cmd.Stderr = logFile, logFile
		if err := cmd.Start(); err != nil {
			logFile.Close()
			return response{Error: err.Error()}
		}
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
			_ = logFile.Close()
		}()
		for i := 0; i < 250; i++ {
			readyData, err := os.ReadFile("/probe-state/service-ready")
			if err == nil {
				var ready map[string]any
				if err := json.Unmarshal(readyData, &ready); err != nil {
					return response{Error: fmt.Sprintf("decode service readiness: %v", err)}
				}
				ready["pid"] = cmd.Process.Pid
				return rawResponse(ready)
			}
			select {
			case err := <-done:
				logData, _ := os.ReadFile("/probe-state/service.log")
				return response{Error: fmt.Sprintf("service child exited before readiness: %v: %s", err, strings.TrimSpace(string(logData)))}
			default:
			}
			time.Sleep(20 * time.Millisecond)
		}
		logData, _ := os.ReadFile("/probe-state/service.log")
		return response{Error: fmt.Sprintf("service child did not become ready: %s", strings.TrimSpace(string(logData)))}
	case "cross-probe":
		return launchCaptured(canaries, "/probe", "cross-probe", req.Token, strconv.Itoa(req.TargetPID))
	case "reject-uid":
		return policyRejection(req.Value, map[int]bool{declaredUID: true}, "uid")
	case "reject-gid":
		return policyRejection(req.Value, map[int]bool{declaredGID: true}, "gid")
	case "reject-group":
		return policyRejection(req.Value, map[int]bool{declaredGroup1: true, declaredGroup2: true}, "group")
	default:
		return response{Error: fmt.Sprintf("unknown action %q", req.Action)}
	}
}

func policyRejection(value int, accepted map[int]bool, kind string) response {
	if accepted[value] {
		return response{Error: fmt.Sprintf("test requested accepted %s %d", kind, value)}
	}
	b, _ := json.Marshal(result{Test: "supervisor_reject_" + kind, Pass: true,
		Expected: "rejected", Actual: "rejected", Details: map[string]any{"value": value}})
	return response{Pass: true, Rejected: true, Data: b}
}

func launchCommand(canaries *canaries, path string, args ...string) (*exec.Cmd, error) {
	cmdArgs := append([]string{"child-launch", path}, args...)
	cmd := exec.Command("/probe", cmdArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		AmbientCaps: []uintptr{unix.CAP_SETGID, unix.CAP_SETUID, unix.CAP_SETPCAP},
	}
	for index, fd := range canaries.fds {
		dup, err := unix.Dup(fd)
		if err != nil {
			closeExtraFiles(cmd)
			return nil, fmt.Errorf("duplicate canary fd %d: %w", fd, err)
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, os.NewFile(uintptr(dup), fmt.Sprintf("canary-%d", index)))
	}
	childFDs := make([]string, len(cmd.ExtraFiles))
	for index := range cmd.ExtraFiles {
		childFDs[index] = strconv.Itoa(3 + index)
	}
	cmd.Env = append(os.Environ(), canaryFDsEnv+"="+strings.Join(childFDs, ","))
	return cmd, nil
}

func closeExtraFiles(cmd *exec.Cmd) {
	for _, file := range cmd.ExtraFiles {
		_ = file.Close()
	}
	cmd.ExtraFiles = nil
}

func launchCaptured(canaries *canaries, path string, args ...string) response {
	cmd, err := launchCommand(canaries, path, args...)
	if err != nil {
		return response{Error: err.Error()}
	}
	defer closeExtraFiles(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if err != nil {
		return response{Error: fmt.Sprintf("child failed: %v: %s", err, strings.TrimSpace(stderr.String()))}
	}
	data := bytes.TrimSpace(stdout.Bytes())
	if !json.Valid(data) {
		return response{Error: fmt.Sprintf("child returned invalid JSON: %q", data)}
	}
	return response{Pass: true, Data: append(json.RawMessage(nil), data...)}
}

func launchFileCapabilityTarget(canaries *canaries) response {
	xattr := make([]byte, 64)
	size, err := unix.Getxattr("/probe-filecap", "security.capability", xattr)
	if err != nil {
		return response{Error: fmt.Sprintf("read file capability fixture: %v", err)}
	}
	xattr = xattr[:size]
	cmd, err := launchCommand(canaries, "/probe-filecap", "privilege-target")
	if err != nil {
		return response{Error: err.Error()}
	}
	defer closeExtraFiles(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if strings.Contains(message, "exec target /probe-filecap: operation not permitted") {
			return rawResponse(map[string]any{
				"pass": true, "outcome": "execution_denied",
				"security_capability": fmt.Sprintf("%x", xattr),
			})
		}
		return response{Error: fmt.Sprintf("file-capability child failed: %v: %s", err, message)}
	}
	var target map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &target); err != nil {
		return response{Error: fmt.Sprintf("file-capability child returned invalid JSON: %v", err)}
	}
	return rawResponse(map[string]any{
		"pass": target["pass"] == true, "outcome": "executed_without_privilege",
		"security_capability": fmt.Sprintf("%x", xattr), "target": target,
	})
}

func runRequest(args []string) error {
	if len(args) < 1 {
		return errors.New("request requires an action")
	}
	req := request{Action: args[0]}
	for _, arg := range args[1:] {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return fmt.Errorf("invalid request argument %q", arg)
		}
		switch key {
		case "value":
			n, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			req.Value = n
		case "token":
			req.Token = value
		case "target_pid":
			n, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			req.TargetPID = n
		default:
			return fmt.Errorf("unknown request argument %q", key)
		}
	}
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	var resp response
	if err := json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&resp); err != nil {
		return err
	}
	if err := emit(resp); err != nil {
		return err
	}
	if resp.Error != "" || !resp.Pass {
		return errors.New("request failed")
	}
	return nil
}

func runChildLaunch(args []string) error {
	if len(args) < 1 {
		return errors.New("child-launch requires an executable")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := unix.Prctl(unix.PR_SET_KEEPCAPS, 1, 0, 0, 0); err != nil {
		return err
	}
	for cap := 0; cap <= 63; cap++ {
		if cap == unix.CAP_SETPCAP {
			continue
		}
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, uintptr(cap), 0, 0, 0); err != nil && err != unix.EINVAL {
			return fmt.Errorf("drop bounding capability %d: %w", cap, err)
		}
	}
	if err := unix.Prctl(unix.PR_CAPBSET_DROP, unix.CAP_SETPCAP, 0, 0, 0); err != nil {
		return fmt.Errorf("drop CAP_SETPCAP from bounding set: %w", err)
	}
	if err := unix.Setgroups([]int{declaredGroup1, declaredGroup2}); err != nil {
		return err
	}
	if err := unix.Setresgid(declaredGID, declaredGID, declaredGID); err != nil {
		return err
	}
	if err := unix.Setresuid(declaredUID, declaredUID, declaredUID); err != nil {
		return err
	}
	empty := [2]unix.CapUserData{}
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	if err := unix.Capset(&hdr, &empty[0]); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_KEEPCAPS, 0, 0, 0, 0); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	if _, err := unix.Setsid(); err != nil {
		return err
	}
	canaryFDs, err := parseCanaryFDs(os.Getenv(canaryFDsEnv))
	if err != nil {
		return err
	}
	for _, fd := range canaryFDs {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
			return fmt.Errorf("canary fd %d was not inherited by the trusted launcher: %w", fd, err)
		}
	}
	if err := unix.CloseRange(3, ^uint(0), 0); err != nil {
		return err
	}
	for _, fd := range canaryFDs {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != unix.EBADF {
			return fmt.Errorf("canary fd %d survived close_range: %v", fd, err)
		}
	}
	remainingFDs, err := listFDs()
	if err != nil {
		return err
	}
	if fmt.Sprint(remainingFDs) != "[0 1 2]" {
		return fmt.Errorf("unexpected descriptors after close_range: %v", remainingFDs)
	}
	closeRangeEvidence := fmt.Sprintf("canaries=%d;allowlist=0,1,2", len(canaryFDs))
	if err := os.Setenv(closeRangeVerifiedEnv, closeRangeEvidence); err != nil {
		return err
	}
	if err := os.Unsetenv(canaryFDsEnv); err != nil {
		return err
	}
	if err := unix.Exec(args[0], args, os.Environ()); err != nil {
		return fmt.Errorf("exec target %s: %w", args[0], err)
	}
	return nil
}

func parseCanaryFDs(value string) ([]int, error) {
	if value == "" {
		return nil, errors.New("trusted launcher received no canary descriptors")
	}
	parts := strings.Split(value, ",")
	fds := make([]int, 0, len(parts))
	for _, part := range parts {
		fd, err := strconv.Atoi(part)
		if err != nil || fd < 3 {
			return nil, fmt.Errorf("invalid canary fd %q", part)
		}
		fds = append(fds, fd)
	}
	return fds, nil
}

func listFDs() ([]int, error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return nil, err
	}
	var out []int
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil {
			// os.ReadDir's own descriptor is closed before target inspection.
			continue
		}
		if target == "/proc/self/fd" || strings.HasSuffix(target, "/fd") {
			continue
		}
		out = append(out, fd)
	}
	sort.Ints(out)
	return out, nil
}

func denied(name string, err error) result {
	return result{Test: name, Pass: err != nil, Expected: "denied", Actual: ternary(err != nil, "denied", "allowed"), Error: errorString(err)}
}

func ternary[T any](condition bool, yes, no T) T {
	if condition {
		return yes
	}
	return no
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func runAppReport(args []string) error {
	if len(args) != 2 {
		return errors.New("app-report requires supervisor pid and memory address")
	}
	supervisorPID, err := strconv.Atoi(args[0])
	if err != nil {
		return err
	}
	address, err := strconv.ParseUint(args[1], 16, 64)
	if err != nil {
		return err
	}
	status, err := statusFields(os.Getpid())
	if err != nil {
		return err
	}
	fds, err := listFDs()
	if err != nil {
		return err
	}
	groups, err := os.Getgroups()
	if err != nil {
		return err
	}
	sid, err := unix.Getsid(0)
	if err != nil {
		return err
	}
	supervisorSID, err := unix.Getsid(supervisorPID)
	if err != nil {
		return err
	}

	results := []result{
		{Test: "final_uids", Pass: status["Uid"] == "1001\t1001\t1001\t1001", Expected: "1001 1001 1001 1001", Actual: status["Uid"]},
		{Test: "final_gids", Pass: status["Gid"] == "2001\t2001\t2001\t2001", Expected: "2001 2001 2001 2001", Actual: status["Gid"]},
		{Test: "final_groups", Pass: fmt.Sprint(groups) == "[3001 3002]", Expected: "[3001 3002]", Actual: fmt.Sprint(groups)},
		{Test: "empty_capability_sets", Pass: allCapabilitySetsZero(status), Actual: fmt.Sprint(capabilityFields(status))},
		{Test: "no_new_privileges", Pass: status["NoNewPrivs"] == "1", Expected: "1", Actual: status["NoNewPrivs"]},
		{Test: "seccomp_filter", Pass: status["Seccomp"] == "2", Expected: "2", Actual: status["Seccomp"]},
		{Test: "inherited_descriptor_cleanup", Pass: os.Getenv(closeRangeVerifiedEnv) == "canaries=6;allowlist=0,1,2", Expected: "canaries=6;allowlist=0,1,2", Actual: os.Getenv(closeRangeVerifiedEnv), Details: map[string]any{"runtime_fds": fds}},
		{Test: "session_separation", Pass: sid != supervisorSID, Expected: "different", Actual: fmt.Sprintf("child=%d supervisor=%d", sid, supervisorSID)},
	}

	results = append(results,
		attemptIDChange("post_drop_setuid", func() error { return unix.Setuid(undeclaredUID) }),
		attemptIDChange("post_drop_setreuid", func() error { return unix.Setreuid(undeclaredUID, undeclaredUID) }),
		attemptIDChange("post_drop_setresuid", func() error { return unix.Setresuid(undeclaredUID, undeclaredUID, undeclaredUID) }),
		attemptIDChange("post_drop_setfsuid", func() error { return setfsuidChecked(undeclaredUID) }),
		attemptIDChange("post_drop_setgid", func() error { return unix.Setgid(undeclaredGID) }),
		attemptIDChange("post_drop_setregid", func() error { return unix.Setregid(undeclaredGID, undeclaredGID) }),
		attemptIDChange("post_drop_setresgid", func() error { return unix.Setresgid(undeclaredGID, undeclaredGID, undeclaredGID) }),
		attemptIDChange("post_drop_setfsgid", func() error { return setfsgidChecked(undeclaredGID) }),
		attemptIDChange("post_drop_setgroups", func() error { return unix.Setgroups([]int{undeclaredGroup}) }),
	)

	results = append(results,
		denied("child_kill_permission", unix.Kill(supervisorPID, 0)),
		denied("child_sigcont", unix.Kill(supervisorPID, unix.SIGCONT)),
	)
	pidfd, pidfdErr := unix.PidfdOpen(supervisorPID, 0)
	if pidfdErr == nil {
		sendErr := unix.PidfdSendSignal(pidfd, unix.SIGCONT, nil, 0)
		_ = unix.Close(pidfd)
		results = append(results, denied("child_pidfd_send_signal", sendErr))
	} else {
		results = append(results, result{Test: "child_pidfd_send_signal", Pass: true, Expected: "denied", Actual: "pidfd_open_denied", Error: pidfdErr.Error()})
	}
	ptraceErr := unix.PtraceAttach(supervisorPID)
	if ptraceErr == nil {
		_ = unix.PtraceDetach(supervisorPID)
	}
	results = append(results, denied("child_ptrace", ptraceErr))
	mem, memErr := os.OpenFile(fmt.Sprintf("/proc/%d/mem", supervisorPID), os.O_RDWR, 0)
	if memErr == nil {
		_ = mem.Close()
	}
	results = append(results, denied("child_proc_mem", memErr))
	local := []byte{0}
	localIO := []unix.Iovec{{Base: &local[0], Len: 1}}
	remoteIO := []unix.RemoteIovec{{Base: uintptr(address), Len: 1}}
	_, readErr := unix.ProcessVMReadv(supervisorPID, localIO, remoteIO, 0)
	results = append(results, denied("child_process_vm_readv", readErr))
	_, writeErr := unix.ProcessVMWritev(supervisorPID, localIO, remoteIO, 0)
	results = append(results, denied("child_process_vm_writev", writeErr))
	_, vmspliceErr := vmspliceRoundTrip()
	results = append(results, result{
		Test: "seccomp_vmsplice_denied", Pass: vmspliceErr == unix.EPERM,
		Expected: "operation not permitted", Actual: errorString(vmspliceErr),
	})
	rootErr := os.WriteFile("/rootfs-write-must-fail", []byte("x"), 0o600)
	results = append(results, denied("readonly_rootfs", rootErr))
	writableErr := os.WriteFile("/probe-state/app-write", []byte("x"), 0o600)
	results = append(results, result{Test: "declared_writable_storage", Pass: writableErr == nil, Expected: "allowed", Actual: ternary(writableErr == nil, "allowed", "denied"), Error: errorString(writableErr)})

	pass := true
	for _, item := range results {
		pass = pass && item.Pass
	}
	return emit(map[string]any{"pass": pass, "status": status, "results": results})
}

func vmspliceRoundTrip() (int, error) {
	var pipe [2]int
	if err := unix.Pipe(pipe[:]); err != nil {
		return 0, err
	}
	defer unix.Close(pipe[0])
	defer unix.Close(pipe[1])
	payload := []byte("vmsplice-control")
	iov := unix.Iovec{Base: &payload[0], Len: uint64(len(payload))}
	written, err := unix.Vmsplice(pipe[1], []unix.Iovec{iov}, 0)
	if err != nil {
		return 0, err
	}
	received := make([]byte, len(payload))
	read, err := unix.Read(pipe[0], received)
	if err != nil {
		return written, err
	}
	if written != len(payload) || read != len(payload) || !bytes.Equal(received, payload) {
		return written, fmt.Errorf("unexpected vmsplice round trip: wrote %d read %d", written, read)
	}
	return written, nil
}

func runSeccompControl() error {
	written, err := vmspliceRoundTrip()
	return emit(map[string]any{
		"test":    "seccomp_vmsplice_unfiltered_control",
		"pass":    err == nil,
		"written": written,
		"error":   errorString(err),
	})
}

func capabilityFields(status map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
		out[key] = status[key]
	}
	return out
}

func allCapabilitySetsZero(status map[string]string) bool {
	for _, value := range capabilityFields(status) {
		if strings.TrimLeft(value, "0") != "" {
			return false
		}
	}
	return true
}

func attemptIDChange(name string, fn func() error) result {
	err := fn()
	return denied(name, err)
}

func setfsuidChecked(id int) error {
	if err := unix.Setfsuid(id); err != nil {
		return err
	}
	return requireFilesystemID("Uid", id)
}

func setfsgidChecked(id int) error {
	if err := unix.Setfsgid(id); err != nil {
		return err
	}
	return requireFilesystemID("Gid", id)

}

func requireFilesystemID(field string, expected int) error {
	status, err := statusFields(os.Getpid())
	if err != nil {
		return err
	}
	values := strings.Fields(status[field])
	if len(values) != 4 {
		return fmt.Errorf("unexpected %s status field %q", field, status[field])
	}
	if values[3] != strconv.Itoa(expected) {
		return fmt.Errorf("filesystem %s remained %s instead of %d", strings.ToLower(field), values[3], expected)
	}
	return nil
}

func runPrivilegeTarget() error {
	status, err := statusFields(os.Getpid())
	if err != nil {
		return err
	}
	info, err := os.Stat(os.Args[0])
	if err != nil {
		return err
	}
	fixture, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("set-ID fixture has no Linux stat metadata")
	}
	fixturePass := fixture.Uid == supervisorUID && fixture.Gid == supervisorGID &&
		info.Mode()&os.ModeSetuid != 0 && info.Mode()&os.ModeSetgid != 0
	pass := os.Getuid() == declaredUID && os.Geteuid() == declaredUID &&
		os.Getgid() == declaredGID && os.Getegid() == declaredGID &&
		allCapabilitySetsZero(status) && fixturePass
	return emit(map[string]any{
		"pass": pass, "uid": os.Getuid(), "euid": os.Geteuid(),
		"gid": os.Getgid(), "egid": os.Getegid(), "status": status,
		"fixture": map[string]any{
			"path": os.Args[0], "uid": fixture.Uid, "gid": fixture.Gid,
			"setuid": info.Mode()&os.ModeSetuid != 0,
			"setgid": info.Mode()&os.ModeSetgid != 0,
		},
	})
}

func emitCapabilityReport(test string) error {
	status, err := statusFields(os.Getpid())
	if err != nil {
		return err
	}
	return emit(map[string]any{"test": test, "pass": allCapabilitySetsZero(status), "status": status})
}

type rawCase struct {
	Name     string
	Op       string
	ID       int
	Expected bool
}

func runRawMatrix(args []string) error {
	if len(args) != 1 || (args[0] != "exact" && args[0] != "bounded") {
		return errors.New("raw-matrix requires exact or bounded")
	}
	profile := args[0]
	classes := []struct {
		Name            string
		UID, GID, Group int
		Exact, Bounded  bool
	}{
		{"declared", declaredUID, declaredGID, declaredGroup1, true, true},
		{"undeclared", undeclaredUID, undeclaredGID, undeclaredGroup, false, true},
		{"outside", outsideID, outsideID, outsideID, false, false},
	}
	var cases []rawCase
	for _, class := range classes {
		expected := class.Bounded
		if profile == "exact" {
			expected = class.Exact
		}
		for _, op := range []string{"setuid", "setreuid", "setresuid", "setfsuid"} {
			cases = append(cases, rawCase{class.Name + "_" + op, op, class.UID, expected})
		}
		for _, op := range []string{"setgid", "setregid", "setresgid", "setfsgid"} {
			cases = append(cases, rawCase{class.Name + "_" + op, op, class.GID, expected})
		}
		cases = append(cases, rawCase{class.Name + "_setgroups", "setgroups", class.Group, expected})
	}
	// The raw mechanism maps this runtime-only GID, but supervisor policy must reject it.
	cases = append(cases, rawCase{"runtime_required_setgid", "setgid", runtimeGID, true})
	results := make([]result, 0, len(cases))
	for _, tc := range cases {
		cmd := exec.Command("/probe", "raw-attempt", tc.Op, strconv.Itoa(tc.ID))
		err := cmd.Run()
		actual := err == nil
		results = append(results, result{Test: tc.Name, Pass: actual == tc.Expected,
			Expected: ternary(tc.Expected, "allowed", "denied"), Actual: ternary(actual, "allowed", "denied"), Error: errorString(err)})
	}
	pass := true
	for _, item := range results {
		pass = pass && item.Pass
	}
	return emit(map[string]any{"profile": profile, "pass": pass, "results": results})
}

func runRawAttempt(args []string) error {
	if len(args) != 2 {
		return errors.New("raw-attempt requires operation and id")
	}
	id, err := strconv.Atoi(args[1])
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch args[0] {
	case "setuid":
		return unix.Setuid(id)
	case "setreuid":
		return unix.Setreuid(id, id)
	case "setresuid":
		return unix.Setresuid(id, id, id)
	case "setfsuid":
		return setfsuidChecked(id)
	case "setgid":
		return unix.Setgid(id)
	case "setregid":
		return unix.Setregid(id, id)
	case "setresgid":
		return unix.Setresgid(id, id, id)
	case "setfsgid":
		return setfsgidChecked(id)
	case "setgroups":
		return unix.Setgroups([]int{id})
	default:
		return fmt.Errorf("unknown raw operation %q", args[0])
	}
}

func writePrivate(args []string) error {
	if len(args) != 1 {
		return errors.New("write-private requires a path")
	}
	if err := os.WriteFile(args[0], []byte("private\n"), 0o600); err != nil {
		return err
	}
	return emit(map[string]any{"pass": true, "path": args[0]})
}

func tokenKey(token string) int {
	var value uint32 = 2166136261
	for _, b := range []byte(token) {
		value = (value ^ uint32(b)) * 16777619
	}
	return int(value&0x3fffffff) | 0x10000
}

func startPOSIXIPCServer(token string) (*exec.Cmd, map[string]any, error) {
	cmd := exec.Command("/posix-ipc-probe", "serve", token)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	var report map[string]any
	if err := json.NewDecoder(stdout).Decode(&report); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("read POSIX IPC server readiness: %w", err)
	}
	if report["pass"] != true {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("POSIX IPC server failed its positive controls: %v", report)
	}
	return cmd, report, nil
}

func runServiceServer(args []string) error {
	if len(args) != 1 {
		return errors.New("service-server requires token")
	}
	token := args[0]
	key := tokenKey(token)
	shm, err := unix.SysvShmGet(key, 4096, unix.IPC_CREAT|unix.IPC_EXCL|0o600)
	if err != nil {
		return fmt.Errorf("create SysV shm: %w", err)
	}
	_, _, semErr := unix.Syscall(unix.SYS_SEMGET, uintptr(key), 1, unix.IPC_CREAT|unix.IPC_EXCL|0o600)
	if semErr != 0 {
		return fmt.Errorf("create SysV semaphore: %w", semErr)
	}
	_, _, msgErr := unix.Syscall(unix.SYS_MSGGET, uintptr(key), unix.IPC_CREAT|unix.IPC_EXCL|0o600, 0)
	if msgErr != 0 {
		return fmt.Errorf("create SysV message queue: %w", msgErr)
	}
	posixServer, posixIPC, err := startPOSIXIPCServer(token)
	if err != nil {
		return err
	}
	defer func() {
		_ = posixServer.Process.Kill()
		_ = posixServer.Wait()
	}()
	// The POSIX API requires a leading slash, but libc strips it before the
	// Linux mq_open syscall. This static probe calls the syscall directly.
	mqName := "reploy-" + token
	mqPtr, _ := unix.BytePtrFromString(mqName)
	mq, _, mqErr := unix.Syscall6(unix.SYS_MQ_OPEN, uintptr(unsafe.Pointer(mqPtr)), unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_NONBLOCK, 0o600, 0, 0, 0)
	if mqErr != 0 {
		info, statErr := os.Stat("/dev/mqueue")
		if statErr != nil {
			return fmt.Errorf("create POSIX message queue: %w; stat /dev/mqueue: %v", mqErr, statErr)
		}
		stat := info.Sys().(*syscall.Stat_t)
		return fmt.Errorf("create POSIX message queue: %w; /dev/mqueue mode=%s uid=%d gid=%d", mqErr, info.Mode(), stat.Uid, stat.Gid)
	}
	defer unix.Close(int(mq))
	fmt.Fprintln(os.Stderr, "service: POSIX message queue opened")
	payload := []byte("mq-round-trip")
	_, _, mqSendErr := unix.Syscall6(
		unix.SYS_MQ_TIMEDSEND, mq, uintptr(unsafe.Pointer(&payload[0])),
		uintptr(len(payload)), 1, 0, 0,
	)
	if mqSendErr != 0 {
		return fmt.Errorf("send POSIX message: %w", mqSendErr)
	}
	fmt.Fprintln(os.Stderr, "service: POSIX message sent")
	received := make([]byte, 8192)
	var priority uint32
	receivedSize, _, mqReceiveErr := unix.Syscall6(
		unix.SYS_MQ_TIMEDRECEIVE, mq, uintptr(unsafe.Pointer(&received[0])),
		uintptr(len(received)), uintptr(unsafe.Pointer(&priority)), 0, 0,
	)
	if mqReceiveErr != 0 {
		return fmt.Errorf("receive POSIX message: %w", mqReceiveErr)
	}
	if string(received[:receivedSize]) != string(payload) || priority != 1 {
		return fmt.Errorf("unexpected POSIX message round trip")
	}
	fmt.Fprintln(os.Stderr, "service: POSIX message received")
	// A network-none namespace deliberately leaves loopback down. Binding the
	// wildcard address still creates namespace-local sockets for the isolation
	// probe without requiring network setup.
	tcpLn, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", servicePort))
	if err != nil {
		return err
	}
	defer tcpLn.Close()
	fmt.Fprintln(os.Stderr, "service: TCP listener ready")
	udp, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", servicePort))
	if err != nil {
		return err
	}
	defer udp.Close()
	fmt.Fprintln(os.Stderr, "service: UDP listener ready")
	abstract, err := net.Listen("unix", "\x00reploy-"+token)
	if err != nil {
		return err
	}
	defer abstract.Close()
	fmt.Fprintln(os.Stderr, "service: abstract Unix listener ready")
	go acceptAndDiscard(tcpLn)
	go acceptAndDiscard(abstract)
	go echoUDP(udp)
	if err := os.WriteFile("/probe-state/service-secret", []byte("secret"), 0o600); err != nil {
		return err
	}
	ready, err := json.Marshal(map[string]any{
		"pass": true, "sysv_shm_id": shm, "posix_ipc": posixIPC,
		"posix_mq_round_trip": true,
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile("/probe-state/service-ready", ready, 0o644); err != nil {
		return err
	}
	select {}
}

func acceptAndDiscard(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}

func echoUDP(conn net.PacketConn) {
	buf := make([]byte, 64)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		_, _ = conn.WriteTo(buf[:n], addr)
	}
}

func runCrossProbe(args []string) error {
	if len(args) != 2 {
		return errors.New("cross-probe requires token and target pid")
	}
	token := args[0]
	target, err := strconv.Atoi(args[1])
	if err != nil {
		return err
	}
	key := tokenKey(token)
	var results []result
	_, shmErr := unix.SysvShmGet(key, 1, 0)
	results = append(results, denied("cross_sysv_shm", shmErr))
	_, _, semErr := unix.Syscall(unix.SYS_SEMGET, uintptr(key), 1, 0)
	results = append(results, denied("cross_sysv_sem", semErr))
	_, _, msgErr := unix.Syscall(unix.SYS_MSGGET, uintptr(key), 0, 0)
	results = append(results, denied("cross_sysv_msg", msgErr))
	posixOutput, posixErr := exec.Command("/posix-ipc-probe", "probe", token).CombinedOutput()
	var posixProbe map[string]any
	posixDecodeErr := json.Unmarshal(bytes.TrimSpace(posixOutput), &posixProbe)
	posixPass := posixErr == nil && posixDecodeErr == nil && posixProbe["pass"] == true
	results = append(results, result{
		Test: "cross_posix_shm_and_sem", Pass: posixPass,
		Expected: "shm_open and sem_open denied with ENOENT",
		Actual:   strings.TrimSpace(string(posixOutput)),
		Error:    errorString(errors.Join(posixErr, posixDecodeErr)),
	})
	private, privateErr := os.Open("/probe-state/service-secret")
	if privateErr == nil {
		_ = private.Close()
	}
	results = append(results, denied("cross_private_state", privateErr))
	mqName := "reploy-" + token
	mqPtr, _ := unix.BytePtrFromString(mqName)
	mq, _, mqErr := unix.Syscall6(unix.SYS_MQ_OPEN, uintptr(unsafe.Pointer(mqPtr)), unix.O_RDONLY|unix.O_NONBLOCK, 0, 0, 0, 0)
	if mqErr == 0 {
		_ = unix.Close(int(mq))
	}
	results = append(results, denied("cross_posix_mq", mqErr))
	tcp, tcpErr := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", servicePort), 200*time.Millisecond)
	if tcpErr == nil {
		_ = tcp.Close()
	}
	results = append(results, denied("cross_tcp", tcpErr))
	udp, udpErr := net.DialTimeout("udp4", fmt.Sprintf("127.0.0.1:%d", servicePort), 200*time.Millisecond)
	if udpErr == nil {
		_ = udp.SetDeadline(time.Now().Add(200 * time.Millisecond))
		_, udpErr = udp.Write([]byte("probe"))
		if udpErr == nil {
			_, udpErr = udp.Read(make([]byte, 16))
		}
		_ = udp.Close()
	}
	results = append(results, denied("cross_udp", udpErr))
	abs, absErr := net.DialTimeout("unix", "\x00reploy-"+token, 200*time.Millisecond)
	if absErr == nil {
		_ = abs.Close()
	}
	results = append(results, denied("cross_abstract_unix", absErr))
	results = append(results,
		denied("cross_process_visibility", unix.Kill(target, 0)),
		denied("cross_process_sigcont", unix.Kill(target, unix.SIGCONT)),
	)
	pidfd, pidfdErr := unix.PidfdOpen(target, 0)
	if pidfdErr == nil {
		pidfdErr = unix.PidfdSendSignal(pidfd, unix.SIGCONT, nil, 0)
		_ = unix.Close(pidfd)
	}
	results = append(results, denied("cross_pidfd_signal", pidfdErr))
	ptraceErr := unix.PtraceAttach(target)
	if ptraceErr == nil {
		_ = unix.PtraceDetach(target)
	}
	results = append(results, denied("cross_ptrace", ptraceErr))
	mem, memErr := os.Open(fmt.Sprintf("/proc/%d/mem", target))
	if memErr == nil {
		_ = mem.Close()
	}
	results = append(results, denied("cross_proc_mem", memErr))
	pass := true
	for _, item := range results {
		pass = pass && item.Pass
	}
	return emit(map[string]any{"pass": pass, "results": results})
}

func stampFileCapability(args []string) error {
	if len(args) != 1 {
		return errors.New("stamp-filecap requires a path")
	}
	// security.capability revision 2, effective flag, SETGID and SETUID permitted.
	data := make([]byte, 20)
	binary.LittleEndian.PutUint32(data[0:4], 0x02000001)
	binary.LittleEndian.PutUint32(data[4:8], capMask(unix.CAP_SETGID, unix.CAP_SETUID))
	if err := unix.Setxattr(args[0], "security.capability", data, 0); err != nil {
		return fmt.Errorf("set file capability: %w", err)
	}
	return nil
}
