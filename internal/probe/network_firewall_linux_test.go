//go:build linux

package probe

import (
	"errors"
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
	prior := lookupApplicationSessionPeerIPsV1
	t.Cleanup(func() { lookupApplicationSessionPeerIPsV1 = prior })
	lookupApplicationSessionPeerIPsV1 = func(peer string) ([]net.IP, error) {
		if peer != "workload" {
			t.Fatalf("peer = %q", peer)
		}
		return []net.IP{net.ParseIP("fd00:1::3"), net.ParseIP("172.31.0.3"), net.ParseIP("172.31.0.3")}, nil
	}
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := prepareApplicationSessionPeerV1("workload", []string{"172.31.0.0/24", "fd00:1::/64"}, hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"172.31.0.3/32", "fd00:1::3/128"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exact peer CIDRs = %#v, want %#v", got, want)
	}
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "127.0.0.1 localhost\n172.31.0.3 workload\nfd00:1::3 workload\n" {
		t.Fatalf("hosts content = %q", content)
	}
}

func TestPrepareApplicationSessionPeerV1RejectsUnresolvedOrOutOfNetworkAddress(t *testing.T) {
	prior := lookupApplicationSessionPeerIPsV1
	t.Cleanup(func() { lookupApplicationSessionPeerIPsV1 = prior })
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		addresses []net.IP
		err       error
	}{
		{name: "lookup failure", err: errors.New("lookup failed")},
		{name: "no addresses"},
		{name: "outside lease", addresses: []net.IP{net.ParseIP("172.31.0.1")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookupApplicationSessionPeerIPsV1 = func(string) ([]net.IP, error) { return test.addresses, test.err }
			prefixes := []string{"172.31.1.0/24"}
			if _, err := prepareApplicationSessionPeerV1("workload", prefixes, hostsPath); err == nil {
				t.Fatal("invalid peer resolution passed")
			}
		})
	}
}

func TestSessionNetworkDoesNotEnableDNSWhenPublicAndLocalAreDenied(t *testing.T) {
	policy := applicationNetworkPolicyV1{SessionCIDRs: []string{"172.31.0.3/32"}}
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

func TestReadApplicationSessionNetworkCIDRsV1RequiresCanonicalSortedPrefixes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network-prefixes")
	if err := os.WriteFile(path, []byte("172.31.0.0/24\nfd00:1::/64\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readApplicationSessionNetworkCIDRsV1(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"172.31.0.0/24", "fd00:1::/64"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session-network CIDRs = %#v, want %#v", got, want)
	}
	for _, content := range []string{
		"172.31.0.1/24\n",
		"fd00:1::/64\n172.31.0.0/24\n",
		"172.31.0.0/24\n172.31.0.0/24\n",
		"\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readApplicationSessionNetworkCIDRsV1(path); err == nil {
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
