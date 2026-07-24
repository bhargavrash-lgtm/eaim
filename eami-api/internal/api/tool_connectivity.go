package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/eami/api/internal/toolcreds"
)

// safeDialContext resolves addr's host and refuses to connect if any
// resolved address is loopback, link-local, unspecified, or private
// (RFC1918/ULA).
//
// Unlike eami-gateway's real tool-proxy path (which runs on-prem, inside
// the customer's own network -- see eami-gateway/internal/proxy), TestTool
// runs in eami-api, EAMI's own cloud SaaS process. Without this guard, an
// org-scoped admin/operator (a role that has no other legitimate network
// reach into EAMI's cloud environment) could point a tool's base_url/
// connection_string at cloud metadata endpoints (e.g. 169.254.169.254) or
// internal-only services and use connected/auth-failed/unreachable as an
// oracle to probe them -- a real boundary crossing this feature would
// otherwise introduce, not merely a restatement of capability the caller
// already has. Shared by both the REST and database checks.
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
		if isBlockedTestTarget(ip) {
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

// isBlockedTestTarget reports whether ip is a loopback, link-local,
// unspecified, or private (RFC1918/ULA) address -- covers 127.0.0.0/8,
// ::1, 169.254.0.0/16 and fe80::/10 (includes cloud metadata endpoints),
// 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, and fc00::/7, via the
// standard library's own classification.
func isBlockedTestTarget(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// dialContextFunc is an alias for pgconn.DialFunc's signature (identical to
// net.Dialer.DialContext's), used so the same dialer value can be injected
// into both the REST (http.Transport.DialContext) and database
// (pgconn.Config.DialFunc) checks -- production always uses safeDialContext
// (via testToolConnectivity); tests inject an unrestricted dialer to
// exercise real round-trips against local httptest servers/listeners,
// which would otherwise trip safeDialContext's loopback block.
type dialContextFunc = pgconn.DialFunc

// toolTestTimeout bounds every connectivity check in this file. Long enough
// for DNS + TLS handshake + a legitimately slow-but-working downstream,
// short enough that one hung target can't tie up a request indefinitely.
// A single attempt within this bound is the whole v1 contract -- no
// retries, per this task's brief.
const toolTestTimeout = 8 * time.Second

const (
	reasonAuthFailed    = "auth-failed"
	reasonUnreachable   = "unreachable"
	reasonMisconfigured = "misconfigured"
)

// toolTestOutcome is the granular result of a connectivity attempt --
// richer than gateway_tools.status's fixed 3-value DB CHECK constraint
// (connected/degraded/disconnected, schema frozen, not touched by this
// task). The full outcome is what the HTTP response reports; dbStatus()
// is only the lossy projection used for the persisted column.
type toolTestOutcome struct {
	Connected bool
	Reason    string // reasonAuthFailed | reasonUnreachable | reasonMisconfigured; empty if Connected
	Detail    string // human-readable, safe to return -- never derived from decrypted credential material
	LatencyMs int64
}

// dbStatus maps the outcome onto gateway_tools.status's 3-value vocabulary.
// auth-failed is stored as "degraded" (the endpoint was reached; the
// credentials are what's wrong) rather than "disconnected" (which would
// make it indistinguishable from genuine unreachability in the DB).
func (o toolTestOutcome) dbStatus() string {
	switch {
	case o.Connected:
		return "connected"
	case o.Reason == reasonAuthFailed:
		return "degraded"
	default: // unreachable, misconfigured
		return "disconnected"
	}
}

// errorMessage renders the outcome as TestTool's response `error` field:
// nil on success, "<reason>: <detail>" on failure.
func (o toolTestOutcome) errorMessage() *string {
	if o.Connected {
		return nil
	}
	msg := o.Reason
	if o.Detail != "" {
		msg = fmt.Sprintf("%s: %s", o.Reason, o.Detail)
	}
	return &msg
}

// testToolConnectivity attempts a real connection to the tool described by
// toolType/authType/baseURL, decrypting credsEncrypted (if present) via
// cipher first.
//
// cipher may be nil (encryption not configured); if credsEncrypted is also
// nil that's fine, nothing to decrypt. If credsEncrypted is non-nil but
// cipher is nil, or decryption fails (wrong/rotated key), that is reported
// as misconfigured -- never a panic, and the underlying decrypt error is
// never included in the returned Detail (kept generic on purpose: the
// exact reason a stored blob won't decrypt isn't something to expose
// outside this function, even though AES-GCM auth-tag-failure errors don't
// themselves contain the key or plaintext).
func testToolConnectivity(ctx context.Context, toolType, authType string, baseURL *string, credsEncrypted []byte, cipher *toolcreds.Cipher) toolTestOutcome {
	return testToolConnectivityWithDialer(ctx, toolType, authType, baseURL, credsEncrypted, cipher, safeDialContext)
}

// testToolConnectivityWithDialer is testToolConnectivity's actual
// implementation, parameterized by dial so tests can exercise real network
// round-trips against local httptest servers/listeners without tripping
// safeDialContext's loopback/private-address block. testToolConnectivity
// (the only production call site, from TestTool) always passes
// safeDialContext.
func testToolConnectivityWithDialer(ctx context.Context, toolType, authType string, baseURL *string, credsEncrypted []byte, cipher *toolcreds.Cipher, dial dialContextFunc) toolTestOutcome {
	start := time.Now()
	finish := func(o toolTestOutcome) toolTestOutcome {
		o.LatencyMs = time.Since(start).Milliseconds()
		return o
	}

	var creds ToolCredentials
	if len(credsEncrypted) > 0 {
		if cipher == nil {
			return finish(toolTestOutcome{Reason: reasonMisconfigured, Detail: "stored credentials cannot be decrypted (encryption not configured)"})
		}
		plaintext, err := cipher.Decrypt(credsEncrypted)
		if err != nil {
			return finish(toolTestOutcome{Reason: reasonMisconfigured, Detail: "stored credentials could not be decrypted"})
		}
		if err := json.Unmarshal(plaintext, &creds); err != nil {
			return finish(toolTestOutcome{Reason: reasonMisconfigured, Detail: "stored credentials are not in the expected shape"})
		}
	}

	ctx, cancel := context.WithTimeout(ctx, toolTestTimeout)
	defer cancel()

	switch toolType {
	case "rest_api":
		return finish(testRESTTool(ctx, baseURL, authType, creds, dial))
	case "database":
		return finish(testDatabaseTool(ctx, creds.ConnectionString, dial))
	case "mcp":
		// mcp_command describes a local subprocess meant to run on the
		// gateway host, not eami-api's cloud process -- shelling out to an
		// admin-supplied command string from the SaaS backend would be a
		// command-injection/RCE surface, and the wrong host anyway (see
		// eami-gateway/internal/proxy: the gateway's real dispatch path
		// doesn't consult gateway_tools per-tool either). Report honestly
		// rather than fabricate a "connected" this function has no way to
		// verify.
		return finish(toolTestOutcome{Reason: reasonMisconfigured, Detail: "MCP tools run as a local subprocess on the gateway host; connectivity cannot be tested from the SaaS API"})
	default:
		return finish(toolTestOutcome{Reason: reasonMisconfigured, Detail: fmt.Sprintf("unknown tool type %q", toolType)})
	}
}

// testRESTTool performs a real HTTP round-trip to baseURL. Any completed
// HTTP response other than 401/403 counts as "connected" -- reaching the
// server and getting a response (even a 404 or 500) proves DNS/TLS/network
// all work, which is what a connectivity test is actually asserting; 401/
// 403 specifically indicate the credentials were rejected.
func testRESTTool(ctx context.Context, baseURL *string, authType string, creds ToolCredentials, dial dialContextFunc) toolTestOutcome {
	if baseURL == nil || *baseURL == "" {
		return toolTestOutcome{Reason: reasonMisconfigured, Detail: "base_url is not set"}
	}
	if authType == "basic" {
		// ToolCredentials has no username/password fields -- only
		// api_key/oauth_client_id/oauth_client_secret/connection_string
		// (openapi.yaml's documented ToolCreate.credentials shape, frozen
		// by B-022). There is no credential material this function could
		// supply for HTTP Basic auth.
		return toolTestOutcome{Reason: reasonMisconfigured, Detail: "auth_type is basic, but the stored credentials shape has no username/password fields"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *baseURL, nil)
	if err != nil {
		return toolTestOutcome{Reason: reasonMisconfigured, Detail: "base_url is not a valid request URL"}
	}
	switch {
	case creds.APIKey != "":
		req.Header.Set("Authorization", "Bearer "+creds.APIKey)
	case creds.OAuthClientID != "" && creds.OAuthClientSecret != "":
		// Best-effort: not a real OAuth2 token exchange (no token endpoint
		// is stored anywhere to exchange against), just HTTP Basic using
		// the client credentials, which some simple API-key-style
		// integrations accept in place of a real OAuth flow.
		req.SetBasicAuth(creds.OAuthClientID, creds.OAuthClientSecret)
	}
	// No credentials at all is not special-cased -- the request is still
	// attempted, and a real 401 from the far end correctly surfaces as
	// auth-failed, which is an accurate (if unsurprising) result.

	// DisableKeepAlives: this is a one-shot request with no reuse benefit --
	// without it, the connection (and its readLoop/writeLoop goroutines)
	// stays idle in the Transport's pool indefinitely, since this
	// ad-hoc per-call Transport is never referenced again to close it.
	client := &http.Client{Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return toolTestOutcome{Reason: reasonUnreachable, Detail: classifyHTTPError(err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return toolTestOutcome{Reason: reasonAuthFailed, Detail: fmt.Sprintf("remote returned HTTP %d", resp.StatusCode)}
	}
	return toolTestOutcome{Connected: true}
}

// classifyHTTPError turns a client.Do error into a safe, generic
// description -- deliberately not err.Error() verbatim (a *url.Error
// embeds the request URL, which is fine, but there's no reason to risk
// forwarding anything more than a category here).
func classifyHTTPError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timed out"
	}
	return "connection failed"
}

// testDatabaseTool attempts a real Postgres connection. pgx.Connect
// performs the full startup handshake including authentication, so a
// successful connect already proves both reachability and valid
// credentials -- no follow-up query is needed to distinguish them.
func testDatabaseTool(ctx context.Context, connectionString string, dial dialContextFunc) toolTestOutcome {
	if connectionString == "" {
		return toolTestOutcome{Reason: reasonMisconfigured, Detail: "connection_string is not set"}
	}
	cfg, err := pgx.ParseConfig(connectionString)
	if err != nil {
		return toolTestOutcome{Reason: reasonMisconfigured, Detail: "connection_string could not be parsed"}
	}
	cfg.DialFunc = dial

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		reason, detail := classifyPgConnectError(err)
		return toolTestOutcome{Reason: reason, Detail: detail}
	}
	defer func() {
		// Bounded independently of ctx (which may already be near its
		// deadline by the time we get here): if the peer goes unresponsive
		// after a successful handshake, Close must not be able to hang
		// past this regardless -- consistent with "8s bounds everything"
		// above.
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn.Close(closeCtx)
	}()
	return toolTestOutcome{Connected: true}
}

// classifyPgConnectError distinguishes "reached Postgres, credentials
// rejected" from "couldn't reach Postgres at all". Pure function so it can
// be unit-tested directly against constructed errors without a live
// database.
func classifyPgConnectError(err error) (reason, detail string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// SQLSTATE class 28 = invalid_authorization_specification, covers
		// 28P01 (invalid_password) and 28000 (invalid_authorization_specification).
		if strings.HasPrefix(pgErr.Code, "28") {
			return reasonAuthFailed, "database rejected the credentials"
		}
		return reasonUnreachable, "database returned an error"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return reasonUnreachable, "timed out"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return reasonUnreachable, "timed out"
	}
	return reasonUnreachable, "connection failed"
}
