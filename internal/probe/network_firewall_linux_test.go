//go:build linux

package probe

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/nftables/expr"
)

func TestReadApplicationResolverCIDRsV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	content := "search example.test\n" +
		"nameserver 127.0.0.11 # Docker embedded DNS\n" +
		"nameserver 8.8.8.8\n" +
		"nameserver 2001:4860:4860::8888%eth0\n" +
		"nameserver 8.8.8.8 ; duplicate\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readApplicationResolverCIDRsV1(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.11/32", "2001:4860:4860::8888/128", "8.8.8.8/32"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolver CIDRs = %#v, want %#v", got, want)
	}
}

func TestPrepareApplicationSessionPeerV1FreezesOnlyExactValidatedAddresses(t *testing.T) {
	priorLookup := lookupApplicationSessionPeerIPsV1
	priorInterfaces := applicationInterfaceAddrsV1
	t.Cleanup(func() {
		lookupApplicationSessionPeerIPsV1 = priorLookup
		applicationInterfaceAddrsV1 = priorInterfaces
	})
	lookupApplicationSessionPeerIPsV1 = func(peer string) ([]net.IP, error) {
		if peer != "workload" {
			t.Fatalf("peer = %q", peer)
		}
		return []net.IP{net.ParseIP("fd00:1::3"), net.ParseIP("172.31.0.3")}, nil
	}
	applicationInterfaceAddrsV1 = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("172.31.0.2"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("fd00:1::2"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("172.30.0.2"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}
	addresses := []net.IP{net.ParseIP("fd00:1::3"), net.ParseIP("172.31.0.3")}
	got, local, err := prepareApplicationSessionPeerV1("workload", []string{"172.31.0.0/24", "fd00:1::/64"}, addresses)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"172.31.0.3/32", "fd00:1::3/128"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exact peer CIDRs = %#v, want %#v", got, want)
	}
	if wantLocal := []string{"172.31.0.2/32", "fd00:1::2/128"}; !reflect.DeepEqual(local, wantLocal) {
		t.Fatalf("exact lease-local CIDRs = %#v, want %#v", local, wantLocal)
	}
}

func TestPrepareApplicationSessionPeerV1RejectsUnresolvedOrOutOfNetworkAddress(t *testing.T) {
	priorLookup := lookupApplicationSessionPeerIPsV1
	priorInterfaces := applicationInterfaceAddrsV1
	t.Cleanup(func() {
		lookupApplicationSessionPeerIPsV1 = priorLookup
		applicationInterfaceAddrsV1 = priorInterfaces
	})
	applicationInterfaceAddrsV1 = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("172.31.1.2"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	for _, test := range []struct {
		name      string
		addresses []net.IP
	}{
		{name: "no addresses"},
		{name: "outside lease", addresses: []net.IP{net.ParseIP("172.31.0.1")}},
		{name: "invalid address", addresses: []net.IP{nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookupApplicationSessionPeerIPsV1 = func(string) ([]net.IP, error) { return test.addresses, nil }
			prefixes := []string{"172.31.1.0/24"}
			if _, _, err := prepareApplicationSessionPeerV1("workload", prefixes, test.addresses); err == nil {
				t.Fatal("invalid peer resolution passed")
			}
		})
	}
}

func TestSessionNetworkDoesNotEnableDNSWhenPublicAndLocalAreDenied(t *testing.T) {
	policy := applicationNetworkPolicyV1{
		SessionPeerCIDRs: []string{"172.31.0.3/32"}, SessionLocalCIDRs: []string{"172.31.0.2/32"},
	}
	// Resolver rules are admitted only by the two ordinary grants. Session
	// peers are frozen in /etc/hosts before untrusted code starts.
	if applicationNetworkPolicyAllowsDNSV1(policy) {
		t.Fatal("session peer unexpectedly enables DNS")
	}
	policy.AllowLocal = true
	if !applicationNetworkPolicyAllowsDNSV1(policy) {
		t.Fatal("ordinary local grant did not enable DNS")
	}
}

func TestSessionPeerInputVerdictMatchesExactPeerSourceAndLeaseLocalDestination(t *testing.T) {
	expressions, err := sessionPeerInputVerdictExpressionsV1("172.31.0.3/32", "172.31.0.2/32", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(expressions) != 7 {
		t.Fatalf("session peer input expressions = %#v", expressions)
	}
	source, sourceOK := expressions[0].(*expr.Payload)
	destination, destinationOK := expressions[3].(*expr.Payload)
	verdict, verdictOK := expressions[6].(*expr.Verdict)
	if !sourceOK || source.Offset != 12 || source.Len != 4 ||
		!destinationOK || destination.Offset != 16 || destination.Len != 4 ||
		!verdictOK || verdict.Kind != expr.VerdictAccept {
		t.Fatalf("session peer input expressions = %#v", expressions)
	}
}

func TestReadApplicationResolverCIDRsV1RejectsInvalidNameserver(t *testing.T) {
	for _, content := range []string{"nameserver\n", "nameserver resolver.example\n"} {
		t.Run(strings.TrimSpace(content), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resolv.conf")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readApplicationResolverCIDRsV1(path); err == nil {
				t.Fatalf("resolver content %q unexpectedly passed", content)
			}
		})
	}
}

func TestReadApplicationSessionNetworkConfigurationV1RequiresCanonicalSortedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network-prefixes")
	content := "prefix 172.31.0.0/24\nprefix fd00:1::/64\n" +
		"controller 172.31.0.2\ncontroller fd00:1::2\n" +
		"workload 172.31.0.3\nworkload fd00:1::3\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readApplicationSessionNetworkConfigurationV1(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"172.31.0.0/24", "fd00:1::/64"}; !reflect.DeepEqual(got.Prefixes, want) ||
		!reflect.DeepEqual(got.Peers["controller"], []net.IP{net.ParseIP("172.31.0.2"), net.ParseIP("fd00:1::2")}) ||
		!reflect.DeepEqual(got.Peers["workload"], []net.IP{net.ParseIP("172.31.0.3"), net.ParseIP("fd00:1::3")}) {
		t.Fatalf("session-network configuration = %#v", got)
	}
	for _, content := range []string{
		"prefix 172.31.0.1/24\ncontroller 172.31.0.2\nworkload 172.31.0.3\n",
		"workload 172.31.0.3\nprefix 172.31.0.0/24\ncontroller 172.31.0.2\n",
		"prefix 172.31.0.0/24\nprefix 172.31.0.0/24\ncontroller 172.31.0.2\nworkload 172.31.0.3\n",
		"prefix 172.31.0.0/24\ncontroller 172.31.0.2\n",
		"prefix 172.31.0.0/24\ncontroller 0172.31.0.2\nworkload 172.31.0.3\n",
		"\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readApplicationSessionNetworkConfigurationV1(path); err == nil {
			t.Fatalf("invalid session-network configuration %q passed", content)
		}
	}
}

func TestSessionNetworkVerdictsMatchPeerSourceAndDestinationPrefixes(t *testing.T) {
	for _, test := range []struct {
		name        string
		cidr        string
		addressSize uint32
		source      bool
		wantOffset  uint32
	}{
		{name: "IPv4 input source", cidr: "172.31.0.3/32", addressSize: 4, source: true, wantOffset: 12},
		{name: "IPv4 output destination", cidr: "172.31.0.3/32", addressSize: 4, wantOffset: 16},
		{name: "IPv6 input source", cidr: "fd00:1::3/128", addressSize: 16, source: true, wantOffset: 8},
		{name: "IPv6 output destination", cidr: "fd00:1::3/128", addressSize: 16, wantOffset: 24},
	} {
		t.Run(test.name, func(t *testing.T) {
			expressions, err := addressCIDRVerdictExpressionsV1(test.cidr, test.addressSize, test.source, expr.VerdictAccept)
			if err != nil {
				t.Fatal(err)
			}
			payload, ok := expressions[0].(*expr.Payload)
			if !ok || payload.Offset != test.wantOffset || payload.Len != test.addressSize {
				t.Fatalf("address payload = %#v, want offset %d length %d", expressions[0], test.wantOffset, test.addressSize)
			}
			verdict, ok := expressions[len(expressions)-1].(*expr.Verdict)
			if !ok || verdict.Kind != expr.VerdictAccept {
				t.Fatalf("address verdict = %#v", expressions[len(expressions)-1])
			}
		})
	}
}

func TestApplicationResponseTrafficAcceptsOnlyEstablishedConnections(t *testing.T) {
	if applicationResponseStateMaskV1 != expr.CtStateBitESTABLISHED || applicationResponseStateMaskV1&expr.CtStateBitRELATED != 0 {
		t.Fatalf("response conntrack mask = %#x", applicationResponseStateMaskV1)
	}
}

func TestApplicationRelatedTrafficIsLimitedToICMPNetworkErrors(t *testing.T) {
	if !reflect.DeepEqual(applicationRelatedICMPv4TypesV1, []byte{3, 11, 12}) {
		t.Fatalf("IPv4 related ICMP types = %#v", applicationRelatedICMPv4TypesV1)
	}
	if !reflect.DeepEqual(applicationRelatedICMPv6TypesV1, []byte{1, 2, 3, 4}) {
		t.Fatalf("IPv6 related ICMP types = %#v", applicationRelatedICMPv6TypesV1)
	}
}

func TestApplicationNetworkCIDRsSeparateLocalAmbiguousAndPublicExceptions(t *testing.T) {
	for _, address := range []string{"192.88.99.2", "100:0:0:1::1", "2001:2::1", "3fff::1", "5f00::1"} {
		if !applicationCIDRsContainAddressV1(applicationLocalIPv4CIDRsV1, applicationLocalIPv6CIDRsV1, address) {
			t.Fatalf("special destination %s is not classified as local", address)
		}
	}
	for _, address := range []string{"64:ff9b::a00:1", "64:ff9b:1::1", "2001::1", "2002:a00:1::1"} {
		if !applicationCIDRsContainAddressV1(applicationAmbiguousIPv4CIDRsV1, applicationAmbiguousIPv6CIDRsV1, address) {
			t.Fatalf("translation or tunneling destination %s is not classified as ambiguous", address)
		}
	}
	if applicationCIDRsContainAddressV1(applicationAmbiguousIPv4CIDRsV1, applicationAmbiguousIPv6CIDRsV1, "::ffff:10.0.0.1") {
		t.Fatal("IPv4-mapped IPv6 destination must use its embedded IPv4 class")
	}
	for _, address := range []string{"192.0.0.9", "192.0.0.10", "2001:20::1", "2001:30::1"} {
		if !applicationCIDRsContainAddressV1(applicationPublicIPv4ExceptionsV1, applicationPublicIPv6ExceptionsV1, address) {
			t.Fatalf("globally reachable exception %s is not classified as public", address)
		}
	}
}

func applicationCIDRsContainAddressV1(ipv4 []string, ipv6 []string, address string) bool {
	ip := net.ParseIP(address)
	cidrs := ipv6
	if !strings.Contains(address, ":") {
		cidrs = ipv4
	}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
