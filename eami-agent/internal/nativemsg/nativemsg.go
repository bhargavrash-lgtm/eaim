// Package nativemsg implements a browser native-messaging host for
// eami-agent: a process the browser spawns fresh per connection and talks
// to exclusively over stdin/stdout, using Chrome/Edge/Firefox's standard
// length-prefixed-JSON protocol -- not a network port (see this task's
// brief for why native messaging was chosen over a local HTTP/websocket
// server: smaller attack surface, no listening port, and the browser
// itself enforces which extension IDs may invoke this process at all, via
// the native-messaging manifest's allowed_origins/allowed_extensions).
//
// A received paste event is attached to eami-agent's own known identity
// (agent_id, hostname -- the extension never supplies or guesses these)
// and forwarded immediately via the existing collector.Sender, bypassing
// the 5-minute poll cycle entirely. This package never touches the poll
// loop or any scanner -- main.go dispatches to RunHost as a wholly
// separate process invocation (see cmd/agent/main.go's
// --native-messaging-host flag), so the two code paths never run in the
// same process instance.
package nativemsg

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"time"
)

// maxMessageBytes caps the declared length of an incoming or outgoing
// native message. Chrome's own limit is 1MB extension->host (4GB
// host->extension since Chrome 224); paste-event payloads are always
// small, so 1MB is used both ways. This is also the load-bearing guard
// against malformed/hostile input (acceptance criterion #3): a corrupted
// or adversarial 4-byte length prefix (e.g. 0xFFFFFFFF, ~4GB) must never
// cause an attempted multi-gigabyte allocation/read.
const maxMessageBytes = 1 << 20 // 1 MiB

// PasteEventMessage is what the browser extension sends over native
// messaging for a single paste event. Deliberately has no field capable
// of carrying the pasted text itself -- mirrors B-032's PasteEventReport
// guarantee -- and no org_id/agent_id/hostname: the host attaches its own
// known identity, per this brief's contract that the extension should
// never need to supply or guess them.
type PasteEventMessage struct {
	Type              string  `json:"type"`
	DestinationDomain string  `json:"destination_domain"`
	OccurredAt        string  `json:"occurred_at"` // RFC3339
	ContentLength     *int32  `json:"content_length,omitempty"`
	ContentHash       *string `json:"content_hash,omitempty"`
	OSUsername        *string `json:"os_username,omitempty"`
}

// PasteEvent is a validated PasteEventMessage, ready to send onward.
type PasteEvent struct {
	DestinationDomain string  `json:"destination_domain"`
	OccurredAt        string  `json:"occurred_at"`
	ContentLength     *int32  `json:"content_length,omitempty"`
	ContentHash       *string `json:"content_hash,omitempty"`
	OSUsername        *string `json:"os_username,omitempty"`
}

// outboundReport is a minimal, standalone payload -- deliberately NOT
// payload.Report -- sent immediately via the existing collector.Sender
// the moment a paste event arrives, bypassing the 5-minute poll cycle
// entirely. eami-collector's IngestHandler only requires agent_id/
// hostname/collected_at (see eami-collector/internal/api/ingest.go's
// validateReport) and buffers/forwards the raw JSON bytes verbatim
// end-to-end, so this smaller shape is accepted by the existing pipeline
// completely unmodified -- no eami-collector/eami-api change needed to
// receive it (see BACKLOG.md for the follow-up item on actually routing
// it into paste_events server-side, which is out of scope here since both
// modules are frozen for this brief).
type outboundReport struct {
	AgentID     string       `json:"agent_id"`
	Hostname    string       `json:"hostname"`
	CollectedAt time.Time    `json:"collected_at"`
	PasteEvents []PasteEvent `json:"paste_events"`
}

// response is what RunHost writes back to the extension for every message.
type response struct {
	Status  string `json:"status"` // "ok" | "error"
	Message string `json:"message,omitempty"`
}

// Sender is the subset of collector.Sender's interface RunHost needs,
// letting tests inject a fake without a live collector -- mirrors this
// codebase's existing test-seam convention (eami-api's
// toolStoreOverride/toolDialOverride, eami-gateway's episodeWriteDB).
// *collector.Sender already satisfies this exactly.
type Sender interface {
	Send(ctx context.Context, report any) error
}

// readMessage reads one length-prefixed native message from r: a 4-byte
// little-endian uint32 byte count, then that many bytes of JSON.
func readMessage(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err // includes io.EOF/io.ErrUnexpectedEOF -- caller treats as shutdown
	}
	n := binary.LittleEndian.Uint32(lenBuf[:])
	if n > maxMessageBytes {
		return nil, fmt.Errorf("nativemsg: declared message length %d exceeds max %d bytes", n, maxMessageBytes)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("nativemsg: read message body: %w", err)
	}
	return buf, nil
}

// writeMessage writes v to w as one length-prefixed native message.
func writeMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("nativemsg: marshal response: %w", err)
	}
	if len(body) > maxMessageBytes {
		return fmt.Errorf("nativemsg: response %d bytes exceeds max %d bytes", len(body), maxMessageBytes)
	}
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(body)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("nativemsg: write length prefix: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("nativemsg: write body: %w", err)
	}
	return nil
}

// validate checks a PasteEventMessage and converts it to a PasteEvent.
func validate(m PasteEventMessage) (PasteEvent, error) {
	if m.Type != "paste_event" {
		return PasteEvent{}, fmt.Errorf("unknown message type %q", m.Type)
	}
	if m.DestinationDomain == "" {
		return PasteEvent{}, errors.New("destination_domain is required")
	}
	if m.OccurredAt == "" {
		return PasteEvent{}, errors.New("occurred_at is required")
	}
	if _, err := time.Parse(time.RFC3339, m.OccurredAt); err != nil {
		return PasteEvent{}, fmt.Errorf("occurred_at must be RFC3339: %w", err)
	}
	return PasteEvent{
		DestinationDomain: m.DestinationDomain,
		OccurredAt:        m.OccurredAt,
		ContentLength:     m.ContentLength,
		ContentHash:       m.ContentHash,
		OSUsername:        m.OSUsername,
	}, nil
}

// RunHost reads native-messaging-protocol messages from r until EOF (the
// browser closing the pipe -- normal shutdown, not an error) or a fatal
// write error, writing one response per message to w. agentID/hostname
// are the host's own known identity, attached to every outbound event.
//
// Malformed input never crashes this process: a bad length prefix or
// unparseable JSON produces an error response and the loop continues
// serving subsequent messages; a panic while handling any single message
// is recovered (handleOne), matching B-027's per-iteration panic-recovery
// discipline for background/IPC loops. Since this function is only ever
// invoked from a dedicated --native-messaging-host process instance (see
// cmd/agent/main.go), a panic here can never reach the poll-loop process.
func RunHost(ctx context.Context, r io.Reader, w io.Writer, sender Sender, agentID, hostname string, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	for {
		body, err := readMessage(r)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil // browser closed the pipe -- normal shutdown
			}
			log.Warn("nativemsg: read message failed", "err", err)
			if werr := writeMessage(w, response{Status: "error", Message: "malformed message"}); werr != nil {
				return fmt.Errorf("nativemsg: write error response: %w", werr)
			}
			continue
		}

		resp := handleOne(ctx, sender, agentID, hostname, body, log)
		if err := writeMessage(w, resp); err != nil {
			return fmt.Errorf("nativemsg: write response: %w", err)
		}
	}
}

// handleOne processes exactly one message. Recovers from any panic so a
// single bad/unexpected message can never crash the host process.
func handleOne(ctx context.Context, sender Sender, agentID, hostname string, body []byte, log *slog.Logger) (resp response) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("nativemsg: recovered panic handling message", "panic", r, "stack", string(debug.Stack()))
			resp = response{Status: "error", Message: "internal error"}
		}
	}()

	var msg PasteEventMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return response{Status: "error", Message: "invalid JSON"}
	}

	event, err := validate(msg)
	if err != nil {
		return response{Status: "error", Message: err.Error()}
	}

	report := outboundReport{
		AgentID:     agentID,
		Hostname:    hostname,
		CollectedAt: time.Now().UTC(),
		PasteEvents: []PasteEvent{event},
	}
	if err := sender.Send(ctx, report); err != nil {
		log.Warn("nativemsg: forwarding failed", "err", err)
		return response{Status: "error", Message: "forwarding failed"}
	}
	return response{Status: "ok"}
}
