// issue_http_preauth_test.go -- eami-gateway/internal/identity
//
// Pure unit tests for B-120's pre-auth concurrency gate on
// POST /v1/gateway/tokens, needing no Postgres: a fake, controllable
// APIKeyValidator lets these tests hold N calls genuinely "in flight"
// simultaneously (blocked inside ValidateAndResolveAgent until released),
// which is what makes it possible to PROVE -- not infer from timing --
// that a request arriving while every slot is occupied is rejected without
// ever reaching the DB validation step, and that a released slot really is
// usable again afterward. Complements issue_http_preauth_pg_test.go's
// real-Postgres proof of the same contract.
package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBlockingAPIKeyValidator blocks inside ValidateAndResolveAgent until
// release fires, first signaling on started (non-blocking, buffered) so a
// test can wait for a call to genuinely be in flight before proceeding.
// Counts total calls made, regardless of blocking.
type fakeBlockingAPIKeyValidator struct {
	started chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
}

func newFakeBlockingAPIKeyValidator(startedCapacity int) *fakeBlockingAPIKeyValidator {
	return &fakeBlockingAPIKeyValidator{
		started: make(chan struct{}, startedCapacity),
		release: make(chan struct{}),
	}
}

func (f *fakeBlockingAPIKeyValidator) ValidateAndResolveAgent(_ context.Context, _, _ string) (*APIKeyRecord, *ResolvedAgent, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	f.started <- struct{}{}
	<-f.release
	return nil, nil, ErrInvalidAPIKey
}

func (f *fakeBlockingAPIKeyValidator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// noopTokenEventStore is never actually exercised by these tests (every
// request here is rejected before Manager.Issue would run), but
// NewIssueHandler requires a non-nil TokenEventStore.
type noopTokenEventStore struct{}

func (noopTokenEventStore) RecordIssued(context.Context, string, string, string, string, string) error {
	return nil
}
func (noopTokenEventStore) RecordRevoked(context.Context, string, string, string, string) error {
	return nil
}

func newPreAuthTestManager(t *testing.T) *Manager {
	t.Helper()
	keyPath := t.TempDir() + "/gateway.key"
	m, err := NewManager(keyPath, 300, "eami-gateway-preauth-test")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func preAuthRequest() *http.Request {
	body, _ := json.Marshal(IssueRequest{AgentID: "agent:whoever", Scope: "read:test", Task: "t", Model: "m", Owner: "o", RiskTier: "low"})
	return httptest.NewRequest(http.MethodPost, "/v1/gateway/tokens", strings.NewReader(string(body)))
}

// TestHandleIssue_PreAuthGate_RejectsRequestArrivingWhileSaturated is
// B-120's centerpiece / AC2's proof: with the gate's every slot genuinely
// occupied by a real in-flight (blocked) ValidateAndResolveAgent call, one
// more request is rejected immediately with 429 and never itself reaches
// ValidateAndResolveAgent -- proven by an exact call count after every
// blocked call is released and completes, not inferred from response
// latency. Parameterized over two concurrency limits to also prove the
// threshold is a real, configurable parameter (AC1), not a hardcoded
// constant.
func TestHandleIssue_PreAuthGate_RejectsRequestArrivingWhileSaturated(t *testing.T) {
	for _, maxConcurrent := range []int{1, 4} {
		t.Run("", func(t *testing.T) {
			keys := newFakeBlockingAPIKeyValidator(maxConcurrent)
			h := NewIssueHandler(newPreAuthTestManager(t), keys, noopTokenEventStore{}, IssueRateLimits{
				PerAgentLimit: 100000, PerAgentWindow: time.Minute,
				PreAuthMaxConcurrent: maxConcurrent,
			})

			var wg sync.WaitGroup
			codes := make([]int, maxConcurrent)
			for i := 0; i < maxConcurrent; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					w := httptest.NewRecorder()
					h.HandleIssue(w, preAuthRequest())
					codes[idx] = w.Code
				}(i)
			}

			// Wait until all maxConcurrent calls are genuinely in flight
			// (each has entered ValidateAndResolveAgent and is blocked
			// there), not merely scheduled.
			for i := 0; i < maxConcurrent; i++ {
				select {
				case <-keys.started:
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for all %d concurrent calls to start", maxConcurrent)
				}
			}

			// One more request, while every slot is genuinely held, must
			// be rejected immediately -- and must NOT itself reach
			// ValidateAndResolveAgent (it would otherwise also block on
			// keys.release, which nothing has fired yet, and this call
			// would hang until the test's own deadline).
			w := httptest.NewRecorder()
			h.HandleIssue(w, preAuthRequest())
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("request over the concurrency limit of %d: status = %d, want 429", maxConcurrent, w.Code)
			}
			if retryAfter := w.Header().Get("Retry-After"); retryAfter == "" {
				t.Error("expected a Retry-After header on the pre-auth 429")
			}

			close(keys.release)
			wg.Wait()
			for i, c := range codes {
				if c != http.StatusUnauthorized {
					t.Errorf("in-flight request %d: status = %d, want 401 (a real validation ran once unblocked)", i, c)
				}
			}
			if got := keys.callCount(); got != maxConcurrent {
				t.Fatalf("ValidateAndResolveAgent calls = %d, want exactly %d -- the rejected request must never have reached it", got, maxConcurrent)
			}
		})
	}
}

// TestHandleIssue_PreAuthGate_ReleaseFreesSlotForNextCaller proves the gate
// doesn't leak a slot: once an in-flight call completes (and releases its
// slot), a subsequent request can genuinely acquire it, rather than every
// slot being permanently consumed after first use.
func TestHandleIssue_PreAuthGate_ReleaseFreesSlotForNextCaller(t *testing.T) {
	keys := newFakeBlockingAPIKeyValidator(1)
	h := NewIssueHandler(newPreAuthTestManager(t), keys, noopTokenEventStore{}, IssueRateLimits{
		PerAgentLimit: 100000, PerAgentWindow: time.Minute,
		PreAuthMaxConcurrent: 1,
	})

	firstDone := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		h.HandleIssue(w, preAuthRequest())
		firstDone <- w.Code
	}()
	select {
	case <-keys.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first call to start")
	}

	// The sole slot is held -- a second request right now must be rejected.
	w := httptest.NewRecorder()
	h.HandleIssue(w, preAuthRequest())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request while the slot is held: status = %d, want 429", w.Code)
	}

	// Release the first call and let it finish, freeing its slot.
	close(keys.release)
	select {
	case code := <-firstDone:
		if code != http.StatusUnauthorized {
			t.Fatalf("first request: status = %d, want 401", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first call to finish")
	}

	// A third request now must succeed in acquiring the (freed) slot --
	// keys.release is already closed, so this call returns immediately
	// rather than blocking.
	w2 := httptest.NewRecorder()
	h.HandleIssue(w2, preAuthRequest())
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("third request after the slot was freed: status = %d, want 401 (a real validation ran, proving the slot was reusable)", w2.Code)
	}
	if got := keys.callCount(); got != 2 {
		t.Fatalf("ValidateAndResolveAgent calls = %d, want exactly 2 (first + third; the rejected second never reached it)", got)
	}
}
