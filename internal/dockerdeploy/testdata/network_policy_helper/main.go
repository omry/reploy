//go:build linux

package main

import (
	"bufio"
	"context"
	"fmt"
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
	case "workload":
		workload(os.Args[2:])
	default:
		fail("unknown mode %q", os.Args[1])
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
	}{{args[1], wantLocal}, {args[2], wantLocal}, {args[3], wantPublic}, {args[4], wantPublic}, {args[5], wantAmbiguous}, {args[6], wantPublicException}} {
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
	resolver := net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", "127.0.0.11:53")
		},
	}
	lookupContext, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	_, lookupErr := resolver.LookupHost(lookupContext, args[12])
	cancel()
	if got := lookupErr == nil; got != wantDNS {
		fail("DNS lookup %s succeeded=%t want=%t err=%v", args[12], got, wantDNS, lookupErr)
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
