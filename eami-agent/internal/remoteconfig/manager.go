package remoteconfig

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/eami/agent/internal/config"
)

// Config sources reported with every scan (config_source).
const (
	SourceRemote    = "remote"    // fetched and applied during this run
	SourcePersisted = "persisted" // last-known-good from the state file; no fetch has succeeded yet this run
	SourceLocal     = "local"     // no remote config applies; the YAML file was read
	SourceDefaults  = "defaults"  // no remote config applies and there is no YAML file
)

// FetchTimeout caps each config fetch so it never holds up a scan for
// longer, including the first scan after a restart.
const FetchTimeout = 10 * time.Second

// FetchResult is one poll of the config endpoint, as the sender saw it.
type FetchResult struct {
	Status   int    // HTTP status; 0 when the request itself failed
	Body     []byte // capped at MaxResponseBytes
	TooLarge bool   // the body exceeded MaxResponseBytes
	Err      error  // transport error, for the local log only
}

// Status is what each report says about the config it ran with.
type Status struct {
	Version       string // config_version: "" unless a versioned config is in force
	Source        string // config_source
	Error         string // config_error: the last rejection's reason code, or ""
	MaxReportSize int64  // bytes; 0 means no cap (no versioned remote config)
}

// Manager owns the agent's effective config. Only the scan loop calls it,
// so it needs no locking; each scan gets its own Snapshot, so a scanner
// still running from an earlier cycle (B-281) never sees a later change.
type Manager struct {
	log   *slog.Logger
	base  *config.Config // local YAML or built-in defaults; never mutated
	store *Store         // nil when persistence is off
	id    Identity
	now   func() time.Time

	cur            Config
	haveRemote     bool
	versioned      bool
	version        string
	source         string
	lastErr        string
	persisted      string // version currently in the state file
	consecutive404 int
	warnedVersion  string
}

// NewManager starts from the local config. store may be nil.
func NewManager(base *config.Config, store *Store, id Identity, log *slog.Logger) *Manager {
	m := &Manager{log: log, base: base, store: store, id: id, now: time.Now}
	m.source = m.localSource()
	return m
}

func (m *Manager) localSource() string {
	if m.base.FileLoaded {
		return SourceLocal
	}
	return SourceDefaults
}

// LoadPersisted applies a valid state file, or records why it was ignored
// and keeps the local config.
func (m *Manager) LoadPersisted() {
	if m.store == nil {
		return
	}
	c, v, reason, ok, detail := m.store.Load(m.id)
	if reason != "" {
		m.lastErr = reason
		m.log.Warn("remote config: saved config ignored", "reason", reason)
		m.log.Debug("remote config: saved config detail", "detail", detail)
	}
	if !ok {
		return
	}
	m.applyVersioned(c, v)
	m.source = SourcePersisted
	m.persisted = v
}

// Apply takes one fetch result. Anything that isn't a verified config
// leaves the current config in force (last-known-good).
func (m *Manager) Apply(r FetchResult) {
	switch {
	case r.TooLarge:
		m.consecutive404 = 0
		m.reject(ReasonResponseTooLarge, nil)
		return
	case r.Err != nil || (r.Status != http.StatusOK && r.Status != http.StatusNotFound):
		m.consecutive404 = 0
		m.reject(ReasonFetchFailed, r.Err)
		return
	case r.Status == http.StatusNotFound:
		m.handleNotFound()
		return
	}
	m.consecutive404 = 0
	u, reason := ParseResponse(r.Body)
	if reason != "" {
		m.reject(reason, nil)
		return
	}
	if u.Versioned {
		m.applyVersioned(u.Config, u.Version)
		m.source, m.lastErr = SourceRemote, ""
		// Persist the server's config as received: its hash covers the
		// unfiltered scanner list, so saving the filtered one would fail its
		// own hash check on the next load (security review M-1).
		if m.store != nil && m.persisted != u.Version {
			if err := m.store.Save(m.id, u.Config, u.Version, m.now()); err != nil {
				m.reject(ReasonStateWriteFailed, err)
			} else {
				m.persisted = u.Version
			}
		}
		return
	}
	// An older server: merge exactly as before B-293, never persisted.
	if !m.haveRemote {
		m.cur = m.fromLocal()
	}
	if u.Has.Interval {
		m.cur.ScanIntervalSeconds = u.Config.ScanIntervalSeconds
	}
	if u.Has.Scanners {
		m.cur.EnabledScanners = u.Config.EnabledScanners
	}
	if u.Has.Paths {
		m.cur.ModelScanPaths = u.Config.ModelScanPaths
	}
	m.haveRemote, m.versioned, m.version = true, false, ""
	m.source, m.lastErr = SourceRemote, ""
}

// handleNotFound: the server has no config for this endpoint (unregistered
// or unlinked). Decision D-c: the agent goes back to local config, but only
// after two consecutive 404s, so one stray response can't drop it. The
// state file is deleted only when it is known to be this identity's -- it
// was loaded or written for it during this run (m.persisted). A file for
// another identity, ignored as stale, is never deleted, even after a legacy
// merge put a remote config in force (code review M2).
func (m *Manager) handleNotFound() {
	m.lastErr = ""
	m.consecutive404++
	if m.consecutive404 < 2 || !m.haveRemote {
		return
	}
	if m.store != nil && m.persisted != "" {
		if err := m.store.Delete(); err != nil {
			m.reject(ReasonStateWriteFailed, err)
		}
	}
	m.log.Info("remote config: endpoint has no remote config; reverted to local config")
	m.cur, m.haveRemote, m.versioned, m.version, m.persisted = Config{}, false, false, "", ""
	m.source, m.consecutive404 = m.localSource(), 0
}

func (m *Manager) applyVersioned(c Config, version string) {
	scanners, dropped := KnownScanners(c.EnabledScanners, config.AllScanners)
	if dropped && m.warnedVersion != version {
		m.warnedVersion = version
		m.log.Info("remote config: scanner names this agent doesn't run were ignored")
	}
	c = c.Clone()
	c.EnabledScanners = scanners
	m.cur, m.haveRemote, m.versioned, m.version = c, true, true, version
}

func (m *Manager) reject(reason string, detail error) {
	m.lastErr = reason
	m.log.Warn("remote config: not applied, keeping current config", "reason", reason)
	if detail != nil {
		m.log.Debug("remote config: rejection detail", "reason", reason, "detail", detail)
	}
}

func (m *Manager) fromLocal() Config {
	return Config{
		ScanIntervalSeconds: int64(m.base.Agent.IntervalSecs),
		EnabledScanners:     append([]string{}, m.base.Detection.EnabledScanners...),
		ModelScanPaths:      append([]string{}, m.base.Detection.ModelFileScanPaths...),
		ModelFileSizeMB:     m.base.Detection.MinModelSizeMB,
	}
}

// Snapshot returns a private copy of the full agent config with the
// effective remote config applied, for one scan cycle.
func (m *Manager) Snapshot() *config.Config {
	cp := *m.base
	cp.Detection.EnabledScanners = append([]string{}, m.base.Detection.EnabledScanners...)
	cp.Detection.ModelFileScanPaths = append([]string{}, m.base.Detection.ModelFileScanPaths...)
	if m.haveRemote {
		cp.Agent.IntervalSecs = int(m.cur.ScanIntervalSeconds)
		cp.Detection.EnabledScanners = append([]string{}, m.cur.EnabledScanners...)
		cp.Detection.ModelFileScanPaths = append([]string{}, m.cur.ModelScanPaths...)
		if m.versioned {
			cp.Detection.MinModelSizeMB = m.cur.ModelFileSizeMB
		}
	}
	return &cp
}

// Status describes the config the last Snapshot carries.
func (m *Manager) Status() Status {
	s := Status{Source: m.source, Error: m.lastErr}
	if m.haveRemote && m.versioned {
		s.Version, s.MaxReportSize = m.version, m.cur.MaxReportSizeBytes
	}
	return s
}
