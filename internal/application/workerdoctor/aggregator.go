// Package workerdoctor — aggregator.go (RW-PROD-016, June 2026).
//
// Core types for the pre-boot introspection aggregator. Every probe
// is a func() ProbeReceipt registered via SetCheck; RunOnce runs all
// checks and produces a typed Report. The CLI in cmd/worker/doctor_main.go
// reads the report and maps the Verdict to an exit code.
//
// Design principle: the aggregator is a leaf state machine — no DB,
// no network (the probes do that), no goroutines. Tests can construct
// an Aggregator literal and call SetCheck with fakes without touching
// the production wiring.

package workerdoctor

import (
	"context"
	"sort"
	"sync"
	"time"
)

// ── Exit codes ────────────────────────────────────────────────────────

const (
	ExitUsage    = 2
	ExitInternal = 3
)

// ── Check IDs ─────────────────────────────────────────────────────────

const (
	CheckIDConfig          = "config"
	CheckIDCert            = "cert"
	CheckIDFilesystem      = "filesystem"
	CheckIDEngine          = "engine"
	CheckIDRegistry        = "registry"
	CheckIDMasterReachable = "master_reachable"
	CheckIDReady           = "ready"
	CheckIDRuntime         = "runtime"
)

// AllCheckIDs returns every check ID in registration order. Used by
// the CLI emitter to produce a deterministic table.
var AllCheckIDs = []string{
	CheckIDConfig,
	CheckIDCert,
	CheckIDFilesystem,
	CheckIDEngine,
	CheckIDRegistry,
	CheckIDMasterReachable,
	CheckIDReady,
	CheckIDRuntime,
}

// ── Probe receipt ─────────────────────────────────────────────────────

// ProbeReceipt is the canonical result of a single probe check.
// Each probe returns exactly one of these.
type ProbeReceipt struct {
	OK         bool           `json:"ok"`
	Applicable bool           `json:"applicable"`
	Error      string         `json:"error,omitempty"`
	Note       string         `json:"note,omitempty"`
	Extras     map[string]any `json:"extras,omitempty"`
}

// ── Verdict ───────────────────────────────────────────────────────────

// Verdict is the aggregated outcome of all probes.
type Verdict string

const (
	VerdictReady    Verdict = "READY"
	VerdictNotReady Verdict = "NOT_READY"
)

// ReturnCodeFromVerdict maps a Verdict to a Unix exit code.
func ReturnCodeFromVerdict(v Verdict) int {
	switch v {
	case VerdictReady:
		return 0
	default:
		return 1
	}
}

// ── Check result ──────────────────────────────────────────────────────

// CheckResult pairs a check ID with its ProbeReceipt and runtime
// metadata. The CLI emitter reads OK, Applicable, Error, Note, and
// DurationMS directly.
type CheckResult struct {
	OK         bool   `json:"ok"`
	Applicable bool   `json:"applicable"`
	Error      string `json:"error,omitempty"`
	Note       string `json:"note,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// ── Report ────────────────────────────────────────────────────────────

// Report is the aggregated output of Aggregator.RunOnce. The CLI
// emitter marshals it directly.
type Report struct {
	SchemaVersion   int                    `json:"schema_version"`
	Verdict         Verdict                `json:"verdict"`
	Timestamp       time.Time              `json:"timestamp"`
	TotalDurationMS int64                  `json:"total_duration_ms"`
	WorkerID        string                 `json:"worker_id,omitempty"`
	WorkerVersion   string                 `json:"worker_version,omitempty"`
	MasterURL       string                 `json:"master_url,omitempty"`
	MTLSEnabled     bool                   `json:"mtls_enabled"`
	Checks          map[string]CheckResult `json:"checks"`
}

// ── Aggregator ────────────────────────────────────────────────────────

// Aggregator collects checks and runs them on demand. It is safe for
// single-threaded use; RunOnce uses a mutex for call-site safety only.
type Aggregator struct {
	// Exported metadata — set by the CLI before RunOnce.
	WorkerID      string
	WorkerVersion string
	MasterURL     string
	MTLSEnabled   bool
	ConfigPath    string
	Production    bool

	checks []checkEntry
	mu     sync.Mutex
}

type checkEntry struct {
	id string
	fn func() ProbeReceipt
}

// NewAggregator returns an empty Aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{}
}

// SetCheck registers a probe by ID. Registered probes run in
// registration order during RunOnce.
func (a *Aggregator) SetCheck(id string, fn func() ProbeReceipt) {
	if a == nil {
		return
	}
	a.checks = append(a.checks, checkEntry{id: id, fn: fn})
}

// RunOnce runs every registered check and produces a Report.
// Checks that return Applicable=false are excluded from the
// Ready/NotReady verdict.
func (a *Aggregator) RunOnce(_ context.Context) *Report {
	if a == nil {
		return &Report{
			SchemaVersion: 1,
			Verdict:       VerdictNotReady,
			Checks:        make(map[string]CheckResult),
		}
	}
	a.mu.Lock()
	entries := make([]checkEntry, len(a.checks))
	copy(entries, a.checks)
	a.mu.Unlock()

	start := time.Now()
	rep := &Report{
		SchemaVersion: 1,
		Timestamp:     start.UTC(),
		WorkerID:      a.WorkerID,
		WorkerVersion: a.WorkerVersion,
		MasterURL:     a.MasterURL,
		MTLSEnabled:   a.MTLSEnabled,
		Checks:        make(map[string]CheckResult, len(entries)),
	}
	allOK := true
	for _, e := range entries {
		t0 := time.Now()
		rec := e.fn()
		elapsed := time.Since(t0).Milliseconds()
		rep.Checks[e.id] = CheckResult{
			OK:         rec.OK,
			Applicable: rec.Applicable,
			Error:      rec.Error,
			Note:       rec.Note,
			DurationMS: elapsed,
		}
		if rec.Applicable && !rec.OK {
			allOK = false
		}
	}
	if allOK {
		rep.Verdict = VerdictReady
	} else {
		rep.Verdict = VerdictNotReady
	}
	rep.TotalDurationMS = time.Since(start).Milliseconds()
	return rep
}

// Diagnose returns a human-readable list of failure strings. It walks
// every check in the report and surfaces non-OK Applicable errors.
func (a *Aggregator) Diagnose(rep *Report) []string {
	if rep == nil {
		return nil
	}
	var lines []string
	for id, c := range rep.Checks {
		if c.Applicable && !c.OK && c.Error != "" {
			lines = append(lines, id+": "+c.Error)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	sort.Strings(lines)
	return lines
}
