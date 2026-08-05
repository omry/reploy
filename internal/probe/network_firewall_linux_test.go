//go:build linux

package probe

import (
	"net"
	"reflect"
	"testing"

	"github.com/google/nftables/expr"
)

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

func TestApplicationNonPublicCIDRsCoverSpecialAndTranslationDestinations(t *testing.T) {
	for _, address := range []string{
		"192.88.99.2",
		"64:ff9b::a00:1",
		"64:ff9b:1::1",
		"100:0:0:1::1",
		"2001:2::1",
		"2002:a00:1::1",
		"3fff::1",
		"5f00::1",
	} {
		if !applicationCIDRsContainAddressV1(address) {
			t.Fatalf("special destination %s is not classified as non-public", address)
		}
	}
	if applicationCIDRsContainAddressV1("2001:20::1") {
		t.Fatal("globally reachable ORCHIDv2 destination is classified as non-public")
	}
}

func applicationCIDRsContainAddressV1(address string) bool {
	ip := net.ParseIP(address)
	cidrs := applicationNonPublicIPv6CIDRsV1
	if ip.To4() != nil {
		cidrs = applicationNonPublicIPv4CIDRsV1
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
