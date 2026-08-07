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
