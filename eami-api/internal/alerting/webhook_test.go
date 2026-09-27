package alerting

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eami/api/internal/netguard"
)

func unrestrictedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

// B-238: save-time validation rejects every internal or non-https target
// with the one fixed error, and accepts a public https URL.
func TestValidateWebhookURL(t *testing.T) {
	for _, raw := range []string{
		"http://hooks.slack.com/services/x", // not https
		"http://127.0.0.1:8081/health",
		"http://postgres:5432/",
		"https://127.0.0.1/",
		"https://127.0.0.1:1/",
		"https://[::1]/",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.5/",
		"https://172.16.3.4/",
		"https://192.168.1.1/",
		"https://0.0.0.0/",
		"https://localhost/",
		"https://svc.localhost/",
		"https://does-not-exist.invalid/",
		"https://user:pw@93.184.216.34/",
		"https:///nohost",
		"not a url",
		"ftp://93.184.216.34/",
	} {
		if err := ValidateWebhookURL(context.Background(), raw); !errors.Is(err, ErrInvalidWebhookURL) {
			t.Errorf("ValidateWebhookURL(%q) = %v, want ErrInvalidWebhookURL", raw, err)
		}
	}
	// A public IP literal needs no DNS, so this stays hermetic.
	if err := ValidateWebhookURL(context.Background(), "https://93.184.216.34/services/T/B/x"); err != nil {
		t.Fatalf("public https URL rejected: %v", err)
	}
}

// The production webhook client never reaches a loopback server; the same
// send with an unrestricted dialer does (proving the server was reachable
// and the guard is what stopped it).
func TestSendSlack_ProductionClientBlocksInternalTargets(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()

	if err := SendSlack(context.Background(), NewWebhookClient(), srv.URL, "x"); err == nil {
		t.Fatal("production webhook client delivered to a loopback server")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("loopback server received %d requests through the production client", n)
	}
	if err := SendSlack(context.Background(), netguard.NewHTTPClient(unrestrictedDial, time.Second), srv.URL, "x"); err != nil {
		t.Fatalf("unrestricted send: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("hits=%d after unrestricted send, want 1", n)
	}
}

// NewEngine wires the guarded client in (hermetic: no DB needed to prove
// the alert engine's sends go through the guard).
func TestNewEngine_UsesGuardedWebhookClient(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()
	e := NewEngine(nil, "", "")
	if err := SendSlack(context.Background(), e.webhook, srv.URL, "x"); err == nil || hits.Load() != 0 {
		t.Fatalf("engine webhook client: err=%v hits=%d, want a refusal and no request", err, hits.Load())
	}
}

// The webhook URL is a credential: SendSlack's error (which callers log)
// must never contain it.
func TestSendSlack_ErrorNeverContainsURL(t *testing.T) {
	for _, raw := range []string{
		"https://127.0.0.1:1/services/T0/B0/SECRETTOKEN",      // blocked
		"https://does-not-exist.invalid/services/SECRETTOKEN", // DNS failure
		"https://exa mple.com/services/SECRETTOKEN",           // unparseable
	} {
		err := SendSlack(context.Background(), NewWebhookClient(), raw, "x")
		if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") {
			t.Errorf("SendSlack(%q) err=%v, want an error without the URL", raw, err)
		}
	}
}
