//go:build linux

package probe

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

type applicationNetworkPolicyV1 struct {
	AllowPublic       bool
	AllowLocal        bool
	AllowAmbiguous    bool
	InboundTCP        []uint16
	SessionPeerCIDRs  []string
	SessionLocalCIDRs []string
}

const applicationResponseStateMaskV1 uint32 = expr.CtStateBitESTABLISHED

const applicationResolverConfigurationV1 = "/etc/resolv.conf"
const applicationHostsConfigurationV1 = "/etc/hosts"
const applicationResolverConfigurationLimitV1 = 64 * 1024
const applicationSessionNetworkConfigurationLimitV1 = 4 * 1024

var applicationRelatedICMPv4TypesV1 = []byte{3, 11, 12}
var applicationRelatedICMPv6TypesV1 = []byte{1, 2, 3, 4}

// Keep these destination classes aligned with the IANA special-purpose
// registries. Public exceptions must precede their containing local ranges.
// Translation and tunneling prefixes are ambiguous because they can represent
// either public or local destinations; by default they require both grants.
// IPv4-mapped IPv6 socket addresses are not listed here: Linux emits those as
// IPv4 packets, so the embedded IPv4 destination receives its ordinary class.
// https://www.iana.org/assignments/iana-ipv4-special-registry/
// https://www.iana.org/assignments/iana-ipv6-special-registry/
var applicationLocalIPv4CIDRsV1 = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
	"192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
	"224.0.0.0/4", "240.0.0.0/4",
}

var applicationPublicIPv4ExceptionsV1 = []string{
	"192.0.0.9/32", "192.0.0.10/32",
}

var applicationAmbiguousIPv4CIDRsV1 = []string{}

var applicationLocalIPv6CIDRsV1 = []string{
	"::/128", "::1/128", "100::/64", "100:0:0:1::/64", "2001::/23",
	"2001:db8::/32", "3fff::/20", "5f00::/16",
	"fc00::/7", "fe80::/10", "ff00::/8",
}

var applicationPublicIPv6ExceptionsV1 = []string{
	"2001:1::1/128", "2001:1::2/128", "2001:1::3/128", "2001:3::/32",
	"2001:4:112::/48", "2001:20::/28", "2001:30::/28",
}

var applicationAmbiguousIPv6CIDRsV1 = []string{
	"64:ff9b::/96", "64:ff9b:1::/48", "2001::/32", "2002::/16",
}

func installApplicationNetworkPolicyV1(policy applicationNetworkPolicyV1) error {
	ports := append([]uint16(nil), policy.InboundTCP...)
	slices.Sort(ports)
	ports = slices.Compact(ports)
	resolverCIDRs := []string{}
	if applicationNetworkPolicyAllowsDNSV1(policy) {
		var err error
		resolverCIDRs, err = readApplicationResolverCIDRsV1(applicationResolverConfigurationV1)
		if err != nil {
			return fmt.Errorf("read application DNS resolvers: %w", err)
		}
		if len(resolverCIDRs) == 0 {
			return fmt.Errorf("read application DNS resolvers: no nameserver entries")
		}
	}
	connection := &nftables.Conn{}
	for _, family := range []struct {
		tableFamily      nftables.TableFamily
		local            []string
		publicExceptions []string
		ambiguous        []string
		addressSize      uint32
	}{
		{nftables.TableFamilyIPv4, applicationLocalIPv4CIDRsV1, applicationPublicIPv4ExceptionsV1, applicationAmbiguousIPv4CIDRsV1, 4},
		{nftables.TableFamilyIPv6, applicationLocalIPv6CIDRsV1, applicationPublicIPv6ExceptionsV1, applicationAmbiguousIPv6CIDRsV1, 16},
	} {
		if err := replaceApplicationNetworkTableV1(connection, family.tableFamily, family.local, family.publicExceptions, family.ambiguous, resolverCIDRs, family.addressSize, policy, ports); err != nil {
			return err
		}
	}
	if err := connection.Flush(); err != nil {
		return fmt.Errorf("apply nftables transaction: %w", err)
	}
	return nil
}

func replaceApplicationNetworkTableV1(connection *nftables.Conn, family nftables.TableFamily, local []string, publicExceptions []string, ambiguous []string, resolverCIDRs []string, addressSize uint32, policy applicationNetworkPolicyV1, ports []uint16) error {
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
	if err := addSessionPeerInputVerdictsV1(
		connection, table, input, policy.SessionPeerCIDRs, policy.SessionLocalCIDRs, addressSize,
	); err != nil {
		return err
	}
	for _, port := range ports {
		addTCPPortVerdictV1(connection, table, input, port, expr.VerdictAccept)
	}

	if applicationNetworkPolicyAllowsDNSV1(policy) {
		if err := addDNSResolverVerdictsV1(connection, table, output, resolverCIDRs, addressSize); err != nil {
			return err
		}
	}
	if family == nftables.TableFamilyIPv4 {
		// Docker's embedded resolver is reached through container loopback.
		// Permit only its engine-owned DNS service before accepting ordinary
		// application loopback traffic.
		if err := addCIDRVerdictV1(connection, table, output, "127.0.0.11/32", addressSize, expr.VerdictDrop); err != nil {
			return err
		}
	}
	addInterfaceVerdictV1(connection, table, output, expr.MetaKeyOIFNAME, "lo", expr.VerdictAccept)
	addEstablishedVerdictV1(connection, table, output, expr.VerdictAccept)
	if family == nftables.TableFamilyIPv6 {
		addIPv6NeighborDiscoveryRulesV1(connection, table, output)
	}
	if err := addCIDRVerdictsV1(connection, table, output, policy.SessionPeerCIDRs, addressSize, expr.VerdictAccept); err != nil {
		return err
	}
	ambiguousVerdict := expr.VerdictDrop
	if policy.AllowAmbiguous {
		ambiguousVerdict = expr.VerdictAccept
	}
	if err := addCIDRVerdictsV1(connection, table, output, ambiguous, addressSize, ambiguousVerdict); err != nil {
		return err
	}
	publicVerdict := expr.VerdictDrop
	if policy.AllowPublic {
		publicVerdict = expr.VerdictAccept
	}
	if err := addCIDRVerdictsV1(connection, table, output, publicExceptions, addressSize, publicVerdict); err != nil {
		return err
	}
	localVerdict := expr.VerdictDrop
	if policy.AllowLocal {
		localVerdict = expr.VerdictAccept
	}
	if err := addCIDRVerdictsV1(connection, table, output, local, addressSize, localVerdict); err != nil {
		return err
	}
	if policy.AllowPublic {
		connection.AddRule(&nftables.Rule{Table: table, Chain: output, Exprs: []expr.Any{&expr.Verdict{Kind: expr.VerdictAccept}}})
	}
	return nil
}

func applicationNetworkPolicyAllowsDNSV1(policy applicationNetworkPolicyV1) bool {
	return policy.AllowPublic || policy.AllowLocal
}

var lookupApplicationSessionPeerIPsV1 = net.LookupIP
var applicationInterfaceAddrsV1 = net.InterfaceAddrs

// prepareApplicationSessionPeerV1 resolves only the fixed Docker-owned peer
// alias while trusted startup code still owns the network namespace. It then
// freezes that mapping in the container-local hosts file and returns exact
// host prefixes for the firewall. Untrusted application code starts only
// after the firewall is installed, so deny/deny does not require a DNS grant.
func prepareApplicationSessionPeerV1(peer string, sessionSubnets []string, hostsPath string) ([]string, []string, error) {
	if peer == "" {
		if len(sessionSubnets) != 0 {
			return nil, nil, fmt.Errorf("session-network prefixes require a peer alias")
		}
		return []string{}, []string{}, nil
	}
	if peer != "controller" && peer != "workload" {
		return nil, nil, fmt.Errorf("session-network peer alias %q is invalid", peer)
	}
	if len(sessionSubnets) == 0 {
		return nil, nil, fmt.Errorf("session-network peer alias requires realized prefixes")
	}
	allowed := make([]*net.IPNet, len(sessionSubnets))
	for index, prefix := range sessionSubnets {
		_, network, err := net.ParseCIDR(prefix)
		if err != nil || network.String() != prefix {
			return nil, nil, fmt.Errorf("session-network prefix %q is not canonical CIDR", prefix)
		}
		allowed[index] = network
	}
	addresses, err := lookupApplicationSessionPeerIPsV1(peer)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve fixed peer alias %q: %w", peer, err)
	}
	if len(addresses) == 0 {
		return nil, nil, fmt.Errorf("resolve fixed peer alias %q: no addresses", peer)
	}
	exact := make([]string, 0, len(addresses))
	hosts := make([]string, 0, len(addresses))
	seen := map[string]struct{}{}
	for _, address := range addresses {
		canonical := address.String()
		if canonical == "<nil>" {
			return nil, nil, fmt.Errorf("resolve fixed peer alias %q: invalid address", peer)
		}
		permitted := false
		for _, subnet := range allowed {
			if subnet.Contains(address) {
				permitted = true
				break
			}
		}
		if !permitted {
			return nil, nil, fmt.Errorf("fixed peer alias %q resolved outside the realized session network", peer)
		}
		bits := 128
		if address.To4() != nil {
			canonical = address.To4().String()
			bits = 32
		}
		prefix := fmt.Sprintf("%s/%d", canonical, bits)
		if _, duplicate := seen[prefix]; duplicate {
			continue
		}
		seen[prefix] = struct{}{}
		exact = append(exact, prefix)
		hosts = append(hosts, canonical+" "+peer+"\n")
	}
	sort.Strings(exact)
	sort.Strings(hosts)
	local, err := applicationSessionLocalCIDRsV1(allowed)
	if err != nil {
		return nil, nil, err
	}
	for _, peerCIDR := range exact {
		peerIP, _, _ := net.ParseCIDR(peerCIDR)
		foundFamily := false
		for _, localCIDR := range local {
			localIP, _, _ := net.ParseCIDR(localCIDR)
			if (peerIP.To4() != nil) == (localIP.To4() != nil) {
				foundFamily = true
				break
			}
		}
		if !foundFamily {
			return nil, nil, fmt.Errorf("fixed peer alias %q has no same-family lease-local address", peer)
		}
	}
	file, err := os.OpenFile(hostsPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open container hosts file: %w", err)
	}
	_, writeErr := file.WriteString(strings.Join(hosts, ""))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return nil, nil, fmt.Errorf("freeze fixed peer alias in container hosts file: %w", errors.Join(writeErr, closeErr))
	}
	return exact, local, nil
}

func applicationSessionLocalCIDRsV1(sessionSubnets []*net.IPNet) ([]string, error) {
	addresses, err := applicationInterfaceAddrsV1()
	if err != nil {
		return nil, fmt.Errorf("inspect container interface addresses: %w", err)
	}
	result := map[string]struct{}{}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil {
			return nil, fmt.Errorf("parse container interface address %q: %w", address, err)
		}
		for _, subnet := range sessionSubnets {
			if !subnet.Contains(ip) {
				continue
			}
			bits := 128
			if ipv4 := ip.To4(); ipv4 != nil {
				ip = ipv4
				bits = 32
			}
			result[fmt.Sprintf("%s/%d", ip, bits)] = struct{}{}
			break
		}
	}
	local := make([]string, 0, len(result))
	for prefix := range result {
		local = append(local, prefix)
	}
	sort.Strings(local)
	if len(local) == 0 {
		return nil, fmt.Errorf("container has no address in the realized session network")
	}
	return local, nil
}

func readApplicationSessionNetworkCIDRsV1(path string) ([]string, error) {
	if path == "" {
		return []string{}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, applicationSessionNetworkConfigurationLimitV1+1))
	if err != nil {
		return nil, err
	}
	if len(content) > applicationSessionNetworkConfigurationLimitV1 {
		return nil, fmt.Errorf("session-network configuration exceeds %d bytes", applicationSessionNetworkConfigurationLimitV1)
	}
	result := []string{}
	previous := ""
	for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		if line == "" {
			return nil, fmt.Errorf("session-network configuration contains an empty prefix")
		}
		_, network, err := net.ParseCIDR(line)
		if err != nil || network.String() != line {
			return nil, fmt.Errorf("session-network prefix %q is not canonical CIDR", line)
		}
		if previous != "" && previous >= line {
			return nil, fmt.Errorf("session-network prefixes must be unique and sorted")
		}
		result = append(result, line)
		previous = line
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("session-network configuration contains no prefixes")
	}
	return result, nil
}

func readApplicationResolverCIDRsV1(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, applicationResolverConfigurationLimitV1+1))
	if err != nil {
		return nil, err
	}
	if len(content) > applicationResolverConfigurationLimitV1 {
		return nil, fmt.Errorf("resolver configuration exceeds %d bytes", applicationResolverConfigurationLimitV1)
	}
	result := map[string]struct{}{}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := scanner.Text()
		if offset := strings.IndexAny(line, "#;"); offset >= 0 {
			line = line[:offset]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "nameserver" {
			continue
		}
		if len(fields) < 2 {
			return nil, fmt.Errorf("nameserver entry has no address")
		}
		address := fields[1]
		if host, _, found := strings.Cut(address, "%"); found {
			address = host
		}
		ip := net.ParseIP(address)
		if ip == nil {
			return nil, fmt.Errorf("nameserver address %q is not an IP address", fields[1])
		}
		bits := 128
		if ipv4 := ip.To4(); ipv4 != nil {
			ip = ipv4
			bits = 32
		}
		result[fmt.Sprintf("%s/%d", ip.String(), bits)] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	resolverCIDRs := make([]string, 0, len(result))
	for cidr := range result {
		resolverCIDRs = append(resolverCIDRs, cidr)
	}
	sort.Strings(resolverCIDRs)
	return resolverCIDRs, nil
}

func addDNSResolverVerdictsV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, resolverCIDRs []string, addressSize uint32) error {
	for _, cidr := range resolverCIDRs {
		ip, _, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("parse application DNS resolver %q: %w", cidr, err)
		}
		if (addressSize == 4 && ip.To4() == nil) || (addressSize == 16 && ip.To4() != nil) {
			continue
		}
		for _, protocol := range []byte{unix.IPPROTO_UDP, unix.IPPROTO_TCP} {
			if err := addTransportPortCIDRVerdictV1(connection, table, chain, cidr, addressSize, protocol, 53, expr.VerdictAccept); err != nil {
				return err
			}
		}
	}
	return nil
}

func addTransportPortCIDRVerdictV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, cidr string, addressSize uint32, protocol byte, port uint16, verdict expr.VerdictKind) error {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("parse application network prefix %q: %w", cidr, err)
	}
	address := ip.To4()
	if addressSize == 16 {
		address = ip.To16()
	}
	portData := make([]byte, 2)
	binary.BigEndian.PutUint16(portData, port)
	connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{
		// Docker DNATs embedded-DNS traffic from port 53 to an internal
		// high-numbered socket before the filter hook. Match the original
		// conntrack tuple so the narrow resolver grant survives that rewrite.
		&expr.Ct{Register: 1, Key: expr.CtKeyDST, Direction: 0},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: addressSize, Mask: network.Mask, Xor: make([]byte, addressSize)},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: address},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protocol}},
		&expr.Ct{Register: 1, Key: expr.CtKeyPROTODST, Direction: 0},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: portData},
		&expr.Verdict{Kind: verdict},
	}})
	return nil
}

func addCIDRVerdictsV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, cidrs []string, addressSize uint32, verdict expr.VerdictKind) error {
	for _, cidr := range cidrs {
		if err := addCIDRVerdictV1(connection, table, chain, cidr, addressSize, verdict); err != nil {
			return err
		}
	}
	return nil
}

func addSessionPeerInputVerdictsV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, peerCIDRs []string, localCIDRs []string, addressSize uint32) error {
	for _, peerCIDR := range peerCIDRs {
		for _, localCIDR := range localCIDRs {
			expressions, err := sessionPeerInputVerdictExpressionsV1(peerCIDR, localCIDR, addressSize)
			if err != nil {
				return err
			}
			if expressions != nil {
				connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: expressions})
			}
		}
	}
	return nil
}

func sessionPeerInputVerdictExpressionsV1(peerCIDR string, localCIDR string, addressSize uint32) ([]expr.Any, error) {
	peer, err := addressCIDRMatchExpressionsV1(peerCIDR, addressSize, true)
	if err != nil || peer == nil {
		return nil, err
	}
	local, err := addressCIDRMatchExpressionsV1(localCIDR, addressSize, false)
	if err != nil || local == nil {
		return nil, err
	}
	return append(append(peer, local...), &expr.Verdict{Kind: expr.VerdictAccept}), nil
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
	return addAddressCIDRVerdictV1(connection, table, chain, cidr, addressSize, false, verdict)
}

func addAddressCIDRVerdictV1(connection *nftables.Conn, table *nftables.Table, chain *nftables.Chain, cidr string, addressSize uint32, source bool, verdict expr.VerdictKind) error {
	expressions, err := addressCIDRVerdictExpressionsV1(cidr, addressSize, source, verdict)
	if err != nil || expressions == nil {
		return err
	}
	connection.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: expressions})
	return nil
}

func addressCIDRVerdictExpressionsV1(cidr string, addressSize uint32, source bool, verdict expr.VerdictKind) ([]expr.Any, error) {
	expressions, err := addressCIDRMatchExpressionsV1(cidr, addressSize, source)
	if err != nil || expressions == nil {
		return expressions, err
	}
	return append(expressions, &expr.Verdict{Kind: verdict}), nil
}

func addressCIDRMatchExpressionsV1(cidr string, addressSize uint32, source bool) ([]expr.Any, error) {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse application network prefix %q: %w", cidr, err)
	}
	offset := uint32(16)
	if source {
		offset = 12
	}
	address := ip.To4()
	if addressSize == 16 {
		offset = 24
		if source {
			offset = 8
		}
		address = ip.To16()
	}
	if address == nil || addressSize == 4 && ip.To4() == nil || addressSize == 16 && ip.To4() != nil {
		return nil, nil
	}
	return []expr.Any{
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: addressSize},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: addressSize, Mask: network.Mask, Xor: make([]byte, addressSize)},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: address},
	}, nil
}
