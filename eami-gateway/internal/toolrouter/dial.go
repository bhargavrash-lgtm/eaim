package toolrouter

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// safeDialContext resolves addr's host and refuses to connect if any
// resolved address is loopback, link-local, unspecified, or private
// (RFC1918/ULA).
//
// Duplicated from eami-api/internal/api/tool_connectivity.go (B-023)
// rather than imported: eami-gateway and eami-api are separate Go modules
// under go.work, and Go's internal/ package visibility is scoped to the
// module tree, so eami-gateway cannot import eami-api/internal/... at all,
// regardless of export status -- "relocate to a shared location" would
// require a new shared Go module, real structural work this brief doesn't
// scope. This repo already has a precedent for exactly this situation:
// B-025 duplicated isPlaceholderSecret/dsnHasPlaceholderPassword verbatim
// into both eami-api and eami-gateway rather than build shared
// infrastructure for ~30 lines. Same call here.
//
// Higher stakes than the original: B-023's copy guards a connectivity
// *test button* in eami-api's cloud process. This copy guards eami-gateway's
// real dispatch path -- every dynamically-routed tool call actually dials
// through this, not just an admin-initiated test click.
//
// The resolved IP is dialed directly rather than re-resolving addr's host
// inside the dialer, so a DNS answer that changes between the check above
// and the actual connection (rebinding) can't slip through.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
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
		if isBlockedTarget(ip) {
			return nil, errors.New("connections to loopback/link-local/private addresses are not permitted")
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

// isBlockedTarget reports whether ip is a loopback, link-local,
// unspecified, or private (RFC1918/ULA) address -- covers 127.0.0.0/8,
// ::1, 169.254.0.0/16 and fe80::/10 (includes cloud metadata endpoints),
// 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, and fc00::/7, via the
// standard library's own classification.
func isBlockedTarget(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// dialContextFunc matches http.Transport.DialContext's and
// net.Dialer.DialContext's signature -- used so the same Router type can be
// constructed with either the real safeDialContext (production, via New)
// or an unrestricted dialer (tests, via newWithDialer) that can reach a
// local httptest server without tripping the loopback block above.
// Mirrors eami-api/internal/api/tool_connectivity.go's identical
// dialContextFunc seam.
type dialContextFunc = func(ctx context.Context, network, addr string) (net.Conn, error)
