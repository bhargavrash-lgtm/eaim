package remoteconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StateFileName is the last-known-good remote config, kept apart from the
// admin's YAML: the agent never rewrites that file (B-278 #4).
const StateFileName = "remote-config.json"

// Identity binds persisted config to the agent and collector it came from.
// A file written for another identity (the agent re-pointed at another
// collector or org) is never applied.
type Identity struct {
	AgentID          string
	CollectorURLHash string
}

// NewIdentity hashes the collector URL so the state file doesn't record it.
func NewIdentity(agentID, collectorURL string) Identity {
	sum := sha256.Sum256([]byte(collectorURL))
	return Identity{AgentID: agentID, CollectorURLHash: hex.EncodeToString(sum[:])}
}

type stateConfig struct {
	ScanIntervalSeconds int64    `json:"scan_interval_seconds"`
	EnabledScanners     []string `json:"enabled_scanners"`
	ModelScanPaths      []string `json:"model_scan_paths"`
	ModelFileSizeMB     int64    `json:"model_file_size_mb"`
	MaxReportSizeBytes  int64    `json:"max_report_size_bytes"`
}

type stateFile struct {
	Format             int         `json:"format"`
	AgentID            string      `json:"agent_id"`
	CollectorURLSHA256 string      `json:"collector_url_sha256"`
	ConfigVersion      string      `json:"config_version"`
	FetchedAt          time.Time   `json:"fetched_at"`
	Config             stateConfig `json:"config"`
}

const stateFormat = 1

// errAbsent means there is no state file: not an error, nothing to report.
var errAbsent = errors.New("no state file")

// Store reads and writes the state file in Dir. Base is the trusted,
// OS-owned directory Dir sits under (%ProgramData%, /var/lib, ...): every
// directory from Base down to Dir is checked on each use, not just the last
// one, because a standard user can create folders under %ProgramData% and
// a squatted or junctioned parent would redirect the agent's file
// operations (security review H-1).
type Store struct {
	Dir  string
	Base string
}

// DefaultStore is the per-OS store, or nil where the agent has none.
func DefaultStore() *Store {
	dir, base := DefaultDir(), DefaultBase()
	if dir == "" || base == "" {
		return nil
	}
	return &Store{Dir: dir, Base: base}
}

func (s Store) path() string { return filepath.Join(s.Dir, StateFileName) }

// chain lists the directories from just below Base down to Dir, outermost
// first. Without a usable Base it is just Dir.
func (s Store) chain() []string {
	rel, err := filepath.Rel(s.Base, s.Dir)
	if s.Base == "" || err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{s.Dir}
	}
	var out []string
	cur := s.Base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		out = append(out, cur)
	}
	return out
}

// checkChain verifies every directory in the chain. os.ErrNotExist means
// the store hasn't been created yet.
func (s Store) checkChain() error {
	for _, d := range s.chain() {
		if err := checkTrusted(d, true); err != nil {
			return err
		}
	}
	return nil
}

// Load returns the persisted config and its version, or a reason code.
// Reason "" with ok=false means no file exists. Checks run in a fixed
// order: trust (owner and permissions), size, strict decode, hash, bounds,
// identity. Any failure means the file is ignored, never partly applied.
func (s Store) Load(id Identity) (cfg Config, version, reason string, ok bool, detail error) {
	if err := s.checkChain(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, "", "", false, nil
		}
		return Config{}, "", ReasonStateUntrusted, false, err
	}
	// Every directory down to Dir is controlled by trusted identities only,
	// so nobody else can swap the file between this check and the open.
	p := s.path()
	if err := checkTrusted(p, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, "", "", false, nil
		}
		return Config{}, "", ReasonStateUntrusted, false, err
	}
	f, err := os.Open(p)
	if err != nil {
		return Config{}, "", ReasonStateCorrupt, false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxResponseBytes+1))
	if err != nil || len(raw) > MaxResponseBytes {
		return Config{}, "", ReasonStateCorrupt, false, fmt.Errorf("read state: %v (len %d)", err, len(raw))
	}
	var st stateFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil || st.Format != stateFormat {
		return Config{}, "", ReasonStateCorrupt, false, fmt.Errorf("decode state: %v (format %d)", err, st.Format)
	}
	c := Config{
		ScanIntervalSeconds: st.Config.ScanIntervalSeconds,
		EnabledScanners:     append([]string{}, st.Config.EnabledScanners...),
		ModelScanPaths:      append([]string{}, st.Config.ModelScanPaths...),
		ModelFileSizeMB:     st.Config.ModelFileSizeMB,
		MaxReportSizeBytes:  st.Config.MaxReportSizeBytes,
	}
	if c.Version() != st.ConfigVersion {
		return Config{}, "", ReasonStateCorrupt, false, errors.New("state hash mismatch")
	}
	if r := Validate(c); r != "" {
		return Config{}, "", ReasonStateInvalid, false, fmt.Errorf("state out of bounds: %s", r)
	}
	if st.AgentID != id.AgentID || st.CollectorURLSHA256 != id.CollectorURLHash {
		return Config{}, "", ReasonStateStale, false, errors.New("state written for another agent identity")
	}
	return c, st.ConfigVersion, "", true, nil
}

// Save atomically replaces the state file: secure the directory, write a
// temp file in it, fsync, rename. A torn write can only ever leave the old
// file or a file that fails Load's checks.
func (s Store) Save(id Identity, c Config, version string, now time.Time) error {
	for _, d := range s.chain() {
		if err := ensureSecureDir(d); err != nil {
			return err
		}
	}
	st := stateFile{
		Format: stateFormat, AgentID: id.AgentID, CollectorURLSHA256: id.CollectorURLHash,
		ConfigVersion: version, FetchedAt: now.UTC(),
		Config: stateConfig{
			ScanIntervalSeconds: c.ScanIntervalSeconds,
			EnabledScanners:     append([]string{}, c.EnabledScanners...),
			ModelScanPaths:      append([]string{}, c.ModelScanPaths...),
			ModelFileSizeMB:     c.ModelFileSizeMB,
			MaxReportSizeBytes:  c.MaxReportSizeBytes,
		},
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".remote-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := restrictFile(tmp); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path())
}

// Delete removes the state file (decision D-c). A missing file is fine; a
// chain that isn't trusted is never followed.
func (s Store) Delete() error {
	if err := s.checkChain(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.Remove(s.path()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
