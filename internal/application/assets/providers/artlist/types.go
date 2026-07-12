package artlist

import (
	"fmt"
	"os"

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
func RunDedupKey(term, rootFolderID, strategy string, dryRun bool) string {
	return runDedupKey(term, rootFolderID, strategy, dryRun)
}

// RunTagItem represents the result for a single clip in the full pipeline.
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
// godlike/07 invariant: probes MUST be real reachability checks (HTTP
// /health, exec.LookPath + binary version probe, db.PingContext +
// BEGIN IMMEDIATE/ROLLBACK, FolderManagerPort.ProbeFolderAccess, etc).
// An object-existence check (e.g. `if s.dispatcher != nil`) is
// forbidden — the diagnostic endpoint must NEVER report a passing
// probe without actually exercising the dependency. The benchmark
// is: "could a probe-read show a real `Error` string with a real
// underlying error message if the dep is genuinely broken?"
type ProbeResult struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Detail    string `json:"detail,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms"`
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
