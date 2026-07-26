// main_integration_test.go — manual-test-harness-as-an-automated-test for
// eami-agent's native-messaging host mode (paste-detection groundwork).
//
// This builds the real eami-agent binary, starts it as a genuine separate
// OS process in --native-messaging-host mode (exactly how a browser would
// invoke it), and drives it with a hand-constructed, real
// length-prefixed-JSON native-messaging-protocol message over its actual
// stdin/stdout pipes -- not an in-process function call. A fake HTTP
// server stands in for eami-collector so the test can assert the event
// was genuinely forwarded (a real HTTP POST fired), not just parsed.
//
// To run this exact scenario by hand instead of via `go test` (the literal
// "manual test harness" the task brief asks for):
//
//	go build -o eami-agent.exe ./cmd/agent
//	cat > test-agent.yaml <<'EOF'
//	agent:
//	  id: manual-test-agent
//	collector:
//	  url: http://localhost:8888   # point at a real or mock eami-collector
//	EOF
//	./eami-agent.exe --native-messaging-host --config test-agent.yaml
//	# then, in the same terminal, pipe a length-prefixed JSON message to
//	# its stdin (4 little-endian length bytes + the JSON body) and read
//	# the length-prefixed response back from stdout.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newNativeMessagingHostCmd builds the --native-messaging-host subprocess
// command. EAMI_NM_SKIP_PARENT_CHECK is set because these tests spawn the
// binary directly from `go test` (parent process is go.exe/the test
// runner, not a real browser) -- internal/nmlauncher's parent-process
// check would otherwise correctly refuse to start it, since go.exe isn't
// a recognized browser. This is the same documented operator escape
// hatch real deployments can use, reused here as a test seam rather than
// a separate mechanism.
func newNativeMessagingHostCmd(binPath, cfgPath string) *exec.Cmd {
	cmd := exec.Command(binPath, "--native-messaging-host", "--config", cfgPath)
	cmd.Env = append(os.Environ(), "EAMI_NM_SKIP_PARENT_CHECK=1")
	return cmd
}

func buildTestBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "eami-agent-test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binPath := filepath.Join(dir, name)

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "." // this test file's own directory: cmd/agent
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build eami-agent test binary: %v\n%s", err, out)
	}
	return binPath
}

func writeLengthPrefixed(t *testing.T, w io.Writer, body []byte) {
	t.Helper()
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(body)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		t.Fatalf("write length prefix: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("write body: %v", err)
	}
}

// readLengthPrefixed reads one response with a bounded wait, so a real
// regression that hangs the subprocess fails this test quickly and
// clearly instead of blocking until the whole `go test` run times out.
func readLengthPrefixed(t *testing.T, r io.Reader) []byte {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var lenBuf [4]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			ch <- result{nil, err}
			return
		}
		n := binary.LittleEndian.Uint32(lenBuf[:])
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			ch <- result{nil, err}
			return
		}
		ch <- result{buf, nil}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read length-prefixed response: %v", res.err)
		}
		return res.data
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a response from the native-messaging host process")
		return nil
	}
}

// TestNativeMessagingHost_EndToEnd is acceptance criterion #1: a
// manually-constructed native-messaging-protocol test message, sent to
// eami-agent's real host-mode process, is correctly parsed and results in
// an event being forwarded toward the backend ingestion path.
func TestNativeMessagingHost_EndToEnd(t *testing.T) {
	binPath := buildTestBinary(t)

	// Stand-in for eami-collector's POST /v1/ingest -- records whether a
	// real HTTP request actually arrived, proving "forwarded toward the
	// ingestion path" rather than merely "parsed".
	received := make(chan map[string]any, 1)
	mockCollector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ingest" {
			http.NotFound(w, r)
			return
		}
		var body io.Reader = r.Body
		// The real sender gzip-compresses; decompress isn't needed here
		// since Go's gzip.Reader wrapping happens only if the collector
		// sets Content-Encoding, which the real sender always sets --
		// use the same decompression eami-collector itself would.
		if r.Header.Get("Content-Encoding") == "gzip" {
			gr, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Errorf("mock collector: gzip reader: %v", err)
				http.Error(w, "bad gzip", http.StatusBadRequest)
				return
			}
			defer gr.Close()
			body = gr
		}
		var payload map[string]any
		if err := json.NewDecoder(body).Decode(&payload); err != nil {
			t.Errorf("mock collector: decode body: %v", err)
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer mockCollector.Close()

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "eami-agent.yaml")
	cfgYAML := fmt.Sprintf("agent:\n  id: manual-test-agent\ncollector:\n  url: %s\n", mockCollector.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	cmd := newNativeMessagingHostCmd(binPath, cfgPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start eami-agent --native-messaging-host: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	// A real, hand-constructed native-messaging-protocol message -- not
	// built via any of nativemsg's own helpers, deliberately, so this test
	// doesn't just re-exercise the same code it's meant to verify against.
	msgJSON := []byte(`{"type":"paste_event","destination_domain":"chat.openai.com","occurred_at":"2026-07-26T12:00:00Z","content_length":42,"content_hash":"deadbeef"}`)
	writeLengthPrefixed(t, stdin, msgJSON)

	respBody := readLengthPrefixed(t, stdout)
	var resp struct {
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", respBody, err)
	}
	if resp.Status != "ok" {
		t.Fatalf("want status ok, got %+v (stderr: %s)", resp, stderr.String())
	}

	select {
	case payload := <-received:
		if payload["agent_id"] != "manual-test-agent" {
			t.Errorf("want agent_id manual-test-agent, got %v", payload["agent_id"])
		}
		events, ok := payload["paste_events"].([]any)
		if !ok || len(events) != 1 {
			t.Fatalf("want exactly 1 paste event in the forwarded payload, got %v", payload["paste_events"])
		}
		ev := events[0].(map[string]any)
		if ev["destination_domain"] != "chat.openai.com" {
			t.Errorf("want destination_domain chat.openai.com, got %v", ev["destination_domain"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mock collector never received the forwarded event")
	}
}

// TestNativeMessagingHost_GarbageInput_DoesNotCrashProcess is acceptance
// criterion #3, exercised against the real subprocess (not just the
// in-process nativemsg tests): garbage input must not crash the host
// process, and a valid message sent afterward must still work.
func TestNativeMessagingHost_GarbageInput_DoesNotCrashProcess(t *testing.T) {
	binPath := buildTestBinary(t)

	mockCollector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer mockCollector.Close()

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "eami-agent.yaml")
	cfgYAML := fmt.Sprintf("agent:\n  id: garbage-test-agent\ncollector:\n  url: %s\n", mockCollector.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	cmd := newNativeMessagingHostCmd(binPath, cfgPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start eami-agent --native-messaging-host: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	// A correctly-framed message (valid length prefix) whose body is pure
	// random-looking garbage bytes, not JSON at all -- exercises the
	// "malformed/garbage input" acceptance criterion without desyncing
	// the length-prefix framing itself (a badly-aligned raw byte dump
	// would just corrupt the *next* message's framing too, which isn't
	// what's being tested here -- that's a stream-desync scenario, not a
	// "one bad message" scenario).
	writeLengthPrefixed(t, stdin, []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x11, 0x22, 0x33, 0x44})

	garbageResp := readLengthPrefixed(t, stdout)
	var gresp struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(garbageResp, &gresp); err != nil {
		t.Fatalf("process appears to have crashed or desynced after garbage input -- couldn't parse the error response: %v", err)
	}
	if gresp.Status != "error" {
		t.Fatalf("want status error for the garbage-body message, got %+v", gresp)
	}

	// The process must still be alive and able to serve a real message
	// right afterward -- proving garbage input didn't crash it or desync
	// the framing.
	validMsg := []byte(`{"type":"paste_event","destination_domain":"claude.ai","occurred_at":"2026-07-26T12:00:00Z"}`)
	writeLengthPrefixed(t, stdin, validMsg)

	respBody := readLengthPrefixed(t, stdout)
	var resp struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("process appears to have crashed or desynced after garbage input -- couldn't parse a response: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("want status ok for the valid message after garbage, got %+v", resp)
	}

	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		t.Fatalf("process exited unexpectedly after garbage input")
	}
}

// TestNativeMessagingHost_LoggingNeverCorruptsStdout is a regression test
// for a real bug this session's own testing caught: RunHost's internal
// log.Warn/log.Error calls (e.g. on a malformed length prefix) and this
// mode's parent-process-check log line both used to share the same
// slog.Logger as the rest of the agent, which writes to stdout -- the
// exact stream the native messaging protocol uses for framing. A single
// log line interleaved with protocol bytes desyncs the browser's
// length-prefix parsing and breaks the session; there is no way for the
// reader to tell log text from a length prefix. main.go now routes all
// logging to stderr in --native-messaging-host mode specifically. This
// test sends a malformed length prefix (a value that genuinely triggers
// nativemsg's log.Warn call, unlike the garbage-body case above, which
// doesn't log at all) and asserts stdout still parses as a clean,
// correctly-framed response.
func TestNativeMessagingHost_LoggingNeverCorruptsStdout(t *testing.T) {
	binPath := buildTestBinary(t)

	mockCollector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer mockCollector.Close()

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "eami-agent.yaml")
	cfgYAML := fmt.Sprintf("agent:\n  id: stdout-purity-test-agent\ncollector:\n  url: %s\n", mockCollector.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	cmd := newNativeMessagingHostCmd(binPath, cfgPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start eami-agent --native-messaging-host: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	// An oversized declared length -- readMessage rejects this and
	// RunHost logs a Warn before writing the error response, exercising
	// exactly the code path that used to corrupt stdout.
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], 0xFFFFFFFF)
	if _, err := stdin.Write(lenBuf[:]); err != nil {
		t.Fatalf("write oversized length prefix: %v", err)
	}

	respBody := readLengthPrefixed(t, stdout)
	var resp struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("stdout did not contain a clean, correctly-framed response (log output likely leaked onto stdout): %v -- raw bytes: %q", err, respBody)
	}
	if resp.Status != "error" {
		t.Fatalf("want status error for the oversized-length message, got %+v", resp)
	}

	// The warning should have gone to stderr, proving logging still
	// happens -- just on the right stream, not silently dropped.
	if !strings.Contains(stderr.String(), "read message failed") {
		t.Errorf("want the read-failure warning to appear on stderr, got stderr=%q", stderr.String())
	}
}
