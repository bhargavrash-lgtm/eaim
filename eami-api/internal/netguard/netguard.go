// Package netguard is eami-api's single outbound-connection SSRF guard.
//
// eami-api runs in EAMI's own cloud environment, so any feature that makes
// it connect to a tenant-supplied address (a tool's base_url, an OpenAPI
// spec_url, a Slack webhook) would otherwise let an org-scoped admin or
// operator reach loopback, private-network and cloud-metadata addresses
// they have no other route to, and use success/failure differences to map
// them. Every such outbound path dials through DialContext.
//
// Moved here from api/tool_connectivity.go (B-238) so the alerting engine,
// which the api package imports, can share the exact same guard rather
// than a second copy.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// ErrBlockedAddress is returned by DialContext when the target resolves to
// a blocked address.
var ErrBlockedAddress = errors.New("connections to loopback/link-local/private addresses are not permitted")

// DialFunc is net.Dialer.DialContext's signature. It is an unnamed func
// type so pgconn.DialFunc values (the api package's test override) are
// assignable to it.
type DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// IsBlocked reports whether ip must never be connected to:
//   - loopback, link-local (incl. 169.254.169.254 / fd00:ec2::254-style
//     metadata), unspecified, private (RFC1918/ULA) and multicast, via the
//     standard library's own classification;
//   - special-purpose IPv4 ranges the standard library doesn't classify
//     (blockedPrefixes: CGNAT 100.64.0.0/10 -- which holds Alibaba Cloud's
//     100.100.100.200 metadata service -- 0.0.0.0/8, 198.18.0.0/15,
//     240.0.0.0/4 incl. broadcast, documentation and IETF ranges);
//   - IPv6 forms that carry an IPv4 address (IPv4-mapped, IPv4-compatible
//     ::/96, NAT64 64:ff9b::/96, 6to4 2002::/16): the embedded IPv4 is
//     checked by the same rules, so a NAT64 route to 10.0.0.5 is refused
//     while a NAT64 route to a public address still works (B-238 review);
//   - IPv6 prefixes whose embedded address can't be recovered or that are
//     never a legitimate public target (Teredo 2001::/32, local-use NAT64
//     64:ff9b:1::/48, site-local fec0::/10, documentation 2001:db8::/32).
func IsBlocked(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true // not a valid IP at all: refuse
	}
	addr = addr.Unmap()
	if v4, ok := embeddedIPv4(addr); ok && isBlockedAddr(v4) {
		return true
	}
	return isBlockedAddr(addr)
}

func isBlockedAddr(a netip.Addr) bool {
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT (Alibaba metadata 100.100.100.200)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments (incl. 192.0.0.192)
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, incl. 255.255.255.255
	netip.MustParsePrefix("2001::/32"),       // Teredo (obfuscated embedded IPv4)
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64 (RFC 8215)
	netip.MustParsePrefix("fec0::/10"),       // deprecated site-local
}

var (
	ipv4CompatPrefix = netip.MustParsePrefix("::/96")
	nat64Prefix      = netip.MustParsePrefix("64:ff9b::/96")
	siitPrefix       = netip.MustParsePrefix("::ffff:0:0:0/96") // RFC 7915 IPv4-translated (::ffff:0:a.b.c.d)
	sixToFourPrefix  = netip.MustParsePrefix("2002::/16")
)

// embeddedIPv4 returns the IPv4 address carried inside an IPv6 address in
// the IPv4-compatible (::a.b.c.d), SIIT IPv4-translated (::ffff:0:a.b.c.d),
// NAT64 well-known-prefix (64:ff9b::/96)
// or 6to4 (2002:AABB:CCDD::/48) forms.
func embeddedIPv4(a netip.Addr) (netip.Addr, bool) {
	if !a.Is6() {
		return netip.Addr{}, false
	}
	b := a.As16()
	switch {
	case nat64Prefix.Contains(a), ipv4CompatPrefix.Contains(a), siitPrefix.Contains(a):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case sixToFourPrefix.Contains(a):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	return netip.Addr{}, false
}

// DialContext resolves addr's host and refuses to connect if any resolved
// address is blocked (see IsBlocked).
//
// The resolved IP is dialed directly rather than re-resolving addr's host
// inside the dialer, so a DNS answer that changes between the check and
// the actual connection (rebinding) can't slip through.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	var resolved []net.IP
	if ip := net.ParseIP(host); ip != nil {
		resolved = []net.IP{ip}
	} else {
		ipAddrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range ipAddrs {
			resolved = append(resolved, a.IP)
		}
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("no addresses resolved for %q", host)
	}
	for _, ip := range resolved {
		if IsBlocked(ip) {
			return nil, ErrBlockedAddress
		}
	}

	// Try every validated address in order (mirrors net.Dialer's own
	// multi-address fallback behavior) rather than only the first -- a host
	// whose first A/AAAA record happens to be down but whose second is
	// reachable should still succeed, not report unreachable.
	d := &net.Dialer{}
	var lastErr error
	for _, ip := range resolved {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// NewHTTPClient returns an HTTP client whose every connection goes through
// dial (production: DialContext), bounded by timeout end to end.
//
//   - Proxy is explicitly nil: an environment-configured proxy would make
//     the dialer see only the proxy's address, never the real target.
//   - Redirects are never followed: a public URL answering 302 to an
//     internal address must not become a second request. The 3xx response
//     itself is returned to the caller.
func NewHTTPClient(dial DialFunc, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dial,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
