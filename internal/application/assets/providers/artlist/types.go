package artlist

import (
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/idempotency"
	"gopkg.in/yaml.v3"
)

// PresetConfig defines the configuration for a preset.
// FPS is carried as a rational FPSNum/FPSDen pair (canonical FrameRate).
type PresetConfig struct {
	ClipDuration int    `yaml:"clip_duration"`
	Width        int    `yaml:"width"`
	Height       int    `yaml:"height"`
	FPSNum       int    `yaml:"fps_num"`
	FPSDen       int    `yaml:"fps_den"`
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
	FPSNum       int    `json:"fps_num,omitempty"`
	FPSDen       int    `json:"fps_den,omitempty"`
	// Concurrency sets how many clips are downloaded in parallel.
	// Default: 3. Max: 10. Set to 1 for sequential downloads (legacy behavior).
	Concurrency int `json:"concurrency,omitempty"`
}

// RunDedupKey creates a deduplication key for artlist jobs.
// Returns the canonical 64-char lowercase SHA-256 hex string of
// the JSON-marshaled canonical request map (delegates to
// pkg/idempotency.BuildKey — godlike/06 SSOT).
//
// Commit B (FASE 5 follow-up, July 2026): the signature changed
// from `string` to `(string, error)`. The previous shape silently
// produced a fake-availability string when canonical json.Marshal
// failed (the legacy runDedupKey had a fmt.Sprintf fallback path);
// the new shape fails closed per godlike/07 with a typed sentinel
// from the canonical idempotency package:
//
//   - pkg/idempotency.ErrInvalidRunForDedup — empty discriminator,
//     empty canonical map, or unmarshalable canonical content.
//   - pkg/idempotency.ErrInvalidSegment — provider discriminator
//     contains ':' (segment-delimiter collision guard).
//   - pkg/idempotency.ErrEmptyProvider — same as
//     ErrInvalidRunForDedup for the empty-discriminator path,
//     reachable only when the caller bypasses the discriminator
//     check; surfaced via errors.Is so handlers can branch on a
//     typed diagnostic.
//
// Callers (RunTagRequest across handler + orchestrator) MUST
// handle the error: the artlist handler maps the error to
// apiutil.BadRequest (HTTP 400) because the malformed run-dedup
// input is an operator-input error (godlike/07), and
// DiscoverAndQueueRun propagates the error to its caller (which
// then logs + returns an HTTP-error envelope).
//
// Fase 5 / Commit 3 (July 2026) follow-up: the signature carries
// `limit` because a replay of the same run with a DIFFERENT
// limit (e.g. limit=8 vs limit=16) is a DIFFERENT run, not a
// same-run replay — omitting limit from the key would collapse
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
// identity layer. The underlying idempotency.BuildKey produces a
// 64-char lowercase hex SHA-256 — byte-stable across the migration
// from the legacy runDedupKey private helper so in-flight
// queued jobs MATCH across deployment.
func RunDedupKey(term, rootFolderID, strategy string, dryRun bool, limit int) (string, error) {
	canonical := map[string]any{
		"term":           strings.ToLower(strings.TrimSpace(term)),
		"root_folder_id": strings.TrimSpace(rootFolderID),
		"strategy":       strings.ToLower(strings.TrimSpace(strategy)),
		"dry_run":        dryRun,
		"limit":          limit,
	}
	return idempotency.BuildKey("artlist-run", canonical)
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
	ClipID        string `json:"clip_id"`
	Name          string `json:"name"`
	Filename      string `json:"filename"`
	Status        string `json:"status"`
	DownloadURL   string `json:"download_url,omitempty"`
	DriveLink     string `json:"drive_link,omitempty"`
	DriveFileID   string `json:"drive_file_id"`
	DownloadLink  string `json:"download_link,omitempty"`
	LocalPath     string `json:"local_path,omitempty"`
	LegacyFileMD5 string `json:"legacy_file_md5,omitempty"`
	Error         string `json:"error,omitempty"`
	// Metadata carries provider provenance through processing into the
	// canonical finalizer payload.
	Metadata map[string]any `json:"metadata,omitempty"`
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

// SearchMode is the canonical Go→Node catalog resolution mode.
type SearchMode string

const (
	SearchModeCatalogFirst SearchMode = "catalog_first"
	SearchModeCatalogOnly  SearchMode = "catalog_only"
	SearchModeLiveRequired SearchMode = "live_required"
)

func (m SearchMode) IsValid() bool {
	return m == SearchModeCatalogFirst || m == SearchModeCatalogOnly || m == SearchModeLiveRequired
}

// ResolveSearchMode preserves legacy callers while making the Node contract
// explicit: remote-preferred searches are live_required; all other searches
// default to catalog_first unless the caller explicitly requests catalog_only.
func ResolveSearchMode(mode SearchMode, preferRemote bool) SearchMode {
	if mode.IsValid() {
		return mode
	}
	if preferRemote {
		return SearchModeLiveRequired
	}
	return SearchModeCatalogFirst
}

// SearchRequest represents a search request.
//
// Mode is always propagated to the Node scraper by infrastructure adapters.
// PreferRemote remains as a compatibility input for callers that predate the
// explicit mode contract.
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
	Term     string     `json:"term"`
	Limit    int        `json:"limit"`
	Mode     SearchMode `json:"mode,omitempty"`
	PreferDB bool       `json:"prefer_db"`
	// ForceRefresh bypasses provider-side search caches. It is used by
	// provider-enabled VidRush discovery so a stale stream URL cannot be
	// promoted into the acquisition pipeline.
	ForceRefresh bool `json:"force_refresh,omitempty"`
	PreferRemote bool `json:"prefer_remote,omitempty"`
}

// ImportClipRequest represents a request to import a single Artlist clip
// by its detail page URL.
type ImportClipRequest struct {
	// ClipPageURL is the Artlist clip detail page, e.g.
	// https://artlist.io/stock-footage/clip/slug/123456
	ClipPageURL string `json:"clip_page_url"`
	// RootFolderID is the Drive folder where the clip should land.
	// When empty the configured Artlist root folder is used.
	RootFolderID string `json:"root_folder_id,omitempty"`
	// Download controls whether the video is downloaded, normalized,
	// uploaded to Drive, and indexed. When false, only metadata is
	// fetched and the asset is saved as STAGING/DISCOVERED.
	Download bool `json:"download,omitempty"`
}

// ImportClipResponse is the result of a single-clip import.
type ImportClipResponse struct {
	OK            bool           `json:"ok"`
	ClipID        string         `json:"clip_id,omitempty"`
	Name          string         `json:"name,omitempty"`
	Description   string         `json:"description,omitempty"`
	Status        string         `json:"status,omitempty"`
	DriveLink     string         `json:"drive_link,omitempty"`
	DriveFileID   string         `json:"drive_file_id,omitempty"`
	LocalPath     string         `json:"local_path,omitempty"`
	LegacyFileMD5 string         `json:"legacy_file_md5,omitempty"`
	DownloadLink  string         `json:"download_link,omitempty"`
	ClipPageURL   string         `json:"clip_page_url,omitempty"`
	ThumbnailURL  string         `json:"thumbnail_url,omitempty"`
	PreviewURL    string         `json:"preview_url,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	Categories    []string       `json:"categories,omitempty"`
	Creator       string         `json:"creator,omitempty"`
	Country       string         `json:"country,omitempty"`
	Location      string         `json:"location,omitempty"`
	Error         string         `json:"error,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// SearchResponse represents a search response with canonical asset types.
type SearchResponse struct {
	OK     bool          `json:"ok"`
	Term   string        `json:"term"`
	Source string        `json:"source"`
	Clips  []asset.Asset `json:"clips"`
	Error  string        `json:"error,omitempty"`
}
