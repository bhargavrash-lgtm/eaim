package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/eami/api/internal/store"
)

// Master-sequence item 4: GET /v1/endpoints and /v1/endpoints/{id} must let
// the UI tell "never reported", "disabled", "scan failed", "found nothing"
// and "not known" apart, from real stored data only:
//   - has_report is false only when the endpoint has no scan report at all
//     (a paste event can create one -- the real path is used below);
//   - scanner_status is the LATEST report's per-scanner object, passed
//     through; null when that report predates the field (an older agent).

type scannerStatusItem struct {
	ID            string            `json:"id"`
	Hostname      string            `json:"hostname"`
	GPUCount      int64             `json:"gpu_count"`
	HasReport     bool              `json:"has_report"`
	ScannerStatus map[string]string `json:"scanner_status"`
	LatestReport  json.RawMessage   `json:"latest_report"`
}

func seedEndpointReport(t *testing.T, env *workspaceTestEnv, ctx context.Context, endpointID, orgID uuid.UUID, at time.Time, report string) {
	t.Helper()
	if _, err := env.pool.Exec(ctx,
		`INSERT INTO endpoint_reports (endpoint_id, org_id, collected_at, report) VALUES ($1, $2, $3, $4::jsonb)`,
		endpointID, orgID, at, report); err != nil {
		t.Fatalf("seed report: %v", err)
	}
}

func TestEndpoints_ScannerStatusAndHasReport_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "item4-scanner-status")
	seedDiscoveryLicense(t, env, ctx, orgID)
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@item4.test", "viewer")
	q := store.New(env.pool)

	// 1. Never reported: created by the real paste-event path, no scan report.
	neverID, err := q.ResolvePasteSourceEndpoint(ctx, store.ResolvePasteSourceEndpointParams{OrgID: orgID, AgentID: "item4-paste-only", Hostname: "item4-never"})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Not known: its only report comes from an agent older than scanner_status.
	var legacyID uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname) VALUES ($1, 'item4-legacy', 'item4-legacy') RETURNING id`, orgID).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	seedEndpointReport(t, env, ctx, legacyID, orgID, time.Now().Add(-time.Hour), `{"agent_id":"item4-legacy","gpus":null,"python_envs":null}`)

	// 3. Current agent: an OLDER report without the field, then the latest
	// one with it -- the latest report's status must win. gpu ran and found
	// nothing (null list + "ok"), models disabled, python_envs failed.
	var currentID uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname) VALUES ($1, 'item4-current', 'item4-current') RETURNING id`, orgID).Scan(&currentID); err != nil {
		t.Fatal(err)
	}
	seedEndpointReport(t, env, ctx, currentID, orgID, time.Now().Add(-2*time.Hour), `{"agent_id":"item4-current","gpus":[{"name":"old"}]}`)
	seedEndpointReport(t, env, ctx, currentID, orgID, time.Now().Add(-time.Minute),
		`{"agent_id":"item4-current","gpus":null,"local_models":null,"python_envs":null,"scanner_status":{"gpu":"ok","models":"disabled","python_envs":"error"}}`)

	// 4. Downgraded: an OLDER report with scanner_status, then the latest one
	// from an agent without it. The stale older status must not be shown:
	// only the latest report counts, so this is "not known".
	var downgradedID uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname) VALUES ($1, 'item4-downgraded', 'item4-downgraded') RETURNING id`, orgID).Scan(&downgradedID); err != nil {
		t.Fatal(err)
	}
	seedEndpointReport(t, env, ctx, downgradedID, orgID, time.Now().Add(-2*time.Hour), `{"agent_id":"item4-downgraded","scanner_status":{"gpu":"disabled"}}`)
	seedEndpointReport(t, env, ctx, downgradedID, orgID, time.Now().Add(-time.Minute), `{"agent_id":"item4-downgraded","gpus":null}`)

	// List.
	resp := env.do(t, http.MethodGet, "/v1/endpoints?per_page=100", viewer, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/endpoints = %d: %s", resp.StatusCode, readWSBody(resp))
	}
	var list struct {
		Data []scannerStatusItem `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	byID := map[string]scannerStatusItem{}
	for _, d := range list.Data {
		byID[d.ID] = d
	}
	checkItem := func(where string, it scannerStatusItem, id uuid.UUID) {
		t.Helper()
		switch id {
		case neverID:
			if it.HasReport || it.ScannerStatus != nil {
				t.Errorf("%s never-reported: has_report=%v scanner_status=%v, want false/null", where, it.HasReport, it.ScannerStatus)
			}
		case legacyID, downgradedID:
			if !it.HasReport || it.ScannerStatus != nil {
				t.Errorf("%s legacy: has_report=%v scanner_status=%v, want true/null (not known)", where, it.HasReport, it.ScannerStatus)
			}
		case currentID:
			want := map[string]string{"gpu": "ok", "models": "disabled", "python_envs": "error"}
			if !it.HasReport || len(it.ScannerStatus) != len(want) {
				t.Fatalf("%s current: has_report=%v scanner_status=%v, want true/%v", where, it.HasReport, it.ScannerStatus, want)
			}
			for k, v := range want {
				if it.ScannerStatus[k] != v {
					t.Errorf("%s current: scanner_status[%s]=%q, want %q (latest report must win)", where, k, it.ScannerStatus[k], v)
				}
			}
			if it.GPUCount != 0 {
				t.Errorf("%s current: gpu_count=%d, want 0 (ran, found nothing)", where, it.GPUCount)
			}
		}
	}
	for _, id := range []uuid.UUID{neverID, legacyID, currentID, downgradedID} {
		it, ok := byID[id.String()]
		if !ok {
			t.Fatalf("endpoint %s missing from the list", id)
		}
		checkItem("list", it, id)
	}

	// Detail: same fields; a never-reported endpoint has a null latest_report.
	for _, id := range []uuid.UUID{neverID, legacyID, currentID, downgradedID} {
		resp := env.do(t, http.MethodGet, "/v1/endpoints/"+id.String(), viewer, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/endpoints/%s = %d: %s", id, resp.StatusCode, readWSBody(resp))
		}
		var it scannerStatusItem
		if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		checkItem("detail", it, id)
		if id == neverID && string(it.LatestReport) != "null" {
			t.Errorf("never-reported detail latest_report = %s, want null", it.LatestReport)
		}
	}
}

// Two reports with the SAME collected_at: whichever one is "latest", its
// scanner_status, gpu_count and latest_report must all come from that one
// row -- never one report's status paired with another report's items.
func TestEndpoints_ScannerStatusConsistentOnCollectedAtTie_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "item4-tie")
	seedDiscoveryLicense(t, env, ctx, orgID)
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@item4tie.test", "viewer")
	var id uuid.UUID
	if err := env.pool.QueryRow(ctx, `INSERT INTO endpoints (org_id, agent_id, hostname) VALUES ($1, 'item4-tie', 'item4-tie') RETURNING id`, orgID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute)
	seedEndpointReport(t, env, ctx, id, orgID, at, `{"agent_id":"item4-tie","gpus":[{"name":"a"}],"scanner_status":{"gpu":"ok"}}`)
	seedEndpointReport(t, env, ctx, id, orgID, at, `{"agent_id":"item4-tie","gpus":null,"scanner_status":{"gpu":"disabled"}}`)

	resp := env.do(t, http.MethodGet, "/v1/endpoints/"+id.String(), viewer, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d: %s", resp.StatusCode, readWSBody(resp))
	}
	defer resp.Body.Close()
	var it scannerStatusItem
	if err := json.NewDecoder(resp.Body).Decode(&it); err != nil {
		t.Fatal(err)
	}
	var rep struct {
		ScannerStatus map[string]string `json:"scanner_status"`
	}
	if err := json.Unmarshal(it.LatestReport, &rep); err != nil {
		t.Fatal(err)
	}
	switch it.ScannerStatus["gpu"] {
	case "ok":
		if it.GPUCount != 1 || rep.ScannerStatus["gpu"] != "ok" {
			t.Fatalf("status ok but gpu_count=%d, latest_report status=%v (mixed rows)", it.GPUCount, rep.ScannerStatus)
		}
	case "disabled":
		if it.GPUCount != 0 || rep.ScannerStatus["gpu"] != "disabled" {
			t.Fatalf("status disabled but gpu_count=%d, latest_report status=%v (mixed rows)", it.GPUCount, rep.ScannerStatus)
		}
	default:
		t.Fatalf("unexpected scanner_status %v", it.ScannerStatus)
	}
}
