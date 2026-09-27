package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// B-242 (option 2): users.email is globally UNIQUE, so inviting an email that
// already has an account used to 500 with the raw unique-violation text. Now
// an email registered in this org, in another org, or on a soft-deleted
// account all get one byte-identical 409 with no DB text and nothing
// written. This does not close the existence oracle: "exists somewhere" is
// still signalled (and GET /v1/users lets an admin tell their own org's
// emails apart). The full fix is out-of-band invite delivery, deferred until
// email sending exists.
func TestInviteUser_ExistingEmailUniform409_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "b242-a")
	orgB := seedTestOrg(t, ctx, env.pool, "b242-b")
	adminA := env.token(t, seedTestUser(t, ctx, env.pool, orgA), orgA, "admin@b242-a.test", "admin")

	suffix := uuid.NewString()[:8]
	sameOrg := "b242-same-" + suffix + "@example.test"
	otherOrg := "b242-other-" + suffix + "@example.test"
	deleted := "b242-deleted-" + suffix + "@example.test"
	for _, u := range []struct {
		org     uuid.UUID
		email   string
		deleted bool
	}{{orgA, sameOrg, false}, {orgB, otherOrg, false}, {orgB, deleted, true}} {
		if _, err := env.pool.Exec(ctx, `INSERT INTO users (org_id, email, role, deleted_at) VALUES ($1, $2, 'viewer', CASE WHEN $3 THEN now() END)`, u.org, u.email, u.deleted); err != nil {
			t.Fatalf("seed %s: %v", u.email, err)
		}
	}
	countUsers := func() (n int) {
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE org_id IN ($1, $2)`, orgA, orgB).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	countTokens := func() (n int) {
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM invite_tokens t JOIN users u ON u.id = t.user_id WHERE u.org_id IN ($1, $2)`, orgA, orgB).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	users0, tokens0 := countUsers(), countTokens()

	const want = "{\"code\":\"conflict\",\"message\":\"this email cannot be invited\"}\n"
	for _, tc := range []struct{ name, email string }{
		{"email already in this org", sameOrg},
		{"email registered in another org", otherOrg},
		{"email of a soft-deleted account in another org", deleted},
	} {
		body := expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/users/invite", adminA, map[string]any{"email": tc.email, "role": "viewer"}), http.StatusConflict, tc.name)
		if body != want {
			t.Fatalf("%s: body %q, want the one fixed %q (identical for every case, no DB text)", tc.name, body, want)
		}
		for _, leak := range []string{"users_email_key", "duplicate", "SQLSTATE", "23505", tc.email} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s: response leaks %q: %s", tc.name, leak, body)
			}
		}
	}
	if u, tk := countUsers(), countTokens(); u != users0 || tk != tokens0 {
		t.Fatalf("rejected invites wrote rows: users %d→%d, invite_tokens %d→%d", users0, u, tokens0, tk)
	}

	// A genuinely new email still invites normally.
	fresh := "b242-new-" + suffix + "@example.test"
	body := expectCMDBStatus(t, env.do(t, http.MethodPost, "/v1/users/invite", adminA, map[string]any{"email": fresh, "role": "viewer"}), http.StatusCreated, "new email invite")
	if !strings.Contains(body, "/accept-invite?token=") || !strings.Contains(body, fresh) {
		t.Fatalf("new email invite: %s, want the created user and an invite link", body)
	}
}
