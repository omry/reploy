//go:build linux

package probe

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/mdlayher/netlink"
	"github.com/omry/reploy/internal/canonical"
	"golang.org/x/sys/unix"
)

func TestMappedExpectedNetworkRulesNoIOV1(t *testing.T) {
	for _, policy := range []applicationNetworkPolicyV1{{}, {
		AllowPublic: true, AllowLocal: true, AllowAmbiguous: true, InboundTCP: []uint16{443, 80, 443},
	}} {
		blocked := errors.New("unexpected kernel exchange")
		exchanges := 0
		connection, err := nftables.New(nftables.WithTestDial(func([]netlink.Message) ([]netlink.Message, error) {
			exchanges++
			return nil, blocked
		}))
		if err != nil {
			t.Fatal(err)
		}
		builder := &applicationNetworkRuleBuilderV1{Conn: connection}
		if err := applicationNetworkRulesV1(builder, policy, []string{"192.0.2.53/32"}); err != nil {
			t.Fatalf("expected-rule generation requires kernel access: %v", err)
		}
		if exchanges != 0 || len(builder.rules) == 0 {
			t.Fatalf("generation exchanged %d messages or omitted rules", exchanges)
		}
		// Prove the transport trap is live, without changing any real rules.
		if err := builder.Flush(); !errors.Is(err, blocked) || exchanges == 0 {
			t.Fatalf("explicit flush missed the kernel-exchange control: %v", err)
		}
	}
}

func TestMappedNetworkExpressionKernelABIV1(t *testing.T) {
	marshal := func(attributes []netlink.Attribute) []byte {
		t.Helper()
		for i := range attributes {
			attributes[i].Length = 0
		}
		data, err := netlink.MarshalAttributes(attributes)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	unmarshal := func(data []byte) []netlink.Attribute {
		t.Helper()
		attributes, err := netlink.UnmarshalAttributes(data)
		if err != nil {
			t.Fatal(err)
		}
		return attributes
	}
	var unflag func([]byte) []byte
	unflag = func(data []byte) []byte {
		attributes := unmarshal(data)
		for i := range attributes {
			if attributes[i].Type&unix.NLA_F_NESTED != 0 {
				attributes[i].Data = unflag(attributes[i].Data)
			}
			attributes[i].Type &= 0x3fff
		}
		return marshal(attributes)
	}
	for _, value := range []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeyDST, Direction: 1},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: []byte{255, 255, 0, 0}, Xor: make([]byte, 4)},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{1, 0, 0, 0, 0, 0, 0, 0}},
		&expr.Verdict{Kind: expr.VerdictAccept},
	} {
		original, err := expr.Marshal(byte(nftables.TableFamilyIPv4), value)
		if err != nil {
			t.Fatal(err)
		}
		attributes := unmarshal(original)
		for i := range attributes {
			if attributes[i].Type&0x3fff != unix.NFTA_EXPR_DATA {
				continue
			}
			fields := unmarshal(attributes[i].Data)
			switch value.(type) {
			case *expr.Ct:
				for j := range fields {
					if fields[j].Type == unix.NFTA_CT_DIRECTION {
						fields[j].Data = []byte{1}
					}
				}
			case *expr.Bitwise:
				fields = append(fields, netlink.Attribute{Type: mappedNFTABitwiseOpV1, Data: make([]byte, 4)})
			}
			attributes[i].Data = marshal(fields)
		}
		observed := unflag(marshal(attributes))
		want, err := mappedNetworkExpressionV1(original)
		if err != nil {
			t.Fatal(err)
		}
		got, err := mappedNetworkExpressionV1(observed)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("kernel %T encoding changed semantics: %v\noriginal=%x observed=%x\nwant=%s\ngot=%s", value, err, original, observed, want, got)
		}
		// An additional unknown field must remain visible to comparison.
		attributes = unmarshal(observed)
		for i := range attributes {
			if attributes[i].Type == unix.NFTA_EXPR_DATA {
				fields := unmarshal(attributes[i].Data)
				fields = append(fields, netlink.Attribute{Type: 123, Data: []byte{1}})
				attributes[i].Data = marshal(fields)
			}
		}
		changed, err := mappedNetworkExpressionV1(marshal(attributes))
		if err == nil && bytes.Equal(changed, want) {
			t.Fatalf("unknown %T field disappeared", value)
		}
	}
	makeBitwise := func(operation uint32) []byte {
		return marshal([]netlink.Attribute{
			{Type: unix.NFTA_EXPR_NAME, Data: []byte("bitwise\x00")},
			{Type: unix.NFTA_EXPR_DATA, Data: marshal([]netlink.Attribute{{Type: mappedNFTABitwiseOpV1, Data: []byte{0, 0, 0, byte(operation)}}})},
		})
	}
	zero, err := mappedNetworkExpressionV1(makeBitwise(0))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := mappedNetworkExpressionV1(makeBitwise(1))
	if err != nil || bytes.Equal(zero, changed) {
		t.Fatal("non-default bitwise operation lost its meaning", err)
	}
	duplicate := marshal([]netlink.Attribute{
		{Type: unix.NFTA_EXPR_NAME, Data: []byte("bitwise\x00")},
		{Type: unix.NFTA_EXPR_DATA, Data: marshal([]netlink.Attribute{
			{Type: mappedNFTABitwiseOpV1, Data: make([]byte, 4)}, {Type: mappedNFTABitwiseOpV1, Data: make([]byte, 4)},
		})},
	})
	if _, err := mappedNetworkExpressionV1(duplicate); err == nil {
		t.Fatal("duplicate default operation was silently normalized")
	}
}

func TestMappedNetworkObservationInputV1(t *testing.T) {
	request, err := canonical.Sum("mapped-network-test", "v1", "request")
	if err != nil {
		t.Fatal(err)
	}
	input := MappedNetworkObservationV1{Request: request, Namespace: "net:[0]"}
	encoded, err := canonical.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		input io.Reader
		want  string
	}{
		{"oversized", strings.NewReader(strings.Repeat("x", (512<<10)+1)), "read bounded mapped observation"},
		{"read error", iotest.ErrReader(errors.New("read refused")), "read bounded mapped observation"},
		{"malformed", strings.NewReader("{"), "unexpected end"},
		{"unbound", strings.NewReader("{}"), "canonical bound request"},
		{"noncanonical", bytes.NewReader(append([]byte(" "), encoded...)), "canonical bound request"},
		{"unknown field", bytes.NewReader(append(append([]byte{}, encoded[:len(encoded)-1]...), []byte(`,"Extra":true}`)...)), "canonical bound request"},
		{"wrong namespace", bytes.NewReader(encoded), "different network namespace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output, diagnostic bytes.Buffer
			if code := Main([]string{"mapped-sandbox-observe-v1"}, test.input, &output, &diagnostic); code != 1 || output.Len() != 0 || !strings.Contains(diagnostic.String(), test.want) {
				t.Fatalf("invalid observation produced a reply or unrelated refusal: code=%d output=%s error=%s", code, output.String(), diagnostic.String())
			}
		})
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, cancelled} {
		if err := VerifyMappedNetworkPolicyV1(ctx, input); err == nil || !strings.Contains(err.Error(), "live context") {
			t.Fatalf("invalid observation context was accepted: %v", err)
		}
	}
	input.Arguments = []string{"--mapped-capabilities=0000000000000000", "--uid", "0", "--gid", "0", "--public", "deny", "--local", "deny", "--ambiguous", "require-both", "--", "/probe"}
	input.Public = true
	if err := VerifyMappedNetworkPolicyV1(t.Context(), input); err == nil || !strings.Contains(err.Error(), "locked startup policy") {
		t.Fatalf("request changed the frozen network policy: %v", err)
	}
}

func TestMappedNetworkReadBoundV1(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, connection, cleanup, err := mappedNetworkReadConnectionV1(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			done := make(chan error, 1)
			go func() {
				_, err := connection.Receive() // No request or subscription can produce a reply.
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("unrequested kernel receive did not block: %v", err)
			case <-time.After(10 * time.Millisecond):
			}
			limit := 8 * time.Second
			if mode == "cancel" {
				cancel()
				limit = time.Second
			}
			select {
			case err := <-done:
				if err == nil || mode == "deadline" && !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("unrequested read returned an unrelated result: %v", err)
				}
			case <-time.After(limit):
				cleanup()
				<-done
				t.Fatal("observation read did not honor its bound")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := mappedNetworkReadConnectionV1(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled observer connection opened: %v", err)
	}
}

func TestMappedNetworkDumpCollectV1(t *testing.T) {
	for _, dump := range []struct {
		name    string
		command uint16
		data    []byte
	}{
		{"tables", unix.NFT_MSG_GETTABLE, []byte{unix.NFPROTO_IPV4, 0, 0, 0}},
		{"chains", unix.NFT_MSG_GETCHAIN, []byte{unix.NFPROTO_IPV6, 0, 0, 0}},
		{"rules", unix.NFT_MSG_GETRULE, []byte{unix.NFPROTO_IPV4, 0, 0, 0}},
	} {
		t.Run(dump.name, func(t *testing.T) {
			t.Run("multipart completion", func(t *testing.T) {
				raw, receiver, sender, sourcePID, receiverPID := mappedNetworkDumpSocketsV1(t)
				request := mappedNetworkDumpRequestV1(dump.command, dump.data, receiverPID)
				want := netlink.Message{Header: netlink.Header{
					Length: 20, Type: netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | unix.NFT_MSG_NEWTABLE),
					Flags: netlink.Multi, Sequence: request.Header.Sequence, PID: receiverPID,
				}, Data: []byte{unix.NFPROTO_IPV4, 0, 0, 0}}
				mappedNetworkSendDumpMessageV1(t, sender, receiverPID, want)
				mappedNetworkSendDumpMessageV1(t, sender, receiverPID, netlink.Message{Header: netlink.Header{
					Length: 16, Type: netlink.Done, Flags: netlink.Multi, Sequence: request.Header.Sequence, PID: receiverPID,
				}})
				got, err := mappedNetworkCollectV1(raw, request, sourcePID)
				if err != nil || len(got) != 1 || got[0].Header.Sequence != request.Header.Sequence || !bytes.Equal(got[0].Data, want.Data) {
					t.Fatalf("bounded multipart read changed its reply: messages=%+v error=%v", got, err)
				}
				var next [1]byte
				if n, _, err := unix.Recvfrom(receiver, next[:], unix.MSG_PEEK|unix.MSG_DONTWAIT); n >= 0 || !errors.Is(err, unix.EAGAIN) {
					t.Fatalf("collector returned before multipart completion: n=%d err=%v", n, err)
				}
			})

			t.Run("sequence, sender, and kernel errors", func(t *testing.T) {
				for _, control := range []string{"sequence", "sender", "kernel error"} {
					t.Run(control, func(t *testing.T) {
						raw, _, sender, sourcePID, receiverPID := mappedNetworkDumpSocketsV1(t)
						request := mappedNetworkDumpRequestV1(dump.command, dump.data, receiverPID)
						message := netlink.Message{Header: netlink.Header{
							Length: 20, Type: netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | unix.NFT_MSG_NEWTABLE),
							Flags: netlink.Multi, Sequence: request.Header.Sequence, PID: receiverPID,
						}, Data: []byte{unix.NFPROTO_IPV4, 0, 0, 0}}
						wantError := ""
						senderExpected := sourcePID
						if control == "sequence" {
							message.Header.Sequence++
							wantError = "sequence"
						} else if control == "sender" {
							senderExpected++
							wantError = "datagram"
						} else {
							message.Header.Type = netlink.Error
							message.Header.Flags = 0
							message.Data = make([]byte, 4)
							binary.NativeEndian.PutUint32(message.Data, ^uint32(unix.EPERM-1))
						}
						mappedNetworkSendDumpMessageV1(t, sender, receiverPID, message)
						_, err := mappedNetworkCollectV1(raw, request, senderExpected)
						if control == "kernel error" {
							if !errors.Is(err, unix.EPERM) {
								t.Fatalf("kernel error response was ignored: %v", err)
							}
						} else if err == nil || !strings.Contains(err.Error(), wantError) {
							t.Fatalf("invalid %s response was accepted: %v", control, err)
						}
					})
				}
			})

			t.Run("dump_interrupted", func(t *testing.T) {
				for _, location := range []string{"data_record", "done"} {
					t.Run(location, func(t *testing.T) {
						raw, _, sender, sourcePID, receiverPID := mappedNetworkDumpSocketsV1(t)
						request := mappedNetworkDumpRequestV1(dump.command, dump.data, receiverPID)
						message := netlink.Message{Header: netlink.Header{
							Type:  netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | unix.NFT_MSG_NEWTABLE),
							Flags: netlink.Multi | netlink.DumpInterrupted, Sequence: request.Header.Sequence, PID: receiverPID,
						}, Data: []byte{unix.NFPROTO_IPV4, 0, 0, 0}}
						if location == "done" {
							message.Header.Type, message.Data = netlink.Done, nil
						}
						mappedNetworkSendDumpMessageV1(t, sender, receiverPID, message)
						if location == "data_record" {
							mappedNetworkSendDumpMessageV1(t, sender, receiverPID, netlink.Message{Header: netlink.Header{
								Type: netlink.Done, Flags: netlink.Multi, Sequence: request.Header.Sequence, PID: receiverPID,
							}})
						}
						got, err := mappedNetworkCollectV1(raw, request, sourcePID)
						if err == nil || err.Error() != "kernel marked network dump interrupted" || got != nil {
							t.Fatalf("interrupted %s dump returned success or an unrelated refusal: messages=%+v error=%v", location, got, err)
						}
					})
				}
			})

			for _, budget := range []struct {
				name       string
				messages   int
				dataLength int
			}{
				{"message count", mappedNetworkDumpMessagesV1 + 2, 4},
				{"aggregate bytes", 260, 4096},
			} {
				t.Run(budget.name, func(t *testing.T) {
					raw, receiver, sender, sourcePID, receiverPID := mappedNetworkDumpSocketsV1(t)
					request := mappedNetworkDumpRequestV1(dump.command, dump.data, receiverPID)
					for i := 0; i < budget.messages; i++ {
						data := make([]byte, budget.dataLength)
						data[0] = byte(i)
						mappedNetworkSendDumpMessageV1(t, sender, receiverPID, netlink.Message{Header: netlink.Header{
							Length: uint32((16 + len(data) + 3) &^ 3),
							Type:   netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | unix.NFT_MSG_NEWTABLE), Flags: netlink.Multi,
							Sequence: request.Header.Sequence, PID: receiverPID,
						}, Data: data})
					}
					mappedNetworkSendDumpMessageV1(t, sender, receiverPID, netlink.Message{Header: netlink.Header{
						Length: 16, Type: netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | unix.NFT_MSG_NEWTABLE),
						Flags: netlink.Multi, Sequence: request.Header.Sequence, PID: receiverPID,
					}, Data: []byte{1, 2, 3, 4}})
					mappedNetworkSendDumpMessageV1(t, sender, receiverPID, netlink.Message{Header: netlink.Header{
						Length: 16, Type: netlink.Done, Flags: netlink.Multi, Sequence: request.Header.Sequence, PID: receiverPID,
					}})
					if _, err := mappedNetworkCollectV1(raw, request, sourcePID); err == nil || !strings.Contains(err.Error(), "excessive network dump data") {
						t.Fatalf("oversized multipart dump was accepted: %v", err)
					}
					var next [1]byte
					if n, _, err := unix.Recvfrom(receiver, next[:], unix.MSG_PEEK|unix.MSG_DONTWAIT); err != nil || n == 0 {
						t.Fatalf("collector consumed the full oversized stream: n=%d err=%v", n, err)
					}
				})
			}

			t.Run("oversized datagram", func(t *testing.T) {
				raw, _, sender, sourcePID, receiverPID := mappedNetworkDumpSocketsV1(t)
				request := mappedNetworkDumpRequestV1(dump.command, dump.data, receiverPID)
				data := make([]byte, mappedNetworkDatagramV1)
				message := netlink.Message{Header: netlink.Header{
					Length: uint32(16 + len(data)), Type: netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | unix.NFT_MSG_NEWTABLE),
					Flags: netlink.Multi, Sequence: request.Header.Sequence, PID: receiverPID,
				}, Data: data}
				mappedNetworkSendDumpMessageV1(t, sender, receiverPID, message)
				mappedNetworkSendDumpMessageV1(t, sender, receiverPID, netlink.Message{Header: netlink.Header{
					Length: 16, Type: netlink.Done, Flags: netlink.Multi, Sequence: request.Header.Sequence, PID: receiverPID,
				}})
				if _, err := mappedNetworkCollectV1(raw, request, sourcePID); err == nil {
					t.Fatal("oversized datagram was accepted")
				}
			})
		})
	}
}

func mappedNetworkDumpSocketsV1(t *testing.T) (syscall.RawConn, int, int, uint32, uint32) {
	t.Helper()
	receiver, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.NETLINK_USERSOCK)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.SetsockoptInt(receiver, unix.SOL_SOCKET, unix.SO_RCVBUF, 4<<20); err != nil {
		t.Fatal(err)
	}
	if err := unix.Bind(receiver, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		unix.Close(receiver)
		t.Fatal(err)
	}
	sender, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_USERSOCK)
	if err != nil {
		unix.Close(receiver)
		t.Fatal(err)
	}
	if err := unix.SetsockoptInt(sender, unix.SOL_SOCKET, unix.SO_SNDBUF, 4<<20); err != nil {
		unix.Close(receiver)
		unix.Close(sender)
		t.Fatal(err)
	}
	if err := unix.Bind(sender, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		unix.Close(receiver)
		unix.Close(sender)
		t.Fatal(err)
	}
	readerAddress, err := unix.Getsockname(receiver)
	if err != nil {
		unix.Close(receiver)
		unix.Close(sender)
		t.Fatal(err)
	}
	senderAddress, err := unix.Getsockname(sender)
	if err != nil {
		unix.Close(receiver)
		unix.Close(sender)
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(receiver), "network-dump-reader")
	raw, err := reader.SyscallConn()
	if err != nil {
		reader.Close()
		unix.Close(sender)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = unix.Close(sender) })
	return raw, receiver, sender, senderAddress.(*unix.SockaddrNetlink).Pid, readerAddress.(*unix.SockaddrNetlink).Pid
}

func mappedNetworkDumpRequestV1(command uint16, data []byte, receiverPID uint32) netlink.Message {
	return netlink.Message{Header: netlink.Header{
		Type: netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | command), Flags: netlink.Request | netlink.Dump,
		Sequence: 17, PID: receiverPID,
	}, Data: data}
}

func mappedNetworkSendDumpMessageV1(t *testing.T, sender int, receiverPID uint32, message netlink.Message) {
	t.Helper()
	message.Header.Length = uint32((16 + len(message.Data) + 3) &^ 3)
	data, err := message.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Sendto(sender, data, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Pid: receiverPID}); err != nil {
		t.Fatal(err)
	}
}
