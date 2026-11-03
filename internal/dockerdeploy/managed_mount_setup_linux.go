//go:build linux

package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
	"golang.org/x/sys/unix"
)

const managedMountSetupLimitV1 = 1 << 20
const managedMountSetupSealsV1 = unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE

type managedMountSetupManifestV1 struct {
	Geometry identitymapping.Geometry
	Objects  []blueprint.ManagedMountObject
	Recorded []string
	Parent   int
}

func managedMountSetupHostIDV1(id uint32, rows []identitymapping.Interval) (uint32, bool) {
	for _, row := range rows {
		if id >= row.ContainerStart && uint64(id) < uint64(row.ContainerStart)+uint64(row.Count) {
			return row.HostStart + (id - row.ContainerStart), true
		}
	}
	return 0, false
}

func managedMountSetupSocketV1(file *os.File) (*net.UnixConn, error) {
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, err
	}
	packet, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("setup handoff is not a Unix socket")
	}
	return packet, nil
}

// prepareManagedMountSetupV1 borrows setup-owned, exclusive, unattached storage
// descriptors and their provenance. F2 must supply that resource authority;
// matching ownership is insufficient. The existing mapping operation lock must
// remain held throughout. Success transfers the returned FDs only after helper
// retirement; any failure leaves all effects inhibited for explicit recovery.
func prepareManagedMountSetupV1(ctx context.Context, ops deploy.IdentityMappingOperationsV1, expected deploy.StateV1, active identitymapping.Geometry, operation string, root, staging *os.File, objects []blueprint.ManagedMountObject, recorded map[string]*os.File) (result map[string]preparedManagedMountObjectV1, err error) {
	if ctx == nil || root == nil || staging == nil {
		return nil, fmt.Errorf("setup requires context and held storage descriptors")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err = ops.ObserveEffects(ctx, expected, active, operation); err != nil {
		return nil, err
	}
	ordered, err := orderManagedMountObjectsV1(objects)
	if err != nil {
		return nil, err
	}
	for _, object := range ordered {
		_, uid := managedMountSetupHostIDV1(object.Identity.UID, active.UIDs)
		_, gid := managedMountSetupHostIDV1(object.Identity.GID, active.GIDs)
		if !uid || !gid {
			return nil, fmt.Errorf("setup declaration contains an unmapped identity")
		}
	}
	manifest := managedMountSetupManifestV1{Geometry: active, Objects: ordered, Parent: os.Getpid()}
	for name, file := range recorded {
		if file == nil {
			return nil, fmt.Errorf("nil recorded descriptor")
		}
		manifest.Recorded = append(manifest.Recorded, name)
	}
	sort.Strings(manifest.Recorded)
	content, err := json.Marshal(manifest)
	if err != nil || len(content) > managedMountSetupLimitV1 {
		return nil, fmt.Errorf("setup manifest exceeds its bounded contract: %v", err)
	}
	fd, err := unix.MemfdCreate("reploy-mount-setup", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	frozen := os.NewFile(uintptr(fd), "setup-manifest")
	defer frozen.Close()
	if _, err = frozen.Write(content); err != nil {
		return nil, err
	}
	if _, err = unix.FcntlInt(frozen.Fd(), unix.F_ADD_SEALS, managedMountSetupSealsV1); err != nil {
		return nil, err
	}
	if _, err = frozen.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parent, child := os.NewFile(uintptr(pair[0]), "setup-parent"), os.NewFile(uintptr(pair[1]), "setup-child")
	defer parent.Close()
	defer child.Close()
	channel, err := managedMountSetupSocketV1(parent)
	if err != nil {
		return nil, err
	}
	defer channel.Close()
	deadline, _ := ctx.Deadline()
	if err = channel.SetDeadline(deadline); err != nil {
		return nil, err
	}
	executable, err := os.Open("/proc/self/exe")
	if err != nil {
		return nil, err
	}
	defer executable.Close()
	files := []*os.File{frozen, child, root, staging}
	for _, name := range manifest.Recorded {
		files = append(files, recorded[name])
	}
	files = append(files, executable)
	for _, name := range []string{"unshare", "newuidmap", "newgidmap"} {
		var stat unix.Stat_t
		if err = unix.Stat("/usr/bin/"+name, &stat); err != nil || stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o022 != 0 {
			return nil, fmt.Errorf("setup requires trusted /usr/bin/%s: %v", name, err)
		}
	}
	// Keep the creator thread alive: Linux associates Pdeathsig with that thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	command := exec.CommandContext(ctx, "/usr/bin/unshare", "--user", "--keep-caps", "/proc/self/fd/"+strconv.Itoa(2+len(files)), managedMountSetupArgumentV1)
	command.ExtraFiles, command.Env, command.Stderr = files, []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}, os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err = command.Start(); err != nil {
		return nil, err
	}
	child.Close()
	retired := false
	defer func() {
		if !retired {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		if err != nil {
			for _, object := range result {
				unix.Close(object.FD)
			}
			result = nil
		}
	}()
	stop := context.AfterFunc(ctx, func() { channel.Close() })
	defer stop()
	var signal [1]byte
	if _, err = io.ReadFull(channel, signal[:]); err != nil || signal[0] != 1 {
		return nil, fmt.Errorf("setup readiness failed: %v", err)
	}
	for i, rows := range [][]identitymapping.Interval{active.UIDs, active.GIDs} {
		args := []string{strconv.Itoa(command.Process.Pid)}
		for _, row := range rows {
			args = append(args, strconv.FormatUint(uint64(row.ContainerStart), 10), strconv.FormatUint(uint64(row.HostStart), 10), strconv.FormatUint(uint64(row.Count), 10))
		}
		mapper := exec.CommandContext(ctx, []string{"/usr/bin/newuidmap", "/usr/bin/newgidmap"}[i], args...)
		mapper.Env, mapper.Stderr = command.Env, os.Stderr
		if err = mapper.Run(); err != nil {
			return nil, fmt.Errorf("install complete setup map: %w", err)
		}
	}
	// Neither the manifest nor an earlier successful check is current authority.
	if _, err = ops.ObserveEffects(ctx, expected, active, operation); err != nil {
		return nil, err
	}
	if _, err = channel.Write([]byte{2}); err != nil {
		return nil, err
	}
	result = make(map[string]preparedManagedMountObjectV1, len(ordered))
	for _, object := range ordered {
		var data [512]byte
		rights := make([]byte, unix.CmsgSpace(4))
		n, oobn, flags, _, readErr := channel.ReadMsgUnix(data[:], rights)
		messages, parseErr := unix.ParseSocketControlMessage(rights[:max(0, oobn)])
		var received []int
		for _, message := range messages {
			fds, e := unix.ParseUnixRights(&message)
			parseErr = errors.Join(parseErr, e)
			received = append(received, fds...)
		}
		var identity managedMountObjectIdentityV1
		decodeErr := json.Unmarshal(data[:max(0, n)], &identity)
		if readErr != nil || parseErr != nil || decodeErr != nil || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 || len(received) != 1 {
			for _, fd := range received {
				unix.Close(fd)
			}
			return result, fmt.Errorf("invalid setup descriptor response: %v %v %v", readErr, parseErr, decodeErr)
		}
		unix.CloseOnExec(received[0])
		result[object.Path] = preparedManagedMountObjectV1{FD: received[0], Identity: identity}
		actual, checkErr := managedMountObjectIdentityForFDV1(received[0])
		translated := identity
		for i, rows := range [][]identitymapping.Interval{active.UIDs, active.GIDs} {
			id := &translated.UID
			if i == 1 {
				id = &translated.GID
			}
			translatedID, mapped := managedMountSetupHostIDV1(*id, rows)
			if !mapped {
				return result, fmt.Errorf("setup returned an unmapped identity")
			}
			*id = translatedID
		}
		if checkErr != nil || actual != translated {
			return result, fmt.Errorf("setup handoff identity differs from held descriptor: %v", checkErr)
		}
	}
	err = command.Wait()
	retired = true
	if err != nil {
		return result, fmt.Errorf("setup helper did not complete: %w", err)
	}
	return result, ctx.Err()
}

func runManagedMountSetupChildV1() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	frozen := os.NewFile(3, "setup-manifest")
	defer frozen.Close()
	seals, err := unix.FcntlInt(frozen.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || seals&managedMountSetupSealsV1 != managedMountSetupSealsV1 {
		return fmt.Errorf("setup requires a sealed manifest")
	}
	data, err := io.ReadAll(io.LimitReader(frozen, managedMountSetupLimitV1+1))
	if err != nil || len(data) > managedMountSetupLimitV1 {
		return fmt.Errorf("invalid bounded setup manifest")
	}
	var manifest managedMountSetupManifestV1
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil {
		return err
	}
	geometry, err := identitymapping.ValidateGeometry(manifest.Geometry)
	if err != nil || !reflect.DeepEqual(geometry, manifest.Geometry) || manifest.Parent != os.Getppid() {
		return fmt.Errorf("invalid setup geometry or parent")
	}
	socket := os.NewFile(4, "setup-channel")
	defer socket.Close()
	channel, err := managedMountSetupSocketV1(socket)
	if err != nil {
		return err
	}
	defer channel.Close()
	if err = channel.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	if _, err = channel.Write([]byte{1}); err != nil {
		return err
	}
	var signal [1]byte
	if _, err = io.ReadFull(channel, signal[:]); err != nil || signal[0] != 2 {
		return fmt.Errorf("setup release refused: %v", err)
	}
	for i, rows := range [][]identitymapping.Interval{geometry.UIDs, geometry.GIDs} {
		data, readErr := os.ReadFile([]string{"/proc/self/uid_map", "/proc/self/gid_map"}[i])
		actual, parseErr := mappedKernelRowsV1(data)
		if readErr != nil || parseErr != nil || !reflect.DeepEqual(actual, rows) {
			return fmt.Errorf("setup kernel map differs from selected geometry")
		}
	}
	if err = syscall.Setgroups(nil); err != nil {
		return err
	}
	if err = syscall.Setresgid(0, 0, 0); err != nil {
		return err
	}
	if err = syscall.Setresuid(0, 0, 0); err != nil {
		return err
	}
	// Credential changes clear Pdeathsig. Re-arm and check the original parent
	// before any storage effect, closing the parent-death race during transition.
	if err = unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGKILL), 0, 0, 0); err != nil || manifest.Parent != os.Getppid() {
		return fmt.Errorf("setup lost its original parent: %v", err)
	}
	if err = unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	recorded := make(map[string]int, len(manifest.Recorded))
	for i, name := range manifest.Recorded {
		recorded[name] = 7 + i
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prepared, err := prepareManagedMountObjectsV1(ctx, 5, 6, manifest.Objects, recorded)
	if err != nil {
		return err
	}
	defer func() {
		for _, object := range prepared {
			unix.Close(object.FD)
		}
	}()
	for _, declaration := range manifest.Objects {
		object := prepared[declaration.Path]
		data, err := json.Marshal(object.Identity)
		if err != nil {
			return err
		}
		if _, _, err = channel.WriteMsgUnix(data, unix.UnixRights(object.FD), nil); err != nil {
			return err
		}
	}
	return nil
}
