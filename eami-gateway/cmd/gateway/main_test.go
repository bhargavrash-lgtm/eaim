package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/eami/gateway/internal/mcp"
)

// TestSafeWriteTokenUsage_PanicRecovered proves acceptance criterion #2 for
// the token-usage writer: a panic inside the fire-and-forget write doesn't
// crash the goroutine (and, by extension, the process).
func TestSafeWriteTokenUsage_PanicRecovered(t *testing.T) {
	orig := tokenUsageWriteFunc
	defer func() { tokenUsageWriteFunc = orig }()

	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		panic("simulated write failure")
	}

	done := make(chan struct{})
	go func() {
		safeWriteTokenUsage("http://eami-api", "service-key", tokenUsagePayload{AgentName: "agent-1"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("safeWriteTokenUsage did not return — panic escaped")
	}
}

// TestSafeWriteTokenUsage_NextCallStillWritesNormally proves that after one
// call panics, a subsequent call (the next dispatched event) still performs
// a normal write — the panic doesn't leave the writer disabled.
func TestSafeWriteTokenUsage_NextCallStillWritesNormally(t *testing.T) {
	orig := tokenUsageWriteFunc
	defer func() { tokenUsageWriteFunc = orig }()

	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		panic("simulated write failure")
	}
	safeWriteTokenUsage("http://eami-api", "service-key", tokenUsagePayload{})

	var gotPayload tokenUsagePayload
	called := make(chan struct{})
	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		gotPayload = p
		close(called)
		return nil
	}

	go safeWriteTokenUsage("http://eami-api", "service-key", tokenUsagePayload{AgentName: "agent-2"})

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("second call to safeWriteTokenUsage never invoked the write func")
	}
	if gotPayload.AgentName != "agent-2" {
		t.Fatalf("expected agent-2's payload to be written, got %+v", gotPayload)
	}
}

// TestSafeWriteTokenUsage_NoPanic_BehavesLikeBefore is a regression guard:
// with no panic, the wrapper is transparent to writeTokenUsage's own error
// handling (still just logged, not surfaced).
func TestSafeWriteTokenUsage_NoPanic_BehavesLikeBefore(t *testing.T) {
	orig := tokenUsageWriteFunc
	defer func() { tokenUsageWriteFunc = orig }()

	called := false
	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		called = true
		return nil
	}
	safeWriteTokenUsage("http://eami-api", "service-key", tokenUsagePayload{})
	if !called {
		t.Fatal("tokenUsageWriteFunc was not called")
	}
}

// ─── recordTokenUsage (B-099): the shared helper both dispatch branches ───
// ─── (immediate-Allow and escalate-then-approved) now call ────────────────

// TestRecordTokenUsage_ExtractsAndWrites proves the shared helper performs
// the exact same extract-then-write sequence the immediate-Allow branch
// always has: a body shaped like a real ai_provider response yields a
// correctly populated payload reaching tokenUsageWriteFunc.
func TestRecordTokenUsage_ExtractsAndWrites(t *testing.T) {
	orig := tokenUsageWriteFunc
	defer func() { tokenUsageWriteFunc = orig }()

	var gotPayload tokenUsagePayload
	called := make(chan struct{})
	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		gotPayload = p
		close(called)
		return nil
	}

	body := json.RawMessage(`{"model":"claude-haiku-4-5-20251001","usage":{"input_tokens":8,"output_tokens":1}}`)
	ac := mcp.ActionContext{OrgID: "org-1", AgentUUID: "agent-uuid-1", AgentName: "agent-alpha", Tool: "claude-connector"}

	recordTokenUsage("http://eami-api", "service-key", body, ac)

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("recordTokenUsage never invoked tokenUsageWriteFunc")
	}
	if gotPayload.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("Model = %q, want claude-haiku-4-5-20251001", gotPayload.Model)
	}
	if gotPayload.InputTokens != 8 || gotPayload.OutputTokens != 1 {
		t.Errorf("InputTokens/OutputTokens = %d/%d, want 8/1", gotPayload.InputTokens, gotPayload.OutputTokens)
	}
	if gotPayload.OrgID != "org-1" || gotPayload.AgentID != "agent-uuid-1" || gotPayload.AgentName != "agent-alpha" {
		t.Errorf("identity fields not carried through: %+v", gotPayload)
	}
	if gotPayload.ToolName != "claude-connector" {
		t.Errorf("ToolName = %q, want claude-connector (B-108)", gotPayload.ToolName)
	}
}

// TestExtractTokenUsage_ToolNameSurvivesUnparseableBody proves ToolName
// (B-108) is set from ac.Tool unconditionally -- unlike Model/token counts,
// which live inside result and are zeroed when result is empty or doesn't
// parse, ToolName is known at the call site regardless of what the
// downstream response body contains.
func TestExtractTokenUsage_ToolNameSurvivesUnparseableBody(t *testing.T) {
	ac := mcp.ActionContext{OrgID: "org-1", AgentUUID: "agent-1", AgentName: "agent-alpha", Tool: "some-connector"}

	// Empty body: extractTokenUsage returns early, before Model/tokens are set.
	p := extractTokenUsage(json.RawMessage(``), ac)
	if p.ToolName != "some-connector" {
		t.Errorf("empty body: ToolName = %q, want some-connector", p.ToolName)
	}
	if p.Model != "" || p.InputTokens != 0 || p.OutputTokens != 0 {
		t.Errorf("empty body: expected zero Model/token fields, got %+v", p)
	}

	// Unparseable body: same early-return-with-zeros path for Model/tokens.
	p = extractTokenUsage(json.RawMessage(`not json`), ac)
	if p.ToolName != "some-connector" {
		t.Errorf("unparseable body: ToolName = %q, want some-connector", p.ToolName)
	}
}

// ─── Cache-token extraction (B-111) ────────────────────────────────────────

// TestExtractTokenUsage_CacheBreakdownPresent_UsesExactSplit uses the exact
// real response shape captured from a live claude-haiku-4-5-20251001
// dispatch with a pure 1h-only cache_control (no 5m mixed in) — proving the
// breakdown object is trusted directly, not just the flat total, and that a
// single-TTL response (not just a documented mixed one) really does carry it.
func TestExtractTokenUsage_CacheBreakdownPresent_UsesExactSplit(t *testing.T) {
	ac := mcp.ActionContext{OrgID: "org-1", AgentUUID: "agent-1", AgentName: "agent-alpha", Tool: "claude"}
	body := json.RawMessage(`{"model":"claude-haiku-4-5-20251001","usage":{"input_tokens":14,"cache_creation_input_tokens":4812,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":4812},"output_tokens":5}}`)

	p := extractTokenUsage(body, ac)

	if p.CacheCreation5mTokens != 0 {
		t.Errorf("CacheCreation5mTokens = %d, want 0", p.CacheCreation5mTokens)
	}
	if p.CacheCreation1hTokens != 4812 {
		t.Errorf("CacheCreation1hTokens = %d, want 4812", p.CacheCreation1hTokens)
	}
	if p.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0", p.CacheReadTokens)
	}
	if p.InputTokens != 14 || p.OutputTokens != 5 {
		t.Errorf("InputTokens/OutputTokens = %d/%d, want 14/5 (unaffected by cache parsing)", p.InputTokens, p.OutputTokens)
	}
}

// TestExtractTokenUsage_CacheRead_NoBreakdownNeeded proves cache_read_input_tokens
// is parsed independent of any cache_creation activity — a pure cache-hit
// request (reading a previously-written cache, writing nothing new).
func TestExtractTokenUsage_CacheRead_NoBreakdownNeeded(t *testing.T) {
	ac := mcp.ActionContext{OrgID: "org-1", AgentUUID: "agent-1", AgentName: "agent-alpha", Tool: "claude"}
	body := json.RawMessage(`{"model":"claude-haiku-4-5-20251001","usage":{"input_tokens":14,"cache_creation_input_tokens":0,"cache_read_input_tokens":4812,"output_tokens":5}}`)

	p := extractTokenUsage(body, ac)

	if p.CacheReadTokens != 4812 {
		t.Errorf("CacheReadTokens = %d, want 4812", p.CacheReadTokens)
	}
	if p.CacheCreation5mTokens != 0 || p.CacheCreation1hTokens != 0 {
		t.Errorf("CacheCreation5mTokens/1hTokens = %d/%d, want 0/0", p.CacheCreation5mTokens, p.CacheCreation1hTokens)
	}
}

// TestExtractTokenUsage_CacheBreakdownAbsent_FallsBackToFlat5m covers the
// defensive fallback branch (not observed reachable against the real API in
// B-111's live verification, but kept in case some response path ever omits
// the breakdown object): a flat cache_creation_input_tokens with no nested
// cache_creation object is attributed entirely to the 5m tier, not dropped.
func TestExtractTokenUsage_CacheBreakdownAbsent_FallsBackToFlat5m(t *testing.T) {
	ac := mcp.ActionContext{OrgID: "org-1", AgentUUID: "agent-1", AgentName: "agent-alpha", Tool: "claude"}
	body := json.RawMessage(`{"model":"claude-haiku-4-5-20251001","usage":{"input_tokens":14,"cache_creation_input_tokens":300,"cache_read_input_tokens":0,"output_tokens":5}}`)

	p := extractTokenUsage(body, ac)

	if p.CacheCreation5mTokens != 300 {
		t.Errorf("CacheCreation5mTokens = %d, want 300 (fallback: flat total attributed to 5m)", p.CacheCreation5mTokens)
	}
	if p.CacheCreation1hTokens != 0 {
		t.Errorf("CacheCreation1hTokens = %d, want 0", p.CacheCreation1hTokens)
	}
}

// TestExtractTokenUsage_NoCaching_CacheFieldsStayZero is the regression
// guard for AC3: a plain response with no cache fields at all leaves all
// three new fields at their zero value, and InputTokens/OutputTokens parse
// exactly as they did before B-111.
func TestExtractTokenUsage_NoCaching_CacheFieldsStayZero(t *testing.T) {
	ac := mcp.ActionContext{OrgID: "org-1", AgentUUID: "agent-1", AgentName: "agent-alpha", Tool: "claude"}
	body := json.RawMessage(`{"model":"claude-haiku-4-5-20251001","usage":{"input_tokens":8,"output_tokens":1}}`)

	p := extractTokenUsage(body, ac)

	if p.CacheCreation5mTokens != 0 || p.CacheCreation1hTokens != 0 || p.CacheReadTokens != 0 {
		t.Errorf("expected all cache fields to stay 0 for a non-caching response, got %+v", p)
	}
	if p.InputTokens != 8 || p.OutputTokens != 1 {
		t.Errorf("InputTokens/OutputTokens = %d/%d, want 8/1", p.InputTokens, p.OutputTokens)
	}
}

// TestRecordTokenUsage_CalledTwiceIndependently proves the shared helper is
// genuinely reusable across two call sites with independent ActionContexts
// (standing in for the Allow branch and the escalate-then-approved branch)
// without cross-contaminating state between calls -- the exact property
// B-099's fix depends on, since both branches now call the same function.
func TestRecordTokenUsage_CalledTwiceIndependently(t *testing.T) {
	orig := tokenUsageWriteFunc
	defer func() { tokenUsageWriteFunc = orig }()

	var mu sync.Mutex
	var got []tokenUsagePayload
	done := make(chan struct{}, 2)
	tokenUsageWriteFunc = func(ctx context.Context, apiBase, serviceKey string, p tokenUsagePayload) error {
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
		done <- struct{}{}
		return nil
	}

	allowBody := json.RawMessage(`{"model":"model-allow","usage":{"input_tokens":10,"output_tokens":2}}`)
	allowAC := mcp.ActionContext{OrgID: "org-1", AgentUUID: "agent-1", AgentName: "allow-agent"}
	escalateBody := json.RawMessage(`{"model":"model-escalate","usage":{"input_tokens":20,"output_tokens":4}}`)
	escalateAC := mcp.ActionContext{OrgID: "org-1", AgentUUID: "agent-2", AgentName: "escalate-agent"}

	recordTokenUsage("http://eami-api", "service-key", allowBody, allowAC)
	recordTokenUsage("http://eami-api", "service-key", escalateBody, escalateAC)

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 2 expected writes observed", i)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d payloads, want 2: %+v", len(got), got)
	}
	byAgent := map[string]tokenUsagePayload{}
	for _, p := range got {
		byAgent[p.AgentName] = p
	}
	allow, ok := byAgent["allow-agent"]
	if !ok || allow.Model != "model-allow" || allow.InputTokens != 10 {
		t.Errorf("allow-agent payload wrong or missing: %+v", byAgent)
	}
	escalate, ok := byAgent["escalate-agent"]
	if !ok || escalate.Model != "model-escalate" || escalate.InputTokens != 20 {
		t.Errorf("escalate-agent payload wrong or missing: %+v", byAgent)
	}
}
