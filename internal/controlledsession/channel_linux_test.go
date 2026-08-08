//go:build linux

package controlledsession

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPrivateChannelV1CreatesOneControllerOwnedClaim(t *testing.T) {
	channel, config := prepareCurrentIdentityChannelV1(t)
	socket := channel.SocketPath()
	controllerIdentity := config.Opened.Authorization.Controller.RuntimeIdentity
	assertChannelPathV1(t, config.HostDirectory, 0o700, controllerIdentity)
	assertChannelPathV1(t, socket, 0o600, controllerIdentity)

	claimResult := make(chan struct {
		connection *ControllerConnectionV1
		err        error
	}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		connection, err := channel.Claim(ctx)
		claimResult <- struct {
			connection *ControllerConnectionV1
			err        error
		}{connection: connection, err: err}
	}()
	client := dialPrivateChannelV1(t, socket)
	opened, err := ReadEventV1(client)
	if err != nil {
		t.Fatal(err)
	}
	wantOpened := EventV1{Kind: EventOpenedV1, Opened: &config.Opened}
	if !reflect.DeepEqual(opened, wantOpened) {
		t.Fatalf("opened event = %#v, want %#v", opened, wantOpened)
	}
	result := <-claimResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claimed socket pathname still exists: %v", err)
	}
	if _, err := channel.Claim(ctx); err == nil || !strings.Contains(err.Error(), "already been claimed") {
		t.Fatalf("second Claim() error = %v", err)
	}

	wantRequest := RequestV1{Kind: RequestInputV1, Bytes: []byte{0, 3, 0xff}}
	if err := WriteRequestV1(client, wantRequest); err != nil {
		t.Fatal(err)
	}
	request, err := result.connection.ReadRequest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request, wantRequest) {
		t.Fatalf("request = %#v, want %#v", request, wantRequest)
	}
	wantEvent := EventV1{Kind: EventOutputV1, Bytes: []byte("ordered output")}
	if err := result.connection.WriteEvent(ctx, wantEvent); err != nil {
		t.Fatal(err)
	}
	event, err := ReadEventV1(client)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event, wantEvent) {
		t.Fatalf("event = %#v, want %#v", event, wantEvent)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := result.connection.ReadRequest(ctx); !errors.Is(err, ErrControllerDisconnectedV1) {
		t.Fatalf("ReadRequest() disconnect error = %v", err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(config.HostDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("channel directory remains after Close: %v", err)
	}
}

func TestPrivateChannelV1ClassifiesClaimFailures(t *testing.T) {
	t.Run("before claim", func(t *testing.T) {
		channel, _ := prepareCurrentIdentityChannelV1(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := channel.Claim(ctx)
		phase, ok := ClaimFailurePhaseV1(err)
		if !ok || phase != ChannelBeforeClaimV1 || !errors.Is(err, context.Canceled) {
			t.Fatalf("Claim() error = %v, phase = %q/%t", err, phase, ok)
		}
	})

	t.Run("after claim", func(t *testing.T) {
		config := currentIdentityChannelConfigV1(t)
		server, client := net.Pipe()
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		channel := &PrivateChannelV1{
			opened:      EventV1{Kind: EventOpenedV1, Opened: &config.Opened},
			expectedUID: uint32(os.Geteuid()), expectedGID: uint32(os.Getegid()),
			transport: &fixedPrivateChannelTransportV1{
				connection: server, uid: uint32(os.Geteuid()), gid: uint32(os.Getegid()),
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := channel.Claim(ctx)
		phase, ok := ClaimFailurePhaseV1(err)
		if !ok || phase != ChannelAfterClaimV1 || !errors.Is(err, ErrControllerDisconnectedV1) {
			t.Fatalf("Claim() error = %v, phase = %q/%t", err, phase, ok)
		}
	})

	t.Run("wrong peer identity", func(t *testing.T) {
		config := currentIdentityChannelConfigV1(t)
		cleanupFailure := errors.New("cleanup failure")
		server, client := net.Pipe()
		defer client.Close()
		channel := &PrivateChannelV1{
			opened:      EventV1{Kind: EventOpenedV1, Opened: &config.Opened},
			expectedUID: uint32(os.Geteuid()), expectedGID: uint32(os.Getegid()),
			transport: &fixedPrivateChannelTransportV1{
				connection: server, uid: uint32(os.Geteuid()) + 1, gid: uint32(os.Getegid()), closeErr: cleanupFailure,
			},
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := channel.Claim(ctx)
		phase, ok := ClaimFailurePhaseV1(err)
		if !ok || phase != ChannelBeforeClaimV1 || !strings.Contains(err.Error(), "peer identity") ||
			!errors.Is(err, cleanupFailure) || !strings.Contains(err.Error(), "clean up controlled-session channel") {
			t.Fatalf("Claim() error = %v, phase = %q/%t", err, phase, ok)
		}
	})
}

func TestPreparePrivateChannelV1PreservesPreexistingLeaseDirectory(t *testing.T) {
	config := currentIdentityChannelConfigV1(t)
	if err := os.Mkdir(config.HostDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(config.HostDirectory, "user-data")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreparePrivateChannelV1(config); err == nil || !strings.Contains(err.Error(), "fresh channel directory") {
		t.Fatalf("PreparePrivateChannelV1() error = %v", err)
	}
	payload, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("pre-existing directory was modified: %v", err)
	}
	if string(payload) != "preserve" {
		t.Fatalf("pre-existing sentinel = %q", payload)
	}
}

func TestPreparePrivateChannelV1RejectsOverlongSocketPathBeforeCreatingDirectory(t *testing.T) {
	config := currentIdentityChannelConfigV1(t)
	config.HostDirectory = filepath.Join(filepath.Dir(config.HostDirectory), strings.Repeat("x", 128))
	if _, err := PreparePrivateChannelV1(config); err == nil ||
		!strings.Contains(err.Error(), "exceeding the Linux AF_UNIX maximum") ||
		!strings.Contains(err.Error(), "use a shorter deployment path") {
		t.Fatalf("PreparePrivateChannelV1() error = %v", err)
	}
	if _, err := os.Lstat(config.HostDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("overlong channel directory was created: %v", err)
	}
}

type fixedPrivateChannelTransportV1 struct {
	connection net.Conn
	uid        uint32
	gid        uint32
	closeErr   error
}

func (transport *fixedPrivateChannelTransportV1) Accept(context.Context) (net.Conn, uint32, uint32, error) {
	return transport.connection, transport.uid, transport.gid, nil
}

func (*fixedPrivateChannelTransportV1) StopAccepting() error   { return nil }
func (transport *fixedPrivateChannelTransportV1) Close() error { return transport.closeErr }

func TestPrivateChannelV1RejectsMalformedAndOversizedRequests(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "malformed", data: []byte("not-a-frame"), want: "magic"},
		{name: "oversized", data: func() []byte {
			header := make([]byte, frameHeaderSizeV1)
			copy(header, frameMagicV1[:])
			header[4] = ProtocolVersionV1
			header[5] = byte(wireRequestInputV1)
			binary.BigEndian.PutUint32(header[6:], MaxFramePayloadV1+1)
			return header
		}(), want: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel, _ := prepareCurrentIdentityChannelV1(t)
			server, client := claimPrivateChannelV1(t, channel)
			if _, err := ReadEventV1(client); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Write(test.data); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := server.ReadRequest(ctx); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReadRequest() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestControllerConnectionV1BoundsAndSerializesFlow(t *testing.T) {
	t.Run("read cancellation", func(t *testing.T) {
		server, client := net.Pipe()
		defer client.Close()
		connection := &ControllerConnectionV1{connection: server}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if _, err := connection.ReadRequest(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ReadRequest() error = %v", err)
		}
	})

	t.Run("write backpressure", func(t *testing.T) {
		server, client := net.Pipe()
		defer client.Close()
		connection := &ControllerConnectionV1{connection: server}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		event := EventV1{Kind: EventOutputV1, Bytes: bytes.Repeat([]byte{'x'}, MaxFramePayloadV1)}
		if err := connection.WriteEvent(ctx, event); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("WriteEvent() error = %v", err)
		}
	})

	t.Run("serialized frames", func(t *testing.T) {
		server, client := net.Pipe()
		defer client.Close()
		connection := &ControllerConnectionV1{connection: server}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		events := []EventV1{
			{Kind: EventOutputV1, Bytes: bytes.Repeat([]byte{'a'}, 128*1024)},
			{Kind: EventOutputV1, Bytes: bytes.Repeat([]byte{'b'}, 128*1024)},
		}
		errorsByWriter := make(chan error, len(events))
		for _, event := range events {
			event := event
			go func() { errorsByWriter <- connection.WriteEvent(ctx, event) }()
		}
		got := make([]EventV1, 0, len(events))
		for range events {
			event, err := ReadEventV1(client)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, event)
		}
		for range events {
			if err := <-errorsByWriter; err != nil {
				t.Fatal(err)
			}
		}
		if !(reflect.DeepEqual(got, events) || reflect.DeepEqual(got, []EventV1{events[1], events[0]})) {
			t.Fatalf("serialized events = %#v", got)
		}
	})
}

func TestPreparePrivateChannelV1FreezesOpenedAuthorization(t *testing.T) {
	channel, config := prepareCurrentIdentityChannelV1(t)
	config.Opened.Authorization.Handle = "session-" + strings.Repeat("b", 64)
	server, client := claimPrivateChannelV1(t, channel)
	defer server.Close()
	event, err := ReadEventV1(client)
	if err != nil {
		t.Fatal(err)
	}
	if event.Opened.Authorization.Handle == config.Opened.Authorization.Handle {
		t.Fatal("opened event followed caller mutation after channel preparation")
	}
}

func prepareCurrentIdentityChannelV1(t *testing.T) (*PrivateChannelV1, PrivateChannelConfigV1) {
	t.Helper()
	config := currentIdentityChannelConfigV1(t)
	channel, err := PreparePrivateChannelV1(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = channel.Close() })
	return channel, config
}

func currentIdentityChannelConfigV1(t *testing.T) PrivateChannelConfigV1 {
	t.Helper()
	uid := os.Geteuid()
	gid := os.Getegid()
	identity := RuntimeIdentityV1{
		Username: "reploy", UID: strconv.Itoa(uid), GID: strconv.Itoa(gid), SupplementaryGIDs: []string{},
	}
	if uid == 0 {
		identity.Username = "root"
	}
	authorization := testAuthorizationV1()
	authorization.Controller.RuntimeIdentity = identity
	config := PrivateChannelConfigV1{
		HostDirectory: filepath.Join(shortChannelTestDirectoryV1(t), "session"),
		Opened: OpenedV1{
			Authorization: authorization, Columns: 80, Rows: 24,
			OutputFinalizationTimeoutMilliseconds: DefaultOutputFinalizationTimeoutMillisecondsV1,
		},
	}
	return config
}

func shortChannelTestDirectoryV1(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "reploy-cs-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove short channel test directory: %v", err)
		}
	})
	return directory
}

func claimPrivateChannelV1(t *testing.T, channel *PrivateChannelV1) (*ControllerConnectionV1, *net.UnixConn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	result := make(chan struct {
		connection *ControllerConnectionV1
		err        error
	}, 1)
	go func() {
		connection, err := channel.Claim(ctx)
		result <- struct {
			connection *ControllerConnectionV1
			err        error
		}{connection: connection, err: err}
	}()
	client := dialPrivateChannelV1(t, channel.SocketPath())
	claimed := <-result
	if claimed.err != nil {
		t.Fatal(claimed.err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return claimed.connection, client
}

func dialPrivateChannelV1(t *testing.T, socket string) *net.UnixConn {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
		if err == nil {
			return connection
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial private channel: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func assertChannelPathV1(t *testing.T, path string, mode os.FileMode, identity RuntimeIdentityV1) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %04o, want %04o", path, info.Mode().Perm(), mode)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%s has no Unix stat", path)
	}
	if strconv.FormatUint(uint64(stat.Uid), 10) != identity.UID || strconv.FormatUint(uint64(stat.Gid), 10) != identity.GID {
		t.Fatalf("%s owner = %d:%d, want %s:%s", path, stat.Uid, stat.Gid, identity.UID, identity.GID)
	}
}
