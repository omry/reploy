//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) < 2 {
		fail("missing mode")
	}
	switch os.Args[1] {
	case "peer":
		peer()
	case "dial":
		dial(os.Args[2:])
	case "dns":
		dns(os.Args[2:])
	case "workload":
		workload(os.Args[2:])
	default:
		fail("unknown mode %q", os.Args[1])
	}
}

func dns(args []string) {
	if len(args) != 3 {
		fail("dns requires a name, expected result, and transport")
	}
	want, err := strconv.ParseBool(args[1])
	if err != nil {
		fail("parse DNS expectation: %v", err)
	}
	checkDNS(args[0], want, args[2])
	fmt.Println("DNS_PASS")
}

func checkDNS(name string, want bool, transport string) {
	if transport != "udp" && transport != "tcp" {
		fail("unsupported DNS transport %s", transport)
	}
	resolver := net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, transport, address)
		},
	}
	lookupContext, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	_, lookupErr := resolver.LookupHost(lookupContext, name)
	cancel()
	if got := lookupErr == nil; got != want {
		fail("DNS %s lookup %s succeeded=%t want=%t err=%v", transport, name, got, want, lookupErr)
	}
}

func dial(args []string) {
	if len(args) != 2 {
		fail("dial requires an address and expected result")
	}
	want, err := strconv.ParseBool(args[1])
	if err != nil {
		fail("parse dial expectation: %v", err)
	}
	connection, dialErr := net.DialTimeout("tcp", args[0], 750*time.Millisecond)
	if connection != nil {
		_ = connection.Close()
	}
	if got := dialErr == nil; got != want {
		fail("dial %s succeeded=%t want=%t err=%v", args[0], got, want, dialErr)
	}
	fmt.Println("DIAL_PASS")
}

func peer() {
	go serveDNS()
	listener, err := net.Listen("tcp", ":9090")
	if err != nil {
		fail("listen: %v", err)
	}
	for {
		connection, err := listener.Accept()
		if err != nil {
			fail("accept: %v", err)
		}
		_ = connection.Close()
	}
}

func serveDNS() {
	go serveDNSUDP()
	serveDNSTCP()
}

func serveDNSUDP() {
	connection, err := net.ListenPacket("udp", ":53")
	if err != nil {
		fail("listen for DNS: %v", err)
	}
	buffer := make([]byte, 4096)
	for {
		length, address, err := connection.ReadFrom(buffer)
		if err != nil {
			fail("read DNS query: %v", err)
		}
		response, ok := dnsResponse(buffer[:length])
		if !ok {
			continue
		}
		if _, err := connection.WriteTo(response, address); err != nil {
			fail("write DNS response: %v", err)
		}
	}
}

func serveDNSTCP() {
	listener, err := net.Listen("tcp", ":53")
	if err != nil {
		fail("listen for TCP DNS: %v", err)
	}
	for {
		connection, err := listener.Accept()
		if err != nil {
			fail("accept TCP DNS: %v", err)
		}
		go answerDNSTCP(connection)
	}
}

func answerDNSTCP(connection net.Conn) {
	defer connection.Close()
	lengthBuffer := make([]byte, 2)
	if _, err := io.ReadFull(connection, lengthBuffer); err != nil {
		return
	}
	length := int(binary.BigEndian.Uint16(lengthBuffer))
	if length == 0 || length > 4096 {
		return
	}
	query := make([]byte, length)
	if _, err := io.ReadFull(connection, query); err != nil {
		return
	}
	response, ok := dnsResponse(query)
	if !ok {
		return
	}
	framed := make([]byte, 2, len(response)+2)
	binary.BigEndian.PutUint16(framed, uint16(len(response)))
	framed = append(framed, response...)
	for len(framed) != 0 {
		written, err := connection.Write(framed)
		if err != nil || written == 0 {
			return
		}
		framed = framed[written:]
	}
}

func dnsResponse(query []byte) ([]byte, bool) {
	if len(query) < 17 {
		return nil, false
	}
	offset := 12
	for {
		if offset >= len(query) {
			return nil, false
		}
		length := int(query[offset])
		offset++
		if length == 0 {
			break
		}
		if length > 63 || offset+length > len(query) {
			return nil, false
		}
		offset += length
	}
	if offset+4 > len(query) {
		return nil, false
	}
	questionEnd := offset + 4
	kind := binary.BigEndian.Uint16(query[offset : offset+2])
	var answer []byte
	switch kind {
	case 1:
		answer = net.ParseIP("203.0.113.1").To4()
	case 28:
		answer = net.ParseIP("2001:db8::1").To16()
	default:
		return nil, false
	}
	response := append([]byte(nil), query[:questionEnd]...)
	binary.BigEndian.PutUint16(response[2:4], 0x8180)
	binary.BigEndian.PutUint16(response[6:8], 1)
	response = append(response, 0xc0, 0x0c)
	response = binary.BigEndian.AppendUint16(response, kind)
	response = binary.BigEndian.AppendUint16(response, 1)
	response = binary.BigEndian.AppendUint32(response, 0)
	response = binary.BigEndian.AppendUint16(response, uint16(len(answer)))
	response = append(response, answer...)
	return response, true
}

func workload(args []string) {
	if len(args) != 14 {
		fail("workload requires UID, six addresses, four expectations, serve mode, DNS name, and DNS expectation")
	}
	checkStatus(args[0])
	wantLocal, err := strconv.ParseBool(args[7])
	if err != nil {
		fail("parse local expectation: %v", err)
	}
	wantPublic, err := strconv.ParseBool(args[8])
	if err != nil {
		fail("parse public expectation: %v", err)
	}
	wantAmbiguous, err := strconv.ParseBool(args[9])
	if err != nil {
		fail("parse ambiguous expectation: %v", err)
	}
	wantPublicException, err := strconv.ParseBool(args[10])
	if err != nil {
		fail("parse public exception expectation: %v", err)
	}
	for _, item := range []struct {
		address string
		want    bool
	}{
		{args[1], wantLocal}, {ipv4MappedAddress(args[1]), wantLocal}, {args[2], wantLocal},
		{args[3], wantPublic}, {ipv4MappedAddress(args[3]), wantPublic}, {args[4], wantPublic},
		{args[5], wantAmbiguous}, {args[6], wantPublicException},
	} {
		connection, dialErr := net.DialTimeout("tcp", item.address, 750*time.Millisecond)
		if connection != nil {
			_ = connection.Close()
		}
		if got := dialErr == nil; got != item.want {
			fail("dial %s succeeded=%t want=%t err=%v", item.address, got, item.want, dialErr)
		}
	}
	wantDNS, err := strconv.ParseBool(args[13])
	if err != nil {
		fail("parse DNS expectation: %v", err)
	}
	for _, transport := range []string{"udp", "tcp"} {
		checkDNS(args[12], wantDNS, transport)
	}
	fmt.Println("NETWORK_POLICY_PASS")
	if args[11] != "serve" {
		return
	}
	handler := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(response, "reploy-network-policy")
	})
	declared, err := net.Listen("tcp", ":8080")
	if err != nil {
		fail("listen on declared endpoint: %v", err)
	}
	undeclared, err := net.Listen("tcp", ":8081")
	if err != nil {
		_ = declared.Close()
		fail("listen on undeclared endpoint: %v", err)
	}
	go func() {
		if serveErr := http.Serve(undeclared, handler); serveErr != nil {
			fail("serve undeclared endpoint: %v", serveErr)
		}
	}()
	if err := http.Serve(declared, handler); err != nil {
		fail("serve: %v", err)
	}
}

func ipv4MappedAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		fail("split IPv4 address %s: %v", address, err)
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		fail("address %s is not IPv4", address)
	}
	return net.JoinHostPort("::ffff:"+ip.String(), port)
}

func checkStatus(wantUID string) {
	content, err := os.Open("/proc/self/status")
	if err != nil {
		fail("open status: %v", err)
	}
	defer content.Close()
	seenUID := false
	scanner := bufio.NewScanner(content)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			seenUID = len(fields) == 5 && fields[1] == wantUID && fields[2] == wantUID && fields[3] == wantUID && fields[4] == wantUID
		}
		for _, field := range []string{"CapInh:", "CapPrm:", "CapEff:", "CapBnd:", "CapAmb:"} {
			if strings.HasPrefix(line, field) && strings.TrimLeft(strings.Fields(line)[1], "0") != "" {
				fail("nonempty capability status: %s", line)
			}
		}
		if strings.HasPrefix(line, "NoNewPrivs:") && strings.Fields(line)[1] != "1" {
			fail("no-new-privileges missing: %s", line)
		}
		if strings.HasPrefix(line, "Seccomp:") && strings.Fields(line)[1] != "2" {
			fail("seccomp missing: %s", line)
		}
	}
	if err := scanner.Err(); err != nil || !seenUID {
		fail("status identity mismatch for UID %s: %v", wantUID, err)
	}
	raw, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(3)))
	if err == nil {
		_ = unix.Close(raw)
		fail("raw socket unexpectedly succeeded")
	}
}

func htons(value uint16) uint16 { return value<<8 | value>>8 }

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
