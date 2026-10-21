//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestNetworkPositiveControlsRequireReachableEcho(t *testing.T) {
	for _, network := range []string{"tcp4", "udp4"} {
		t.Run(network, func(t *testing.T) {
			var address string
			if network == "tcp4" {
				listener, err := net.Listen(network, "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				address = listener.Addr().String()
				go echoTCP(listener)
			} else {
				conn, err := net.ListenPacket(network, "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				address = conn.LocalAddr().String()
				go echoUDP(conn)
			}
			if err := networkRoundTrip(network, address); err != nil {
				t.Fatal(err)
			}
			if err := networkRoundTrip(network, "127.0.0.1:0"); err == nil {
				t.Fatal("unreachable control passed")
			}
		})
	}
}

func TestUDPClientSelfEchoDoesNotProveProducerReachability(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	address := listener.LocalAddr().(*net.UDPAddr)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp4", address, address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := networkRoundTripConn("udp4", conn); err == nil || !strings.Contains(err.Error(), "unexpected UDP response") {
		t.Fatalf("self-echo must be rejected as an unframed request: %v", err)
	}
}

func TestTCPControlEchoPreservesFragmentedPayload(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go echoTCP(listener)
	conn, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	for _, value := range []byte("reploy-network-control") {
		if _, err := conn.Write([]byte{value}); err != nil {
			t.Fatal(err)
		}
		var received [1]byte
		if _, err := io.ReadFull(conn, received[:]); err != nil {
			t.Fatal(err)
		}
		if received[0] != value {
			t.Fatalf("incorrect fragmented echo: %d, want %d", received[0], value)
		}
	}
}

func TestNetworkPositiveControlRejectsWrongPayload(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		buffer := make([]byte, len("reploy-network-control"))
		if _, err := conn.Read(buffer); err == nil {
			_, _ = conn.Write([]byte(strings.Repeat("x", len(buffer))))
		}
	}()
	if err := networkRoundTrip("tcp4", listener.Addr().String()); err == nil {
		t.Fatal("incorrect echo payload passed")
	}
}

func TestReadyPublicationNeverExposesPartialJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service-ready")
	if err := publishReady(path, []byte(`{"pass":true}`)); err != nil {
		t.Fatal(err)
	}
	stop, result := make(chan struct{}), make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				result <- nil
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				result <- err
				return
			}
			if !json.Valid(data) {
				result <- fmt.Errorf("partial readiness JSON: %d bytes", len(data))
				return
			}
		}
	}()
	for index := 0; index < 100; index++ {
		data := []byte(fmt.Sprintf(`{"pass":true,"index":%d,"data":"%s"}`, index, strings.Repeat("x", 32768)))
		if err := publishReady(path, data); err != nil {
			close(stop)
			<-result
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "service-ready" {
		t.Fatalf("temporary readiness files remain: %v, %v", entries, err)
	}
}

func TestAdmissionWaitRequiresMatchingRelease(t *testing.T) {
	dir := t.TempDir()
	nonce := strings.Repeat("a", 32)
	finished := make(chan error, 1)
	go func() { finished <- awaitAdmission(dir, nonce, time.Second) }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "probe-admission-ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wait never became ready")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-finished:
		t.Fatalf("passed without release: %v", err)
	default:
	}
	if err := releaseAdmission(dir, []string{strings.Repeat("b", 32)}); err == nil {
		t.Fatal("accepted a foreign nonce")
	}
	if err := releaseAdmission(dir, []string{nonce}); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("admission markers were not removed: %v, %v", entries, err)
	}
}

func TestAdmissionReleaseNeverExposesPartialNonce(t *testing.T) {
	dir := t.TempDir()
	nonce := strings.Repeat("a", 32)
	ready := filepath.Join(dir, "probe-admission-ready")
	release := filepath.Join(dir, "probe-admission-release")
	if err := os.WriteFile(ready, []byte(nonce), 0600); err != nil {
		t.Fatal(err)
	}
	if err := releaseAdmission(dir, []string{nonce}); err != nil {
		t.Fatal(err)
	}
	stop, result := make(chan struct{}), make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				result <- nil
				return
			default:
			}
			data, err := os.ReadFile(release)
			if err != nil || string(data) != nonce {
				result <- fmt.Errorf("partial admission nonce: %q, %v", data, err)
				return
			}
		}
	}()
	for index := 0; index < 1000; index++ {
		if err := releaseAdmission(dir, []string{nonce}); err != nil {
			close(stop)
			<-result
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(release)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("release marker permission mismatch: %v, %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("temporary admission files remain: %v, %v", entries, err)
	}
}

func TestAdmissionFailsClosed(t *testing.T) {
	nonce := strings.Repeat("a", 32)
	t.Run("timeout", func(t *testing.T) {
		if err := awaitAdmission(t.TempDir(), nonce, time.Millisecond); err == nil {
			t.Fatal("passed without release")
		}
	})
	t.Run("foreign marker", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "probe-admission-release"), []byte("foreign"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := awaitAdmission(dir, nonce, time.Second); err == nil {
			t.Fatal("accepted foreign release marker")
		}
	})
	t.Run("unwritable ready", func(t *testing.T) {
		if err := awaitAdmission(filepath.Join(t.TempDir(), "missing"), nonce, time.Second); err == nil {
			t.Fatal("ignored ready marker failure")
		}
	})
	t.Run("missing ready", func(t *testing.T) {
		if err := releaseAdmission(t.TempDir(), []string{nonce}); err == nil {
			t.Fatal("released a container that never became ready")
		}
	})
	for _, args := range [][]string{nil, {"short"}, {strings.Repeat("z", 32)}, {nonce, "request"}, {nonce, "supervisor", "extra"}} {
		if err := runAdmissionWait(args); err == nil {
			t.Fatalf("accepted invalid wait arguments: %v", args)
		}
	}
	for _, args := range [][]string{nil, {"short"}, {nonce, "extra"}} {
		if err := releaseAdmission(t.TempDir(), args); err == nil {
			t.Fatalf("accepted invalid release arguments: %v", args)
		}
	}
}

func TestDenialRequiresFailedSyscall(t *testing.T) {
	for _, name := range []string{"cross_sysv_sem", "cross_sysv_msg", "cross_posix_mq"} {
		for _, err := range []error{nil, unix.Errno(0)} {
			if got := denied(name, err); got.Pass || got.Actual != "allowed" || got.Error != "" {
				t.Fatalf("successful syscall reported as denied: %#v", got)
			}
		}
		for _, err := range []error{unix.EPERM, unix.EACCES} {
			if got := denied(name, err); !got.Pass || got.Actual != "denied" {
				t.Fatalf("failed syscall reported as allowed: %#v", got)
			}
		}
	}
}

func sixCanaryManifest(t *testing.T, fd int) string {
	t.Helper()
	manifest, err := canaryManifest([]int{fd, fd, fd, fd, fd, fd})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestDescriptorCanaryDetectsInheritedDuplicate(t *testing.T) {
	for _, kind := range []string{"file", "pipe"} {
		t.Run(kind, func(t *testing.T) {
			var fd int
			var closeOriginal func() error
			peerFD := -1
			if kind == "file" {
				file, err := os.Create(filepath.Join(t.TempDir(), "private"))
				if err != nil {
					t.Fatal(err)
				}
				fd = int(file.Fd())
				closeOriginal = file.Close
				t.Cleanup(func() { _ = file.Close() })
			} else {
				fds := []int{0, 0}
				if err := unix.Pipe(fds); err != nil {
					t.Fatal(err)
				}
				fd = fds[0]
				closeOriginal = func() error { return unix.Close(fd) }
				peerFD = fds[1]
				t.Cleanup(func() {
					if peerFD >= 0 {
						_ = unix.Close(peerFD)
					}
				})
			}
			manifest := sixCanaryManifest(t, fd)
			dup, err := unix.Dup(fd)
			if err != nil {
				t.Fatal(err)
			}
			if err := closeOriginal(); err != nil {
				t.Fatal(err)
			}
			if err := inheritedCanaryCheck(manifest); err == nil || !strings.Contains(err.Error(), "survived") {
				t.Fatalf("inherited duplicate was not detected: %v", err)
			}
			if err := unix.Close(dup); err != nil {
				t.Fatal(err)
			}
			if peerFD >= 0 {
				if err := unix.Close(peerFD); err != nil {
					t.Fatal(err)
				}
				peerFD = -1
			}
			if err := inheritedCanaryCheck(manifest); err != nil {
				t.Fatalf("closed canary reported as inherited: %v", err)
			}
		})
	}
}

func TestDescriptorCanaryAllowsDescriptorNumberReuse(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	fd := int(file.Fd())
	manifest := sixCanaryManifest(t, fd)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if int(other.Fd()) != fd {
		if err := unix.Dup2(int(other.Fd()), fd); err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
	}
	if err := inheritedCanaryCheck(manifest); err != nil {
		t.Fatalf("unrelated object on reused descriptor rejected: %v", err)
	}
}

func TestDescriptorCanaryManifestFailsClosed(t *testing.T) {
	for _, value := range []string{"", "null", "[]", "{}", "[", strings.Repeat("x", 16385)} {
		if err := inheritedCanaryCheck(value); err == nil {
			t.Fatalf("invalid manifest accepted: %q", value)
		}
	}
	objects := make([]descriptorObject, 6)
	for index := range objects {
		objects[index] = descriptorObject{Device: 1, Inode: 1, Target: "anon_inode:[pidfd]"}
	}
	data, err := json.Marshal(objects)
	if err != nil {
		t.Fatal(err)
	}
	if err := inheritedCanaryCheck(string(data)); err == nil {
		t.Fatal("pidfd without target PID accepted")
	}
	if _, err := canaryManifest([]int{0, 1, 2}); err == nil {
		t.Fatal("partial canary set accepted")
	}
}

func TestDescriptorIdentityBindsPIDFDTarget(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	object, err := descriptorIdentity(fd)
	if err != nil {
		t.Fatal(err)
	}
	if object.Target != "anon_inode:[pidfd]" || object.PID != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pidfd target was not recorded: %#v", object)
	}
}

func TestDescriptorCanaryDetectsInheritedPIDFD(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	manifest := sixCanaryManifest(t, fd)
	dup, err := unix.Dup(fd)
	if err != nil {
		_ = unix.Close(fd)
		t.Fatal(err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatal(err)
	}
	if err := inheritedCanaryCheck(manifest); err == nil {
		_ = unix.Close(dup)
		t.Fatal("inherited pidfd was not detected")
	}
	if err := unix.Close(dup); err != nil {
		t.Fatal(err)
	}
	if err := inheritedCanaryCheck(manifest); err != nil {
		t.Fatalf("closed pidfd reported as inherited: %v", err)
	}
}

func TestDescriptorCanaryDetectsRenamedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	manifest := sixCanaryManifest(t, int(file.Fd()))
	if err := os.Rename(path, path+"-renamed"); err != nil {
		t.Fatal(err)
	}
	if err := inheritedCanaryCheck(manifest); err == nil {
		t.Fatal("renamed canary file was not detected")
	}
}
