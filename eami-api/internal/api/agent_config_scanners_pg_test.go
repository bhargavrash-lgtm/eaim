package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/eami/api/internal/store"
)

// B-271: every governed agent's config must enable all 10 scanners by
// default, and the API must refuse scanner names the agent doesn't know
// (the agent matches names exactly, so an unknown or differently-cased name
// silently disabled that scanner). Needs migration 000025 applied.

func TestAgentConfig_B271_DefaultEnablesAllScanners_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b271-defaults")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@b271.test", "admin")
	all := strings.Join(store.AllScanners, "|")

	// The trigger-created row (what a linked endpoint receives).
	withRow := seedConfigAgent(t, env, ctx, orgID, "b271-trigger-row")
	if r, ok := readAgentConfigRow(t, env, ctx, withRow); !ok || r.scanners != all {
		t.Fatalf("trigger-created row scanners = %q (exists=%v), want all ten %q", r.scanners, ok, all)
	}

	// No row: the API's fallback defaults must match.
	noRow := seedConfigAgent(t, env, ctx, orgID, "b271-no-row")
	if _, err := env.pool.Exec(ctx, `DELETE FROM agent_configs WHERE agent_id=$1`, noRow); err != nil {
		t.Fatal(err)
	}
	body := expectCMDBStatus(t, env.do(t, http.MethodGet, fmt.Sprintf("/v1/gateway/agents/%s/config", noRow), admin, nil), http.StatusOK, "GET config, no row")
	var got struct {
		EnabledScanners []string `json:"enabled_scanners"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || strings.Join(got.EnabledScanners, "|") != all {
		t.Fatalf("no-row GET enabled_scanners = %v (%v), want all ten", got.EnabledScanners, err)
	}

	// A no-row PUT that doesn't send enabled_scanners creates the row with all ten.
	expectCMDBStatus(t, env.do(t, http.MethodPut, fmt.Sprintf("/v1/gateway/agents/%s/config", noRow), admin, map[string]any{"scan_interval_seconds": 120}), http.StatusOK, "PUT config, no row")
	if r, ok := readAgentConfigRow(t, env, ctx, noRow); !ok || r.scanners != all {
		t.Fatalf("no-row PUT created scanners = %q (exists=%v), want all ten", r.scanners, ok)
	}

	// AgentConfigDefaults.EnabledScanners must be its own copy, not a view of
	// AllScanners (the allow-list the 400 validation reads). Writing through
	// a struct copy of the defaults does change the package global, so the
	// original value is restored in t.Cleanup even if the check fails.
	orig := store.AgentConfigDefaults.EnabledScanners[0]
	t.Cleanup(func() { store.AgentConfigDefaults.EnabledScanners[0] = orig })
	store.AgentConfigDefaults.EnabledScanners[0] = "mutated"
	if store.AllScanners[0] == "mutated" {
		t.Fatal("AgentConfigDefaults.EnabledScanners aliases AllScanners")
	}
}

func TestAgentConfig_B271_RejectsUnknownScannerNames_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "b271-validation")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@b271v.test", "admin")
	agent := seedConfigAgent(t, env, ctx, orgID, "b271-validation-agent")
	path := fmt.Sprintf("/v1/gateway/agents/%s/config", agent)
	before, _ := readAgentConfigRow(t, env, ctx, agent)

	for _, bad := range [][]string{
		{"GPU"},                      // wrong case: the agent matches exactly
		{"gpu "},                     // trailing space
		{"scheduled_tasks"},          // a package that exists but isn't wired into the report
		{"browser", "not_a_scanner"}, // one bad name among good ones
		{""},
	} {
		expectCMDBStatus(t, env.do(t, http.MethodPut, path, admin, map[string]any{"enabled_scanners": bad}), http.StatusBadRequest, fmt.Sprintf("PUT enabled_scanners=%q", bad))
	}
	if after, _ := readAgentConfigRow(t, env, ctx, agent); after != before {
		t.Fatalf("a rejected PUT changed the row: before=%+v after=%+v", before, after)
	}

	// Every real name is accepted, alone and all together.
	for _, name := range store.AllScanners {
		expectCMDBStatus(t, env.do(t, http.MethodPut, path, admin, map[string]any{"enabled_scanners": []string{name}}), http.StatusOK, "PUT "+name)
	}
	expectCMDBStatus(t, env.do(t, http.MethodPut, path, admin, map[string]any{"enabled_scanners": store.AllScanners}), http.StatusOK, "PUT all ten")
	if r, _ := readAgentConfigRow(t, env, ctx, agent); r.scanners != strings.Join(store.AllScanners, "|") {
		t.Fatalf("PUT all ten stored %q", r.scanners)
	}
}
