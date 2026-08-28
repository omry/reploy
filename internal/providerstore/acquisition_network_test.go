package providerstore

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredentialFreeAcquisitionURLPolicy(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		good bool
	}{
		{name: "https", raw: "https://mirror.example.test/archive", good: true},
		{name: "http", raw: "http://mirror.example.test/archive"},
		{name: "other scheme", raw: "ftp://mirror.example.test/archive"},
		{name: "userinfo", raw: "https://user:password@mirror.example.test/archive"},
		{name: "fragment", raw: "https://mirror.example.test/archive#part"},
		{name: "query", raw: "https://mirror.example.test/archive?signature=secret"},
		{name: "empty query", raw: "https://mirror.example.test/archive?"},
		{name: "empty host", raw: "https:///archive"},
		{name: "opaque", raw: "https:mirror.example.test/archive"},
		{name: "backslash authority", raw: "https://mirror.example.test\\@internal.example.test/archive"},
		{name: "non-ASCII authority", raw: "https://mîrror.example.test/archive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := url.Parse(test.raw)
			if err != nil {
				if test.good {
					t.Fatalf("parse %q: %v", test.raw, err)
				}
				return
			}
			if got := credentialFreeAcquisitionURL(parsed); got != test.good {
				t.Fatalf("credential-free locator = %v, want %v", got, test.good)
			}
			redirectAllowed := strings.Contains(test.name, "query")
			if got := credentialFreeRedirectURL(parsed); got != (test.good || redirectAllowed) {
				t.Fatalf("redirect locator = %v, want %v", got, test.good || redirectAllowed)
			}
		})
	}
}

func TestAcquisitionPublicAddressClassification(t *testing.T) {
	tests := []struct {
		address string
		public  bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"0.0.0.0", false},
		{"10.1.2.3", false},
		{"100.64.1.2", false},
		{"127.0.0.1", false},
		{"169.254.1.2", false},
		{"172.16.1.2", false},
		{"192.0.0.1", false},
		{"192.0.2.1", false},
		{"192.31.196.1", false},
		{"192.52.193.1", false},
		{"192.88.99.1", false},
		{"192.168.1.2", false},
		{"198.18.1.2", false},
		{"198.51.100.1", false},
		{"203.0.113.1", false},
		{"224.0.0.1", false},
		{"240.0.0.1", false},
		{"2001:4860:4860::8888", true},
		{"::", false},
		{"::1", false},
		{"fc00::1", false},
		{"fd12:3456::1", false},
		{"fe80::1", false},
		{"ff02::1", false},
		{"100::1", false},
		{"100:0:0:1::1", false},
		{"2001::1", false},
		{"2001:1::1", false},
		{"2001:2::1", false},
		{"2001:3::1", false},
		{"2001:4:112::1", false},
		{"2001:10::1", false},
		{"2001:20::1", false},
		{"2001:db8::1", false},
		{"2002::1", false},
		{"3fff::1", false},
		{"4000::1", false},
		{"5f00::1", false},
		{"64:ff9b::1", false},
		{"fec0::1", false},
		{"::ffff:8.8.8.8", true},
		{"::ffff:10.1.2.3", false},
		{"::ffff:192.0.2.1", false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			address, err := netip.ParseAddr(test.address)
			if err != nil {
				t.Fatal(err)
			}
			if got := isPublicAcquisitionAddress(address); got != test.public {
				t.Fatalf("public address = %v, want %v", got, test.public)
			}
		})
	}
}

func TestResolvePublicAcquisitionAddressRejectsMixedAnswersAndSelectsDeterministically(t *testing.T) {
	publicAndPrivate := &acquisitionNetwork{resolve: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{
			netip.MustParseAddr("8.8.8.8"),
			netip.MustParseAddr("10.0.0.1"),
		}, nil
	}}
	if _, err := resolvePublicAcquisitionAddress(context.Background(), "mixed.example.test", publicAndPrivate); !errors.Is(err, errRejectedDestination) {
		t.Fatalf("mixed DNS answers error = %v, want destination rejection", err)
	}

	deterministic := &acquisitionNetwork{resolve: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{
			netip.MustParseAddr("8.8.8.8"),
			netip.MustParseAddr("1.1.1.1"),
			netip.MustParseAddr("1.1.1.1"),
		}, nil
	}}
	address, err := resolvePublicAcquisitionAddress(context.Background(), "public.example.test", deterministic)
	if err != nil {
		t.Fatal(err)
	}
	if address != netip.MustParseAddr("1.1.1.1") {
		t.Fatalf("selected address = %s, want 1.1.1.1", address)
	}
}

func TestAcquisitionDialUsesOneValidatedPinBeforeConnectionAndRevalidatesLaterConnections(t *testing.T) {
	var resolutions atomic.Int32
	var dials atomic.Int32
	network := &acquisitionNetwork{
		resolve: func(context.Context, string) ([]netip.Addr, error) {
			if resolutions.Add(1) == 1 {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("192.168.1.10")}, nil
		},
		dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dials.Add(1)
			if address != "93.184.216.34:443" {
				return nil, errors.New("unexpected dial target " + address)
			}
			return acquisitionNoopConn{}, nil
		},
	}
	request, err := http.NewRequest(http.MethodGet, "https://download.example.test/artifact", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pinAcquisitionRequest(request, network); err != nil {
		t.Fatal(err)
	}
	connection, err := acquisitionDialContext(request.Context(), "tcp", "download.example.test:443", network)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	if resolutions.Load() != 1 || dials.Load() != 1 {
		t.Fatalf("first connection resolution/dial counts = %d/%d, want 1/1", resolutions.Load(), dials.Load())
	}
	if _, err := acquisitionDialContext(request.Context(), "tcp", "download.example.test:443", network); !errors.Is(err, errRejectedDestination) {
		t.Fatalf("rebound connection error = %v, want destination rejection", err)
	}
	if dials.Load() != 1 {
		t.Fatalf("rebound connection dial count = %d, want 1", dials.Load())
	}
}

func TestAcquisitionTransportPinsDialTargetAndRetainsTLSHostname(t *testing.T) {
	var serverName atomic.Value
	certificate := acquisitionTestCertificate(t, "download.example.test")
	serverSide, clientSide := net.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		server := tls.Server(serverSide, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				serverName.Store(hello.ServerName)
				return nil, nil
			},
		})
		if err := server.Handshake(); err != nil {
			serverDone <- err
			return
		}
		request, err := http.ReadRequest(bufio.NewReader(server))
		if err != nil {
			serverDone <- err
			return
		}
		request.Body.Close()
		_, err = fmt.Fprint(server, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		serverDone <- err
	}()

	var resolved atomic.Int32
	var dialTarget atomic.Value
	network := &acquisitionNetwork{
		resolve: func(context.Context, string) ([]netip.Addr, error) {
			resolved.Add(1)
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialTarget.Store(address)
			return clientSide, nil
		},
	}
	transport := newAcquisitionHTTPTransport(network)
	// The test certificate is intentionally self-signed; SNI is the property
	// under test here.
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test-only certificate
	client := &http.Client{Transport: transport}
	request, err := http.NewRequest(http.MethodGet, "https://download.example.test:443/artifact", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pinAcquisitionRequest(request, network); err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if resolved.Load() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolved.Load())
	}
	if got := dialTarget.Load().(string); got != "93.184.216.34:443" {
		t.Fatalf("dial target = %q", got)
	}
	if got := serverName.Load().(string); got != "download.example.test" {
		t.Fatalf("TLS server name = %q, want original hostname", got)
	}
}

func TestAcquisitionTransportIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy-user:proxy-password@proxy.example.test:8080")
	t.Setenv("HTTPS_PROXY", "http://proxy-user:proxy-password@proxy.example.test:8080")
	t.Setenv("ALL_PROXY", "http://proxy-user:proxy-password@proxy.example.test:8080")
	var resolvedHost string
	var dialTarget string
	network := &acquisitionNetwork{
		resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
			resolvedHost = host
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialTarget = address
			return nil, errors.New("controlled dial stop")
		},
	}
	transport := newAcquisitionHTTPTransport(network)
	if transport.Proxy != nil {
		t.Fatal("production acquisition transport retained proxy configuration")
	}
	client := &http.Client{Transport: transport}
	request, err := http.NewRequest(http.MethodGet, "https://download.example.test/artifact", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pinAcquisitionRequest(request, network); err != nil {
		t.Fatal(err)
	}
	_, _ = client.Do(request)
	if resolvedHost != "download.example.test" || dialTarget != "93.184.216.34:443" {
		t.Fatalf("proxy bypass resolved %q and dialed %q", resolvedHost, dialTarget)
	}
}

func TestAcquisitionTransportDoesNotInheritGlobalOrInjectedTLSAndProxyPolicy(t *testing.T) {
	originalDefault := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		Proxy:           http.ProxyURL(&url.URL{Scheme: "http", Host: "proxy.example.test:8080"}),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // malicious test configuration
	}
	t.Cleanup(func() { http.DefaultTransport = originalDefault })

	assertSafe := func(name string, client *http.Client) {
		t.Helper()
		transport, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("%s transport type = %T", name, client.Transport)
		}
		if transport.Proxy != nil || transport.TLSClientConfig != nil || transport.DialTLSContext != nil {
			t.Fatalf("%s inherited proxy or TLS policy: %#v", name, transport)
		}
		if transport.DialContext == nil {
			t.Fatalf("%s omitted the pinned dialer", name)
		}
	}

	production, _ := cloneAcquisitionClient(nil, acquisitionTestNetwork(), CoreMaxArtifactRedirects)
	assertSafe("production", production)
	injected, _ := cloneAcquisitionClient(&http.Client{Transport: http.DefaultTransport}, acquisitionTestNetwork(), CoreMaxArtifactRedirects)
	assertSafe("injected standard", injected)
}

func acquisitionTestCertificate(t *testing.T, host string) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Minute),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func TestAcquisitionRedirectsRevalidateSameAndCrossHostAndRedactQueries(t *testing.T) {
	content := []byte("redirected content")
	descriptor := acquisitionTestDescriptor(content)
	var hosts []string
	var queries []string
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Hostname())
		queries = append(queries, request.URL.RawQuery)
		switch request.URL.Path {
		case "/start":
			return acquisitionTestResponse(request, http.StatusFound, "", "https://mirror.example.test/step?sig=first"), nil
		case "/step":
			if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" || request.Header.Get("Referer") != "" {
				return nil, errors.New("sensitive header propagated")
			}
			return acquisitionTestResponse(request, http.StatusFound, "", "https://cdn.example.test/end?sig=second"), nil
		case "/end":
			if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" || request.Header.Get("Referer") != "" {
				return nil, errors.New("sensitive header propagated")
			}
			return acquisitionTestResponse(request, http.StatusOK, string(content), ""), nil
		default:
			return acquisitionTestResponse(request, http.StatusNotFound, "", ""), nil
		}
	})
	var resolutions atomic.Int32
	network := &acquisitionNetwork{resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
		resolutions.Add(1)
		if host == "mirror.example.test" {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		if host == "cdn.example.test" {
			return []netip.Addr{netip.MustParseAddr("151.101.1.69")}, nil
		}
		return nil, errRejectedDestination
	}}
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact: descriptor,
		Source:   ArtifactSource{ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/start"}},
		client:   client,
		network:  network,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(hosts, ",") != "mirror.example.test,mirror.example.test,cdn.example.test" {
		t.Fatalf("redirect hosts = %v", hosts)
	}
	if strings.Join(queries, ",") != ",sig=first,sig=second" {
		t.Fatalf("redirect queries = %v", queries)
	}
	if resolutions.Load() != 3 {
		t.Fatalf("redirect resolutions = %d, want one per request/hop", resolutions.Load())
	}
	if strings.Contains(fmt.Sprintf("%#v", result.Provenance), "sig=") {
		t.Fatalf("redirect query leaked into provenance: %#v", result.Provenance)
	}
}

func TestAcquisitionRejectsUnsafeRedirectBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		location string
		resolver func(string) ([]netip.Addr, error)
	}{
		{name: "downgrade", location: "http://cdn.example.test/end"},
		{name: "userinfo", location: "https://user:password@cdn.example.test/end"},
		{name: "fragment", location: "https://cdn.example.test/end#fragment"},
		{name: "non-public destination", location: "https://cdn.example.test/end", resolver: func(string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := []byte("never reached")
			descriptor := acquisitionTestDescriptor(content)
			var requests atomic.Int32
			client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
				requests.Add(1)
				return acquisitionTestResponse(request, http.StatusFound, "", test.location), nil
			})
			var resolutions atomic.Int32
			resolver := test.resolver
			if resolver == nil {
				resolver = func(string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
				}
			}
			network := &acquisitionNetwork{resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
				resolutions.Add(1)
				if test.name == "non-public destination" && host == "cdn.example.test" {
					return []netip.Addr{netip.MustParseAddr("192.168.1.10")}, nil
				}
				return resolver(host)
			}}
			store, err := NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.AcquireArtifact(context.Background(), AcquisitionRequest{
				Artifact: descriptor,
				Source:   ArtifactSource{ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/start"}},
				client:   client,
				network:  network,
				Policy: func() AcquisitionPolicy {
					policy := DefaultAcquisitionPolicy()
					policy.MaxRedirects = 1
					return policy
				}(),
			})
			if err == nil || !strings.Contains(err.Error(), "outcome=redirect") || strings.Contains(err.Error(), "fragment") || strings.Contains(err.Error(), "password") {
				t.Fatalf("unsafe redirect error = %v", err)
			}
			if requests.Load() != 1 {
				t.Fatalf("unsafe redirect request count = %d, want 1", requests.Load())
			}
			if test.name == "non-public destination" && resolutions.Load() != 2 {
				t.Fatalf("non-public redirect resolutions = %d, want 2", resolutions.Load())
			}
		})
	}
}

func TestAcquisitionRedirectLimitStopsBeforeFollowingNextTarget(t *testing.T) {
	descriptor := acquisitionTestDescriptor([]byte("redirect limit"))
	var requests atomic.Int32
	client := acquisitionTestClient(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return acquisitionTestResponse(request, http.StatusFound, "", "https://mirror.example.test/next?secret=not-diagnostic"), nil
	})
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultAcquisitionPolicy()
	policy.MaxRedirects = 1
	_, err = store.AcquireArtifact(context.Background(), AcquisitionRequest{
		Artifact:    descriptor,
		Source:      ArtifactSource{ID: "source", SHA256: descriptor.SHA256, Mirrors: []string{"https://mirror.example.test/start"}},
		Policy:      policy,
		client:      client,
		network:     acquisitionTestNetwork(),
		OperationID: "redirect-redaction-test",
	})
	if err == nil || !strings.Contains(err.Error(), "outcome=redirect") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("redirect-limit error = %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("redirect-limit request count = %d, want 2", requests.Load())
	}
	attemptsPath, err := store.AcquisitionAttemptsPath(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(filepath.Join(attemptsPath, "redirect-redaction-test", "attempt-000001.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record), "secret") || strings.Contains(string(record), "/next") {
		t.Fatalf("redirect target leaked into durable failure record: %s", record)
	}
}

type acquisitionNoopConn struct{}

func (acquisitionNoopConn) Read([]byte) (int, error)         { return 0, errors.New("noop connection") }
func (acquisitionNoopConn) Write(buffer []byte) (int, error) { return len(buffer), nil }
func (acquisitionNoopConn) Close() error                     { return nil }
func (acquisitionNoopConn) LocalAddr() net.Addr              { return acquisitionNoopAddr{} }
func (acquisitionNoopConn) RemoteAddr() net.Addr             { return acquisitionNoopAddr{} }
func (acquisitionNoopConn) SetDeadline(_ time.Time) error    { return nil }
func (acquisitionNoopConn) SetReadDeadline(_ time.Time) error {
	return nil
}
func (acquisitionNoopConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

type acquisitionNoopAddr struct{}

func (acquisitionNoopAddr) Network() string { return "tcp" }
func (acquisitionNoopAddr) String() string  { return "noop" }
