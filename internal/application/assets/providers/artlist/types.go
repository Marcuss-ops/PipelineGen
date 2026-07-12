package artlist

import (
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"gopkg.in/yaml.v3"
)

// PresetConfig defines the configuration for a preset.
type PresetConfig struct {
	ClipDuration int    `yaml:"clip_duration"`
	Width        int    `yaml:"width"`
	Height       int    `yaml:"height"`
	FPS          int    `yaml:"fps"`
	Strategy     string `yaml:"strategy"`
}

// PresetsConfig holds all preset definitions from config file.
type PresetsConfig struct {
	Presets map[string]PresetConfig `yaml:"presets"`
}

// LoadPresets loads preset configurations from YAML file.
func LoadPresets(path string) (*PresetsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &PresetsConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// RunTagRequest represents the full Artlist tag pipeline request.
type RunTagRequest struct {
	Term         string `json:"term"`
	Limit        int    `json:"limit"`
	RootFolderID string `json:"root_folder_id,omitempty"`
	Strategy     string `json:"strategy,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
	ClipDuration int    `json:"clip_duration,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	FPS          int    `json:"fps,omitempty"`
	// Concurrency sets how many clips are downloaded in parallel.
	// Default: 3. Max: 10. Set to 1 for sequential downloads (legacy behavior).
	Concurrency int `json:"concurrency,omitempty"`
}

// RunDedupKey creates a deduplication key for artlist jobs.
//
// Fase 5 / Commit 3 (July 2026) follow-up: the signature now
// includes `limit` because a replay of the same run with a
// DIFFERENT limit (e.g. limit=8 vs limit=16) is a DIFFERENT run, not
// a same-run replay — omitting limit from the key would collapse
// distinct runs into one ActiveKey and surface misleading dedup.
//
// godlike/07 fail-closed: the limit is part of the canonical
// identity. Different run parameters MUST produce different keys
// (a replay that re-runs the SAME term + folder + limit + strategy +
// dryRun collapses; a replay that re-runs with a DIFFERENT limit
// does NOT collapse — the operator gets a fresh run, which is the
// honest semantic for "I asked for N clips, the dedup honored
// it, now I want M clips").
//
// The function is the SINGLE canonical surface for the run-level
// dedup key (godlike/06 SSOT). The kernel job broker's UNIQUE index
// on `jobs.active_key` is the storage layer; this function is the
// identity layer.
func RunDedupKey(term, rootFolderID, strategy string, dryRun bool, limit int) string {
	return runDedupKey(term, rootFolderID, strategy, dryRun, limit)
}

// RunTagItem represents the result for a single clip in the full pipeline.
//
// RunTagItem.Status is the per-item audit surface (Fase 6 / Commit 1,
// July 2026 — godlike/07 typed-error block visibility). Canonical values:
//
//	"completed"             happy path (legacy)
//	"media_process_failed"  mediaProcessor.Process returned error (legacy)
//	"drive_upload_failed"   Drive fields missing after persist (legacy, PR-ARTLIST-DOD-GATE-02)
//	"hash_missing"          SHA-256 missing after persist (legacy, PR-ARTLIST-HASH-FIX)
//	"persist_failed"        finalizer OR commit failed (legacy, PR-ARTLIST-OUTCOME-ACCOUNTING)
//	"dry_run"               req.DryRun skip (legacy)
//
//	Fase 6 typed gate-block values (one per Commit of the new authorization
//	gate series; ops MUST be grep-able via these exact lowercase strings):
//
//	"blocked_mode"             acquisition_mode=manual_import surfaced ErrAcquisitionModeBlocked (Commit 1, July 2026)
//	"blocked_daily_limit"      ArtlistDailyDownloadLimit<=0 OR count>=limit surfaced ErrDailyLimitExhausted (Commit 2)
//	"blocked_unauthorized"     account not authorized surfaced ErrAccountUnauthorized (Commit 3)
//	"blocked_session_expired"  session expired surfaced ErrSessionExpired (Commit 4)
//
// RunTagItem.Error carries the typed-error text verbatim when Status is
// one of the blocked_* values (operators grep on the per-item audit log
// without needing a separate /run/:id roundtrip; godlike/07).
//
// godlike/06 SSOT (Status enum): the canonical values are listed here
// + referenced from stageProcessBatch (gate-block short-circuit) +
// from run-record EvaluateRunState (per-status verdict rule). Adding a
// new value MUST land in BOTH places in the SAME PR to avoid drift.
type RunTagItem struct {
	ClipID       string `json:"clip_id"`
	Name         string `json:"name"`
	Filename     string `json:"filename"`
	Status       string `json:"status"`
	DownloadURL  string `json:"download_url,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DriveFileID  string `json:"drive_file_id"`
	DownloadLink string `json:"download_link,omitempty"`
	LocalPath    string `json:"local_path,omitempty"`
	FileHash     string `json:"file_hash,omitempty"`
	Error        string `json:"error,omitempty"`
	// Renditions carries the generated technical variants for this clip.
	// Populated by stageProcessBatch and consumed by stagePersistResults
	// to persist asset_locations + asset_renditions via the canonical
	// AssetFinalizerTx.
	Renditions []asset.RenditionOutput `json:"-"`
}

// RunTagResponse represents the result of the full tag pipeline.
type RunTagResponse struct {
	OK              bool         `json:"ok"`
	RunID           string       `json:"run_id,omitempty"`
	Status          string       `json:"status,omitempty"`
	Term            string       `json:"term"`
	Strategy        string       `json:"strategy,omitempty"`
	DryRun          bool         `json:"dry_run,omitempty"`
	RootFolderID    string       `json:"root_folder_id,omitempty"`
	TagFolderID     string       `json:"tag_folder_id,omitempty"`
	TagFolderLink   string       `json:"tag_folder_link,omitempty"`
	Requested       int          `json:"requested"`
	Found           int          `json:"found"`
	Processed       int          `json:"processed"`
	Skipped         int          `json:"skipped"`
	Failed          int          `json:"failed"`
	WouldProcess    int          `json:"would_process,omitempty"`
	WouldSkip       int          `json:"would_skip,omitempty"`
	EstimatedSize   int          `json:"estimated_size,omitempty"`
	LastProcessedAt *string      `json:"last_processed_at,omitempty"`
	StartedAt       *string      `json:"started_at,omitempty"`
	EndedAt         *string      `json:"ended_at,omitempty"`
	CompletedAt     *string      `json:"completed_at,omitempty"`
	Items           []RunTagItem `json:"items,omitempty"`
	Error           string       `json:"error,omitempty"`
}

// EvaluateRunOutcome evaluates whether a RunTagResponse represents a failed run.
// This centralizes the policy logic that was previously scattered in job handlers and endpoints.
// Returns (isFailed bool, errorMsg string).
//
// PR-ARTLIST-OUTCOME-ACCOUNTING (P1, July 2026): during the outcome-accounting
// refactor stagePersistResults started incrementing resp.Failed for three
// distinct persistence failure modes (drive_upload_failed, hash_missing,
// persist_failed) that previously surfaced as silent drops. Two policies
// now gate the run verdict:
//
//  1. Policy A (pre-existing): when all items failed (Failed > 0 with
//     Processed == 0 and Skipped == 0) the run is unambiguously failed.
//  2. Policy B (added in this PR; literal user spec): even with
//     Processed > 0, if Found - (Processed + Skipped) > 0 AND Failed == 0,
//     the run is undercounted: items showed up in the runner's view of the
//     world (resp.Items) but were never tallied into any bucket, which is
//     the exact silent-loss signature the spec asks us to catch.
//
// KNOWN LIMITATION: with the Failed == 0 guard, runs that mix explicit
// failures AND silent drops (e.g. Found=10, Processed=5, Failed=1,
// Skipped=0, 4 silent drops) will still pass. Operators wanting
// stricter accounting can extend Policy B to
// `gap := resp.Found - (resp.Processed + resp.Skipped + resp.Failed)`
// in a follow-up PR; this PR is bound to the literal user spec.
func EvaluateRunOutcome(resp *RunTagResponse) (bool, string) {
	if resp == nil {
		return true, "nil response"
	}
	if !resp.OK {
		errMsg := resp.Error
		if errMsg == "" {
			errMsg = "run was not successful"
		}
		return true, errMsg
	}
	// Policy A: if all items failed (Failed > 0, Processed == 0, Skipped == 0), mark as failed
	if resp.Failed > 0 && resp.Processed == 0 && resp.Skipped == 0 {
		return true, "all artlist items failed"
	}
	// Policy B (PR-ARTLIST-OUTCOME-ACCOUNTING, July 2026): if no explicit
	// failures were recorded but Found - (Processed + Skipped) > 0, items
	// were silently lost (present in the runner but not tallied). Mark the
	// run as failed with a precise diagnostic so the operator can audit.
	if resp.Failed == 0 {
		gap := resp.Found - (resp.Processed + resp.Skipped)
		if gap > 0 {
			return true, fmt.Sprintf("run undercounted: %d items silently lost (found=%d, processed=%d, skipped=%d, failed=0)",
				gap, resp.Found, resp.Processed, resp.Skipped)
		}
	}
	return false, ""
}

// Stats represents the statistics for Artlist endpoints
type Stats struct {
	OK                bool `json:"ok"`
	ClipsTotal        int  `json:"clips_total"`
	ArtlistClipsTotal int  `json:"artlist_clips_total"`
} // ProbeStage is one named sub-step of a multi-stage probe (Fase 7 /
// Commit B, July 2026 — the artlist Scraper probe is the first user,
// but the wire shape is generic so future probes can re-use it).
//
// godlike/06 SSOT: ProbeStage is the SINGLE canonical name for
// per-stage probe outcomes. The aggregate ProbeResult.OK MUST equal
// the logical AND of all Stages[i].OK (no aggregate is ever OK=true
// when any stage failed — godlike/07 fail-closed). The Stages slice
// is surfaced via JSON so operators can pinpoint EXACTLY which
// stage failed without grepping the JSON for substrings.
//
// ProbeStage.Error is the verbatim underlying error (no truncation,
// no wrapping) so operators can grep for canonical patterns like
// "stage_3_real_query: search returned 0 clips".
type ProbeStage struct {
	// Name is the canonical stage identifier. Operators grep on it.
	// Canonical values for the Scraper probe: "stage_1_chromium_started",
	// "stage_2_session_valid", "stage_3_real_query".
	Name string `json:"name"`
	// OK is the per-stage verdict: true iff this stage passed.
	OK bool `json:"ok"`
	// Error is the verbatim underlying failure when OK=false. Empty
	// when OK=true.
	Error string `json:"error,omitempty"`
	// Detail is the operator-facing diagnostic (e.g. "browser_running=true,
	// pid=42, last_search_at=2026-07-12T10:30:00Z"). Surfaced verbatim.
	Detail string `json:"detail,omitempty"`
	// ElapsedMs is the per-stage wall-clock budget so operators see
	// slow stages independently.
	ElapsedMs int64 `json:"elapsed_ms"`
}

// ProbeResult is the canonical wire-by-wire probe outcome (Fase 2,
// July 2026, godlike/07 NO-FAKE-AVAILABILITY §22).
//
// Per probe report: a single binary `OK` plus a typed `Error` (verbatim
// from the underlying probe call), a `Detail` string (operator-facing
// diagnostic text e.g. "ffmpeg version 6.0.1 /usr/bin/ffmpeg"), and a
// per-probe `ElapsedMs` so operators reading the JSON can spot slow
// probes (e.g. Qdrant probe taking 4.5s on an 8-of-10 probes endpoint).
//
// Stages (Fase 7 / Commit B, July 2026): when the probe is multi-stage
// (e.g. the Scraper probe runs 3 sub-steps), `Stages` carries the
// per-stage verdict. The aggregate `OK` MUST equal the logical AND of
// all `Stages[i].OK`; `Error` is the typed error of the FIRST failing
// stage (operators walk Stages in order to find the actual failure
// point). The aggregate `ElapsedMs` is the sum of per-stage budgets
// (with HTTP roundtrip overlap); `Stages[i].ElapsedMs` reflects each
// stage's own wall-clock.
//
// godlike/07 invariant: probes MUST be real reachability checks (HTTP
// /health, exec.LookPath + binary version probe, db.PingContext +
// BEGIN IMMEDIATE/ROLLBACK, FolderManagerPort.ProbeFolderAccess, etc).
// An object-existence check (e.g. `if s.dispatcher != nil`) is
// forbidden — the diagnostic endpoint must NEVER report a passing
// probe without actually exercising the dependency. The benchmark
// is: "could a probe-read show a real `Error` string with a real
// underlying error message if the dep is genuinely broken?"
type ProbeResult struct {
	OK        bool         `json:"ok"`
	Error     string       `json:"error,omitempty"`
	Detail    string       `json:"detail,omitempty"`
	ElapsedMs int64        `json:"elapsed_ms"`
	Stages    []ProbeStage `json:"stages,omitempty"`
}

// LatestRunSummary is the per-run summary surfaced in
// DiagnosticsResponse.LatestRun, sourced from the artlist_runs SQLite
// table via RunRepository.LatestRun (canonical SSOT per Fase 2).
// Operators see the latest run's RunID + Status + Term + Error +
// CreatedAt without needing a separate /runs/:run_id roundtrip.
//
// godlike/07: when no runs exist the surface is nil (omitted from JSON);
// operators interpret omitempty as "no runs yet, this is a fresh
// install". Surfacing a sentinel-zero-value LatestRunSummary would
// confuse the operator (`run_id="" + status=""` is meaningless).
//
// NOTE: this type is declared ONLY in ports.go (Line ~340) — types.go
// references it for the DiagnosticsResponse.LatestRun field shape;
// the canonical struct definition lives in ports.go alongside the
// RunRepository interface (godlike/06 SSOT — one canonical site).
// =============================================================
// godlike/06 SSOT lockstep note: do NOT add this struct here —
// keep ports.go as the SINGLE canonical declaration site.
// =============================================================

// DiagnosticsResponse reports wire-by-wire probe outcomes for the
// Artlist module (Fase 2 rewrite, July 2026 — replaces the v1
// audit-fail `OK: true` aggregate + `HasDriveClient/HasArtlistDB/
// MainDBReady` object-existence checks the gap-audit §22 verdict
// flagged as NO-FAKE-AVAILABILITY violations).
//
// 10 ProbeResult fields, one per dependency (scraper, browser,
// session, downloader, ffmpeg_binary, drive_folder, sqlite_writable,
// outbox_dispatcher, qdrant_reachable, embedding_provider).
// Operators reading the JSON can spot which dependency is broken
// from a single curl — no aggregate to mask the signal.
//
// Compatibility note (Fase 2): the legacy fields `HasDriveClient`,
// `HasArtlistDB`, `MainDBReady` (object-existence checks) and the
// top-level `OK` aggregate are RETIRED. The audit identified them
// as the §22 anti-pattern; replacing them with real ProbeResult
// fields is the canonical godlike/06 SSOT fix (one canonical
// owner per fact: the probe itself, not a struct-pointer check).
type DiagnosticsResponse struct {
	// RootFolderID is the *configured* Artlist Drive root (informational;
	// NOT a probe — that's the DriveFolder probe). Kept for operator
	// config visibility in the same endpoint payload (no extra roundtrip).
	RootFolderID string `json:"root_folder_id,omitempty"`

	// 10 wire-by-wire probes (one per dependency). Each is a ProbeResult
	// with binary OK + Error + Detail + ElapsedMs. The endpoint reports
	// these regardless of the deprecated `OK` aggregate — operators
	// walk each slot independently to find the broken dependency.
	Scraper           ProbeResult `json:"scraper"`
	Browser           ProbeResult `json:"browser"`
	Session           ProbeResult `json:"session"`
	Downloader        ProbeResult `json:"downloader"`
	FFmpegBinary      ProbeResult `json:"ffmpeg_binary"`
	DriveFolder       ProbeResult `json:"drive_folder"`
	SQLiteWritable    ProbeResult `json:"sqlite_writable"`
	OutboxDispatcher  ProbeResult `json:"outbox_dispatcher"`
	QdrantReachable   ProbeResult `json:"qdrant_reachable"`
	EmbeddingProvider ProbeResult `json:"embedding_provider"`

	// Special informational surfaces (Fase 2): the canonical operator
	// grip on past runs and current indexed-Artlist-clip count without
	// roundtripping /runs/:run_id or querying Qdrant.
	//
	// LatestRun: omitempty — when no artlist_runs rows exist (fresh
	// install), the field is omitted entirely. Operators interpret
	// omitempty as "no runs yet", avoiding sentinel-zero-value noise.
	LatestRun *LatestRunSummary `json:"latest_run,omitempty"`
	// LastError: the error_message column of the latest artlist_runs
	// row (empty when the latest run was successful or no runs exist).
	// Convenience duplicate of LatestRun.Error so operators triaging
	// "what broke?" don't have to walk a nested struct.
	LastError         string `json:"last_error,omitempty"`
	ClipsArtlistTotal int    `json:"clips_artlist_total"`

	// Term-search surface (legacy: preserved for the term-keyed
	// diagnostics use case where the operator wants "term X
	// matching clip count" + LastProcessedAt. NOT a probe; reads
	// from the existing assetStore port, no fake availability).
	SearchTerm      string  `json:"search_term,omitempty"`
	MatchingClips   int     `json:"matching_clips,omitempty"`
	EstimatedSize   int     `json:"estimated_size,omitempty"`
	LastProcessedAt *string `json:"last_processed_at,omitempty"`

	// Error is reserved for fatal diagnostics failure (probe runner
	// panicked, RunRepository not wired, etc). Operators distinguish
	// endpoint-level errors from per-probe errors by absence of the
	// term-search fields + presence of this string.
	Error string `json:"error,omitempty"`
}

// RunStatus is the canonical wire-by-wire status enum surfaced by
// the /api/artlist/runs/:id endpoint (Fase 3, July 2026).
//
// godlike/07 NO-FAKE-AVAILABILITY: never aggregate to a single OK
// bool at this endpoint. Operators must walk the per-bucket counts
// (Found / Processed / Skipped / Failed) to spot the failure mode;
// the status enum is the verdict on the bucketing pattern, not a
// re-aggregate.
//
// Mirrors the canonical state-machine pattern in
// internal/application/voiceover/parent_state.go (succeeded /
// partial_success / failed) — the partial_success string is the
// precedent for a 3-state machine. The Fase 3 enum uses upper-case
// SUCCEEDED / FAILED / PARTIAL_SUCCESS to match the user-spec
// literal and avoid the underscore-cased form in the JSON wire.
type RunStatus string

const (
	// RunStatusSucceeded is the verdict when Processed > 0 AND
	// Failed == 0 (and the Found == Processed+Skipped+Failed
	// invariant holds). Mirrors the user-spec literal:
	// "Solo quando Processed>0 ∧ Failed=0 ⇒ SUCCEEDED".
	RunStatusSucceeded RunStatus = "SUCCEEDED"
	// RunStatusFailed is the verdict when Processed == 0 AND
	// Failed > 0 (zero work done, all failures). Mirrors the
	// user-spec literal: "Processed=0 ∧ Failed>0 ⇒ FAILED".
	RunStatusFailed RunStatus = "FAILED"
	// RunStatusPartialSuccess is the verdict for any mixed case:
	// Processed > 0 AND Failed > 0 (some work, some failures);
	// Processed == 0 AND Failed == 0 AND Skipped > 0 (zero work,
	// zero failure, all skipped); OR the Found != Processed +
	// Skipped + Failed invariant violation; OR the real-DB
	// cross-check mismatch (artlist_runs.processed_count > 0 but
	// media_assets has 0 rows for source='artlist' in the run's
	// time window).
	RunStatusPartialSuccess RunStatus = "PARTIAL_SUCCESS"
	// RunStatusUnknown is the verdict for the "run not found" /
	// "fresh install" / "no runs yet" case. The handler in Commit 3
	// short-circuits the state machine and returns this constant
	// when artlist_runs.LatestRun / artlist_runs.GetByID returns
	// sql.ErrNoRows (rather than feeding all-zero counts to
	// EvaluateRunState, which would fall to Rule 5 and produce
	// PARTIAL_SUCCESS — a misleading verdict for the no-runs case).
	//
	// The handler /api/artlist/runs/:id also pairs this status
	// with HTTP 404 (godlike/07 fail-closed: the run truly does
	// not exist; reporting PARTIAL_SUCCESS would mask that fact).
	RunStatusUnknown RunStatus = "UNKNOWN"
)

// String makes RunStatus satisfy fmt.Stringer so the canonical
// log/diagnostic tag (zap.Stringer(...) rendering) shows the
// wire-format value without explicit casts.
func (s RunStatus) String() string { return string(s) }

// IsTerminal reports whether the RunStatus is a final verdict
// (no further state transitions expected). SUCCEEDED / FAILED /
// PARTIAL_SUCCESS are all terminal (godlike/07 fail-closed: the
// state machine resolves to one of these; no "running" or
// "pending" surface). RunStatusUnknown is ALSO terminal (it
// represents the "no run exists" verdict, which is a final
// state — the run will not transition into existence at a
// later time; it either exists or it doesn't).
func (s RunStatus) IsTerminal() bool {
	switch s {
	case RunStatusSucceeded, RunStatusFailed, RunStatusPartialSuccess, RunStatusUnknown:
		return true
	}
	return false
}

// RunStatusCounts is the per-bucket counts input to EvaluateRunState.
//
// godlike/06 SSOT: this struct is the SINGLE canonical input shape
// for the state-machine evaluation. The /api/artlist/runs/:id
// handler reads Found/Skipped/Failed from artlist_runs (canonical
// aggregate writer) + Processed from artlist_runs.processed_count
// + RealPersisted from a defensive time-window COUNT(*) on
// media_assets. The RealPersisted field is the "zero-asset-
// impossible-to-succeed" cross-check (godlike/07 fail-closed).
type RunStatusCounts struct {
	// Found is the canonical aggregate count (artlist_runs.found_count).
	Found int
	// Processed is the claimed processed count (artlist_runs.processed_count).
	// May be 0 (zero work) or > 0 (some/all work). The real-DB cross-check
	// is the RealPersisted field.
	Processed int
	// Skipped is the canonical aggregate count (artlist_runs.skipped_count).
	Skipped int
	// Failed is the canonical aggregate count (artlist_runs.failed_count).
	Failed int
	// RealPersisted is the COUNT(*) from media_assets WHERE source='artlist'
	// AND created_at >= run.created_at. The defensive cross-check that
	// catches the "artlist_runs.processed_count lies about persisted assets"
	// failure mode. If Processed > 0 but RealPersisted == 0, the verdict
	// is forced to PARTIAL_SUCCESS with diagnostic "real_db_mismatch"
	// (godlike/07 fail-closed: a zero-asset run cannot be SUCCEEDED).
	RealPersisted int
}

// InvariantHolds reports whether the Found == Processed + Skipped +
// Failed invariant holds (the user-spec literal: "Processed+Skipped+
// Failed=Found come vincolo"). When false, the verdict is forced to
// PARTIAL_SUCCESS regardless of the per-bucket pattern.
func (c RunStatusCounts) InvariantHolds() bool {
	return c.Found == c.Processed+c.Skipped+c.Failed
}

// EvaluateRunState applies the canonical 4-rule state machine to
// the per-bucket counts and returns the wire-by-wire RunStatus
// plus the invariant-violation flag + a typed diagnostic.
//
// Rule priority (godlike/07 fail-closed, first match wins):
//
//  1. Invariant violation: Found != Processed + Skipped + Failed
//     → PARTIAL_SUCCESS with InvariantViolated=true + diagnostic.
//  2. Real-DB mismatch: Processed > 0 BUT RealPersisted == 0
//     → PARTIAL_SUCCESS with InvariantViolated=true + diagnostic.
//     (The "zero-asset-impossible-to-succeed" gate per the user
//     spec test requirement.)
//  3. Zero work, all failures: Processed == 0 AND Failed > 0
//     → FAILED.
//  4. All work succeeded: Processed > 0 AND Failed == 0
//     → SUCCEEDED.
//  5. Default (any other case): PARTIAL_SUCCESS.
//
// This function is PURE (no I/O). The /api/artlist/runs/:id handler
// is the only production caller; the matrix test file
// evaluate_run_state_test.go pins the rule priority verbatim.
func EvaluateRunState(c RunStatusCounts) (status RunStatus, invariantViolated bool, diagnostic string) {
	// Rule 1: invariant violation.
	if !c.InvariantHolds() {
		return RunStatusPartialSuccess, true,
			fmt.Sprintf("invariant_violated: Found(%d) != Processed(%d) + Skipped(%d) + Failed(%d)",
				c.Found, c.Processed, c.Skipped, c.Failed)
	}
	// Rule 2: real-DB mismatch (zero-asset-impossible-to-succeed gate).
	if c.Processed > 0 && c.RealPersisted == 0 {
		return RunStatusPartialSuccess, true,
			fmt.Sprintf("real_db_mismatch: artlist_runs.processed_count=%d but media_assets has 0 rows for source='artlist' in the run's time window (a zero-asset run cannot be SUCCEEDED — godlike/07 fail-closed)",
				c.Processed)
	}
	// Rule 3: zero work, all failures.
	if c.Processed == 0 && c.Failed > 0 {
		return RunStatusFailed, false, ""
	}
	// Rule 4: all work succeeded.
	if c.Processed > 0 && c.Failed == 0 {
		return RunStatusSucceeded, false, ""
	}
	// Rule 5: default (mixed case).
	return RunStatusPartialSuccess, false, ""
}

// RunStatusResponse is the canonical wire shape returned by
// /api/artlist/runs/:id (Fase 3, July 2026).
//
// godlike/07 NO-FAKE-AVAILABILITY: the response carries the typed
// status enum + per-bucket counts (no aggregate OK bool). Operators
// walk each field independently to find the failure mode. The
// InvariantViolated + Diagnostic fields surface the
// invariant-violation case explicitly (Rule 1 + Rule 2 of
// EvaluateRunState).
type RunStatusResponse struct {
	RunID  string    `json:"run_id"`
	Term   string    `json:"term"`
	Status RunStatus `json:"status"`
	// Per-bucket counts (Found / Processed / Skipped / Failed) read
	// from artlist_runs (canonical aggregate writer). The "real"
	// Processed count comes from the cross-check
	// (RealPersisted) — see EvaluateRunState Rule 2.
	Found     int `json:"found"`
	Processed int `json:"processed"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
	// InvariantViolated is true when EvaluateRunState fired Rule 1
	// (Found != Processed + Skipped + Failed) or Rule 2
	// (Processed > 0 but RealPersisted == 0). Operators use this
	// flag to spot silent accounting drift.
	InvariantViolated bool `json:"invariant_violated,omitempty"`
	// Diagnostic is the human-readable string EvaluateRunState
	// produced when InvariantViolated is true. Empty otherwise.
	Diagnostic string `json:"diagnostic,omitempty"`
	// EvaluatedAt is the wall-clock timestamp the verdict was
	// computed. time.Time auto-marshals to RFC3339 in JSON
	// ("2026-07-12T10:30:00.000Z") — the Go-idiomatic shape that
	// matches the codebase's other timestamp surfaces
	// (e.g., domain/asset.Asset.IndexedAt, domain/asset.Asset.UpdatedAt).
	// Useful for operator audit trails when comparing a stale
	// verdict against a re-evaluation.
	EvaluatedAt time.Time `json:"evaluated_at"`
}

// SearchRequest represents a search request.
//
// PR-P2-SEARCH-LIVE (July 2026): PreferRemote is the operator-facing
// opt-in flag that flips the canonical SearchService.searchLiveWithFallbacks
// chain order so the Node ScraperSearcher is the PRIMARY provider and
// the local DB cache (DBSearcher) + in-memory TTL cache (CachedSearcher
// wrapper around scraper) are COMPLETELY DROPPED from the chain.
//
// Default behavior at the /api/artlist/search/live handler is true
// (per user-spec contract "default per /api/artlist/search/live").
// Internal callers (DiscoverAndQueueRun, run_orchestrator_stages::
// stageDiscoverClips that calls SearchLiveAndSave) keep
// PreferRemote=false so existing cache-first semantics for "discover
// fresh content" runs is preserved.
type SearchRequest struct {
	Term         string `json:"term"`
	Limit        int    `json:"limit"`
	PreferDB     bool   `json:"prefer_db"`
	PreferRemote bool   `json:"prefer_remote,omitempty"`
}

// SearchResponse represents a search response with canonical asset types.
type SearchResponse struct {
	OK     bool          `json:"ok"`
	Term   string        `json:"term"`
	Source string        `json:"source"`
	Clips  []asset.Asset `json:"clips"`
	Error  string        `json:"error,omitempty"`
}
