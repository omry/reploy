//go:build linux

package probe

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"syscall"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// NFTA_BITWISE_OP in linux/netfilter/nf_tables.h; the pinned x/sys omits it.
const mappedNFTABitwiseOpV1 uint16 = 6

const (
	mappedNetworkDumpMessagesV1 = 2048
	mappedNetworkDumpBytesV1    = 2 << 20
	mappedNetworkDatagramV1     = mappedNetworkDumpBytesV1 + mappedNetworkDumpMessagesV1*20
)

func verifyMappedNetworkPolicyV1(ctx context.Context, input MappedNetworkObservationV1) error {
	plan, err := parseMappedSandboxExecPlanV1(input.Arguments)
	if err != nil || plan.MappedCapabilities == nil || plan.SessionNetworkPeer != "" ||
		plan.AllowPublic != input.Public || plan.AllowLocal != input.Local || plan.AllowAmbiguous != input.Ambiguous {
		return fmt.Errorf("mapped network observation differs from the locked startup policy")
	}
	expected := &applicationNetworkRuleBuilderV1{Conn: &nftables.Conn{}}
	if err := applicationNetworkRulesV1(expected, applicationNetworkPolicyV1{
		AllowPublic: input.Public, AllowLocal: input.Local, AllowAmbiguous: input.Ambiguous, InboundTCP: plan.InboundTCP,
	}, input.Resolvers); err != nil {
		return err
	}
	_, connection, closeConnection, err := mappedNetworkReadConnectionV1(ctx)
	if err != nil {
		return err
	}
	defer closeConnection()
	tables, err := mappedNetworkTablesV1(connection)
	if err != nil || len(tables) != 2 {
		return fmt.Errorf("mapped namespace has an incomplete or unplanned table inventory: %v", err)
	}
	for _, table := range tables {
		if table.Name != "reploy" || table.Family != nftables.TableFamilyIPv4 && table.Family != nftables.TableFamilyIPv6 || table.Flags != 0 {
			return fmt.Errorf("mapped namespace has an unplanned table")
		}
	}
	chains, err := mappedNetworkChainsV1(connection)
	if err != nil || len(chains) != 6 {
		return fmt.Errorf("mapped namespace has an incomplete or unplanned chain inventory: %v", err)
	}
	seen := map[string]bool{}
	for _, chain := range chains {
		hooks := map[string]nftables.ChainHook{"input": *nftables.ChainHookInput, "output": *nftables.ChainHookOutput, "forward": *nftables.ChainHookForward}
		hook, known := hooks[chain.Name]
		key := fmt.Sprintf("%d:%s", chain.Table.Family, chain.Name)
		if !known || seen[key] || chain.Table.Name != "reploy" || chain.Table.Family != nftables.TableFamilyIPv4 && chain.Table.Family != nftables.TableFamilyIPv6 ||
			chain.Type != nftables.ChainTypeFilter || chain.Hooknum == nil || *chain.Hooknum != hook || chain.Priority == nil || *chain.Priority != *nftables.ChainPriorityFilter || chain.Policy == nil || *chain.Policy != nftables.ChainPolicyDrop {
			return fmt.Errorf("mapped effective base chain differs from its exact policy")
		}
		seen[key] = true
		want := [][]byte{}
		for _, rule := range expected.rules {
			if rule.Table.Family == chain.Table.Family && rule.Chain.Name == chain.Name {
				encoded := []byte{}
				for _, expression := range rule.Exprs {
					data, err := expr.Marshal(byte(chain.Table.Family), expression)
					if err != nil {
						return err
					}
					data, err = mappedNetworkExpressionV1(data)
					if err != nil {
						return err
					}
					encoded = append(encoded, data...)
				}
				want = append(want, encoded)
			}
		}
		actual, err := mappedNetworkRulesV1(connection, chain)
		if err != nil || !slices.EqualFunc(actual, want, bytes.Equal) {
			return fmt.Errorf("mapped effective rules differ in %s (observed=%d expected=%d): %v", key, len(actual), len(want), err)
		}
	}
	return ctx.Err()
}

// Keep table, chain and rule reads on the same bounded connection. A cancelled
// lasting client can fall back to transient dials in nftables, so every dial
// also refuses a cancelled context and closes its socket on option failure.
func mappedNetworkReadConnectionV1(ctx context.Context) (*nftables.Conn, *netlink.Conn, func(), error) {
	deadline := time.Now().Add(5 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	var connection *netlink.Conn
	reader, err := nftables.New(nftables.AsLasting(), nftables.WithSockOptions(func(opened *netlink.Conn) error {
		connection = opened
		err := ctx.Err()
		if err == nil {
			err = opened.SetDeadline(deadline)
		}
		if err != nil {
			_ = opened.Close()
		}
		return err
	}))
	if err != nil {
		return nil, nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = reader.CloseLasting() })
	return reader, connection, func() {
		stop()
		_ = reader.CloseLasting()
	}, nil
}

func mappedNetworkRulesV1(connection *netlink.Conn, chain *nftables.Chain) ([][]byte, error) {
	attrs, err := netlink.MarshalAttributes([]netlink.Attribute{
		{Type: unix.NFTA_RULE_TABLE, Data: []byte(chain.Table.Name + "\x00")},
		{Type: unix.NFTA_RULE_CHAIN, Data: []byte(chain.Name + "\x00")},
	})
	if err != nil {
		return nil, err
	}
	messages, err := mappedNetworkDumpV1(connection, unix.NFT_MSG_GETRULE, chain.Table.Family, attrs)
	if err != nil {
		return nil, fmt.Errorf("read bounded actual network rules: %v", err)
	}
	result := [][]byte{}
	for _, message := range messages {
		if len(message.Data) < 4 || message.Data[0] != byte(chain.Table.Family) {
			return nil, fmt.Errorf("invalid kernel rule data")
		}
		attributes, err := netlink.UnmarshalAttributes(message.Data[4:])
		if err != nil {
			return nil, err
		}
		var encoded []byte
		found := false
		for _, attribute := range attributes {
			if attribute.Type&0x3fff != unix.NFTA_RULE_EXPRESSIONS {
				continue
			}
			if found {
				return nil, fmt.Errorf("duplicate kernel expression inventory")
			}
			found = true
			expressions, err := netlink.UnmarshalAttributes(attribute.Data)
			if err != nil {
				return nil, err
			}
			for _, expression := range expressions {
				if expression.Type&0x3fff != unix.NFTA_LIST_ELEM {
					return nil, fmt.Errorf("invalid kernel expression envelope")
				}
				value, err := mappedNetworkExpressionV1(expression.Data)
				if err != nil {
					return nil, err
				}
				encoded = append(encoded, value...)
			}
		}
		if !found {
			return nil, fmt.Errorf("kernel rule omitted its expressions")
		}
		result = append(result, encoded)
	}
	return result, nil
}

func mappedNetworkTablesV1(connection *netlink.Conn) ([]*nftables.Table, error) {
	messages, err := mappedNetworkDumpV1(connection, unix.NFT_MSG_GETTABLE, nftables.TableFamilyUnspecified, nil)
	if err != nil {
		return nil, err
	}
	tables := make([]*nftables.Table, 0, len(messages))
	for _, message := range messages {
		if message.Header.Type != netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8|unix.NFT_MSG_NEWTABLE) &&
			message.Header.Type != netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8|unix.NFT_MSG_DELTABLE) || len(message.Data) < 4 {
			return nil, fmt.Errorf("invalid kernel table inventory message")
		}
		table := &nftables.Table{Family: nftables.TableFamily(message.Data[0])}
		attributes, err := netlink.NewAttributeDecoder(message.Data[4:])
		if err != nil {
			return nil, err
		}
		for attributes.Next() {
			switch attributes.Type() {
			case unix.NFTA_TABLE_NAME:
				table.Name = attributes.String()
			case unix.NFTA_TABLE_USE:
				table.Use = attributes.Uint32()
			case unix.NFTA_TABLE_FLAGS:
				table.Flags = attributes.Uint32()
			}
		}
		if err := attributes.Err(); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, nil
}

func mappedNetworkChainsV1(connection *netlink.Conn) ([]*nftables.Chain, error) {
	messages, err := mappedNetworkDumpV1(connection, unix.NFT_MSG_GETCHAIN, nftables.TableFamilyUnspecified, nil)
	if err != nil {
		return nil, err
	}
	chains := make([]*nftables.Chain, 0, len(messages))
	for _, message := range messages {
		if message.Header.Type != netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8|unix.NFT_MSG_NEWCHAIN) &&
			message.Header.Type != netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8|unix.NFT_MSG_DELCHAIN) || len(message.Data) < 4 {
			return nil, fmt.Errorf("invalid kernel chain inventory message")
		}
		chain := &nftables.Chain{}
		attributes, err := netlink.NewAttributeDecoder(message.Data[4:])
		if err != nil {
			return nil, err
		}
		for attributes.Next() {
			switch attributes.Type() {
			case unix.NFTA_CHAIN_NAME:
				chain.Name = attributes.String()
			case unix.NFTA_TABLE_NAME:
				chain.Table = &nftables.Table{Name: attributes.String(), Family: nftables.TableFamily(message.Data[0])}
			case unix.NFTA_CHAIN_TYPE:
				chain.Type = nftables.ChainType(attributes.String())
			case unix.NFTA_CHAIN_POLICY:
				if len(attributes.Bytes()) != 4 {
					return nil, fmt.Errorf("invalid kernel chain policy")
				}
				policy := nftables.ChainPolicy(binary.BigEndian.Uint32(attributes.Bytes()))
				chain.Policy = &policy
			case unix.NFTA_CHAIN_HOOK:
				hook, priority, err := mappedNetworkHookV1(attributes.Bytes())
				if err != nil {
					return nil, err
				}
				chain.Hooknum, chain.Priority = hook, priority
			}
		}
		if err := attributes.Err(); err != nil {
			return nil, err
		}
		chains = append(chains, chain)
	}
	return chains, nil
}

func mappedNetworkHookV1(data []byte) (*nftables.ChainHook, *nftables.ChainPriority, error) {
	attributes, err := netlink.NewAttributeDecoder(data)
	if err != nil {
		return nil, nil, err
	}
	attributes.ByteOrder = binary.BigEndian
	hook, priority := nftables.ChainHook(0), nftables.ChainPriority(0)
	for attributes.Next() {
		switch attributes.Type() {
		case unix.NFTA_HOOK_HOOKNUM:
			hook = nftables.ChainHook(attributes.Uint32())
		case unix.NFTA_HOOK_PRIORITY:
			priority = nftables.ChainPriority(attributes.Uint32())
		}
	}
	if err := attributes.Err(); err != nil {
		return nil, nil, err
	}
	return &hook, &priority, nil
}

func mappedNetworkDumpV1(connection *netlink.Conn, command uint16, family nftables.TableFamily, attributes []byte) ([]netlink.Message, error) {
	data := append([]byte{byte(family), unix.NFNETLINK_V0, 0, 0}, attributes...)
	request, err := connection.Send(netlink.Message{
		Header: netlink.Header{Type: netlink.HeaderType(unix.NFNL_SUBSYS_NFTABLES<<8 | command), Flags: netlink.Request | netlink.Dump},
		Data:   data,
	})
	if err != nil {
		return nil, err
	}
	raw, err := connection.SyscallConn()
	if err != nil {
		return nil, err
	}
	return mappedNetworkCollectV1(raw, request, 0)
}

func mappedNetworkCollectV1(raw syscall.RawConn, request netlink.Message, senderPID uint32) ([]netlink.Message, error) {
	return mappedNetworkCollectWithBufferV1(raw, request, senderPID, mappedNetworkDatagramV1)
}

func mappedNetworkCollectWithBufferV1(raw syscall.RawConn, request netlink.Message, senderPID uint32, bufferSize int) ([]netlink.Message, error) {
	buffer := make([]byte, bufferSize)
	messages := make([]netlink.Message, 0, 8)
	bytesRead := 0
	for {
		var n, flags int
		var sender unix.Sockaddr
		var receiveErr error
		if err := raw.Read(func(fd uintptr) bool {
			n, _, flags, sender, receiveErr = unix.Recvmsg(int(fd), buffer, nil, 0)
			return receiveErr != unix.EAGAIN && receiveErr != unix.EWOULDBLOCK && receiveErr != unix.EINTR
		}); err != nil {
			return nil, err
		}
		if receiveErr != nil {
			return nil, receiveErr
		}
		address, ok := sender.(*unix.SockaddrNetlink)
		if !ok || address.Pid != senderPID || flags&unix.MSG_TRUNC != 0 || n == 0 {
			return nil, fmt.Errorf("invalid or truncated network dump datagram")
		}
		more := false
		for offset := 0; offset < n; {
			if n-offset < 16 {
				return nil, fmt.Errorf("short network dump message")
			}
			rawLength := binary.NativeEndian.Uint32(buffer[offset : offset+4])
			if rawLength < 16 || uint64(rawLength) > uint64(n-offset) {
				return nil, fmt.Errorf("invalid network dump message length")
			}
			length := int(rawLength)
			aligned := (length + 3) &^ 3
			if aligned > n-offset {
				return nil, fmt.Errorf("invalid network dump message length")
			}
			message := netlink.Message{
				Header: netlink.Header{
					Length:   uint32(length),
					Type:     netlink.HeaderType(binary.NativeEndian.Uint16(buffer[offset+4 : offset+6])),
					Flags:    netlink.HeaderFlags(binary.NativeEndian.Uint16(buffer[offset+6 : offset+8])),
					Sequence: binary.NativeEndian.Uint32(buffer[offset+8 : offset+12]),
					PID:      binary.NativeEndian.Uint32(buffer[offset+12 : offset+16]),
				},
				Data: buffer[offset+16 : offset+length],
			}
			if err := netlink.Validate(request, []netlink.Message{message}); err != nil {
				return nil, err
			}
			if message.Header.Flags&netlink.DumpInterrupted != 0 {
				return nil, fmt.Errorf("kernel marked network dump interrupted")
			}
			if message.Header.Type == netlink.Error || message.Header.Type == netlink.Done && message.Header.Flags&netlink.Multi != 0 && len(message.Data) != 0 {
				if len(message.Data) < 4 {
					return nil, fmt.Errorf("short network dump error message")
				}
				if code := int32(binary.NativeEndian.Uint32(message.Data)); code != 0 {
					return nil, unix.Errno(-code)
				}
			}
			if message.Header.Type == netlink.Done && message.Header.Flags&netlink.Multi != 0 {
				if offset+aligned != n {
					return nil, fmt.Errorf("network dump continued after multipart completion")
				}
				more = false
				offset = n
				break
			}
			if len(messages) >= mappedNetworkDumpMessagesV1 || bytesRead+len(message.Data) > mappedNetworkDumpBytesV1 {
				return nil, fmt.Errorf("excessive network dump data")
			}
			bytesRead += len(message.Data)
			message.Data = append([]byte(nil), message.Data...)
			messages = append(messages, message)
			if message.Header.Flags&netlink.Multi != 0 {
				more = message.Header.Type != netlink.Done
			}
			offset += aligned
		}
		if !more {
			return messages, nil
		}
	}
}

func mappedNetworkExpressionV1(data []byte) ([]byte, error) {
	attributes, err := netlink.UnmarshalAttributes(data)
	if err != nil || len(attributes) != 2 {
		return nil, fmt.Errorf("invalid network expression envelope: %v", err)
	}
	var name string
	var body []byte
	for _, attribute := range attributes {
		switch attribute.Type & 0x3fff {
		case unix.NFTA_EXPR_NAME:
			if name != "" || len(attribute.Data) < 2 || attribute.Data[len(attribute.Data)-1] != 0 {
				return nil, fmt.Errorf("invalid network expression name")
			}
			name = string(attribute.Data[:len(attribute.Data)-1])
		case unix.NFTA_EXPR_DATA:
			if body != nil {
				return nil, fmt.Errorf("duplicate network expression data")
			}
			body = attribute.Data
		default:
			return nil, fmt.Errorf("unknown network expression envelope")
		}
	}
	if name == "" || body == nil {
		return nil, fmt.Errorf("incomplete network expression")
	}
	switch name {
	case "meta", "ct", "payload", "cmp", "bitwise", "immediate":
	default:
		return nil, fmt.Errorf("unsupported network expression %q", name)
	}
	fields, err := mappedNetworkAttributesV1(body, name)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Name   string
		Fields []byte
	}{name, fields})
}

func mappedNetworkAttributesV1(data []byte, kind string) ([]byte, error) {
	attributes, err := netlink.UnmarshalAttributes(data)
	if err != nil {
		return nil, err
	}
	type field struct {
		Type uint16
		Data []byte
	}
	fields := make([]field, 0, len(attributes))
	for _, attribute := range attributes {
		typ, value := attribute.Type&0x3fff, attribute.Data
		nested := ""
		// Use the fixed nftables ABI, not NLA_F_NESTED: kernel dumps omit
		// that flag for these nested fields. Opaque data stays opaque.
		switch {
		case kind == "cmp" && typ == unix.NFTA_CMP_DATA,
			kind == "bitwise" && (typ == unix.NFTA_BITWISE_MASK || typ == unix.NFTA_BITWISE_XOR),
			kind == "immediate" && typ == unix.NFTA_IMMEDIATE_DATA:
			nested = "data"
		case kind == "data" && typ == unix.NFTA_DATA_VERDICT:
			nested = "verdict"
		}
		if nested != "" {
			value, err = mappedNetworkAttributesV1(value, nested)
			if err != nil {
				return nil, err
			}
		}
		// nftables v0.3.0 writes CT direction as u32; Linux returns u8.
		if kind == "ct" && typ == unix.NFTA_CT_DIRECTION && len(value) == 1 {
			direction := uint32(value[0])
			value = make([]byte, 4)
			binary.BigEndian.PutUint32(value, direction)
		}
		fields = append(fields, field{typ, value})
	}
	slices.SortFunc(fields, func(a, b field) int { return int(a.Type) - int(b.Type) })
	for i := 1; i < len(fields); i++ {
		if fields[i-1].Type == fields[i].Type {
			return nil, fmt.Errorf("duplicate network expression attribute")
		}
	}
	// The kernel spells out the default boolean bitwise operation; the
	// pinned writer omits it. Other operation values remain significant.
	if kind == "bitwise" {
		fields = slices.DeleteFunc(fields, func(value field) bool {
			return value.Type == mappedNFTABitwiseOpV1 && bytes.Equal(value.Data, []byte{0, 0, 0, 0})
		})
	}
	return json.Marshal(fields)
}
