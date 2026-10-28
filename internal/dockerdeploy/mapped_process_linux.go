//go:build linux

package dockerdeploy

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/omry/reploy/internal/identitymapping"
	"golang.org/x/sys/unix"
)

type mappedProcessSnapshotV1 struct {
	start      string
	namespaces [5]string
	device     uint64
	inode      uint64
}

type mappedProcessV1 struct {
	directory *os.File
	pidfd     int
	active    identitymapping.Geometry
	argv      []byte
	initial   mappedProcessSnapshotV1
}

func pinMappedProcessV1(pid int, active identitymapping.Geometry, argv []string) (mappedWaiterProcessV1, error) {
	active, err := identitymapping.ValidateGeometry(active)
	if err != nil || len(argv) == 0 {
		return nil, fmt.Errorf("mapped waiter requires exact active geometry and launch arguments")
	}
	if pid <= 0 {
		return nil, fmt.Errorf("mapped waiter requires a live host PID")
	}
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, fmt.Errorf("pin mapped waiter: %w", err)
	}
	p := &mappedProcessV1{pidfd: pidfd, active: active, argv: []byte(strings.Join(argv, "\x00") + "\x00")}
	fd, err := unix.Open("/proc/"+strconv.Itoa(pid), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err == nil {
		p.directory = os.NewFile(uintptr(fd), "mapped-waiter-proc")
		p.initial, err = p.observe()
	}
	if err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}

func (p *mappedProcessV1) close() {
	if p.directory != nil {
		_ = p.directory.Close()
	}
	_ = unix.Close(p.pidfd)
}

func (p *mappedProcessV1) fresh() error {
	current, err := p.observe()
	if err == nil && current != p.initial {
		err = fmt.Errorf("mapped waiter process or namespace identity changed")
	}
	return err
}

func (p *mappedProcessV1) observe() (mappedProcessSnapshotV1, error) {
	var result mappedProcessSnapshotV1
	poll := []unix.PollFd{{Fd: int32(p.pidfd), Events: unix.POLLIN}}
	if n, err := unix.Poll(poll, 0); err != nil || n != 0 {
		return result, fmt.Errorf("mapped waiter exited or its process pin is unavailable")
	}
	root := "/proc/self/fd/" + strconv.FormatUint(uint64(p.directory.Fd()), 10)
	read := func(name string) ([]byte, error) {
		f, err := os.Open(root + "/" + name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
		if len(data) > 64<<10 {
			return nil, fmt.Errorf("mapped process observation exceeds its limit")
		}
		return data, err
	}
	before, err := read("stat")
	if err != nil {
		return result, err
	}
	result.start, err = mappedProcessStartV1(before)
	if err != nil {
		return result, err
	}
	actual := identitymapping.Geometry{Profile: p.active.Profile}
	for i, name := range []string{"uid_map", "gid_map"} {
		data, err := read(name)
		if err != nil {
			return result, err
		}
		rows, err := mappedKernelRowsV1(data)
		if err != nil {
			return result, err
		}
		if i == 0 {
			actual.UIDs = rows
		} else {
			actual.GIDs = rows
		}
	}
	actual, err = identitymapping.ValidateGeometry(actual)
	if err != nil || !reflect.DeepEqual(actual, p.active) {
		return result, fmt.Errorf("complete kernel maps differ from admitted active geometry")
	}
	var owner unix.Stat_t
	if err := unix.Fstat(int(p.directory.Fd()), &owner); err != nil || p.active.UIDs[0].ContainerStart != 0 || p.active.GIDs[0].ContainerStart != 0 || owner.Uid != p.active.UIDs[0].HostStart || owner.Gid != p.active.GIDs[0].HostStart {
		return result, fmt.Errorf("mapped waiter kernel owner differs from mapped setup root")
	}
	for i, name := range []string{"user", "pid", "mnt", "net", "ipc"} {
		link, err := os.Readlink(root + "/ns/" + name)
		host, hostErr := os.Readlink("/proc/self/ns/" + name)
		if err != nil || hostErr != nil || link == host || !strings.HasPrefix(link, name+":[") {
			return result, fmt.Errorf("mapped waiter lacks isolated %s namespace evidence", name)
		}
		result.namespaces[i] = link
	}
	executable, err := os.Stat(root + "/exe")
	trusted, trustedErr := os.Stat(root + "/root/reploy-probe")
	if err != nil || trustedErr != nil || !executable.Mode().IsRegular() || !os.SameFile(executable, trusted) {
		return result, fmt.Errorf("mapped waiter is not the immutable trusted startup executable")
	}
	metadata := executable.Sys().(*syscall.Stat_t)
	result.device, result.inode = uint64(metadata.Dev), metadata.Ino
	command, err := read("cmdline")
	if err != nil || !bytes.Equal(command, p.argv) {
		return result, fmt.Errorf("mapped waiter command differs from its exact trusted launch")
	}
	after, err := read("stat")
	start, parseErr := mappedProcessStartV1(after)
	if err != nil || parseErr != nil || start != result.start {
		return result, fmt.Errorf("mapped waiter process changed during observation")
	}
	if n, err := unix.Poll(poll, 0); err != nil || n != 0 {
		return result, fmt.Errorf("mapped waiter exited during observation")
	}
	return result, nil
}

func mappedProcessStartV1(data []byte) (string, error) {
	// comm may contain spaces and closing parentheses; fields after its last
	// closing parenthesis start at field 3. starttime is field 22.
	i := bytes.LastIndexByte(data, ')')
	fields := strings.Fields(string(data[i+1:]))
	if i < 0 || len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return "", fmt.Errorf("invalid or exited mapped process stat")
	}
	n, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || n == 0 {
		return "", fmt.Errorf("invalid mapped process start identity")
	}
	return fields[19], nil
}

func mappedKernelRowsV1(data []byte) ([]identitymapping.Interval, error) {
	var rows []identitymapping.Interval
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid complete kernel map row")
		}
		var values [3]uint32
		for i, s := range fields {
			n, err := strconv.ParseUint(s, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid complete kernel map number")
			}
			values[i] = uint32(n)
		}
		rows = append(rows, identitymapping.Interval{ContainerStart: values[0], HostStart: values[1], Count: values[2]})
	}
	return rows, nil
}
