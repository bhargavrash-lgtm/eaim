// Package episode records MCP tool call episodes to the episodes table in Postgres.
//
// Embedding strategy: each episode stores a 1536-dim placeholder vector derived
// deterministically from a SHA-256 hash of the task string. This keeps the
// pgvector HNSW index populated and allows basic cosine distance queries.
// Swap for real LLM embeddings once ADR-009 (embedding endpoint) resolves.
package episode

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const embeddingDims = 1536

// Step is one tool call recorded within an episode.
type Step struct {
	ToolName  string          `json:"tool_name"`
	Action    string          `json:"action"`
	Params    map[string]any  `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Decision  string          `json:"decision"` // "allowed" | "denied" | "escalated"
	Timestamp time.Time       `json:"timestamp"`
}

// Recorder writes episodes to Postgres.
type Recorder struct {
	pool *pgxpool.Pool
}

// New returns a Recorder backed by pool.
func New(pool *pgxpool.Pool) *Recorder {
	return &Recorder{pool: pool}
}

// Record writes one episode to the DB. Errors are logged, never returned —
// call via goroutine so the MCP response path is never blocked.
func (r *Recorder) Record(
	ctx context.Context,
	orgID, agentID, agentName string,
	steps []Step,
	outcome string,
) {
	if len(steps) == 0 {
		return
	}
	// Use first step's tool+action as the episode task description.
	task := steps[0].ToolName + "/" + steps[0].Action

	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		slog.Warn("episode: marshal steps", "err", err)
		return
	}

	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		slog.Warn("episode: invalid org_id", "org_id", orgID, "err", err)
		return
	}

	var agentUUIDPtr *uuid.UUID
	if id, parseErr := uuid.Parse(agentID); parseErr == nil {
		agentUUIDPtr = &id
	}

	const sqlInsert = `
		INSERT INTO episodes (org_id, agent_id, agent_name, task, steps, outcome, embedding)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7::vector)
	`
	if _, execErr := r.pool.Exec(ctx, sqlInsert,
		orgUUID, agentUUIDPtr, agentName, task,
		stepsJSON, outcome,
		formatEmbedding(placeholderEmbedding(task)),
	); execErr != nil {
		slog.Warn("episode: db write failed",
			"agent", agentName, "task", task, "err", execErr)
	}
}

// placeholderEmbedding returns a deterministic L2-normalised embeddingDims-dim
// vector derived from a SHA-256 hash of s. Hash bytes are expanded cyclically.
func placeholderEmbedding(s string) []float32 {
	h := sha256.Sum256([]byte(s))
	vec := make([]float32, embeddingDims)
	for i := range vec {
		a := h[(i*2)%32]
		b := h[(i*2+1)%32]
		u16 := binary.LittleEndian.Uint16([]byte{a, b})
		vec[i] = float32(u16)/32767.5 - 1.0
	}
	// L2-normalise so cosine similarity is well-defined.
	var sumsq float64
	for _, v := range vec {
		sumsq += float64(v) * float64(v)
	}
	if norm := float32(math.Sqrt(sumsq)); norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}

// formatEmbedding renders []float32 as the pgvector text literal "[f,f,...]".
func formatEmbedding(v []float32) string {
	b := make([]byte, 0, len(v)*10+2)
	b = append(b, '[')
	for i, f := range v {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendFloat(b, float64(f), 'f', 6, 32)
	}
	b = append(b, ']')
	return string(b)
}
