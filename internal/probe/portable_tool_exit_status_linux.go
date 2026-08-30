//go:build linux

package probe

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const portableToolExitStatusPathV1 = "/tmp/.reploy-portable-tool-exit-status-v1"

const (
	portableToolApplicationReadyFDV1       = 3
	portableToolApplicationReadyV1         = "ready\n"
	portableToolApplicationLaunchStatusV1  = "launch-status:"
	portableToolApplicationLaunchFailureV1 = "launch-failure\n"
)

// Every canonical status in the fixed channel is one to three decimal digits
// followed by a newline. Reserve the maximum four bytes without changing the
// initially empty file size, so an output-filling application cannot consume
// the space needed for the trusted wrapper's final status write.
const portableToolExitStatusReservationBytesV1 = int64(len("255\n"))

func reservePortableToolExitStatusV1(fd int) error {
	if err := unix.Fallocate(fd, unix.FALLOC_FL_KEEP_SIZE, 0, portableToolExitStatusReservationBytesV1); err != nil {
		return fmt.Errorf("reserve fixed portable-tool exit status: %w", err)
	}
	return nil
}

func createPortableToolExitStatusFileV1() (*os.File, error) {
	fd, err := unix.Open(
		portableToolExitStatusPathV1,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("create fixed portable-tool exit status: %w", err)
	}
	if err := reservePortableToolExitStatusV1(fd); err != nil {
		_ = unix.Close(fd)
		_ = unix.Unlink(portableToolExitStatusPathV1)
		return nil, err
	}
	return os.NewFile(uintptr(fd), portableToolExitStatusPathV1), nil
}

func portableToolObservedExecArgvV1(fd int, plan sandboxExecPlanV1, argv []string) []string {
	groups := make([]string, len(plan.Groups))
	for index, group := range plan.Groups {
		groups[index] = strconv.FormatUint(uint64(group), 10)
	}
	result := []string{
		"/proc/self/exe", "portable-tool-observed-exec-v1",
		"--status-fd", strconv.Itoa(fd),
		"--uid", strconv.FormatUint(uint64(plan.UID), 10),
		"--gid", strconv.FormatUint(uint64(plan.GID), 10),
		"--groups", strings.Join(groups, ","),
		"--",
	}
	return append(result, argv...)
}

func portableToolApplicationExecArgvV1(fd int, plan sandboxExecPlanV1, argv []string) []string {
	groups := make([]string, len(plan.Groups))
	for index, group := range plan.Groups {
		groups[index] = strconv.FormatUint(uint64(group), 10)
	}
	result := []string{
		"/proc/self/exe", "portable-tool-application-exec-v1",
		"--ready-fd", strconv.Itoa(fd),
		"--uid", strconv.FormatUint(uint64(plan.UID), 10),
		"--gid", strconv.FormatUint(uint64(plan.GID), 10),
		"--groups", strings.Join(groups, ","),
		"--",
	}
	return append(result, argv...)
}

func parsePortableToolApplicationExecV1(args []string, name string, descriptorFlag string) (int, sandboxExecPlanV1, []string, error) {
	separator := -1
	for index, argument := range args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator == len(args)-1 {
		return 0, sandboxExecPlanV1{}, nil, fmt.Errorf("requires -- followed by an absolute application command")
	}
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(new(strings.Builder))
	descriptor := set.String(descriptorFlag, "", "trusted descriptor")
	uid := set.String("uid", "", "expected application UID")
	gid := set.String("gid", "", "expected application GID")
	groups := set.String("groups", "", "expected supplementary GIDs")
	if err := set.Parse(args[:separator]); err != nil {
		return 0, sandboxExecPlanV1{}, nil, err
	}
	if len(set.Args()) != 0 {
		return 0, sandboxExecPlanV1{}, nil, fmt.Errorf("unexpected positional %s arguments", name)
	}
	fd, err := strconv.Atoi(*descriptor)
	if err != nil || fd < 3 || strconv.Itoa(fd) != *descriptor {
		return 0, sandboxExecPlanV1{}, nil, fmt.Errorf("--%s must be a canonical descriptor number at least 3", descriptorFlag)
	}
	parsedUID, err := parseCredentialV1(*uid)
	if err != nil {
		return 0, sandboxExecPlanV1{}, nil, fmt.Errorf("parse --uid: %w", err)
	}
	parsedGID, err := parseCredentialV1(*gid)
	if err != nil {
		return 0, sandboxExecPlanV1{}, nil, fmt.Errorf("parse --gid: %w", err)
	}
	parsedGroups, err := parseCredentialListV1(*groups)
	if err != nil {
		return 0, sandboxExecPlanV1{}, nil, fmt.Errorf("parse --groups: %w", err)
	}
	plan := sandboxExecPlanV1{UID: parsedUID, GID: parsedGID, Groups: parsedGroups}
	return fd, plan, append([]string(nil), args[separator+1:]...), nil
}

func runPortableToolObservedExecV1(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	fd, plan, application, err := parsePortableToolApplicationExecV1(args, "portable-tool-observed-exec-v1", "status-fd")
	if err != nil {
		return err
	}
	if ruid, euid, suid := unix.Getresuid(); ruid != 0 || euid != 0 || suid != 0 {
		return fmt.Errorf("trusted portable-tool status supervisor must remain root, got UID state %d/%d/%d", ruid, euid, suid)
	}
	status := os.NewFile(uintptr(fd), "portable-tool-exit-status")
	if status == nil {
		return fmt.Errorf("trusted status descriptor is unavailable")
	}
	defer status.Close()
	if err := verifyPortableToolExitStatusFileV1(status); err != nil {
		return err
	}
	// The root supervisor accepts no application input after parsing the literal
	// argv. Keep it non-dumpable and close the root-owned status descriptor
	// across the separate application-helper exec so the application can neither
	// signal the supervisor nor reopen or inherit the trusted result channel.
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("disable portable-tool wrapper dumpability: %w", err)
	}
	if dumpable, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("verify portable-tool wrapper dumpability: %w", err)
	} else if dumpable != 0 {
		return fmt.Errorf("portable-tool wrapper remained dumpable")
	}
	unix.CloseOnExec(fd)
	if flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		return fmt.Errorf("verify portable-tool status descriptor flags: %w", err)
	} else if flags&unix.FD_CLOEXEC == 0 {
		return fmt.Errorf("portable-tool status descriptor remained open across application exec")
	}
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create portable-tool application readiness pipe: %w", err)
	}
	defer readyReader.Close()
	childArgv := portableToolApplicationExecArgvV1(portableToolApplicationReadyFDV1, plan, application)
	command := exec.Command(childArgv[0], childArgv[1:]...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	command.ExtraFiles = []*os.File{readyWriter}
	if err := command.Start(); err != nil {
		_ = readyWriter.Close()
		return fmt.Errorf("start portable-tool application helper: %w", err)
	}
	if err := readyWriter.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("close portable-tool application readiness writer: %w", err)
	}
	protocol, readErr := io.ReadAll(io.LimitReader(readyReader, 128))
	waitErr := command.Wait()
	exitCode, err := portableToolApplicationOutcomeV1(protocol, waitErr, readErr)
	if err != nil {
		return err
	}
	return writePortableToolExitStatusV1(status, exitCode)
}

func runPortableToolApplicationExecV1(args []string) error {
	fd, plan, application, err := parsePortableToolApplicationExecV1(args, "portable-tool-application-exec-v1", "ready-fd")
	if err != nil {
		return err
	}
	ready := os.NewFile(uintptr(fd), "portable-tool-application-readiness")
	if ready == nil {
		return fmt.Errorf("portable-tool application readiness descriptor is unavailable")
	}
	defer ready.Close()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Geteuid() != 0 {
		return fmt.Errorf("portable-tool application authority transition must begin as root")
	}
	if err := dropApplicationAuthorityV1(plan.UID, plan.GID, plan.Groups); err != nil {
		return err
	}
	if err := verifyPortableToolObservedCredentialsV1(plan.UID, plan.GID, plan.Groups); err != nil {
		return err
	}
	return verifyAndExecApplication(application, readApplicationKernelStatus, func(argv []string) error {
		unix.CloseOnExec(fd)
		if flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
			return fmt.Errorf("verify portable-tool readiness descriptor flags: %w", err)
		} else if flags&unix.FD_CLOEXEC == 0 {
			return fmt.Errorf("portable-tool readiness descriptor remained open across application exec")
		}
		if _, err := io.WriteString(ready, portableToolApplicationReadyV1); err != nil {
			return fmt.Errorf("write portable-tool application readiness: %w", err)
		}
		if err := syscall.Exec(argv[0], argv, os.Environ()); err != nil {
			if exitCode, statusErr := portableToolProcessStatusV1(err); statusErr == nil {
				_, writeErr := fmt.Fprintf(ready, "%s%d\n", portableToolApplicationLaunchStatusV1, exitCode)
				return writeErr
			}
			_, _ = io.WriteString(ready, portableToolApplicationLaunchFailureV1)
			return err
		}
		return nil
	})
}

func portableToolApplicationOutcomeV1(protocol []byte, waitErr error, readErr error) (int, error) {
	if readErr != nil {
		return 0, fmt.Errorf("read portable-tool application readiness: %w", readErr)
	}
	if string(protocol) == portableToolApplicationReadyV1 {
		exitCode, err := portableToolProcessStatusV1(waitErr)
		if err != nil {
			return 0, fmt.Errorf("wait for portable-tool application: %w", err)
		}
		return exitCode, nil
	}
	prefix := portableToolApplicationReadyV1 + portableToolApplicationLaunchStatusV1
	if waitErr == nil && strings.HasPrefix(string(protocol), prefix) && strings.HasSuffix(string(protocol), "\n") {
		encoded := strings.TrimSuffix(strings.TrimPrefix(string(protocol), prefix), "\n")
		exitCode, err := strconv.Atoi(encoded)
		if err == nil && strconv.Itoa(exitCode) == encoded && (exitCode == 126 || exitCode == 127) {
			return exitCode, nil
		}
	}
	if string(protocol) == portableToolApplicationReadyV1+portableToolApplicationLaunchFailureV1 {
		if waitErr == nil {
			return 0, fmt.Errorf("portable-tool application exec failed without a helper error")
		}
		return 0, fmt.Errorf("portable-tool application exec failed: %w", waitErr)
	}
	return 0, fmt.Errorf("portable-tool application returned invalid readiness evidence")
}

func writePortableToolExitStatusV1(status *os.File, exitCode int) error {
	content := []byte(strconv.Itoa(exitCode) + "\n")
	written, err := status.Write(content)
	if err != nil {
		return fmt.Errorf("write portable-tool exit status: %w", err)
	}
	if written != len(content) {
		return fmt.Errorf("write portable-tool exit status: short write")
	}
	if err := status.Sync(); err != nil {
		return fmt.Errorf("sync portable-tool exit status: %w", err)
	}
	return nil
}

func portableToolProcessStatusV1(runErr error) (int, error) {
	if runErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		switch {
		case errors.Is(runErr, os.ErrNotExist):
			// A missing executable or interpreter is the declared probe's
			// deterministic launch outcome, conventionally command-not-found.
			return 127, nil
		case errors.Is(runErr, os.ErrPermission), errors.Is(runErr, syscall.ENOEXEC), errors.Is(runErr, syscall.EISDIR):
			// A present declaration that cannot be invoked is conventionally
			// command-found-but-not-executable. Other pre-start failures remain
			// infrastructure errors rather than fabricated tool evidence.
			return 126, nil
		default:
			return 0, runErr
		}
	}
	if exitErr.ProcessState == nil {
		return 0, runErr
	}
	if exitCode := exitErr.ExitCode(); exitCode >= 0 && exitCode <= 255 {
		return exitCode, nil
	}
	waitStatus, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !waitStatus.Signaled() {
		return 0, runErr
	}
	// Use the conventional 128+signal process status so a genuine tool
	// termination remains attributable within the fixed one-byte channel.
	status := 128 + int(waitStatus.Signal())
	if status > 255 {
		return 0, runErr
	}
	return status, nil
}

func verifyPortableToolObservedCredentialsV1(uid uint32, gid uint32, groups []uint32) error {
	ruid, euid, suid := unix.Getresuid()
	if ruid != int(uid) || euid != int(uid) || suid != int(uid) {
		return fmt.Errorf("portable-tool observed exec UID state is %d/%d/%d, want %d", ruid, euid, suid, uid)
	}
	rgid, egid, sgid := unix.Getresgid()
	if rgid != int(gid) || egid != int(gid) || sgid != int(gid) {
		return fmt.Errorf("portable-tool observed exec GID state is %d/%d/%d, want %d", rgid, egid, sgid, gid)
	}
	actualGroups, err := unix.Getgroups()
	if err != nil {
		return fmt.Errorf("read portable-tool supplementary groups: %w", err)
	}
	expectedGroups := make([]int, len(groups))
	for index, group := range groups {
		expectedGroups[index] = int(group)
	}
	if len(actualGroups) != len(expectedGroups) {
		return fmt.Errorf("portable-tool observed exec supplementary groups are %v, want %v", actualGroups, expectedGroups)
	}
	for index := range actualGroups {
		if actualGroups[index] != expectedGroups[index] {
			return fmt.Errorf("portable-tool observed exec supplementary groups are %v, want %v", actualGroups, expectedGroups)
		}
	}
	return nil
}

func verifyPortableToolExitStatusFileV1(file *os.File) error {
	var status unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &status); err != nil {
		return fmt.Errorf("inspect portable-tool exit status: %w", err)
	}
	if status.Mode&unix.S_IFMT != unix.S_IFREG || status.Mode&0o777 != 0o600 || status.Uid != 0 || status.Gid != 0 || status.Nlink != 1 {
		return fmt.Errorf("portable-tool exit status does not match the fixed root-owned regular file")
	}
	var pathStatus unix.Stat_t
	if err := unix.Lstat(portableToolExitStatusPathV1, &pathStatus); err != nil {
		return fmt.Errorf("inspect fixed portable-tool exit status path: %w", err)
	}
	if pathStatus.Dev != status.Dev || pathStatus.Ino != status.Ino {
		return fmt.Errorf("portable-tool exit status descriptor does not match the fixed path")
	}
	return nil
}

func readPortableToolExitStatusV1(stdout io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("trusted status retrieval must run as container root")
	}
	fd, err := unix.Open(portableToolExitStatusPathV1, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open fixed portable-tool exit status: %w", err)
	}
	file := os.NewFile(uintptr(fd), portableToolExitStatusPathV1)
	defer file.Close()
	if err := verifyPortableToolExitStatusFileV1(file); err != nil {
		return err
	}
	content, err := io.ReadAll(io.LimitReader(file, 16))
	if err != nil {
		return fmt.Errorf("read fixed portable-tool exit status: %w", err)
	}
	if _, err := parsePortableToolExitStatusV1(content); err != nil {
		return err
	}
	if written, err := stdout.Write(content); err != nil {
		return fmt.Errorf("write fixed portable-tool exit status: %w", err)
	} else if written != len(content) {
		return fmt.Errorf("write fixed portable-tool exit status: short write")
	}
	return nil
}

func parsePortableToolExitStatusV1(content []byte) (int, error) {
	if len(content) < 2 || len(content) > 4 || content[len(content)-1] != '\n' || bytes.ContainsAny(content[:len(content)-1], "\r\n") {
		return 0, fmt.Errorf("portable-tool exit status is not one canonical line")
	}
	value := string(content[:len(content)-1])
	if value != "0" && (value[0] == '0' || strings.IndexFunc(value, func(char rune) bool { return char < '0' || char > '9' }) >= 0) {
		return 0, fmt.Errorf("portable-tool exit status is not canonical decimal")
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 || parsed > 255 {
		return 0, fmt.Errorf("portable-tool exit status is outside 0..255")
	}
	return parsed, nil
}
