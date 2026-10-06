package api_test

// Admin audit trail (B-269 Slice 0b): the 12 adversarial tests from
// B-269_SLICE0B_PART_A_INVESTIGATION.md §8, at the B-232/B-233 standard
// (real Postgres, one test per layer).
//
// Every test runs in its OWN throwaway database (newThrowawayDB +
// applyMigrations, bootstrap_test.go), so no audit row is ever written to,
// or deleted from, the shared dev database: the trail is append-only and
// fixtures are no exception.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eami/api/internal/api"
	"github.com/eami/api/internal/auth"
	"github.com/eami/api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminAuditEnv struct {
	pool    *pgxpool.Pool
	q       *store.Queries
	authSvc *auth.Service
	url     string
	conn    bootstrapPgConn
	dbName  string
}

func newAdminAuditEnv(t *testing.T) *adminAuditEnv {
	t.Helper()
	c := bootstrapTestPgConn(t)
	dbName := newThrowawayDB(t, c)
	applyMigrations(t, c, dbName)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, c.dbURL(dbName))
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping: could not reach throwaway database: %v", err)
	}
	// A scratch table standing in for Slice 1's real change (a preset row).
	if _, err := pool.Exec(ctx, `CREATE TABLE audit_probe (id UUID PRIMARY KEY, org_id UUID NOT NULL)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	q := store.New(pool)
	authSvc, err := auth.NewService("", time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	ts := httptest.NewServer(api.NewServer(q, authSvc, nil, nil).Handler())
	t.Cleanup(ts.Close)
	return &adminAuditEnv{pool: pool, q: q, authSvc: authSvc, url: ts.URL, conn: c, dbName: dbName}
}

func (e *adminAuditEnv) seedOrg(t *testing.T, label string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	name := label + "-" + id.String()[:8]
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO orgs (id, name, slug) VALUES ($1, $2, $2)`, id, name); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return id
}

func (e *adminAuditEnv) seedUser(t *testing.T, orgID uuid.UUID, role string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO users (id, org_id, email, role) VALUES ($1, $2, $3, $4)`,
		id, orgID, id.String()[:8]+"@audit.test", role); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func (e *adminAuditEnv) token(t *testing.T, userID, orgID uuid.UUID, role string) string {
	t.Helper()
	tok, _, err := e.authSvc.IssueAccessToken(userID, orgID, "x@audit.test", role)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return tok
}

func (e *adminAuditEnv) get(t *testing.T, path, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.url+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// appendEvent runs a probe change plus one audit event through RunAudited,
// the path Slice 1's handlers will use.
func (e *adminAuditEnv) appendEvent(t *testing.T, orgID, actor uuid.UUID, action string, target uuid.UUID) store.AdminAuditEventRow {
	t.Helper()
	row, err := e.q.RunAudited(context.Background(), orgID, func(tx pgx.Tx) (store.AdminAuditEvent, error) {
		if _, err := tx.Exec(context.Background(), `INSERT INTO audit_probe (id, org_id) VALUES ($1, $2)`, uuid.New(), orgID); err != nil {
			return store.AdminAuditEvent{}, err
		}
		return store.AdminAuditEvent{
			OrgID: orgID, Actor: store.AdminAuditActor{Type: store.AdminAuditActorUser, UserID: actor, Role: "admin"},
			Action: action, TargetID: target, Source: store.AdminAuditSourceAPI,
		}, nil
	})
	if err != nil {
		t.Fatalf("RunAudited: %v", err)
	}
	return row
}

func (e *adminAuditEnv) verify(t *testing.T, orgID uuid.UUID) store.AdminAuditVerifyResult {
	t.Helper()
	res, err := e.q.VerifyAdminAuditChain(context.Background(), orgID)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return res
}

// superExec runs SQL with the append-only triggers disabled: what a database
// administrator (or today's superuser app role, B-299) can do.
func (e *adminAuditEnv) superExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `ALTER TABLE admin_audit_events DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disable triggers: %v", err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("tamper %q: %v", sql, err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE admin_audit_events ENABLE TRIGGER USER`); err != nil {
		t.Fatalf("enable triggers: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tamper: %v", err)
	}
}

type adminAuditList struct {
	Data []struct {
		ID       string `json:"id"`
		Seq      int64  `json:"seq"`
		TargetID string `json:"target_id"`
	} `json:"data"`
	Meta struct {
		Total int64 `json:"total"`
	} `json:"meta"`
}

// 1. List isolation (HTTP layer).
func TestAdminAudit_ListIsolation(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA, orgB := e.seedOrg(t, "a"), e.seedOrg(t, "b")
	userA, userB := e.seedUser(t, orgA, "admin"), e.seedUser(t, orgB, "admin")
	targetA := uuid.New()
	e.appendEvent(t, orgA, userA, store.AdminAuditPresetCreated, targetA)
	e.appendEvent(t, orgA, userA, store.AdminAuditPresetPublished, targetA)
	e.appendEvent(t, orgB, userB, store.AdminAuditPresetCreated, uuid.New())
	tokB := e.token(t, userB, orgB, "admin")

	code, body := e.get(t, "/v1/audit/admin-events", tokB)
	if code != 200 {
		t.Fatalf("list as B: %d %s", code, body)
	}
	var l adminAuditList
	_ = json.Unmarshal(body, &l)
	if l.Meta.Total != 1 || len(l.Data) != 1 || l.Data[0].TargetID == targetA.String() {
		t.Fatalf("B saw A's events or wrong count: %s", body)
	}

	_, foreign := e.get(t, "/v1/audit/admin-events?target_id="+targetA.String(), tokB)
	_, missing := e.get(t, "/v1/audit/admin-events?target_id="+uuid.New().String(), tokB)
	if !bytes.Equal(foreign, missing) {
		t.Fatalf("A's target is distinguishable from a nonexistent one:\nforeign=%s\nmissing=%s", foreign, missing)
	}
	_, byActor := e.get(t, "/v1/audit/admin-events?actor_user_id="+userA.String(), tokB)
	if !bytes.Equal(byActor, missing) {
		t.Fatalf("A's actor is distinguishable from a nonexistent one: %s", byActor)
	}

	// A's own admin sees both of A's events, with the actor's email joined.
	_, bodyA := e.get(t, "/v1/audit/admin-events?order=asc", e.token(t, userA, orgA, "admin"))
	var la adminAuditList
	_ = json.Unmarshal(bodyA, &la)
	if la.Meta.Total != 2 || la.Data[0].Seq != 1 || la.Data[1].Seq != 2 {
		t.Fatalf("A's own list wrong: %s", bodyA)
	}
	if !strings.Contains(string(bodyA), "@audit.test") {
		t.Fatalf("actor email not joined: %s", bodyA)
	}
}

// 2. A supplied org_id never selects another org; any unknown parameter fails
// closed with a fixed message.
func TestAdminAudit_OrgIDParamRejected(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA, orgB := e.seedOrg(t, "a"), e.seedOrg(t, "b")
	userA, userB := e.seedUser(t, orgA, "admin"), e.seedUser(t, orgB, "admin")
	e.appendEvent(t, orgA, userA, store.AdminAuditPresetCreated, uuid.New())
	tokB := e.token(t, userB, orgB, "admin")

	for _, path := range []string{
		"/v1/audit/admin-events?org_id=" + orgA.String(),
		"/v1/audit/admin-events/verify?org_id=" + orgA.String(),
	} {
		code, body := e.get(t, path, tokB)
		if code != 400 || strings.Contains(string(body), orgA.String()) {
			t.Fatalf("%s: want 400 without echo, got %d %s", path, code, body)
		}
	}
	// Bad filter values: 400, fixed message, value never reflected.
	for _, bad := range []string{"action=preset.nuked", "target_type=org", "target_id=zzz", "actor_user_id=zzz",
		"from=yesterday", "sort=hash", "order=sideways"} {
		code, body := e.get(t, "/v1/audit/admin-events?"+bad, tokB)
		val := bad[strings.Index(bad, "=")+1:]
		if code != 400 || strings.Contains(string(body), val) {
			t.Fatalf("%s: want 400 without echo, got %d %s", bad, code, body)
		}
	}
}

// 3. Verify isolation: tampering in one org never affects the other's result.
func TestAdminAudit_VerifyIsolation(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA, orgB := e.seedOrg(t, "a"), e.seedOrg(t, "b")
	userA, userB := e.seedUser(t, orgA, "admin"), e.seedUser(t, orgB, "admin")
	for i := 0; i < 3; i++ {
		e.appendEvent(t, orgA, userA, store.AdminAuditPresetDraftUpdated, uuid.New())
		e.appendEvent(t, orgB, userB, store.AdminAuditPresetDraftUpdated, uuid.New())
	}
	e.superExec(t, `UPDATE admin_audit_events SET target_id = $1 WHERE org_id = $2 AND seq = 2`, uuid.New().String(), orgA)
	if r := e.verify(t, orgA); r.Valid {
		t.Fatal("A's tampered chain verified")
	}
	if r := e.verify(t, orgB); !r.Valid || r.Checked != 3 {
		t.Fatalf("B affected by A's tamper: %+v", r)
	}
	e.superExec(t, `UPDATE admin_audit_events SET actor_role = 'viewer' WHERE org_id = $1 AND seq = 1`, orgB)
	if r := e.verify(t, orgB); r.Valid {
		t.Fatal("B's tampered chain verified")
	}

	// The HTTP route verifies the caller's own org only.
	orgC := e.seedOrg(t, "c")
	userC := e.seedUser(t, orgC, "admin")
	code, body := e.get(t, "/v1/audit/admin-events/verify", e.token(t, userC, orgC, "admin"))
	if code != 200 || !strings.Contains(string(body), `"valid":true`) || !strings.Contains(string(body), `"checked":0`) ||
		!strings.Contains(string(body), "database administrator") {
		t.Fatalf("C's verify: %d %s", code, body)
	}
	code, body = e.get(t, "/v1/audit/admin-events/verify", e.token(t, userA, orgA, "admin"))
	if code != 200 || !strings.Contains(string(body), `"valid":false`) || !strings.Contains(string(body), `"first_bad_seq":2`) {
		t.Fatalf("A's verify over HTTP: %d %s", code, body)
	}
}

// 4. Splice: org A's whole chain moved to empty org C, row for row with ids,
// seq and hashes unchanged (only org_id rewritten), does not verify for C.
// Per-org genesis and org_id inside the hash are what catch it. A's now-empty
// chain verifies as "no events": deleting everything is a stated limit.
func TestAdminAudit_SpliceAcrossOrgs(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA, orgC := e.seedOrg(t, "a"), e.seedOrg(t, "c")
	userA := e.seedUser(t, orgA, "admin")
	for i := 0; i < 3; i++ {
		e.appendEvent(t, orgA, userA, store.AdminAuditPresetCreated, uuid.New())
	}
	e.superExec(t, `UPDATE admin_audit_events SET org_id = $2 WHERE org_id = $1`, orgA, orgC)
	r := e.verify(t, orgC)
	if r.Valid || r.FirstBadSeq == nil || *r.FirstBadSeq != 1 {
		t.Fatalf("spliced chain verified for C: %+v", r)
	}
	if r := e.verify(t, orgA); !r.Valid || r.Checked != 0 {
		t.Fatalf("LIMIT: an emptied chain should read as no events: %+v", r)
	}
}

// 5. Concurrency: parallel appends in two orgs give two valid, gap-free,
// unlinked chains.
func TestAdminAudit_ConcurrentAppends(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA, orgB := e.seedOrg(t, "a"), e.seedOrg(t, "b")
	userA, userB := e.seedUser(t, orgA, "admin"), e.seedUser(t, orgB, "admin")
	const n = 25
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		for _, p := range [][2]uuid.UUID{{orgA, userA}, {orgB, userB}} {
			wg.Add(1)
			go func(org, user uuid.UUID) {
				defer wg.Done()
				_, err := e.q.RunAudited(context.Background(), org, func(tx pgx.Tx) (store.AdminAuditEvent, error) {
					return store.AdminAuditEvent{OrgID: org,
						Actor:  store.AdminAuditActor{Type: store.AdminAuditActorUser, UserID: user, Role: "admin"},
						Action: store.AdminAuditPresetDraftUpdated, TargetID: uuid.New(), Source: store.AdminAuditSourceAPI}, nil
				})
				errs <- err
			}(p[0], p[1])
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent append failed: %v", err)
		}
	}
	for _, org := range []uuid.UUID{orgA, orgB} {
		r := e.verify(t, org)
		if !r.Valid || r.Checked != n || r.HeadSeq != n {
			t.Fatalf("org %s chain after concurrency: %+v", org, r)
		}
	}
	var linked int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM admin_audit_events a JOIN admin_audit_events b
		  ON a.prev_hash = b.hash AND a.org_id <> b.org_id`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 0 {
		t.Fatalf("%d rows link across orgs", linked)
	}
}

// 6. Lock backstop: an append in a REPEATABLE READ transaction (whose head
// read would be stale) is refused before any write, and a writer that skips
// the lock and reuses a seq hits UNIQUE (org_id, seq). Neither forks the chain.
func TestAdminAudit_StaleWriterFailsClosed(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA := e.seedOrg(t, "a")
	userA := e.seedUser(t, orgA, "admin")
	e.appendEvent(t, orgA, userA, store.AdminAuditPresetCreated, uuid.New())
	ctx := context.Background()

	for _, iso := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		stale, err := e.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: iso})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.AppendAdminAuditEvent(ctx, stale, store.AdminAuditEvent{OrgID: orgA,
			Actor:  store.AdminAuditActor{Type: store.AdminAuditActorUser, UserID: userA, Role: "admin"},
			Action: store.AdminAuditPresetArchived, TargetID: uuid.New(), Source: store.AdminAuditSourceAPI})
		_ = stale.Rollback(ctx)
		if !errors.Is(err, store.ErrAdminAuditInvalid) {
			t.Fatalf("%s writer: want ErrAdminAuditInvalid, got %v", iso, err)
		}
	}

	// A writer that skips the lock and reuses an existing (org, seq) is
	// refused by the constraint.
	var pgErr *pgconn.PgError
	_, err := e.pool.Exec(ctx, `INSERT INTO admin_audit_events
		(org_id, seq, occurred_at, actor_type, actor_role, action, target_type, target_id, summary, source, prev_hash, hash)
		VALUES ($1, 1, now(), 'system', 'system', 'preset.created', 'preset', $2, '{}', 'system', $3, $3)`,
		orgA, uuid.New().String(), strings.Repeat("a", 64))
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate seq insert: want 23505, got %v", err)
	}
	if r := e.verify(t, orgA); !r.Valid || r.Checked != 1 {
		t.Fatalf("chain after refused writers: %+v", r)
	}
}

// 6b. The org is pinned by the caller (security review M3): an event naming
// any other org than the one RunAudited was given is refused, and the change
// rolls back with it.
func TestAdminAudit_RunAuditedPinsOrg(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA, orgB := e.seedOrg(t, "a"), e.seedOrg(t, "b")
	userA := e.seedUser(t, orgA, "admin")
	ctx := context.Background()
	for label, pin := range map[string]uuid.UUID{"other org": orgA, "no org": uuid.Nil} {
		_, err := e.q.RunAudited(ctx, pin, func(tx pgx.Tx) (store.AdminAuditEvent, error) {
			if _, err := tx.Exec(ctx, `INSERT INTO audit_probe (id, org_id) VALUES ($1, $2)`, uuid.New(), orgB); err != nil {
				return store.AdminAuditEvent{}, err
			}
			return store.AdminAuditEvent{OrgID: orgB, Actor: store.AdminAuditActor{Type: store.AdminAuditActorUser, UserID: userA, Role: "admin"},
				Action: store.AdminAuditPresetCreated, TargetID: uuid.New(), Source: store.AdminAuditSourceAPI}, nil
		})
		if !errors.Is(err, store.ErrAdminAuditInvalid) {
			t.Fatalf("%s: want ErrAdminAuditInvalid, got %v", label, err)
		}
	}
	var probes, events int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_probe`).Scan(&probes)
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events`).Scan(&events)
	if probes != 0 || events != 0 {
		t.Fatalf("a refused org mismatch left rows: probes=%d events=%d", probes, events)
	}
}

// 6c. Query discipline (code review M1, L2) and the verify rate limit
// (security review M2).
func TestAdminAudit_QueryDisciplineAndVerifyLimit(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA := e.seedOrg(t, "a")
	tok := e.token(t, e.seedUser(t, orgA, "admin"), orgA, "admin")
	for _, q := range []string{
		"action=preset.created&action=preset.nuked", "order=asc&order=sideways",
		"page=abc", "page=0", "per_page=-1", "per_page=101", "per_page=abc",
	} {
		code, body := e.get(t, "/v1/audit/admin-events?"+q, tok)
		if code != 400 || strings.Contains(string(body), "nuked") || strings.Contains(string(body), "abc") {
			t.Errorf("%s: want 400 without echo, got %d %s", q, code, body)
		}
	}
	if code, _ := e.get(t, "/v1/audit/admin-events?page=2&per_page=100", tok); code != 200 {
		t.Errorf("valid paging refused: %d", code)
	}
	limited := false
	for i := 0; i < 8; i++ {
		code, _ := e.get(t, "/v1/audit/admin-events/verify", tok)
		if code == 429 {
			limited = true
			break
		}
		if code != 200 {
			t.Fatalf("verify call %d: %d", i, code)
		}
	}
	if !limited {
		t.Error("verify was never rate limited")
	}
	// Another org's limit is separate.
	orgB := e.seedOrg(t, "b")
	if code, _ := e.get(t, "/v1/audit/admin-events/verify", e.token(t, e.seedUser(t, orgB, "admin"), orgB, "admin")); code != 200 {
		t.Errorf("org B limited by org A's calls: %d", code)
	}
}

// 6d. A scanner name since retired from the closed list can still be
// recorded as removed (code review L4), but never as added.
func TestAdminAudit_RetiredEnumRemoval(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA := e.seedOrg(t, "a")
	userA := e.seedUser(t, orgA, "admin")
	_, err := e.q.RunAudited(context.Background(), orgA, func(tx pgx.Tx) (store.AdminAuditEvent, error) {
		return store.AdminAuditEvent{OrgID: orgA, Actor: store.AdminAuditActor{Type: store.AdminAuditActorUser, UserID: userA, Role: "admin"},
			Action: store.AdminAuditPresetDraftUpdated, TargetID: uuid.New(), Source: store.AdminAuditSourceAPI,
			Summary: store.AdminAuditSummary{Changes: []store.AdminAuditChange{
				store.AdminAuditEnumChange("scanners", []string{"models", "retired_scanner"}, []string{"models"})}}}, nil
	})
	if err != nil {
		t.Fatalf("removing a retired scanner name: %v", err)
	}
}

// 7. Rollback and fail closed (B0b-4): a change that fails leaves no event; an
// audit write that fails leaves no change; a stuck chain lock turns into a
// fast, stable error, never a hang.
func TestAdminAudit_RollbackAndFailClosed(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA := e.seedOrg(t, "a")
	userA := e.seedUser(t, orgA, "admin")
	e.appendEvent(t, orgA, userA, store.AdminAuditPresetCreated, uuid.New())
	ctx := context.Background()
	count := func(table string) int {
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE org_id = $1`, orgA).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	probes, events := count("audit_probe"), count("admin_audit_events")

	// (a) The change fails: no event.
	changeErr := errors.New("change failed")
	_, err := e.q.RunAudited(ctx, orgA, func(tx pgx.Tx) (store.AdminAuditEvent, error) {
		_, _ = tx.Exec(ctx, `INSERT INTO audit_probe (id, org_id) VALUES ($1, $2)`, uuid.New(), orgA)
		return store.AdminAuditEvent{}, changeErr
	})
	if !errors.Is(err, changeErr) || count("audit_probe") != probes || count("admin_audit_events") != events {
		t.Fatalf("(a) failed change left rows behind: %v", err)
	}

	// (b) The audit write fails because another session holds the org's
	// chain lock: the change is rolled back and the error arrives within the
	// lock timeout.
	holder, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('admin_audit:' || $1::text, 0))`, orgA); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = e.q.RunAudited(ctx, orgA, func(tx pgx.Tx) (store.AdminAuditEvent, error) {
		if _, err := tx.Exec(ctx, `INSERT INTO audit_probe (id, org_id) VALUES ($1, $2)`, uuid.New(), orgA); err != nil {
			return store.AdminAuditEvent{}, err
		}
		return store.AdminAuditEvent{OrgID: orgA,
			Actor:  store.AdminAuditActor{Type: store.AdminAuditActorUser, UserID: userA, Role: "admin"},
			Action: store.AdminAuditPresetPublished, TargetID: uuid.New(), Source: store.AdminAuditSourceAPI}, nil
	})
	elapsed := time.Since(start)
	_ = holder.Rollback(ctx)
	if !errors.Is(err, store.ErrAdminAuditWrite) || !strings.Contains(err.Error(), "55P03") {
		t.Fatalf("(b) want ErrAdminAuditWrite from the lock timeout (55P03), got %v", err)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("(b) audit failure took %s: a hang, not a bounded failure", elapsed)
	}
	if count("audit_probe") != probes || count("admin_audit_events") != events {
		t.Fatal("(b) the change was committed although its audit event failed")
	}

	// The HTTP mapping is a fixed code and message; the raw error stays local.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/presets", nil)
	if !api.WriteAdminAuditFailureForTest(rec, req, err) {
		t.Fatal("audit error not recognised")
	}
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"code":"audit_write_failed"`) ||
		strings.Contains(rec.Body.String(), "lock") || strings.Contains(rec.Body.String(), "SQLSTATE") {
		t.Fatalf("(b) HTTP mapping: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	if api.WriteAdminAuditFailureForTest(rec, req, changeErr) {
		t.Fatal("a non-audit error was mapped as an audit failure")
	}

	// (c) Append succeeds, then the transaction rolls back: no event.
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendAdminAuditEvent(ctx, tx, store.AdminAuditEvent{OrgID: orgA,
		Actor:  store.AdminAuditActor{Type: store.AdminAuditActorSystem},
		Action: store.AdminAuditEnrollmentKeyExchanged, TargetID: uuid.New(), Source: store.AdminAuditSourceSystem}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if count("admin_audit_events") != events {
		t.Fatal("(c) rolled-back append left an event")
	}
	if r := e.verify(t, orgA); !r.Valid || r.HeadSeq != int64(events) {
		t.Fatalf("chain after rollbacks: %+v", r)
	}
}

// 8. Store layer: list, count and verify scope to the given org on their own,
// independently of the HTTP layer.
func TestAdminAudit_StoreScopedByOrg(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA, orgB := e.seedOrg(t, "a"), e.seedOrg(t, "b")
	userA := e.seedUser(t, orgA, "admin")
	targetA := uuid.New()
	e.appendEvent(t, orgA, userA, store.AdminAuditPresetCreated, targetA)
	ctx := context.Background()
	tid := targetA.String()
	p := store.ListAdminAuditEventsParams{OrgID: orgB, TargetID: &tid, Limit: 100}
	rows, err := e.q.ListAdminAuditEvents(ctx, p)
	if err != nil || len(rows) != 0 {
		t.Fatalf("B listed A's target: %v %d", err, len(rows))
	}
	p.TargetID = nil
	if n, err := e.q.CountAdminAuditEvents(ctx, p); err != nil || n != 0 {
		t.Fatalf("B counted A's events: %v %d", err, n)
	}
	if r := e.verify(t, orgB); r.Checked != 0 {
		t.Fatalf("B's verify read A's rows: %+v", r)
	}
	p.OrgID = orgA
	if n, _ := e.q.CountAdminAuditEvents(ctx, p); n != 1 {
		t.Fatalf("A's own count = %d", n)
	}
}

// 9. Roles: org admins only (B0b-2).
func TestAdminAudit_RoleGate(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA := e.seedOrg(t, "a")
	for _, path := range []string{"/v1/audit/admin-events", "/v1/audit/admin-events/verify"} {
		if code, _ := e.get(t, path, ""); code != 401 {
			t.Fatalf("%s unauthenticated: %d", path, code)
		}
		for _, role := range []string{"operator", "viewer", "approver", "platform_admin"} {
			if code, body := e.get(t, path, e.token(t, uuid.New(), orgA, role)); code != 403 {
				t.Fatalf("%s as %s: %d %s", path, role, code, body)
			}
		}
		if code, body := e.get(t, path, e.token(t, e.seedUser(t, orgA, "admin"), orgA, "admin")); code != 200 {
			t.Fatalf("%s as admin: %d %s", path, code, body)
		}
	}
}

// 10. No values in a row: sentinel secrets fed through every constructor never
// reach any column or the API; hand-built values are refused; a bad request
// ID is dropped.
func TestAdminAudit_NoValuesInRows(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA := e.seedOrg(t, "a")
	userA := e.seedUser(t, orgA, "admin")
	ctx := context.Background()
	const key = "eami_e_SENTINELKEYVALUE0123456789"
	const path = `C:\Users\sentinel-alice\models`
	const name = "Sentinel Preset Name"

	presetSummary := store.AdminAuditSummary{
		Changes: []store.AdminAuditChange{
			store.AdminAuditFieldChanged("name"),
			store.AdminAuditFieldChanged("description"),
			store.AdminAuditListChange("model_scan_paths", []string{"/opt/a"}, []string{"/opt/a", path}),
			store.AdminAuditEnumChange("scanners", []string{"models", "gpu"}, []string{"models", "ai_apps"}),
			store.AdminAuditValueChange("scan_interval_seconds", 300, 600),
		},
		Codes: []string{store.CodePathProfileParent},
		Refs:  &store.AdminAuditRefs{PresetVersion: ptrInt32(3), FromVersion: ptrInt32(2)},
	}
	keySummary := store.AdminAuditSummary{Changes: []store.AdminAuditChange{
		store.AdminAuditValueChange("expires_at", nil, key), // even a misused field only stores a hash
		store.AdminAuditValueChange("max_uses", nil, 10),
	}}
	for _, ev := range []struct {
		action  string
		summary store.AdminAuditSummary
	}{
		{store.AdminAuditPresetCreated, presetSummary}, {store.AdminAuditPresetDraftUpdated, presetSummary},
		{store.AdminAuditPresetPublished, presetSummary}, {store.AdminAuditPresetReverted, presetSummary},
		{store.AdminAuditEnrollmentKeyCreated, keySummary},
	} {
		_, err := e.q.RunAudited(ctx, orgA, func(tx pgx.Tx) (store.AdminAuditEvent, error) {
			return store.AdminAuditEvent{OrgID: orgA, Actor: api.AdminAuditActorForTest(userA, orgA, "admin@audit.test", "admin"),
				Action: ev.action, TargetID: uuid.New(), Summary: ev.summary, Source: store.AdminAuditSourceAPI,
				RequestID: "host/abc-000001"}, nil
		})
		if err != nil {
			t.Fatalf("%s: %v", ev.action, err)
		}
	}

	var dump string
	if err := e.pool.QueryRow(ctx, `SELECT string_agg(e::text, E'\n') FROM admin_audit_events e`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	_, body := e.get(t, "/v1/audit/admin-events?per_page=100", e.token(t, userA, orgA, "admin"))
	for _, s := range []string{key, "SENTINELKEY", "sentinel-alice", path, name, "Sentinel", "sentinel description", "admin@audit.test"} {
		if strings.Contains(dump, s) {
			t.Errorf("row text contains %q", s)
		}
	}
	for _, s := range []string{key, "sentinel-alice", name, "sentinel description"} {
		if strings.Contains(string(body), s) {
			t.Errorf("API output contains %q", s)
		}
	}
	var sums string
	if err := e.pool.QueryRow(ctx, `SELECT string_agg(summary || coalesce(request_id, ''), E'
') FROM admin_audit_events`).Scan(&sums); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sums, path) {
		t.Errorf("summary contains the raw path")
	}
	if !strings.Contains(sums, "host/abc-000001") || !strings.Contains(sums, `"enum_added":["ai_apps"]`) ||
		!strings.Contains(sums, `"removed":1`) || !strings.Contains(sums, "path_profile_parent") ||
		!strings.Contains(sums, `"preset_version":3`) {
		t.Errorf("summary lost its useful parts: %s", sums)
	}

	// Hand-built events carrying values are refused before any database work.
	base := store.AdminAuditEvent{OrgID: orgA, Actor: store.AdminAuditActor{Type: store.AdminAuditActorUser, UserID: userA, Role: "admin"},
		Action: store.AdminAuditPresetCreated, TargetID: uuid.New(), Source: store.AdminAuditSourceAPI}
	bad := map[string]store.AdminAuditSummary{
		"raw value":          {Changes: []store.AdminAuditChange{{Field: "name", New: name}}},
		"raw item":           {Changes: []store.AdminAuditChange{{Field: "model_scan_paths", ItemsNew: []string{path}}}},
		"unknown field":      {Changes: []store.AdminAuditChange{{Field: "enrollment_key", New: "sha256:0123456789abcdef"}}},
		"enum outside":       {Changes: []store.AdminAuditChange{{Field: "scanners", EnumAdded: []string{key}}}},
		"unknown code":       {Codes: []string{"secret_value"}},
		"duplicate field":    {Changes: []store.AdminAuditChange{store.AdminAuditValueChange("max_report_size_bytes", nil, 1), store.AdminAuditValueChange("max_report_size_bytes", nil, 2)}},
		"hash on free text":  {Changes: []store.AdminAuditChange{store.AdminAuditValueChange("name", nil, name)}},
		"enum added unknown": {Changes: []store.AdminAuditChange{store.AdminAuditEnumChange("scanners", nil, []string{"retired_scanner"})}},
	}
	for label, s := range bad {
		ev := base
		ev.Summary = s
		_, err := e.q.RunAudited(ctx, orgA, func(tx pgx.Tx) (store.AdminAuditEvent, error) { return ev, nil })
		if !errors.Is(err, store.ErrAdminAuditInvalid) {
			t.Errorf("%s: want ErrAdminAuditInvalid, got %v", label, err)
		}
	}
	for label, ev := range map[string]store.AdminAuditEvent{
		"unregistered action": {OrgID: orgA, Actor: base.Actor, Action: "user.deleted", TargetID: uuid.New(), Source: "api"},
		"field on fieldless":  {OrgID: orgA, Actor: base.Actor, Action: store.AdminAuditPresetArchived, TargetID: uuid.New(), Source: "api", Summary: store.AdminAuditSummary{Changes: []store.AdminAuditChange{store.AdminAuditValueChange("name", nil, "x")}}},
		"system with user":    {OrgID: orgA, Actor: store.AdminAuditActor{Type: "system", UserID: userA}, Action: base.Action, TargetID: uuid.New(), Source: "system"},
		"unknown role":        {OrgID: orgA, Actor: store.AdminAuditActor{Type: "user", UserID: userA, Role: "root"}, Action: base.Action, TargetID: uuid.New(), Source: "api"},
	} {
		ev := ev
		_, err := e.q.RunAudited(ctx, orgA, func(tx pgx.Tx) (store.AdminAuditEvent, error) { return ev, nil })
		if !errors.Is(err, store.ErrAdminAuditInvalid) {
			t.Errorf("%s: want ErrAdminAuditInvalid, got %v", label, err)
		}
	}

	// Request IDs: a client can set X-Request-Id, so anything off-format is
	// dropped (stored NULL), never stored or echoed (B0b-5).
	for _, rid := range []string{"bad id\nwith newline", "<script>", strings.Repeat("x", 65), "a;DROP"} {
		ev := base
		ev.RequestID = rid
		row, err := e.q.RunAudited(ctx, orgA, func(tx pgx.Tx) (store.AdminAuditEvent, error) { return ev, nil })
		if err != nil || row.RequestID != nil {
			t.Errorf("request id %q: err=%v kept=%v", rid, err, row.RequestID)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if api.AdminAuditRequestIDForTest(req) != "" {
		t.Error("request ID without chi's middleware should be empty")
	}
	if !e.verify(t, orgA).Valid {
		t.Error("chain invalid after the value tests")
	}
}

func ptrInt32(v int32) *int32 { return &v }

// 11. Append-only: UPDATE, DELETE and TRUNCATE are refused, and the down
// migration refuses while any row exists (B0b-6). Then down/up on an empty
// table works.
func TestAdminAudit_AppendOnlyAndDownMigration(t *testing.T) {
	e := newAdminAuditEnv(t)
	orgA := e.seedOrg(t, "a")
	userA := e.seedUser(t, orgA, "admin")
	ctx := context.Background()

	down := readMigration(t, "000029_admin_audit_events.down.sql")
	up := readMigration(t, "000029_admin_audit_events.up.sql")
	// Empty table: down then up again works.
	if err := e.execSimple(t, down); err != nil {
		t.Fatalf("down on an empty trail: %v", err)
	}
	if err := e.execSimple(t, up); err != nil {
		t.Fatalf("up again: %v", err)
	}

	e.appendEvent(t, orgA, userA, store.AdminAuditPresetCreated, uuid.New())
	for _, sql := range []string{
		`UPDATE admin_audit_events SET actor_role = 'viewer'`,
		`DELETE FROM admin_audit_events`,
		`TRUNCATE admin_audit_events`,
	} {
		_, err := e.pool.Exec(ctx, sql)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: want 42501 (append-only), got %v", sql, err)
		}
	}
	_, err := e.pool.Exec(ctx, `DELETE FROM orgs WHERE id = $1`, orgA)
	var fkErr *pgconn.PgError
	if !errors.As(err, &fkErr) || fkErr.Code != "23503" || fkErr.ConstraintName != "admin_audit_events_org_id_fkey" {
		t.Errorf("deleting an org with audit events: want 23503 on admin_audit_events_org_id_fkey, got %v", err)
	}
	if err := e.execSimple(t, down); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("down with rows: want refusal, got %v", err)
	}
	if r := e.verify(t, orgA); !r.Valid || r.Checked != 1 {
		t.Fatalf("trail damaged: %+v", r)
	}
}

// execSimple runs a multi-statement migration file over the simple protocol,
// as applyMigrations does.
func (e *adminAuditEnv) execSimple(t *testing.T, sql string) error {
	t.Helper()
	cfg, err := pgx.ParseConfig(e.conn.dbURL(e.dbName))
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	c, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	_, err = c.Exec(context.Background(), sql)
	return err
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "migrations-v2", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 12. Tamper matrix (B0b-7): what the chain detects, and the limits asserted
// as tests, not just prose.
func TestAdminAudit_TamperMatrix(t *testing.T) {
	seed := func(t *testing.T) (*adminAuditEnv, uuid.UUID) {
		e := newAdminAuditEnv(t)
		org := e.seedOrg(t, "a")
		user := e.seedUser(t, org, "admin")
		for i := 0; i < 5; i++ {
			e.appendEvent(t, org, user, store.AdminAuditPresetDraftUpdated, uuid.New())
		}
		if r := e.verify(t, org); !r.Valid || r.Checked != 5 {
			t.Fatalf("baseline: %+v", r)
		}
		return e, org
	}
	detected := func(t *testing.T, e *adminAuditEnv, org uuid.UUID, wantSeq int64, wantReason string) {
		t.Helper()
		r := e.verify(t, org)
		if r.Valid || r.FirstBadSeq == nil || *r.FirstBadSeq != wantSeq || r.Reason != wantReason {
			t.Fatalf("want detected at seq %d (%s), got %+v", wantSeq, wantReason, r)
		}
	}

	t.Run("edit any column is detected", func(t *testing.T) {
		e, org := seed(t)
		e.superExec(t, `UPDATE admin_audit_events SET summary = '{"v":1,"codes":["path_root"]}' WHERE org_id = $1 AND seq = 3`, org)
		detected(t, e, org, 3, store.AdminAuditBadHash)
	})
	t.Run("edit the timestamp is detected", func(t *testing.T) {
		e, org := seed(t)
		e.superExec(t, `UPDATE admin_audit_events SET occurred_at = occurred_at - interval '1 day' WHERE org_id = $1 AND seq = 4`, org)
		detected(t, e, org, 4, store.AdminAuditBadHash)
	})
	t.Run("interior deletion is detected", func(t *testing.T) {
		e, org := seed(t)
		e.superExec(t, `DELETE FROM admin_audit_events WHERE org_id = $1 AND seq = 2`, org)
		detected(t, e, org, 3, store.AdminAuditBadSeq)
	})
	t.Run("deletion plus renumbering is detected", func(t *testing.T) {
		e, org := seed(t)
		e.superExec(t, `DELETE FROM admin_audit_events WHERE org_id = $1 AND seq = 2`, org)
		e.superExec(t, `UPDATE admin_audit_events SET seq = seq + 1000 WHERE org_id = $1 AND seq > 2`, org)
		e.superExec(t, `UPDATE admin_audit_events SET seq = seq - 1001 WHERE org_id = $1 AND seq > 1000`, org)
		detected(t, e, org, 2, store.AdminAuditBadPrevHash)
	})
	t.Run("reordering is detected", func(t *testing.T) {
		e, org := seed(t)
		e.superExec(t, `UPDATE admin_audit_events SET seq = 1002 WHERE org_id = $1 AND seq = 2`, org)
		e.superExec(t, `UPDATE admin_audit_events SET seq = 2 WHERE org_id = $1 AND seq = 3`, org)
		e.superExec(t, `UPDATE admin_audit_events SET seq = 3 WHERE org_id = $1 AND seq = 1002`, org)
		detected(t, e, org, 2, store.AdminAuditBadPrevHash)
	})
	t.Run("forged replacement row is detected", func(t *testing.T) {
		e, org := seed(t)
		// Replace seq 3 with a forged row that links correctly to seq 2 and
		// carries a correctly computed hash: seq 4 no longer links to it.
		var prev string
		if err := e.pool.QueryRow(context.Background(), `SELECT hash FROM admin_audit_events WHERE org_id=$1 AND seq=2`, org).Scan(&prev); err != nil {
			t.Fatal(err)
		}
		e.superExec(t, `DELETE FROM admin_audit_events WHERE org_id = $1 AND seq = 3`, org)
		forged := forgeRow(org, 3, prev, store.AdminAuditPresetArchived)
		e.superExec(t, insertRowSQL, forged.args()...)
		detected(t, e, org, 4, store.AdminAuditBadPrevHash)
	})

	// LIMITS, asserted: these are NOT detected (B0b-7).
	t.Run("LIMIT: removing the newest rows is not detected", func(t *testing.T) {
		e, org := seed(t)
		e.superExec(t, `DELETE FROM admin_audit_events WHERE org_id = $1 AND seq >= 4`, org)
		r := e.verify(t, org)
		if !r.Valid || r.Checked != 3 {
			t.Fatalf("tail truncation unexpectedly detected (update the guarantee wording): %+v", r)
		}
	})
	t.Run("LIMIT: a database administrator who recomputes the chain is not detected", func(t *testing.T) {
		e, org := seed(t)
		// Rewrite history: change seq 2's action, then recompute every hash
		// from the genesis with the documented algorithm, reimplemented here
		// independently of the store package.
		e.superExec(t, `UPDATE admin_audit_events SET action = 'preset.archived' WHERE org_id = $1 AND seq = 2`, org)
		rows, err := e.pool.Query(context.Background(), `
			SELECT id, seq, occurred_at, actor_type, coalesce(actor_user_id::text, ''), actor_role, action,
			       target_type, target_id, summary, source, coalesce(request_id, '')
			FROM admin_audit_events WHERE org_id = $1 ORDER BY seq`, org)
		if err != nil {
			t.Fatal(err)
		}
		type rec struct {
			id                                                            uuid.UUID
			seq                                                           int64
			at                                                            time.Time
			actorType, actorUser, role, action, tType, tID, sum, src, rid string
		}
		var recs []rec
		for rows.Next() {
			var r rec
			if err := rows.Scan(&r.id, &r.seq, &r.at, &r.actorType, &r.actorUser, &r.role, &r.action, &r.tType, &r.tID, &r.sum, &r.src, &r.rid); err != nil {
				t.Fatal(err)
			}
			recs = append(recs, r)
		}
		rows.Close()
		g := sha256.Sum256([]byte("eami-admin-audit-genesis-v1" + org.String()))
		prev := hex.EncodeToString(g[:])
		for _, r := range recs {
			h := independentRowHash(prev, r.id.String(), org.String(), strconv.FormatInt(r.seq, 10),
				r.at.UTC().Format("2006-01-02T15:04:05.000000Z"), r.actorType, r.actorUser, r.role, r.action,
				r.tType, r.tID, r.sum, r.src, r.rid)
			e.superExec(t, `UPDATE admin_audit_events SET prev_hash = $1, hash = $2 WHERE org_id = $3 AND seq = $4`, prev, h, org, r.seq)
			prev = h
		}
		r := e.verify(t, org)
		if !r.Valid || r.Checked != 5 {
			t.Fatalf("a full recompute was detected (update the guarantee wording): %+v", r)
		}
	})
}

// independentRowHash is the documented algorithm: SHA-256 over
// "eami-admin-audit-v1", prev_hash and every column, each prefixed with its
// 4-byte big-endian length.
func independentRowHash(parts ...string) string {
	all := append([]string{"eami-admin-audit-v1"}, parts...)
	h := sha256.New()
	for _, p := range all {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

const insertRowSQL = `INSERT INTO admin_audit_events
	(id, org_id, seq, occurred_at, actor_type, actor_user_id, actor_role, action, target_type, target_id,
	 summary, source, request_id, prev_hash, hash)
	VALUES ($1,$2,$3,$4,'system',NULL,'system',$5,'preset',$6,'{"v":1}','system',NULL,$7,$8)`

type forgedRow struct {
	id     uuid.UUID
	org    uuid.UUID
	seq    int64
	at     time.Time
	action string
	target string
	prev   string
	hash   string
}

func forgeRow(org uuid.UUID, seq int64, prev, action string) forgedRow {
	r := forgedRow{id: uuid.New(), org: org, seq: seq, at: time.Now().UTC().Truncate(time.Microsecond),
		action: action, target: uuid.New().String(), prev: prev}
	r.hash = independentRowHash(prev, r.id.String(), org.String(), strconv.FormatInt(seq, 10),
		r.at.Format("2006-01-02T15:04:05.000000Z"), "system", "", "system", action, "preset", r.target,
		`{"v":1}`, "system", "")
	return r
}

func (r forgedRow) args() []any {
	return []any{r.id, r.org, r.seq, r.at, r.action, r.target, r.prev, r.hash}
}
