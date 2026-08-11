package approval

import (
	"context"
	"testing"
	"time"
)

// TestEscapeSlackText proves the security-review-caught fix: fields that
// can originate from an untrusted/compromised caller (agent name, tool,
// action, session ID) must have Slack's mrkdwn special characters escaped
// before being interpolated into a notification a human approver reads,
// so an attacker can't inject a fake <url|label> link or misleading
// formatting into the approval decision UI.
func TestEscapeSlackText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"<https://evil.example.com|Click here>", "&lt;https://evil.example.com|Click here&gt;"},
		{"a & b", "a &amp; b"},
		{"<a&b>", "&lt;a&amp;b&gt;"}, // & must be escaped first, not double-escaped via &lt;/&gt;
		{"", ""},
	}
	for _, c := range cases {
		if got := escapeSlackText(c.in); got != c.want {
			t.Errorf("escapeSlackText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHandleNotification_PanicResolvingOneApproval_DoesNotCrash proves
// acceptance criterion #2 for the approval router: a panic processing one
// notification doesn't propagate out of handleNotification. resolve()'s own
// type assertion (v.(*pendingEntry)) panics if something other than a
// *pendingEntry is ever stored under an approval ID — genuinely reachable if
// a future bug stores the wrong type — so storing a bogus value there is a
// real panic path, not a synthetic one.
func TestHandleNotification_PanicResolvingOneApproval_DoesNotCrash(t *testing.T) {
	r := New(nil, nil, 5*time.Second, "", "", nil, nil)
	r.pending.Store("approval-1", "not a *pendingEntry")

	payload := `{"approval_id":"approval-1"}`

	done := make(chan struct{})
	go func() {
		r.handleNotification(context.Background(), payload)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleNotification did not return — panic escaped")
	}
}

// TestHandleNotification_NextNotification_StillProcessesNormally proves the
// loop's next iteration still runs normally after a prior notification
// panicked — the router isn't left in a broken state.
func TestHandleNotification_NextNotification_StillProcessesNormally(t *testing.T) {
	r := New(nil, nil, 5*time.Second, "", "", nil, nil)
	r.pending.Store("approval-1", "not a *pendingEntry")

	// First notification panics inside resolve(), recovered by handleNotification.
	r.handleNotification(context.Background(), `{"approval_id":"approval-1"}`)

	// Second notification references an approval ID with no pending waiter —
	// resolve()'s normal not-found path (no panic risk), proving the router
	// is still functioning normally for subsequent events.
	done := make(chan struct{})
	go func() {
		r.handleNotification(context.Background(), `{"approval_id":"approval-2-not-pending"}`)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second handleNotification call did not return normally")
	}
}

// TestHandleNotification_MalformedPayload_StillHandledCleanly is a
// regression guard: confirms the pre-existing malformed-payload/missing-id
// handling (not a panic path) is unchanged by the recovery wrapper.
func TestHandleNotification_MalformedPayload_StillHandledCleanly(t *testing.T) {
	r := New(nil, nil, 5*time.Second, "", "", nil, nil)

	done := make(chan struct{})
	go func() {
		r.handleNotification(context.Background(), `not valid json`)
		r.handleNotification(context.Background(), `{}`) // missing approval_id
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleNotification did not return for malformed/empty payloads")
	}
}
