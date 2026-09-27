package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func unrestricted(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

// A listener on loopback must never receive a connection through
// DialContext: the guard refuses before dialing.
func TestDialContext_BlocksLoopbackBeforeConnecting(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()
	for _, addr := range []string{ln.Addr().String(), "localhost:" + portOf(t, ln), "169.254.169.254:80", "10.1.2.3:443", "[::1]:80", "0.0.0.0:80"} {
		if _, err := DialContext(context.Background(), "tcp", addr); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("DialContext(%s) err=%v, want ErrBlockedAddress", addr, err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := accepted.Load(); n != 0 {
		t.Fatalf("loopback listener accepted %d connections through the guard", n)
	}
}

func portOf(t *testing.T, ln net.Listener) string {
	_, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The client never follows a redirect: a public URL answering 302 to an
// internal address must not become a second request.
func TestNewHTTPClient_DoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { targetHits.Add(1) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	resp, err := NewHTTPClient(unrestricted, 5*time.Second).Post(redirector.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || targetHits.Load() != 0 {
		t.Fatalf("status=%d targetHits=%d, want the 302 returned and the redirect target never contacted", resp.StatusCode, targetHits.Load())
	}
}

// An environment proxy would make the dialer see only the proxy address,
// so the guarded client must never use one.
func TestNewHTTPClient_NoProxyAndTimeout(t *testing.T) {
	c := NewHTTPClient(DialContext, 3*time.Second)
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil || tr.DialContext == nil {
		t.Fatalf("transport=%#v, want *http.Transport with nil Proxy and the given dialer", c.Transport)
	}
	if c.Timeout != 3*time.Second {
		t.Fatalf("timeout=%v, want 3s", c.Timeout)
	}
}

// A hung target is cut off by the timeout rather than holding the caller.
func TestNewHTTPClient_TimesOut(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	start := time.Now()
	_, err := NewHTTPClient(unrestricted, 300*time.Millisecond).Post(slow.URL, "application/json", nil)
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("err=%v after %v, want a timeout error well under 3s", err, time.Since(start))
	}
}

// B-238 security review: special-purpose ranges and IPv6 forms that embed
// an IPv4 address are refused; public addresses (including a NAT64 or 6to4
// route to a public IPv4) still pass.
func TestIsBlocked_SpecialRangesAndEmbeddedIPv4(t *testing.T) {
	for _, tt := range []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true}, {"::1", true}, {"169.254.169.254", true}, {"fd00:ec2::254", true},
		{"10.0.0.5", true}, {"172.16.0.1", true}, {"192.168.1.1", true}, {"0.0.0.0", true}, {"::", true},
		{"::ffff:127.0.0.1", true}, {"::ffff:169.254.169.254", true}, {"::ffff:10.0.0.1", true},
		{"100.64.0.1", true}, {"100.100.100.200", true}, {"0.1.2.3", true}, {"192.0.0.192", true},
		{"192.0.2.1", true}, {"198.18.0.1", true}, {"198.51.100.1", true}, {"203.0.113.1", true},
		{"240.0.0.1", true}, {"255.255.255.255", true}, {"224.0.0.1", true}, {"239.1.1.1", true},
		{"ff02::1", true}, {"ff0e::1", true}, {"fec0::1", true}, {"2001:db8::1", true}, {"2001:0:4136:e378::1", true},
		{"64:ff9b::a9fe:a9fe", true}, {"64:ff9b::a00:5", true}, {"64:ff9b::7f00:1", true}, {"64:ff9b:1::a00:5", true},
		{"2002:a9fe:a9fe::1", true}, {"2002:7f00:1::1", true}, {"::127.0.0.1", true}, {"::a00:5", true},
		{"::ffff:0:7f00:1", true}, {"::ffff:0:a9fe:a9fe", true}, {"::ffff:0:808:808", false},
		// public: must still pass
		{"8.8.8.8", false}, {"93.184.216.34", false}, {"2001:4860:4860::8888", false},
		{"64:ff9b::808:808", false}, {"2002:808:808::1", false}, {"100.63.255.255", false}, {"100.128.0.1", false},
	} {
		if got := IsBlocked(net.ParseIP(tt.ip)); got != tt.want {
			t.Errorf("IsBlocked(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
	if !IsBlocked(nil) {
		t.Error("IsBlocked(nil) = false, want true")
	}
}
