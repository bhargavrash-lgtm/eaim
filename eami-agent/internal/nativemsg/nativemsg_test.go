package nativemsg

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// ─── readMessage / writeMessage ────────────────────────────────────────────────

func lengthPrefixed(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(body)))
	buf.Write(lenBuf[:])
	buf.WriteString(body)
	return buf.Bytes()
}

func TestReadMessage_ValidMessage(t *testing.T) {
	raw := lengthPrefixed(t, `{"type":"paste_event"}`)
	got, err := readMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if string(got) != `{"type":"paste_event"}` {
		t.Fatalf("got %q", got)
	}
}

func TestReadMessage_EmptyInput_ReturnsEOF(t *testing.T) {
	_, err := readMessage(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF, got %v", err)
	}
}

func TestReadMessage_TruncatedLengthPrefix(t *testing.T) {
	// Only 2 of the required 4 length-prefix bytes.
	_, err := readMessage(bytes.NewReader([]byte{0x05, 0x00}))
	if err == nil {
		t.Fatal("want an error for a truncated length prefix, got nil")
	}
}

func TestReadMessage_OversizedDeclaredLength_RejectedWithoutAllocating(t *testing.T) {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], 0xFFFFFFFF) // ~4GB, garbage/hostile
	_, err := readMessage(bytes.NewReader(lenBuf[:]))
	if err == nil {
		t.Fatal("want an error for an oversized declared length, got nil")
	}
}

func TestReadMessage_TruncatedBody(t *testing.T) {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], 10) // declares 10 bytes, provides fewer
	buf := append(lenBuf[:], []byte("short")...)
	_, err := readMessage(bytes.NewReader(buf))
	if err == nil {
		t.Fatal("want an error for a truncated message body, got nil")
	}
}

func TestWriteMessage_RoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := writeMessage(&buf, response{Status: "ok"}); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	got, err := readMessage(&buf)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var resp response
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("got status %q", resp.Status)
	}
}

// ─── validate ───────────────────────────────────────────────────────────────

func TestValidate_ValidMessage(t *testing.T) {
	msg := PasteEventMessage{
		Type:              "paste_event",
		DestinationDomain: "chat.openai.com",
		OccurredAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := validate(msg); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidate_WrongType(t *testing.T) {
	msg := PasteEventMessage{Type: "something_else", DestinationDomain: "chat.openai.com", OccurredAt: time.Now().Format(time.RFC3339)}
	if _, err := validate(msg); err == nil {
		t.Fatal("want an error for wrong type, got nil")
	}
}

func TestValidate_MissingDomain(t *testing.T) {
	msg := PasteEventMessage{Type: "paste_event", OccurredAt: time.Now().Format(time.RFC3339)}
	if _, err := validate(msg); err == nil {
		t.Fatal("want an error for missing destination_domain, got nil")
	}
}

func TestValidate_MissingOccurredAt(t *testing.T) {
	msg := PasteEventMessage{Type: "paste_event", DestinationDomain: "chat.openai.com"}
	if _, err := validate(msg); err == nil {
		t.Fatal("want an error for missing occurred_at, got nil")
	}
}

func TestValidate_InvalidOccurredAtFormat(t *testing.T) {
	msg := PasteEventMessage{Type: "paste_event", DestinationDomain: "chat.openai.com", OccurredAt: "not-a-timestamp"}
	if _, err := validate(msg); err == nil {
		t.Fatal("want an error for a non-RFC3339 occurred_at, got nil")
	}
}

// ─── RunHost ────────────────────────────────────────────────────────────────

// fakeSender records every report handed to Send. panicOn, when > 0, panics
// on that 1-indexed call number instead of recording/returning, letting
// tests prove RunHost survives a panic originating inside Send.
type fakeSender struct {
	calls   []outboundReport
	sendErr error
	panicOn int
}

func (f *fakeSender) Send(_ context.Context, report any) error {
	f.calls = append(f.calls, report.(outboundReport))
	if f.panicOn > 0 && len(f.calls) == f.panicOn {
		panic("boom: simulated panic inside Sender.Send")
	}
	return f.sendErr
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunHost_ValidPasteEvent_ForwardsWithHostIdentityAttached(t *testing.T) {
	occurredAt := time.Now().UTC().Format(time.RFC3339)
	hash := "abc123"
	length := int32(42)
	msg := PasteEventMessage{
		Type:              "paste_event",
		DestinationDomain: "chat.openai.com",
		OccurredAt:        occurredAt,
		ContentLength:     &length,
		ContentHash:       &hash,
	}
	body, _ := json.Marshal(msg)

	var in bytes.Buffer
	writeRaw(t, &in, body)

	var out bytes.Buffer
	sender := &fakeSender{}

	if err := RunHost(context.Background(), &in, &out, sender, "agent-123", "host-abc", testLogger()); err != nil {
		t.Fatalf("RunHost: %v", err)
	}

	if len(sender.calls) != 1 {
		t.Fatalf("want 1 Send call, got %d", len(sender.calls))
	}
	got := sender.calls[0]
	if got.AgentID != "agent-123" || got.Hostname != "host-abc" {
		t.Fatalf("host identity not attached correctly: %+v", got)
	}
	if len(got.PasteEvents) != 1 || got.PasteEvents[0].DestinationDomain != "chat.openai.com" {
		t.Fatalf("paste event not forwarded correctly: %+v", got)
	}

	resp := readResponse(t, &out)
	if resp.Status != "ok" {
		t.Fatalf("want status ok, got %+v", resp)
	}
}

func TestRunHost_MalformedJSON_ErrorResponse_LoopContinues(t *testing.T) {
	var in bytes.Buffer
	writeRaw(t, &in, []byte(`{not valid json`))
	// A second, valid message right after -- proves the loop kept serving
	// subsequent messages instead of dying on the first bad one.
	validMsg, _ := json.Marshal(PasteEventMessage{
		Type: "paste_event", DestinationDomain: "chat.openai.com",
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
	})
	writeRaw(t, &in, validMsg)

	var out bytes.Buffer
	sender := &fakeSender{}
	if err := RunHost(context.Background(), &in, &out, sender, "agent-1", "host-1", testLogger()); err != nil {
		t.Fatalf("RunHost: %v", err)
	}

	resp1 := readResponse(t, &out)
	if resp1.Status != "error" {
		t.Fatalf("want error status for malformed JSON, got %+v", resp1)
	}
	resp2 := readResponse(t, &out)
	if resp2.Status != "ok" {
		t.Fatalf("want ok status for the subsequent valid message, got %+v", resp2)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("want exactly 1 Send call (only the valid message), got %d", len(sender.calls))
	}
}

func TestRunHost_OversizedLengthPrefix_ErrorResponse_DoesNotCrash(t *testing.T) {
	var in bytes.Buffer
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], 0xFFFFFFFF)
	in.Write(lenBuf[:])

	var out bytes.Buffer
	sender := &fakeSender{}
	if err := RunHost(context.Background(), &in, &out, sender, "agent-1", "host-1", testLogger()); err != nil {
		t.Fatalf("RunHost: %v", err)
	}
	resp := readResponse(t, &out)
	if resp.Status != "error" {
		t.Fatalf("want error status, got %+v", resp)
	}
}

func TestRunHost_SendFails_ErrorResponse_DoesNotCrash(t *testing.T) {
	msg, _ := json.Marshal(PasteEventMessage{
		Type: "paste_event", DestinationDomain: "chat.openai.com",
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
	})
	var in bytes.Buffer
	writeRaw(t, &in, msg)

	var out bytes.Buffer
	sender := &fakeSender{sendErr: errors.New("collector unreachable")}
	if err := RunHost(context.Background(), &in, &out, sender, "agent-1", "host-1", testLogger()); err != nil {
		t.Fatalf("RunHost: %v", err)
	}
	resp := readResponse(t, &out)
	if resp.Status != "error" {
		t.Fatalf("want error status when Send fails, got %+v", resp)
	}
}

// TestRunHost_PanicInsideSend_RecoveredAndProcessSurvives is the direct
// analog of B-027's per-iteration panic-recovery proof: a panic handling
// one message must not crash the host, and the loop must keep serving
// subsequent messages afterward.
func TestRunHost_PanicInsideSend_RecoveredAndProcessSurvives(t *testing.T) {
	msg, _ := json.Marshal(PasteEventMessage{
		Type: "paste_event", DestinationDomain: "chat.openai.com",
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
	})

	var in bytes.Buffer
	writeRaw(t, &in, msg) // 1st call to Send panics
	writeRaw(t, &in, msg) // 2nd message must still be processed normally

	var out bytes.Buffer
	sender := &fakeSender{panicOn: 1}

	if err := RunHost(context.Background(), &in, &out, sender, "agent-1", "host-1", testLogger()); err != nil {
		t.Fatalf("RunHost returned an error (it should have recovered): %v", err)
	}

	resp1 := readResponse(t, &out)
	if resp1.Status != "error" {
		t.Fatalf("want error status for the panicking message, got %+v", resp1)
	}
	resp2 := readResponse(t, &out)
	if resp2.Status != "ok" {
		t.Fatalf("want ok status for the message after the panic, got %+v", resp2)
	}
	if len(sender.calls) != 2 {
		t.Fatalf("want 2 Send calls (panic happens inside Send, after the call is recorded), got %d", len(sender.calls))
	}
}

func TestRunHost_EOF_ReturnsNilNotError(t *testing.T) {
	var in bytes.Buffer // empty -- immediate EOF, simulating the browser closing the pipe
	var out bytes.Buffer
	sender := &fakeSender{}
	if err := RunHost(context.Background(), &in, &out, sender, "agent-1", "host-1", testLogger()); err != nil {
		t.Fatalf("want nil error on clean EOF (normal shutdown), got %v", err)
	}
}

// ─── test helpers ───────────────────────────────────────────────────────────

func writeRaw(t *testing.T, buf *bytes.Buffer, body []byte) {
	t.Helper()
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(body)))
	buf.Write(lenBuf[:])
	buf.Write(body)
}

func readResponse(t *testing.T, r io.Reader) response {
	t.Helper()
	body, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var resp response
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}
