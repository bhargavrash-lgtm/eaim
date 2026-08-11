// dial_test.go -- eami-gateway/internal/aiprovider
//
// Proves the duplicated safeDialContext (see dial.go's doc comment on why
// it's duplicated rather than imported) behaves identically to
// toolrouter's original: real production wiring (NewClaudeAdapter, the
// real guarded dialer -- not unrestrictedDialer) refuses a private/
// link-local target. Defense-in-depth for a hardcoded-hostname adapter
// (see dial.go's own doc comment on why this matters less here than for
// toolrouter's admin-supplied base_url case, but is kept anyway).
package aiprovider

import (
	"context"
	"testing"
)

func TestSafeDialContext_BlocksPrivateAndLinkLocalTargets(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1:1/",     // loopback
		"http://169.254.169.254/", // cloud metadata / link-local
	} {
		t.Run(target, func(t *testing.T) {
			a := NewClaudeAdapter() // the real, guarded dialer -- not unrestrictedDialer
			claudeMessagesURLOverride = target
			defer func() { claudeMessagesURLOverride = "" }()

			_, err := a.Dispatch(context.Background(), Credentials{APIKey: "sk-ant-x"}, Request{Action: "messages", Params: map[string]any{}})
			if err == nil {
				t.Fatal("expected Dispatch to reject a private/link-local target, got nil error")
			}
		})
	}
}
