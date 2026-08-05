//go:build linux

package probe

import (
	"encoding/binary"
	"fmt"
	"net"
	"slices"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

type applicationNetworkPolicyV1 struct {
	AllowPublic bool
	AllowLocal  bool
	InboundTCP  []uint16
}

const applicationResponseStateMaskV1 uint32 = expr.CtStateBitESTABLISHED

var applicationRelatedICMPv4TypesV1 = []byte{3, 11, 12}
var applicationRelatedICMPv6TypesV1 = []byte{1, 2, 3, 4}

// Keep these conservative destination classes aligned with the IANA
// special-purpose registries. Translation/tunneling prefixes are classified
// as local so an embedded private destination cannot bypass local denial.
// https://www.iana.org/assignments/iana-ipv4-special-registry/
// https://www.iana.org/assignments/iana-ipv6-special-registry/
var applicationNonPublicIPv4CIDRsV1 = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
	"192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
	"224.0.0.0/4", "240.0.0.0/4",
}

var applicationNonPublicIPv6CIDRsV1 = []string{
	"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48",
	"100::/64", "100:0:0:1::/64", "2001::/32", "2001:2::/48", "2001:10::/28",
	"2001:db8::/32", "2002::/16", "3fff::/20", "5f00::/16",
	"fc00::/7", "fe80::/10", "ff00::/8",
}

func installApplicationNetworkPolicyV1(policy applicationNetworkPolicyV1) error {
	if policy.AllowPublic && policy.AllowLocal {
		return nil
	}
	ports := append([]uint16(nil), policy.InboundTCP...)
	slices.Sort(ports)
	ports = slices.Compact(ports)
	connection := &nftables.Conn{}
	for _, family := range []struct {
		tableFamily nftables.TableFamily
		cidrs       []string
		addressSize uint32
	}{
		{nftables.TableFamilyIPv4, applicationNonPublicIPv4CIDRsV1, 4},
		{nftables.TableFamilyIPv6, applicationNonPublicIPv6CIDRsV1, 16},
	} {
		if err := replaceApplicationNetworkTableV1(connection, family.tableFamily, family.cidrs, family.addressSize, policy, ports); err != nil {
			return err
		}
	}
	if err := connection.Flush(); err != nil {
		return fmt.Errorf("apply nftables transaction: %w", err)
	}
	return nil
}

func replaceApplicationNetworkTableV1(connection *nftables.Conn, family nftables.TableFamily, cidrs []string, addressSize uint32, policy applicationNetworkPolicyV1, ports []uint16) error {
	tables, err := connection.ListTablesOfFamily(family)
	if err != nil {
		return fmt.Errorf("list nftables family %d: %w", family, err)
	}
	for _, table := range tables {
		if table.Name == "reploy" {
			connection.DelTable(table)
		}
	}
	table := connection.AddTable(&nftables.Table{Family: family, Name: "reploy"})
	drop := nftables.ChainPolicyDrop
	input := connection.AddChain(&nftables.Chain{
		Name: "input", Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookInput, Priority: nftables.ChainPriorityFilter, Policy: &drop,
	})
	output := connection.AddChain(&nftables.Chain{
		Name: "output", Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityFilter, Policy: &drop,
	})
	forward := connection.AddChain(&nftables.Chain{
		Name: "forward", Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookForward, Priority: nftables.ChainPriorityFilter, Policy: &drop,
	})
	_ = forward
	addInterfaceVerdictV1(connection, table, input, expr.MetaKeyIIFNAME, "lo", expr.VerdictAccept)
	addEstablishedVerdictV1(connection, table, input, expr.VerdictAccept)
	addRelatedICMPErrorVerdictsV1(connection, table, input, family)
	if family == nftables.TableFamilyIPv6 {
		addIPv6NeighborDiscoveryRulesV1(connection, table, input)
	}
	for _, port := range ports {
		addTCPPortVerdictV1(connection, table, input, port, expr.VerdictAccept)
	}

	if family == nftables.TableFamilyIPv4 && !policy.AllowPublic {
		// Docker's embedded resolver is reached through container loopback and
		// may forward queries externally. Deny its address before accepting
		// ordinary application loopback traffic.
		if err := addCIDRVerdictV1(connection, table, output, "127.0.0.11/32", addressSize, expr.VerdictDrop); err != nil {
			return err
		}
	}
	addInterfaceVerdictV1(connection, table, output, expr.MetaKeyOIFNAME, "lo", expr.VerdictAccept)
	addEstablishedVerdictV1(connection, table, output, expr.VerdictAccept)
	if family == nftables.TableFamilyIPv6 {
		addIPv6NeighborDiscoveryRulesV1(connection, table, output)
	}
	if policy.AllowLocal != policy.AllowPublic {
		verdict := expr.VerdictDrop
		if policy.AllowLocal {
			verdict = expr.VerdictAccept
		}
		for _, cidr := range cidrs {
			if err := addCIDRVerdictV1(connection, table, output, cidr, addressSize, verdict); err != nil {
				return err
			}
		}
	}
	if policy.AllowPublic {
		connection.AddRule(&nftables.Rule{Table: table, Chain: output, Exprs: []expr.Any{&expr.Verdict{Kind: expr.VerdictAccept}}})
	}
	return nil
}

func addRelatedICMPErrorVerdictsV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, family nftables.TableFamily) {
	protocol := byte(unix.IPPROTO_ICMP)
	types := applicationRelatedICMPv4TypesV1
	if family == nftables.TableFamilyIPv6 {
		protocol = unix.IPPROTO_ICMPV6
		types = applicationRelatedICMPv6TypesV1
	}
	for _, kind := range types {
		connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protocol}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{kind}},
			&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
			&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4,
				Mask: binaryutil.NativeEndian.PutUint32(expr.CtStateBitRELATED),
				Xor:  binaryutil.NativeEndian.PutUint32(0)},
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
			&expr.Verdict{Kind: expr.VerdictAccept},
		}})
	}
}

func addInterfaceVerdictV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, key expr.MetaKey, name string, verdict expr.VerdictKind) {
	data := make([]byte, 16)
	copy(data, name+"\x00")
	connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{
		&expr.Meta{Key: key, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: data},
		&expr.Verdict{Kind: verdict},
	}})
}

func addEstablishedVerdictV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, verdict expr.VerdictKind) {
	connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4,
			Mask: binaryutil.NativeEndian.PutUint32(applicationResponseStateMaskV1),
			Xor:  binaryutil.NativeEndian.PutUint32(0)},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
		&expr.Verdict{Kind: verdict},
	}})
}

func addTCPPortVerdictV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, port uint16, verdict expr.VerdictKind) {
	data := make([]byte, 2)
	binary.BigEndian.PutUint16(data, port)
	connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: data},
		&expr.Verdict{Kind: verdict},
	}})
}

func addIPv6NeighborDiscoveryRulesV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain) {
	for _, kind := range []byte{133, 134, 135, 136} {
		connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_ICMPV6}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{kind}},
			&expr.Verdict{Kind: expr.VerdictAccept},
		}})
	}
}

func addCIDRVerdictV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, cidr string, addressSize uint32, verdict expr.VerdictKind) error {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parse application network prefix %q: %w", cidr, err)
	}
	offset := uint32(16)
	address := ip.To4()
	if addressSize == 16 {
		offset = 24
		address = ip.To16()
	}
	connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: addressSize},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: addressSize, Mask: network.Mask, Xor: make([]byte, addressSize)},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: address},
		&expr.Verdict{Kind: verdict},
	}})
	return nil
}
