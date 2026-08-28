package providerstore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	errRejectedRedirect    = errors.New("redirect rejected by acquisition bounds")
	errRejectedDestination = errors.New("destination rejected by acquisition network policy")
)

// acquisitionNetwork is deliberately package-internal. It is a hermetic test
// seam; production callers always use the standard resolver and pinned dialer.
type acquisitionNetwork struct {
	resolve func(context.Context, string) ([]netip.Addr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
}

type acquisitionRedirectCounter struct {
	count int
}

type acquisitionPinContextKey struct{}

type acquisitionPinnedAddress struct {
	mu        sync.Mutex
	host      string
	addresses []netip.Addr
	used      bool
}

var (
	acquisitionNonPublicIPv4 = [...]netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("192.31.196.0/24"),
		netip.MustParsePrefix("192.52.193.0/24"),
		netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/4"),
	}
	acquisitionNonPublicIPv6 = [...]netip.Prefix{
		netip.MustParsePrefix("::/96"),
		netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("100:0:0:1::/64"),
		netip.MustParsePrefix("2001::/32"),
		netip.MustParsePrefix("2001:1::/32"),
		netip.MustParsePrefix("2001:2::/48"),
		netip.MustParsePrefix("2001:3::/32"),
		netip.MustParsePrefix("2001:4:112::/48"),
		netip.MustParsePrefix("2001:10::/28"),
		netip.MustParsePrefix("2001:20::/28"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2002::/16"),
		netip.MustParsePrefix("3fff::/20"),
		netip.MustParsePrefix("5f00::/16"),
		netip.MustParsePrefix("64:ff9b::/96"),
		netip.MustParsePrefix("64:ff9b:1::/48"),
		netip.MustParsePrefix("fec0::/10"),
	}
	acquisitionAllocatedGlobalIPv6 = netip.MustParsePrefix("2000::/3")
)

func credentialFreeAcquisitionURLString(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && credentialFreeAcquisitionURL(parsed)
}

func credentialFreeAcquisitionURL(parsed *url.URL) bool {
	return credentialFreeURL(parsed, false)
}

func credentialFreeRedirectURL(parsed *url.URL) bool {
	return credentialFreeURL(parsed, true)
}

func credentialFreeURL(parsed *url.URL, allowQuery bool) bool {
	if parsed == nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawFragment != "" || strings.ContainsAny(parsed.String(), "\r\n") {
		return false
	}
	if strings.ContainsAny(parsed.Host, "\\") || strings.Contains(parsed.Hostname(), "%") || strings.IndexFunc(parsed.Hostname(), func(character rune) bool { return character > '\x7f' }) >= 0 {
		return false
	}
	return allowQuery || (!parsed.ForceQuery && parsed.RawQuery == "")
}

func isPublicAcquisitionAddress(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	if address.Is4() {
		for _, prefix := range acquisitionNonPublicIPv4 {
			if prefix.Contains(address) {
				return false
			}
		}
		return address.IsGlobalUnicast()
	}
	if !address.Is6() || !acquisitionAllocatedGlobalIPv6.Contains(address) || address.IsUnspecified() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsMulticast() || !address.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range acquisitionNonPublicIPv6 {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func defaultAcquisitionResolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address}, nil
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func defaultAcquisitionDial(ctx context.Context, network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func resolvePublicAcquisitionAddresses(ctx context.Context, host string, network *acquisitionNetwork) ([]netip.Addr, error) {
	if host == "" {
		return nil, errRejectedDestination
	}
	resolver := defaultAcquisitionResolve
	if network != nil && network.resolve != nil {
		resolver = network.resolve
	}
	addresses, err := resolver(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve acquisition destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errRejectedDestination
	}
	unique := make(map[netip.Addr]struct{}, len(addresses))
	normalized := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		if !isPublicAcquisitionAddress(address) {
			return nil, errRejectedDestination
		}
		address = address.Unmap()
		if _, found := unique[address]; found {
			continue
		}
		unique[address] = struct{}{}
		normalized = append(normalized, address)
	}
	if len(normalized) == 0 {
		return nil, errRejectedDestination
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].Compare(normalized[right]) < 0
	})
	return normalized, nil
}

func pinAcquisitionRequest(request *http.Request, network *acquisitionNetwork) error {
	if request == nil || request.URL == nil || !credentialFreeRedirectURL(request.URL) {
		return errRejectedDestination
	}
	addresses, err := resolvePublicAcquisitionAddresses(request.Context(), request.URL.Hostname(), network)
	if err != nil {
		return err
	}
	pin := &acquisitionPinnedAddress{
		host:      strings.ToLower(request.URL.Hostname()),
		addresses: append([]netip.Addr(nil), addresses...),
	}
	*request = *request.WithContext(context.WithValue(request.Context(), acquisitionPinContextKey{}, pin))
	return nil
}

func takeAcquisitionPin(ctx context.Context, host string) ([]netip.Addr, bool) {
	pin, ok := ctx.Value(acquisitionPinContextKey{}).(*acquisitionPinnedAddress)
	if !ok || pin == nil || !strings.EqualFold(pin.host, host) {
		return nil, false
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	if pin.used {
		return nil, false
	}
	pin.used = true
	return append([]netip.Addr(nil), pin.addresses...), true
}

func acquisitionDialContext(ctx context.Context, networkName, address string, network *acquisitionNetwork) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, errRejectedDestination
	}
	var pinned []netip.Addr
	var found bool
	if pinned, found = takeAcquisitionPin(ctx, host); !found {
		pinned, err = resolvePublicAcquisitionAddresses(ctx, host, network)
		if err != nil {
			return nil, err
		}
	}
	dialer := defaultAcquisitionDial
	if network != nil && network.dial != nil {
		dialer = network.dial
	}
	var dialErrors []error
	for _, address := range pinned {
		if networkName == "tcp4" && !address.Is4() || networkName == "tcp6" && !address.Is6() {
			continue
		}
		connection, dialErr := dialer(ctx, networkName, net.JoinHostPort(address.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		dialErrors = append(dialErrors, dialErr)
	}
	if len(dialErrors) == 0 {
		return nil, errRejectedDestination
	}
	return nil, errors.Join(dialErrors...)
}

func newAcquisitionHTTPTransport(network *acquisitionNetwork) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, networkName, address string) (net.Conn, error) {
			return acquisitionDialContext(ctx, networkName, address, network)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

type acquisitionValidatingRoundTripper struct {
	next    http.RoundTripper
	network *acquisitionNetwork
}

func (transport *acquisitionValidatingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || !credentialFreeRedirectURL(request.URL) {
		return nil, errRejectedDestination
	}
	if _, found := takeAcquisitionPin(request.Context(), request.URL.Hostname()); !found {
		request = request.Clone(request.Context())
		if err := pinAcquisitionRequest(request, transport.network); err != nil {
			return nil, errRejectedDestination
		}
	}
	return transport.next.RoundTrip(request)
}

func (transport *acquisitionValidatingRoundTripper) CloseIdleConnections() {
	if closer, ok := transport.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func cloneAcquisitionClient(base *http.Client, network *acquisitionNetwork, maxRedirects int) (*http.Client, *acquisitionRedirectCounter) {
	client := &http.Client{Transport: newAcquisitionHTTPTransport(network)}
	if base != nil {
		*client = *base
		client.Jar = nil
		if base.Transport == nil {
			client.Transport = newAcquisitionHTTPTransport(network)
		} else if _, isStandardTransport := base.Transport.(*http.Transport); isStandardTransport {
			client.Transport = newAcquisitionHTTPTransport(network)
		} else {
			client.Transport = &acquisitionValidatingRoundTripper{next: base.Transport, network: network}
		}
	}
	redirects := &acquisitionRedirectCounter{}
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		redirects.count++
		if redirects.count > maxRedirects || !credentialFreeRedirectURL(request.URL) {
			return errRejectedRedirect
		}
		// net/http copies request headers and synthesizes Referer before this
		// callback. Artifact requests need no caller-supplied headers, so clear
		// them all before a redirect can disclose a signed prior-hop query.
		request.Header = make(http.Header)
		if err := pinAcquisitionRequest(request, network); err != nil {
			return errRejectedRedirect
		}
		return nil
	}
	return client, redirects
}
