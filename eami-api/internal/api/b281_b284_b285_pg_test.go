package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// B-284: "latest report" is chosen by the server's receive time, never the
// agent-reported collected_at. A report with a far-FUTURE collected_at that
// arrived first must not stay "latest" once a later report arrives -- on the
// list and the detail, for scanner_status, gpu_count and latest_report alike.
func TestEndpoints_LatestReportIsByServerReceiveTime_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b284-skew")
	seedDiscoveryLicense(t, env, ctx, orgID)
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@b284.test", "viewer")

	var id uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname) VALUES ($1, 'b284-skew', 'b284-skew') RETURNING id`, orgID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	insert := func(collected, received time.Time, report string) {
		t.Helper()
		if _, err := env.pool.Exec(ctx,
			`INSERT INTO endpoint_reports (endpoint_id, org_id, collected_at, received_at, report) VALUES ($1, $2, $3, $4, $5::jsonb)`,
			id, orgID, collected, received, report); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	// Received first, but the agent's clock claimed a year in the future.
	insert(now.Add(365*24*time.Hour), now.Add(-10*time.Minute),
		`{"agent_id":"b284-skew","gpus":[{"name":"stale"},{"name":"stale2"}],"scanner_status":{"gpu":"ok","models":"disabled"}}`)
	// Received later, with an honest clock: this is the current state.
	insert(now.Add(-time.Minute), now.Add(-time.Minute),
		`{"agent_id":"b284-skew","gpus":null,"scanner_status":{"gpu":"disabled","models":"ok"}}`)

	check := func(where string, it scannerStatusItem) {
		t.Helper()
		if it.ScannerStatus["gpu"] != "disabled" || it.ScannerStatus["models"] != "ok" || it.GPUCount != 0 {
			t.Fatalf("%s: scanner_status=%v gpu_count=%d -- the future-dated report won (want the later-received one)", where, it.ScannerStatus, it.GPUCount)
		}
	}
	resp := env.do(t, http.MethodGet, "/v1/endpoints?per_page=100", viewer, nil)
	var list struct {
		Data []scannerStatusItem `json:"data"`
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	found := false
	for _, it := range list.Data {
		if it.ID == id.String() {
			check("list", it)
			found = true
		}
	}
	if !found {
		t.Fatal("endpoint missing from list")
	}

	resp = env.do(t, http.MethodGet, "/v1/endpoints/"+id.String(), viewer, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail = %d", resp.StatusCode)
	}
	var it scannerStatusItem
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	check("detail", it)
	var rep struct {
		ScannerStatus map[string]string `json:"scanner_status"`
	}
	_ = json.Unmarshal(it.LatestReport, &rep)
	if rep.ScannerStatus["gpu"] != "disabled" {
		t.Fatalf("detail latest_report is the future-dated report: %s", it.LatestReport)
	}
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// B-285: a report's scanner_errors produce one server-side log line per failed
// scanner (known scanner + known reason only); unrecognised entries are
// counted, never echoed.
func TestIngest_ScannerFailuresAreLoggedServerSide_RealDB(t *testing.T) {
	env := newIngestRelayEnv(t)
	agentID := "b285-log-" + uuid.NewString()[:8]
	env.cleanupAgent(t, agentID)

	var sink syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&sink, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	now := time.Now().UTC().Format(time.RFC3339)
	item := map[string]interface{}{
		"id": uuid.NewString(), "agent_id": agentID, "hostname": agentID, "received_at": now,
		"report": map[string]interface{}{
			"agent_id": agentID, "hostname": agentID, "collected_at": now,
			"scanner_status": map[string]string{"models": "error", "gpu": "error", "ai_apps": "ok"},
			"scanner_errors": map[string]string{
				"models":        "timeout",
				"gpu":           "panic",
				"evil\nscanner": "timeout",           // unknown scanner name
				"ai_processes":  "rm -rf / injected", // unknown reason
			},
		},
	}
	resp := env.postBatch(t, []map[string]interface{}{item})
	if accepted := decodeIngestAccepted(t, resp); accepted != 1 {
		t.Fatalf("accepted = %d", accepted)
	}

	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(sink.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil {
			lines = append(lines, m)
		}
	}
	var endpointID string
	if err := env.pool.QueryRow(context.Background(), `SELECT id::text FROM endpoints WHERE agent_id = $1`, agentID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	unrecognised := -1.0
	for _, m := range lines {
		switch m["msg"] {
		case "endpoint scanner failed":
			if m["agent_id"] == agentID {
				got[m["scanner"].(string)] = m["reason"].(string)
			}
		case "endpoint report has unrecognised scanner_errors entries":
			if m["endpoint_id"] == endpointID {
				unrecognised, _ = m["count"].(float64)
			}
		}
	}
	if len(got) != 2 || got["models"] != "timeout" || got["gpu"] != "panic" {
		t.Fatalf("logged failures = %v, want models=timeout gpu=panic only", got)
	}
	if unrecognised != 2 {
		t.Fatalf("unrecognised count = %v, want 2", unrecognised)
	}
	if out := sink.String(); strings.Contains(out, "rm -rf") || strings.Contains(out, "evil") {
		t.Fatal("agent-supplied scanner_errors text was echoed into the server log")
	}

	// Also queryable in the stored report.
	var stored string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT r.report->'scanner_errors'->>'models' FROM endpoint_reports r JOIN endpoints e ON e.id = r.endpoint_id WHERE e.agent_id = $1`,
		agentID).Scan(&stored); err != nil || stored != "timeout" {
		t.Fatalf("stored scanner_errors.models = %q (%v), want timeout", stored, err)
	}
}
