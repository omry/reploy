package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const webSocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func main() {
	if len(os.Args) < 2 {
		fail("missing mode")
	}
	switch os.Args[1] {
	case "serve":
		serve()
	case "listen":
		if len(os.Args) != 3 {
			fail("listen requires an address")
		}
		listen(os.Args[2])
	case "dial":
		if len(os.Args) != 4 {
			fail("dial requires an address and expected result")
		}
		want, err := strconv.ParseBool(os.Args[3])
		if err != nil {
			fail("parse dial expectation: %v", err)
		}
		dial(os.Args[2], want)
	default:
		fail("unsupported mode %q", os.Args[1])
	}
}

func serve() {
	undeclared, err := net.Listen("tcp", ":8081")
	if err != nil {
		fail("listen on undeclared port: %v", err)
	}
	defer undeclared.Close()
	go acceptAll(undeclared)
	httpListener, err := net.Listen("tcp", ":8080")
	if err != nil {
		fail("listen for HTTP and WebSocket: %v", err)
	}
	defer httpListener.Close()
	go acceptHTTP(httpListener)
	fmt.Println("NETWORK-READY")
	select {}
}

func acceptHTTP(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go handleHTTPConnection(connection)
	}
}

func handleHTTPConnection(connection net.Conn) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	requestLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("HTTP-READ-ERROR %v\n", err)
		return
	}
	parts := strings.Fields(strings.TrimSpace(requestLine))
	if len(parts) != 3 {
		fmt.Printf("HTTP-REQUEST-LINE-ERROR %q\n", requestLine)
		return
	}
	headers := map[string]string{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("HTTP-HEADER-ERROR %v\n", err)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			fmt.Printf("HTTP-HEADER-LINE-ERROR %q\n", line)
			return
		}
		headers[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
	}
	buffer := bufio.NewWriter(connection)
	if parts[0] == http.MethodGet && parts[1] == "/http" {
		payload := "SESSION_HTTP_PASS"
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(payload), payload)
		_ = buffer.Flush()
		return
	}
	if parts[0] != http.MethodGet || parts[1] != "/ws" || !strings.EqualFold(headers["upgrade"], "websocket") {
		_, _ = fmt.Fprint(buffer, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		_ = buffer.Flush()
		return
	}
	digest := sha1.Sum([]byte(headers["sec-websocket-key"] + webSocketGUID))
	accept := base64.StdEncoding.EncodeToString(digest[:])
	_, _ = fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	payload := []byte("SESSION_WS_PASS")
	_ = buffer.WriteByte(0x81)
	_ = buffer.WriteByte(byte(len(payload)))
	_, _ = buffer.Write(payload)
	_ = buffer.Flush()
}

func listen(address string) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fail("listen on %s: %v", address, err)
	}
	fmt.Println("LISTEN-READY")
	acceptAll(listener)
}

func acceptAll(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		_ = connection.Close()
	}
}

func dial(address string, want bool) {
	connection, err := net.DialTimeout("tcp", address, 750*time.Millisecond)
	if connection != nil {
		_ = connection.Close()
	}
	if (err == nil) != want {
		fail("dial %s succeeded=%t want=%t err=%v", address, err == nil, want, err)
	}
	fmt.Printf("DIAL_PASS %s %t\n", address, want)
}

func fail(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
