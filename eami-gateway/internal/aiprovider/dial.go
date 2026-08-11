package aiprovider

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
// Duplicated from eami-gateway/internal/toolrouter/dial.go (B-023/B-044)
// rather than imported or exported-and-shared: toolrouter is explicitly
// read-only scope for this brief (its rest_api dispatch logic is frozen),
// and its safeDialContext/isBlockedTarget are unexported, so importing
// them isn't possible without modifying that file. This repo already has
// precedent for duplicating this exact function across a real module
// boundary (toolrouter/dial.go's own doc comment, duplicating from
// eami-api); here the boundary is a scope constraint rather than a Go
// module boundary, but the resolution is the same: duplicate a small,
// self-contained security helper rather than touch frozen code.
//
// Lower stakes than toolrouter's copy in one real sense (Thread A
// investigation, Part C): an AI provider adapter's target host is
// hardcoded per-adapter in code (e.g. claude.go's api.anthropic.com), not
// admin-supplied free text -- there is no equivalent arbitrary-URL SSRF
// surface for the common case. This guard is kept anyway as defense in
// depth (a compromised/spoofed DNS answer for a hardcoded hostname is
// still worth blocking from resolving to a private range) and because a
// future enterprise-variant adapter (e.g. Azure OpenAI, with an
// admin-configured endpoint) would need it for real.
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
// unspecified, or private (RFC1918/ULA) address.
func isBlockedTarget(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// dialContextFunc matches http.Transport.DialContext's signature -- used
// so the same http.Client-building code can be exercised in tests against
// a local httptest server without tripping the loopback block above.
// Mirrors toolrouter/dial.go's identical seam.
type dialContextFunc = func(ctx context.Context, network, addr string) (net.Conn, error)
