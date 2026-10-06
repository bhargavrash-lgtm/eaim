package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// B-269 Slice 0 (B-277 path rules, decision D10 codes, S4/S5): the config
// PUT rejects with stable {code, field} bodies that never echo the value;
// the stricter rules (whole-profile parents) apply only when the paths
// change; legacy paths stay accepted and flagged; nothing leaks across orgs.

type fieldErr struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func putConfig(t *testing.T, env *workspaceTestEnv, tok string, agent uuid.UUID, body map[string]any) (int, fieldErr, map[string]any) {
	t.Helper()
	resp := env.do(t, http.MethodPut, fmt.Sprintf("/v1/gateway/agents/%s/config", agent), tok, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var fe fieldErr
	var ok map[string]any
	if resp.StatusCode == http.StatusOK {
		_ = json.Unmarshal(raw, &ok)
	} else {
		_ = json.Unmarshal(raw, &fe)
	}
	return resp.StatusCode, fe, ok
}

func TestAgentConfig_Slice0_CodesNeverEchoValues_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "s0-codes")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@s0c.test", "admin")
	agent := seedConfigAgent(t, env, ctx, orgID, "s0-codes-agent")

	secret := "SECRET-VALUE-7f3a"
	cases := []struct {
		body        map[string]any
		code, field string
	}{
		{map[string]any{"scan_interval_seconds": 5}, "interval_out_of_range", "scan_interval_seconds"},
		{map[string]any{"max_report_size_bytes": 1}, "report_size_out_of_range", "max_report_size_bytes"},
		{map[string]any{"model_file_size_mb": 0}, "model_size_out_of_range", "model_file_size_mb"},
		{map[string]any{"model_scan_paths": []string{secret}}, "path_not_absolute", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{`\\` + secret + `\share`}}, "path_network", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{"/"}}, "path_root", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{`C:\..\`}}, "path_not_normalized", "model_scan_paths"},
		// Forms Windows silently rewrites back to C:\Users (security review M-1).
		{map[string]any{"model_scan_paths": []string{`C:\Users.`}}, "path_not_normalized", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{`C:\Users::$INDEX_ALLOCATION`}}, "path_invalid_chars", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{`C:\ProgramData\..\Users`}}, "path_not_normalized", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{`C:\Documents and Settings`}}, "path_profile_parent", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{"/System/Volumes/Data/Users"}}, "path_profile_parent", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{""}}, "path_empty", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{"/" + secret + "\n"}}, "path_invalid_chars", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{"/" + strings.Repeat("a", 1100)}}, "path_too_long", "model_scan_paths"},
		{map[string]any{"enabled_scanners": []string{secret}}, "unknown_scanner", "enabled_scanners"},
		{map[string]any{"model_scan_paths": []string{"/home"}}, "path_profile_parent", "model_scan_paths"},
		{map[string]any{"model_scan_paths": []string{`C:\Users`, "/srv/m"}}, "path_profile_parent", "model_scan_paths"},
	}
	for _, c := range cases {
		status, fe, _ := putConfig(t, env, admin, agent, c.body)
		if status != http.StatusBadRequest || fe.Code != c.code || fe.Field != c.field || fe.Message == "" {
			t.Errorf("%v: status %d body %+v, want 400 %s/%s", c.body, status, fe, c.code, c.field)
		}
		if strings.Contains(fe.Message, secret) || strings.Contains(fe.Message, "SECRET") {
			t.Errorf("%v: the message echoed the submitted value: %q", c.body, fe.Message)
		}
	}
	// Nothing was written by any rejected PUT: still the empty default (S4).
	var n int
	if err := env.pool.QueryRow(ctx, `SELECT cardinality(model_scan_paths) FROM agent_configs WHERE agent_id=$1`, agent).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("new agent's default paths: %d entries, want 0 (S4)", n)
	}
	// Specific folders inside a profile tree are fine.
	if status, fe, _ := putConfig(t, env, admin, agent, map[string]any{"model_scan_paths": []string{"/home/alice/models", `C:\Users\bob\models`}}); status != http.StatusOK {
		t.Fatalf("specific profile subfolders: %d %+v", status, fe)
	}
}

// S5: a legacy row carrying /home can still save its other fields (paths
// unchanged), its paths are flagged, and re-sending the same list is not a
// change; changing the list applies the full rules.
func TestAgentConfig_Slice0_LegacyPathsAcceptedAndFlagged_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	orgID := seedTestOrg(t, ctx, env.pool, "s0-legacy")
	admin := env.token(t, seedTestUser(t, ctx, env.pool, orgID), orgID, "admin@s0l.test", "admin")
	agent := seedConfigAgent(t, env, ctx, orgID, "s0-legacy-agent")
	if _, err := env.pool.Exec(ctx, `UPDATE agent_configs SET model_scan_paths = ARRAY['/home','/Users','C:\\Users'] WHERE agent_id=$1`, agent); err != nil {
		t.Fatal(err)
	}

	status, fe, ok := putConfig(t, env, admin, agent, map[string]any{"scan_interval_seconds": 900})
	if status != http.StatusOK {
		t.Fatalf("other field on a legacy row: %d %+v", status, fe)
	}
	if w, _ := ok["path_warnings"].([]any); len(w) != 1 || w[0] != "path_profile_parent" {
		t.Fatalf("legacy paths not flagged: %v", ok["path_warnings"])
	}
	// The UI always re-sends exactly the paths it loaded: the same set, in
	// another order, is not a change. (The stored legacy value really is
	// C:\\Users with a doubled backslash; it round-trips unchanged.)
	if status, fe, _ := putConfig(t, env, admin, agent, map[string]any{"model_scan_paths": []string{`C:\\Users`, "/home", "/Users"}, "scan_interval_seconds": 600}); status != http.StatusOK {
		t.Fatalf("unchanged legacy paths re-sent: %d %+v", status, fe)
	}
	// A real change is checked in full.
	if status, fe, _ := putConfig(t, env, admin, agent, map[string]any{"model_scan_paths": []string{"/home", "/srv/models"}}); status != http.StatusBadRequest || fe.Code != "path_profile_parent" {
		t.Fatalf("changed list keeping /home: %d %+v", status, fe)
	}
	if status, fe, ok := putConfig(t, env, admin, agent, map[string]any{"model_scan_paths": []string{"/srv/models"}}); status != http.StatusOK {
		t.Fatalf("clean replacement: %d %+v", status, fe)
	} else if w, _ := ok["path_warnings"].([]any); len(w) != 0 {
		t.Fatalf("clean paths still flagged: %v", ok["path_warnings"])
	}
}

// The full rules read the stored row, so they run only after the ownership
// check: another org can't learn anything from them (B-232/B-233 standard).
func TestAgentConfig_Slice0_CrossOrg_RealDB(t *testing.T) {
	env := newWorkspaceTestEnv(t)
	ctx := context.Background()
	victimOrg := seedTestOrg(t, ctx, env.pool, "s0-victim")
	attackerOrg := seedTestOrg(t, ctx, env.pool, "s0-attacker")
	attacker := env.token(t, seedTestUser(t, ctx, env.pool, attackerOrg), attackerOrg, "admin@s0a.test", "admin")
	victimAgent := seedConfigAgent(t, env, ctx, victimOrg, "s0-victim-agent")
	if _, err := env.pool.Exec(ctx, `UPDATE agent_configs SET model_scan_paths = ARRAY['/home'] WHERE agent_id=$1`, victimAgent); err != nil {
		t.Fatal(err)
	}
	for _, body := range []map[string]any{
		{"model_scan_paths": []string{"/home"}},              // same as the victim's: would be 200 in-org
		{"model_scan_paths": []string{"/home", "/srv"}},      // a change: would be 400 in-org
		{"model_scan_paths": []string{"/srv/ok"}},            // valid
	} {
		if status, fe, _ := putConfig(t, env, attacker, victimAgent, body); status != http.StatusNotFound {
			t.Errorf("cross-org PUT %v: %d %+v, want 404", body, status, fe)
		}
	}
	resp := env.do(t, http.MethodGet, fmt.Sprintf("/v1/gateway/agents/%s/config", victimAgent), attacker, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-org GET: %d", resp.StatusCode)
	}
	var paths string
	if err := env.pool.QueryRow(ctx, `SELECT array_to_string(model_scan_paths, '|') FROM agent_configs WHERE agent_id=$1`, victimAgent).Scan(&paths); err != nil {
		t.Fatal(err)
	}
	if paths != "/home" {
		t.Fatalf("victim row changed: %q", paths)
	}
}

// S2: every model source the agent sends is stored as itself (lm_studio was
// stored as "unknown" before; gpt4all too; scan_path is new).
func TestIngest_Slice0_ModelSourcesStored(t *testing.T) {
	env := newIngestRelayEnv(t)
	agentID := "s0-sources-" + uuid.NewString()[:8]
	env.cleanupAgent(t, agentID)
	now := time.Now().UTC().Format(time.RFC3339)
	model := func(name, src string) map[string]any {
		return map[string]any{"name": name, "source": src, "file_path": "/srv/" + name, "size_bytes": 200 << 20}
	}
	item := map[string]any{
		"id": uuid.NewString(), "agent_id": agentID, "hostname": agentID, "received_at": now,
		"report": map[string]any{
			"agent_id": agentID, "hostname": agentID, "collected_at": now,
			"local_models": []map[string]any{
				model("a.gguf", "lm_studio"), model("b.gguf", "gpt4all"), model("c.safetensors", "scan_path"),
				model("d", "ollama"), model("e", "huggingface"), model("f", "something_new"),
			},
		},
	}
	if accepted := decodeIngestAccepted(t, env.postBatch(t, []map[string]any{item})); accepted != 1 {
		t.Fatalf("accepted %d", accepted)
	}
	rows, err := env.pool.Query(context.Background(),
		`SELECT mf.name, mf.source FROM endpoint_model_files mf JOIN endpoints e ON e.id = mf.endpoint_id WHERE e.agent_id = $1`, agentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var n, s string
		if err := rows.Scan(&n, &s); err != nil {
			t.Fatal(err)
		}
		got[n] = s
	}
	want := map[string]string{"a.gguf": "lmstudio", "b.gguf": "gpt4all", "c.safetensors": "scan_path", "d": "ollama", "e": "huggingface", "f": "unknown"}
	for n, s := range want {
		if got[n] != s {
			t.Errorf("%s: stored source %q, want %q", n, got[n], s)
		}
	}
}
