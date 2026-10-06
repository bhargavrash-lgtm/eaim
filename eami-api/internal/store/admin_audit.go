package store

// Admin audit trail (B-269 Slice 0b, decision D2; the first slice of B-224).
//
// One append-only table, admin_audit_events (migration 000029), with its own
// hash chain PER ORG. Deliberately not audit_log: that chain is single-writer
// (the gateway's in-process mutex) and its hash doesn't cover a summary. See
// B-269_SLICE0B_PART_A_INVESTIGATION.md.
//
// What the chain proves, and what it doesn't (B0b-7):
//   - DETECTED: an edit to any column of any row, deletion of an interior
//     row (a seq gap and a broken prev_hash), reordering, and a row copied
//     from another org (org-specific genesis, org_id inside the hash).
//   - NOT DETECTED: deletion of the newest rows (the remaining chain is still
//     valid), deletion of every row, or a full recompute of the chain by
//     anyone with database write access. There is no secret key and no
//     external anchor. It is tamper-evident against edits, not tamper-proof
//     against a database administrator; the append-only triggers stop
//     application bugs, not a superuser (and the app role is one, B-299).
//
// Rows never carry values (the standing no-secrets rule, B0b-3): summaries
// hold field names, short value hashes, counts, closed-enum values, stable
// codes, IDs and version numbers only. Validation below enforces the shape,
// so a caller can't smuggle a value in by building the struct by hand.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrAdminAuditWrite marks a failure to record the audit event. The change it
// accompanied was rolled back with it (fail closed, B0b-4). Handlers map it
// to a fixed 500 code (api.writeAdminAuditFailure), never to its text.
var ErrAdminAuditWrite = errors.New("admin audit: could not record event")

// ErrAdminAuditInvalid marks an event rejected by validation before any
// database work: a programming error in the caller, never user input.
var ErrAdminAuditInvalid = errors.New("admin audit: invalid event")

// Action codes: <target_type>.<verb>. Only presets, assignment and enrollment
// keys are registered (Slice 0b scope); B-224 adds the rest from the reserved
// list in the Part A report, without a migration (the database checks only
// the format).
const (
	AdminAuditPresetCreated      = "preset.created"
	AdminAuditPresetDraftUpdated = "preset.draft_updated"
	AdminAuditPresetPublished    = "preset.published"
	AdminAuditPresetReverted     = "preset.reverted"
	AdminAuditPresetArchived     = "preset.archived"
	AdminAuditPresetUnarchived   = "preset.unarchived"
	// One event for a bulk adopt, carrying the count (design §7).
	AdminAuditPresetBulkAssigned = "preset.bulk_assigned"

	AdminAuditEndpointPresetAssigned   = "endpoint.preset_assigned"
	AdminAuditEndpointPresetUnassigned = "endpoint.preset_unassigned"

	AdminAuditEnrollmentKeyCreated   = "enrollment_key.created"
	AdminAuditEnrollmentKeyRevoked   = "enrollment_key.revoked"
	AdminAuditEnrollmentKeyExchanged = "enrollment_key.exchanged"
)

type adminAuditFieldKind int

const (
	fieldValue   adminAuditFieldKind = iota // one hashed value
	fieldList                               // a list of strings, hashed per item
	fieldEnum                               // a set drawn from a closed list, stored as names
	fieldChanged                            // admin-typed text: only "this field changed", no hash
)

type adminAuditField struct {
	kind adminAuditFieldKind
	enum []string // fieldEnum only
}

// presetFields are the preset content fields (design §2). name and
// description are admin-typed text (B0b-3): only the fact that they changed
// is recorded, never a value or a hash (a short unsalted hash of guessable
// text would confirm a guess).
var presetFields = map[string]adminAuditField{
	"name":                  {kind: fieldChanged},
	"description":           {kind: fieldChanged},
	"scanners":              {kind: fieldEnum, enum: AllScanners},
	"scan_interval_seconds": {kind: fieldValue},
	"model_scan_paths":      {kind: fieldList},
	"model_file_size_mb":    {kind: fieldValue},
	"max_report_size_bytes": {kind: fieldValue},
}

var enrollmentKeyFields = map[string]adminAuditField{
	"expires_at": {kind: fieldValue},
	"max_uses":   {kind: fieldValue},
}

type adminAuditAction struct {
	targetType string
	fields     map[string]adminAuditField
}

var adminAuditActions = map[string]adminAuditAction{
	AdminAuditPresetCreated:      {"preset", presetFields},
	AdminAuditPresetDraftUpdated: {"preset", presetFields},
	AdminAuditPresetPublished:    {"preset", presetFields},
	AdminAuditPresetReverted:     {"preset", presetFields},
	AdminAuditPresetArchived:     {"preset", nil},
	AdminAuditPresetUnarchived:   {"preset", nil},
	AdminAuditPresetBulkAssigned: {"preset", nil},

	AdminAuditEndpointPresetAssigned:   {"endpoint", nil},
	AdminAuditEndpointPresetUnassigned: {"endpoint", nil},

	AdminAuditEnrollmentKeyCreated:   {"enrollment_key", enrollmentKeyFields},
	AdminAuditEnrollmentKeyRevoked:   {"enrollment_key", nil},
	AdminAuditEnrollmentKeyExchanged: {"enrollment_key", nil},
}

// IsAdminAuditAction reports whether code is a registered action. The read
// API validates its action filter with it.
func IsAdminAuditAction(code string) bool {
	_, ok := adminAuditActions[code]
	return ok
}

// IsAdminAuditTargetType reports whether t is the target type of any
// registered action.
func IsAdminAuditTargetType(t string) bool {
	for _, a := range adminAuditActions {
		if a.targetType == t {
			return true
		}
	}
	return false
}

// adminAuditCodes are the stable codes a summary may carry: the config
// limit and path-rule codes (agent_config_limits.go).
var adminAuditCodes = map[string]bool{
	CodeIntervalOutOfRange: true, CodeReportSizeOutOfRange: true, CodeModelSizeOutOfRange: true,
	CodeTooManyPaths: true, CodePathEmpty: true, CodePathTooLong: true, CodePathInvalidChars: true,
	CodePathNetwork: true, CodePathNotAbsolute: true, CodePathRoot: true, CodePathNotNormalized: true,
	CodePathProfileParent: true, CodeTooManyScanners: true, CodeUnknownScanner: true,
}

// adminAuditRoles are the roles an actor may hold (users.role plus the
// platform tier, and 'system' for system actors).
var adminAuditRoles = map[string]bool{
	"admin": true, "operator": true, "approver": true, "viewer": true, "platform_admin": true,
}

const (
	AdminAuditActorUser    = "user"
	AdminAuditActorSystem  = "system"
	AdminAuditSourceAPI    = "api"
	AdminAuditSourceSystem = "system"

	adminAuditSummaryVersion = 1
	adminAuditMaxSummary     = 8192
	adminAuditHashVersion    = "eami-admin-audit-v1"
	adminAuditGenesisSeed    = "eami-admin-audit-genesis-v1"
	// adminAuditLockTimeout bounds the wait for the org's chain lock, so a
	// stuck writer turns into a fast ErrAdminAuditWrite, never a hang (B0b-4).
	adminAuditLockTimeout = "5s"
)

var (
	adminAuditValueHashRe = regexp.MustCompile(`^sha256:[0-9a-f]{16}$`)
	adminAuditEnumNameRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	// The same pattern the database CHECK enforces. chi's RequestID accepts
	// a client-supplied X-Request-Id, so anything else is dropped (B0b-5).
	adminAuditRequestIDRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,64}$`)
)

// AdminAuditActor is who made the change. Role is a snapshot from the JWT at
// write time. No email or name is stored (the read API joins users).
type AdminAuditActor struct {
	Type   string    // AdminAuditActorUser or AdminAuditActorSystem
	UserID uuid.UUID // required for a user; uuid.Nil for system
	Role   string    // a user's role; ignored for system (stored as "system")
}

// AdminAuditChange is one changed field. Build it with AdminAuditValueChange,
// AdminAuditListChange or AdminAuditEnumChange; Append re-validates it.
type AdminAuditChange struct {
	Field       string   `json:"field"`
	Old         string   `json:"old,omitempty"`
	New         string   `json:"new,omitempty"`
	Added       *int     `json:"added,omitempty"`
	Removed     *int     `json:"removed,omitempty"`
	ItemsOld    []string `json:"items_old,omitempty"`
	ItemsNew    []string `json:"items_new,omitempty"`
	EnumAdded   []string `json:"enum_added,omitempty"`
	EnumRemoved []string `json:"enum_removed,omitempty"`
}

// AdminAuditRefs are typed references: IDs and numbers only.
type AdminAuditRefs struct {
	PresetID        *uuid.UUID `json:"preset_id,omitempty"`
	PresetVersion   *int32     `json:"preset_version,omitempty"`
	FromVersion     *int32     `json:"from_version,omitempty"`
	EnrollmentKeyID *uuid.UUID `json:"enrollment_key_id,omitempty"`
	EndpointID      *uuid.UUID `json:"endpoint_id,omitempty"`
	Count           *int32     `json:"count,omitempty"`
}

// AdminAuditSummary is the change summary stored as canonical JSON.
type AdminAuditSummary struct {
	Changes []AdminAuditChange `json:"changes,omitempty"`
	Codes   []string           `json:"codes,omitempty"`
	Refs    *AdminAuditRefs    `json:"refs,omitempty"`
}

// AdminAuditEvent is what a caller appends.
type AdminAuditEvent struct {
	OrgID     uuid.UUID
	Actor     AdminAuditActor
	Action    string
	TargetID  uuid.UUID
	Summary   AdminAuditSummary
	Source    string // AdminAuditSourceAPI or AdminAuditSourceSystem
	RequestID string // kept only if it matches adminAuditRequestIDRe
}

// AdminAuditEventRow is a stored row.
type AdminAuditEventRow struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Seq         int64
	OccurredAt  time.Time
	ActorType   string
	ActorUserID *uuid.UUID
	ActorRole   string
	ActorEmail  *string // list only: joined from users in the same org
	Action      string
	TargetType  string
	TargetID    string
	Summary     string
	Source      string
	RequestID   *string
	PrevHash    string
	Hash        string
}

// ── Summary constructors ─────────────────────────────────────────────────────

// adminAuditHashValue is "sha256:" plus 16 hex characters over the value's
// JSON. It identifies a change; it is not confidentiality (a guessable value
// stays guessable), which is why values never enter a row at all.
func adminAuditHashValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprintf("%v", v))
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

// AdminAuditValueChange records a scalar field as old and new hashes. A nil
// side (creation, removal) is omitted.
func AdminAuditValueChange(field string, oldV, newV any) AdminAuditChange {
	c := AdminAuditChange{Field: field}
	if oldV != nil {
		c.Old = adminAuditHashValue(oldV)
	}
	if newV != nil {
		c.New = adminAuditHashValue(newV)
	}
	return c
}

// AdminAuditFieldChanged records only that an admin-typed text field (a name
// or description) changed: no value and no hash.
func AdminAuditFieldChanged(field string) AdminAuditChange {
	return AdminAuditChange{Field: field}
}

// AdminAuditListChange records a string list (for example scan paths) as
// whole-list hashes, sorted per-item hashes and added/removed counts.
func AdminAuditListChange(field string, oldL, newL []string) AdminAuditChange {
	oldSet, newSet := toSet(oldL), toSet(newL)
	added, removed := 0, 0
	for k := range newSet {
		if !oldSet[k] {
			added++
		}
	}
	for k := range oldSet {
		if !newSet[k] {
			removed++
		}
	}
	return AdminAuditChange{
		Field:    field,
		Old:      adminAuditHashValue(sortedCopy(oldL)),
		New:      adminAuditHashValue(sortedCopy(newL)),
		Added:    &added,
		Removed:  &removed,
		ItemsOld: hashItems(oldL),
		ItemsNew: hashItems(newL),
	}
}

// AdminAuditEnumChange records a set drawn from a closed list (for example
// scanner names) by name: the names are fixed identifiers, not values.
func AdminAuditEnumChange(field string, oldL, newL []string) AdminAuditChange {
	oldSet, newSet := toSet(oldL), toSet(newL)
	added, removed := []string{}, []string{}
	for k := range newSet {
		if !oldSet[k] {
			added = append(added, k)
		}
	}
	for k := range oldSet {
		if !newSet[k] {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	na, nr := len(added), len(removed)
	return AdminAuditChange{Field: field, Added: &na, Removed: &nr, EnumAdded: added, EnumRemoved: removed}
}

func toSet(l []string) map[string]bool {
	m := make(map[string]bool, len(l))
	for _, s := range l {
		m[s] = true
	}
	return m
}

func sortedCopy(l []string) []string {
	c := append([]string{}, l...)
	sort.Strings(c)
	return c
}

func hashItems(l []string) []string {
	if len(l) == 0 {
		return nil
	}
	out := make([]string, 0, len(l))
	for _, s := range l {
		out = append(out, adminAuditHashValue(s))
	}
	sort.Strings(out)
	return out
}

// ── Validation ───────────────────────────────────────────────────────────────

func invalid(reason string) error { return fmt.Errorf("%w: %s", ErrAdminAuditInvalid, reason) }

func validateAdminAuditEvent(ev AdminAuditEvent) (adminAuditAction, error) {
	act, ok := adminAuditActions[ev.Action]
	if !ok {
		return act, invalid("unregistered action")
	}
	if ev.OrgID == uuid.Nil {
		return act, invalid("missing org")
	}
	if ev.TargetID == uuid.Nil {
		return act, invalid("missing target")
	}
	switch ev.Actor.Type {
	case AdminAuditActorUser:
		if ev.Actor.UserID == uuid.Nil || !adminAuditRoles[ev.Actor.Role] {
			return act, invalid("bad user actor")
		}
	case AdminAuditActorSystem:
		if ev.Actor.UserID != uuid.Nil {
			return act, invalid("system actor with a user")
		}
	default:
		return act, invalid("bad actor type")
	}
	if ev.Source != AdminAuditSourceAPI && ev.Source != AdminAuditSourceSystem {
		return act, invalid("bad source")
	}
	seen := map[string]bool{}
	for _, c := range ev.Summary.Changes {
		f, ok := act.fields[c.Field]
		if !ok || seen[c.Field] {
			return act, invalid("field not allowed for action")
		}
		seen[c.Field] = true
		if err := validateChange(c, f); err != nil {
			return act, err
		}
	}
	for _, code := range ev.Summary.Codes {
		if !adminAuditCodes[code] {
			return act, invalid("unknown code")
		}
	}
	return act, nil
}

func validateChange(c AdminAuditChange, f adminAuditField) error {
	isHash := func(s string) bool { return s == "" || adminAuditValueHashRe.MatchString(s) }
	if !isHash(c.Old) || !isHash(c.New) {
		return invalid("value is not a hash")
	}
	for _, h := range append(append([]string{}, c.ItemsOld...), c.ItemsNew...) {
		if !adminAuditValueHashRe.MatchString(h) {
			return invalid("item is not a hash")
		}
	}
	switch f.kind {
	case fieldChanged:
		if c.Old != "" || c.New != "" || c.Added != nil || c.Removed != nil ||
			len(c.ItemsOld)+len(c.ItemsNew)+len(c.EnumAdded)+len(c.EnumRemoved) > 0 {
			return invalid("data on a changed-only field")
		}
	case fieldValue:
		if c.Added != nil || c.Removed != nil || len(c.ItemsOld)+len(c.ItemsNew)+len(c.EnumAdded)+len(c.EnumRemoved) > 0 {
			return invalid("list data on a value field")
		}
	case fieldList:
		if len(c.EnumAdded)+len(c.EnumRemoved) > 0 {
			return invalid("enum data on a list field")
		}
	case fieldEnum:
		if c.Old != "" || c.New != "" || len(c.ItemsOld)+len(c.ItemsNew) > 0 {
			return invalid("hash data on an enum field")
		}
		// Added names must be in the current closed list. A removed name may
		// be one since retired from it, so it only has to look like a name;
		// otherwise retiring a scanner would block every later preset edit.
		allowed := toSet(f.enum)
		for _, v := range c.EnumAdded {
			if !allowed[v] {
				return invalid("value outside the closed list")
			}
		}
		for _, v := range c.EnumRemoved {
			if !adminAuditEnumNameRe.MatchString(v) {
				return invalid("value is not a name")
			}
		}
	}
	return nil
}

// ── Hashing ──────────────────────────────────────────────────────────────────

// AdminAuditGenesis is the chain's starting prev_hash for one org. It differs
// per org, so a row can't be spliced from one org's chain into another's.
func AdminAuditGenesis(orgID uuid.UUID) string {
	sum := sha256.Sum256([]byte(adminAuditGenesisSeed + orgID.String()))
	return hex.EncodeToString(sum[:])
}

const adminAuditTimeLayout = "2006-01-02T15:04:05.000000Z"

// adminAuditRowHash is SHA-256 over a length-prefixed encoding of every
// column except hash, so no two different rows encode the same bytes.
func adminAuditRowHash(r AdminAuditEventRow) string {
	actorUser, reqID := "", ""
	if r.ActorUserID != nil {
		actorUser = r.ActorUserID.String()
	}
	if r.RequestID != nil {
		reqID = *r.RequestID
	}
	parts := []string{
		adminAuditHashVersion, r.PrevHash, r.ID.String(), r.OrgID.String(),
		strconv.FormatInt(r.Seq, 10), r.OccurredAt.UTC().Format(adminAuditTimeLayout),
		r.ActorType, actorUser, r.ActorRole, r.Action, r.TargetType, r.TargetID,
		r.Summary, r.Source, reqID,
	}
	h := sha256.New()
	var n [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ── Write ────────────────────────────────────────────────────────────────────

// AppendAdminAuditEvent appends ev to its org's chain inside tx, the change's
// own transaction: if the change rolls back, so does the event. It takes a
// pgx.Tx (not *Queries) so it can't be called outside a transaction.
//
// One org per transaction: appends for two orgs in one transaction would take
// two chain locks. The transaction must be READ COMMITTED (checked).
//
// Call it LAST, just before commit. It takes the org's chain lock
// (pg_advisory_xact_lock, held until commit or rollback) with a bounded
// wait, reads the committed head (READ COMMITTED: a fresh snapshot after the
// lock), and inserts head+1. UNIQUE (org_id, seq) is the backstop for a
// writer that skips the lock.
//
// The caller must already have proven the target belongs to ev.OrgID (the
// same org-scoped check that guards the change itself, the B-232 pattern).
// Every database failure is wrapped as ErrAdminAuditWrite.
func AppendAdminAuditEvent(ctx context.Context, tx pgx.Tx, ev AdminAuditEvent) (AdminAuditEventRow, error) {
	act, err := validateAdminAuditEvent(ev)
	if err != nil {
		return AdminAuditEventRow{}, err
	}
	summary := ev.Summary
	summaryJSON, err := json.Marshal(struct {
		V int `json:"v"`
		AdminAuditSummary
	}{adminAuditSummaryVersion, summary})
	if err != nil {
		return AdminAuditEventRow{}, invalid("summary does not encode")
	}
	if len(summaryJSON) > adminAuditMaxSummary {
		return AdminAuditEventRow{}, invalid("summary too large")
	}

	row := AdminAuditEventRow{
		ID:         uuid.New(),
		OrgID:      ev.OrgID,
		OccurredAt: time.Now().UTC().Truncate(time.Microsecond),
		ActorType:  ev.Actor.Type,
		Action:     ev.Action,
		TargetType: act.targetType,
		TargetID:   ev.TargetID.String(),
		Summary:    string(summaryJSON),
		Source:     ev.Source,
	}
	if ev.Actor.Type == AdminAuditActorUser {
		uid := ev.Actor.UserID
		row.ActorUserID = &uid
		row.ActorRole = ev.Actor.Role
	} else {
		row.ActorRole = "system"
	}
	if adminAuditRequestIDRe.MatchString(ev.RequestID) {
		rid := ev.RequestID
		row.RequestID = &rid
	}

	wrap := func(step string, err error) error {
		return fmt.Errorf("%w (%s): %v", ErrAdminAuditWrite, step, err)
	}
	// The head read after the lock relies on READ COMMITTED's per-statement
	// snapshot. Under REPEATABLE READ or SERIALIZABLE it would be stale and
	// every concurrent append would fail on UNIQUE (org_id, seq), so refuse
	// up front: a programming error, not a runtime condition.
	var iso string
	if err := tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&iso); err != nil {
		return AdminAuditEventRow{}, wrap("isolation", err)
	}
	if iso != "read committed" {
		return AdminAuditEventRow{}, invalid("transaction must be READ COMMITTED")
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '"+adminAuditLockTimeout+"'"); err != nil {
		return AdminAuditEventRow{}, wrap("lock_timeout", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('admin_audit:' || $1::text, 0))`, ev.OrgID); err != nil {
		return AdminAuditEventRow{}, wrap("lock", err)
	}
	var headSeq int64
	var headHash string
	err = tx.QueryRow(ctx,
		`SELECT seq, hash FROM admin_audit_events WHERE org_id = $1 ORDER BY seq DESC LIMIT 1`, ev.OrgID,
	).Scan(&headSeq, &headHash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		headSeq, headHash = 0, AdminAuditGenesis(ev.OrgID)
	case err != nil:
		return AdminAuditEventRow{}, wrap("head", err)
	}
	row.Seq = headSeq + 1
	row.PrevHash = headHash
	row.Hash = adminAuditRowHash(row)

	if _, err := tx.Exec(ctx, `
		INSERT INTO admin_audit_events
		  (id, org_id, seq, occurred_at, actor_type, actor_user_id, actor_role, action,
		   target_type, target_id, summary, source, request_id, prev_hash, hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		row.ID, row.OrgID, row.Seq, row.OccurredAt, row.ActorType, row.ActorUserID, row.ActorRole,
		row.Action, row.TargetType, row.TargetID, row.Summary, row.Source, row.RequestID,
		row.PrevHash, row.Hash,
	); err != nil {
		return AdminAuditEventRow{}, wrap("insert", err)
	}
	return row, nil
}

// RunAudited runs change and its audit event in one transaction and commits
// both or neither (fail closed, B0b-4). change does its writes on tx and
// returns the event describing them; an error from change is returned as-is.
// An audit failure is ErrAdminAuditWrite; nothing is committed.
//
// orgID pins the chain: handlers pass the caller's JWT org (uc.OrgID), and an
// event naming any other org is refused, so a handler that built the event
// from a looked-up row can't write into another org's chain.
func (q *Queries) RunAudited(ctx context.Context, orgID uuid.UUID, change func(tx pgx.Tx) (AdminAuditEvent, error)) (AdminAuditEventRow, error) {
	tx, err := q.Begin(ctx)
	if err != nil {
		return AdminAuditEventRow{}, err
	}
	defer tx.Rollback(ctx) // no-op after a successful commit

	ev, err := change(tx)
	if err != nil {
		return AdminAuditEventRow{}, err
	}
	if orgID == uuid.Nil || ev.OrgID != orgID {
		return AdminAuditEventRow{}, invalid("event org differs from the caller's org")
	}
	row, err := AppendAdminAuditEvent(ctx, tx, ev)
	if err != nil {
		return AdminAuditEventRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminAuditEventRow{}, fmt.Errorf("%w (commit): %v", ErrAdminAuditWrite, err)
	}
	return row, nil
}

// ── Read ─────────────────────────────────────────────────────────────────────

// ListAdminAuditEventsParams are the list filters. OrgID always comes from
// the caller's JWT; every other filter applies within that org.
type ListAdminAuditEventsParams struct {
	OrgID       uuid.UUID
	Action      *string
	TargetType  *string
	TargetID    *string
	ActorUserID *uuid.UUID
	From        *time.Time
	To          *time.Time
	Ascending   bool
	Limit       int32
	Offset      int32
}

const adminAuditFilterSQL = `
	WHERE e.org_id = $1
	  AND ($2::text IS NULL OR e.action = $2)
	  AND ($3::text IS NULL OR e.target_type = $3)
	  AND ($4::text IS NULL OR e.target_id = $4)
	  AND ($5::uuid IS NULL OR e.actor_user_id = $5)
	  AND ($6::timestamptz IS NULL OR e.occurred_at >= $6)
	  AND ($7::timestamptz IS NULL OR e.occurred_at < $7)`

// ListAdminAuditEvents returns one page of the org's events, ordered by seq.
func (q *Queries) ListAdminAuditEvents(ctx context.Context, p ListAdminAuditEventsParams) ([]AdminAuditEventRow, error) {
	order := "DESC"
	if p.Ascending {
		order = "ASC"
	}
	rows, err := q.db.Query(ctx, `
		SELECT e.id, e.org_id, e.seq, e.occurred_at, e.actor_type, e.actor_user_id, e.actor_role,
		       u.email, e.action, e.target_type, e.target_id, e.summary, e.source, e.request_id,
		       e.prev_hash, e.hash
		FROM admin_audit_events e
		LEFT JOIN users u ON u.id = e.actor_user_id AND u.org_id = e.org_id`+
		adminAuditFilterSQL+`
		ORDER BY e.seq `+order+`
		LIMIT $8 OFFSET $9`,
		p.OrgID, p.Action, p.TargetType, p.TargetID, p.ActorUserID, p.From, p.To, p.Limit, p.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminAuditEventRow{}
	for rows.Next() {
		var r AdminAuditEventRow
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Seq, &r.OccurredAt, &r.ActorType, &r.ActorUserID,
			&r.ActorRole, &r.ActorEmail, &r.Action, &r.TargetType, &r.TargetID, &r.Summary,
			&r.Source, &r.RequestID, &r.PrevHash, &r.Hash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountAdminAuditEvents counts the org's events matching the same filters.
func (q *Queries) CountAdminAuditEvents(ctx context.Context, p ListAdminAuditEventsParams) (int64, error) {
	var n int64
	err := q.db.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events e`+adminAuditFilterSQL,
		p.OrgID, p.Action, p.TargetType, p.TargetID, p.ActorUserID, p.From, p.To).Scan(&n)
	return n, err
}

// Stable reasons a chain fails verification.
const (
	AdminAuditBadSeq      = "seq_gap"
	AdminAuditBadPrevHash = "prev_hash_mismatch"
	AdminAuditBadHash     = "hash_mismatch"
)

// AdminAuditVerifyResult reports what VerifyAdminAuditChain checked. Valid
// means no edit, interior deletion, reordering or splice was found; it can't
// mean the newest rows weren't removed or the chain wasn't recomputed.
type AdminAuditVerifyResult struct {
	Valid       bool
	Checked     int64
	HeadSeq     int64
	HeadHash    string
	FirstBadSeq *int64
	Reason      string
}

// adminAuditVerifyTimeout bounds one verify walk (security review M2).
const adminAuditVerifyTimeout = "30s"

// VerifyAdminAuditChain re-walks one org's chain from its genesis. It reads
// only that org's rows, in a read-only transaction with a statement timeout.
func (q *Queries) VerifyAdminAuditChain(ctx context.Context, orgID uuid.UUID) (AdminAuditVerifyResult, error) {
	tx, err := q.Begin(ctx)
	if err != nil {
		return AdminAuditVerifyResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
		return AdminAuditVerifyResult{}, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '"+adminAuditVerifyTimeout+"'"); err != nil {
		return AdminAuditVerifyResult{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, org_id, seq, occurred_at, actor_type, actor_user_id, actor_role, action,
		       target_type, target_id, summary, source, request_id, prev_hash, hash
		FROM admin_audit_events WHERE org_id = $1 ORDER BY seq ASC`, orgID)
	if err != nil {
		return AdminAuditVerifyResult{}, err
	}
	defer rows.Close()

	res := AdminAuditVerifyResult{Valid: true, HeadHash: AdminAuditGenesis(orgID)}
	expectSeq, prev := int64(1), res.HeadHash
	fail := func(seq int64, reason string) {
		if res.Valid {
			s := seq
			res.Valid, res.FirstBadSeq, res.Reason = false, &s, reason
		}
	}
	for rows.Next() {
		var r AdminAuditEventRow
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Seq, &r.OccurredAt, &r.ActorType, &r.ActorUserID,
			&r.ActorRole, &r.Action, &r.TargetType, &r.TargetID, &r.Summary, &r.Source,
			&r.RequestID, &r.PrevHash, &r.Hash); err != nil {
			return AdminAuditVerifyResult{}, err
		}
		res.Checked++
		switch {
		case r.Seq != expectSeq:
			fail(r.Seq, AdminAuditBadSeq)
		case r.PrevHash != prev:
			fail(r.Seq, AdminAuditBadPrevHash)
		case adminAuditRowHash(r) != r.Hash:
			fail(r.Seq, AdminAuditBadHash)
		}
		expectSeq, prev = r.Seq+1, r.Hash
		res.HeadSeq, res.HeadHash = r.Seq, r.Hash
	}
	return res, rows.Err()
}
