package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// B-226/B-227: GET /v1/endpoints search was documented but never read, so
// every query returned the whole inventory. These tests pin that search
// filters, that meta.total counts exactly what the list pages through, and
// that server pagination past the first page is complete.

type endpointListBody struct {
	Data []struct {
		ID       string `json:"id"`
		Hostname string `json:"hostname"`
	} `json:"data"`
	Meta struct {
		Total   int64 `json:"total"`
		Page    int   `json:"page"`
		PerPage int   `json:"per_page"`
	} `json:"meta"`
}

func getEndpoints(t *testing.T, env *workspaceTestEnv, token string, query url.Values) endpointListBody {
	t.Helper()
	resp := env.do(t, http.MethodGet, "/v1/endpoints?"+query.Encode(), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/endpoints?%s = %d: %s", query.Encode(), resp.StatusCode, readWSBody(resp))
	}
	defer resp.Body.Close()
	var body endpointListBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func (b endpointListBody) hostnames() []string {
	out := make([]string, 0, len(b.Data))
	for _, d := range b.Data {
		out = append(out, d.Hostname)
	}
	sort.Strings(out)
	return out
}

func seedEndpointHosts(t *testing.T, env *workspaceTestEnv, ctx context.Context, orgID uuid.UUID, hosts ...string) {
	t.Helper()
	for _, h := range hosts {
		if _, err := env.pool.Exec(ctx, `INSERT INTO endpoints(org_id,agent_id,hostname,agent_version) VALUES($1,$2,$3,'')`, orgID, "b226-"+uuid.NewString(), h); err != nil {
			t.Fatalf("seed endpoint %s: %v", h, err)
		}
	}
}

func TestListEndpoints_SearchFiltersAndCountMatches_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgA := seedTestOrg(t, ctx, env.pool, "b226-search-a")
	orgB := seedTestOrg(t, ctx, env.pool, "b226-search-b")
	seedDiscoveryLicense(t, env, ctx, orgA)
	seedDiscoveryLicense(t, env, ctx, orgB)
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgA), orgA, "viewer@b226.test", "viewer")

	seedEndpointHosts(t, env, ctx, orgA, "alpha-web-01", "ALPHA-db-02", "beta_host", "betaXhost", "gamma%node", `back\slash`)
	seedEndpointHosts(t, env, ctx, orgB, "alpha-foreign")

	cases := []struct {
		search string
		want   []string
	}{
		{"", []string{"ALPHA-db-02", "alpha-web-01", `back\slash`, "betaXhost", "beta_host", "gamma%node"}},
		{"alpha", []string{"ALPHA-db-02", "alpha-web-01"}},
		{"  AlPhA  ", []string{"ALPHA-db-02", "alpha-web-01"}},
		{"zz-no-such-host-zz", nil},
		{"beta_host", []string{"beta_host"}},
		{"_", []string{"beta_host"}},
		{"%", []string{"gamma%node"}},
		{`\`, []string{`back\slash`}},
		{"foreign", nil},
	}
	for _, c := range cases {
		got := getEndpoints(t, env, viewer, url.Values{"search": {c.search}})
		want := append([]string{}, c.want...)
		sort.Strings(want)
		if strings.Join(got.hostnames(), "|") != strings.Join(want, "|") {
			t.Fatalf("search=%q returned %v, want %v", c.search, got.hostnames(), want)
		}
		if got.Meta.Total != int64(len(want)) {
			t.Fatalf("search=%q meta.total=%d but list has %d matching rows (count and list must share one filter)", c.search, got.Meta.Total, len(want))
		}
	}

	// Limits are in characters, not bytes: 200 three-byte characters (600
	// bytes) are accepted, 201 are not. NUL and invalid UTF-8 are client
	// errors, not a Postgres 500.
	for _, c := range []struct {
		name, rawQuery string
		want           int
	}{
		{"201 ascii chars", "search=" + strings.Repeat("a", 201), http.StatusBadRequest},
		{"200 CJK chars", url.Values{"search": {strings.Repeat("端", 200)}}.Encode(), http.StatusOK},
		{"201 CJK chars", url.Values{"search": {strings.Repeat("端", 201)}}.Encode(), http.StatusBadRequest},
		{"NUL byte", "search=alpha%00", http.StatusBadRequest},
		{"invalid UTF-8", "search=%FF%FE", http.StatusBadRequest},
	} {
		resp := env.do(t, http.MethodGet, "/v1/endpoints?"+c.rawQuery, viewer, nil)
		body := readWSBody(resp)
		if resp.StatusCode != c.want {
			t.Fatalf("%s: status %d want %d: %s", c.name, resp.StatusCode, c.want, body)
		}
	}
}

// B-227: paging past the first 25 must return every matching endpoint exactly
// once. The fixtures share one last_seen (a single INSERT ... generate_series),
// so without the id tie-breaker in ORDER BY rows could repeat or be skipped
// across pages. Postgres happened to order the ties stably without it in every
// mutation run, so this test does not reliably catch that tie-breaker's
// removal; the tie-breaker itself is review-verified (B-226_B-227_VERIFICATION.md).
func TestListEndpoints_PaginationPast25WithSearchIsCompleteAndStable_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b227-paging")
	seedDiscoveryLicense(t, env, ctx, orgID)
	viewer := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "viewer@b227.test", "viewer")

	if _, err := env.pool.Exec(ctx, `INSERT INTO endpoints(org_id,agent_id,hostname,agent_version) SELECT $1,'b227-'||g,'page-host-'||lpad(g::text,2,'0'),'' FROM generate_series(1,30) g`, orgID); err != nil {
		t.Fatal(err)
	}
	seedEndpointHosts(t, env, ctx, orgID, "other-1", "other-2", "other-3")

	for _, tc := range []struct {
		search    string
		wantTotal int64
	}{{"page-host", 30}, {"", 33}} {
		seen := map[string]bool{}
		for page, wantRows := range map[int]int{1: 25, 2: int(tc.wantTotal) - 25} {
			got := getEndpoints(t, env, viewer, url.Values{"search": {tc.search}, "per_page": {"25"}, "page": {strconv.Itoa(page)}})
			if got.Meta.Total != tc.wantTotal || got.Meta.Page != page || len(got.Data) != wantRows {
				t.Fatalf("search=%q page=%d: rows=%d total=%d page=%d, want rows=%d total=%d", tc.search, page, len(got.Data), got.Meta.Total, got.Meta.Page, wantRows, tc.wantTotal)
			}
			for _, d := range got.Data {
				if seen[d.ID] {
					t.Fatalf("search=%q: endpoint %s (%s) returned on more than one page", tc.search, d.ID, d.Hostname)
				}
				seen[d.ID] = true
				if tc.search != "" && !strings.Contains(d.Hostname, tc.search) {
					t.Fatalf("search=%q returned non-matching host %s", tc.search, d.Hostname)
				}
			}
		}
		if int64(len(seen)) != tc.wantTotal {
			t.Fatalf("search=%q: pages covered %d distinct endpoints, want %d", tc.search, len(seen), tc.wantTotal)
		}
		beyond := getEndpoints(t, env, viewer, url.Values{"search": {tc.search}, "per_page": {"25"}, "page": {"3"}})
		if len(beyond.Data) != 0 || beyond.Meta.Total != tc.wantTotal {
			t.Fatalf("search=%q page 3: rows=%d total=%d, want 0 rows and total %d", tc.search, len(beyond.Data), beyond.Meta.Total, tc.wantTotal)
		}
	}
}
