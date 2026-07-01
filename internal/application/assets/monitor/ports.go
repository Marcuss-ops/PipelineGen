// Package monitor — ports.go: Pattern 0 port surface + DTO types.
//
// Step 9 (June 2026, Channel Monitor Blocco 6 architectural rewrite):
// the package is now exactly 5 production files (scheduler.go,
// discovery.go, analyzer.go, enqueue.go, ports.go). This file owns:
//   - The 4 port interfaces (MonitorDownloaderPort is the legacy
//     yt-dlp slice; TranscriptProvider + VideoAnalyzer + JobEnqueuer
//     are the new ports that hide os/exec, OllamaClient, VTT parsing,
//     and the jobs.Service broker behind typed boundaries).
//   - The CompositionDeps struct (the new NewChannelMonitor signature).
//   - ChannelConfig + MonitorConfig + ChannelCheckResult +
//     Analysis + EnqueueExtractRequest — DTO types used across files.
//   - The compile-time assertions pinning the concrete YTDLPDownloader
//     satisfies MonitorDownloaderPort, and the temporary *unbound*Xyz
//     stubs satisfy their respective ports.
//
// Per AGENTS.md / godlike/06 §"Database and config ownership": every new
// dependency crosses the boundary through a typed port (no direct
// concrete adapter reference from inside the package). The next commit
// will install YTDLPSubtitleAdapter (concrete TranscriptProvider) and
// OllamaAnalyzer (concrete VideoAnalyzer) at internal/application/{transcripts,semantic}/
// — this commit keeps the package import-clean (no os/exec, no Ollama,
// no VTT regex) via the unbound stub types.
package monitor

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	transcript "github.com/Marcuss-ops/PipelineGen/internal/domain/transcript"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Priority + global-defaults ────────────────────────────────────────────

// Priority levels for batch channel scheduling.
const (
	PriorityHot    = 1
	PriorityNormal = 2
	PriorityCold   = 3
)

// DefaultPlaylistEnd is the global default for how many videos to scan
// per channel check when no channel-level override is set.
const DefaultPlaylistEnd = 50

// ── CompositionDeps (the new ctor signature) ─────────────────────────────

// CompositionDeps is the ctor payload for NewChannelMonitor. Replaces
// the pre-Step-9 7-parameter signature which exposed concrete Ollama
// and yt-dlp dependencies. With this struct, the only concrete
// dependencies the monitor holds are domain-shaped (cfg + the channels
// service); all AI/VTT/subprocess concerns cross through ports.
type CompositionDeps struct {
	// Cfg drives monitor-level defaults (CheckInterval, MaxConcurrentChannelChecks,
	// the global OllamaModel for fallback). The per-channel fields live in
	// channels.Channel + ChannelConfig; the monitor reads what it needs.
	Cfg *config.Config

	// ChannelsSvc is the canonical authority for category_channels.
	// ClaimDue / GetByID / MarkChecked / UpdateCursor flow through here.
	ChannelsSvc *channels.Service

	// Log is the zap logger. Cannot be nil at ctor time (panic-safe guard
	// below).
	Log *zap.Logger

	// Ytdlp is the typed port for video listing. The concrete
	// *downloader.YTDLPDownloader satisfies this; tests inject a fakeLister.
	Ytdlp MonitorDownloaderPort

	// Transcript abstracts "given a YouTube URL, return the plain-text
	// transcript". The concrete adapter owns os/exec + yt-dlp subprocess
	// invocation + temp-file lifecycle + VTT regex parsing. THIS COMMIT
	// ships a no-op unbound stub; the next commit installs the actual
	// YTDLPSubtitleAdapter under internal/application/transcripts/.
	Transcript TranscriptProvider

	// Analyzer abstracts "given a transcript, return relevance score +
	// matched keyword + category + best segments". The concrete adapter
	// owns Ollama SimpleGenerate + JSON parse + category fallback. THIS
	// COMMIT ships a no-op unbound stub; the next commit installs the
	// OllamaAnalyzer under internal/application/semantic/.
	Analyzer VideoAnalyzer

	// Enqueuer abstracts "given an analysis, build the ExtractRequest +
	// emit a job via the broker + update the channel cursor". The
	// concrete adapter owns *jobtools.Service + ActiveKey construction
	// + cursor persistence. THIS COMMIT ships a no-op unbound stub; the
	// next commit installs the real JobsEnqueuer binding.
	Enqueuer JobEnqueuer

	// Discoveries (Commit D, June 2026) is the typed port over the
	// youtube_discoveries ledger (migrations/sqlite/113_youtube_discoveries.sql
	// + internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go).
	// Composition wires the concrete adapter via Compose (lifecycle.go).
	// Optional so partial-deploy + test fixtures without a yoga-discoveries
	// migration keep compiling (nil port → defensive
	// OutcomeAlreadyScheduled classification in processVideo).
	Discoveries YoutubeDiscoveriesPort

	// Policy is the per-instance runtime configuration (TickInterval,
	// LeaseDuration, ClaimLimit, MaxConcurrentChannels,
	// MaxConcurrentVideos, PerChannelTimeout, WorkerIDPrefix,
	// BackoffInitial, BackoffCap). Nil falls back to
	// DefaultMonitorRuntimePolicy (Commit A, P1 #10 — extracted from
	// the previous scheduler.go constant block). Optional so existing
	// tests that construct ChannelMonitor by struct literal without
	// going through CompositionDeps keep working.
	Policy *MonitorRuntimePolicy
}

// ── ChannelConfig + MonitorConfig (DTO types) ────────────────────────────

// ChannelConfig represents a monitored YouTube channel.
// PR 2 (June 2026): the JSON list of channels is removed; channels are now
// loaded exclusively from category_channels via channels.Service.ListEnabled().
// This struct remains the runtime config the monitor uses per channel.
//
// NOTE (Step 9): after the port extraction, ChannelConfig fields are read
// at specific call sites rather than as a single struct on ChannelMonitor.
// Kept as a public type for documentation + back-compat with code that still
// references the field shape (no current callers do — listed here for
// future grep auditability).
type ChannelConfig struct {
	ID               string        `json:"id"`
	URL              string        `json:"url"`
	Category         string        `json:"category"`
	Keywords         []string      `json:"keywords"`
	MinViews         int           `json:"min_views"`
	MaxClipDuration  int           `json:"max_clip_duration"`
	DriveFolderID    string        `json:"drive_folder_id,omitempty"`
	PlaylistEnd      int           `json:"playlist_end,omitempty"`
	SemanticKeywords []string      `json:"semantic_keywords,omitempty"`
	MinSemanticScore int           `json:"min_semantic_score,omitempty"`
	CheckInterval    time.Duration `json:"check_interval,omitempty"`
	MaxVideosPerRun  int           `json:"max_videos_per_run,omitempty"`
	LookbackDays     int           `json:"lookback_days,omitempty"`
	MaxSegments      int           `json:"max_segments,omitempty"`
	SegmentPrompt    string        `json:"segment_prompt,omitempty"`
	Priority         int           `json:"priority,omitempty"`
}

// EffectivePriority returns the channel's priority, defaulting to normal.
func (c *ChannelConfig) EffectivePriority() int {
	if c.Priority > 0 {
		return c.Priority
	}
	return PriorityNormal
}

// MonitorConfig holds global monitor configuration (yt-dlp path, cookies, etc.).
// PR 2 (June 2026): the Channels field is removed — channels come exclusively
// from category_channels via channels.Service. This struct only holds global
// technical defaults.
//
// NOTE (Step 9): kept as a public DTO for legacy call sites; monitor/ no
// longer reads from a concrete MonitorConfig field — it derives per-channel
// behavior from channels.Channel + cfg.* globals directly.
type MonitorConfig struct {
	CheckInterval   time.Duration `json:"check_interval"`
	YtdlpPath       string        `json:"ytdlp_path"`
	CookiesPath     string        `json:"cookies_path"`
	MaxClipDuration int           `json:"max_clip_duration"`
	PlaylistEnd     int           `json:"playlist_end"`
	MaxFilesize     string        `json:"max_filesize"`
	OllamaURL       string        `json:"ollama_url"`
}

// ── ChannelCheckResult (scheduler return type) ────────────────────────────

// ChannelCheckResult is the typed payload returned by ChannelMonitor.checkChannel.
// It carries the per-check outcome so the scheduler (calculation of nextCheckTime,
// MarkChecked success/failure) can decide what to record without inspecting the
// error.
//
// VideosDiscovered = number of entries yt-dlp returned for the channel
// (regardless of subsequent filter outcome).
//
// VideosEnqueued = number of jobs posted onto the broker on the `Enqueued`
// outcome path (Commit D, June 2026). Strictly excludes
// `AlreadyScheduled` (ledger-dedupe lost the race) and `Rejected`
// (passed entry filters but failed the AI gate / MaxVideosPerRun slot);
// those flow into VideosAlreadyScheduled + VideosRejected for operator
// observability.
//
// VideosSkipped = legacy aggregate of "did NOT become a job" — preserved
// for backward-compat with the pre-Commit-D scheduler logs:
// VideosSkipped == VideosDiscovered - VideosEnqueued - VideosAlreadyScheduled
// - VideosRejected + VideosRejected (conceptually the same set, just
// split per-outcome for the new counters).
//
// VideosAlreadyScheduled = the per-video dedupe ledger's "lost the race"
// counter — a previous cycle already INSERT'd youtube_discoveries for
// these (channel_id, video_id) pairs, so this cycle classifies them as
// AlreadyScheduled and does NOT re-emit a durable job.
//
// VideosRejected = the per-video outcome for videos that passed the
// cheap lexical entry filters but failed the AI gate, hit the
// MaxVideosPerRun budget, or otherwise exited the pipeline without a
// broker record. Persisted to youtube_discoveries.outcome='rejected'
// with rejection_reason in metadata for audit.
type ChannelCheckResult struct {
	VideosDiscovered       int
	VideosEnqueued         int
	VideosSkipped          int
	VideosAlreadyScheduled int
	VideosRejected         int
}

// EnqueueOutcome is the typed label for a single video's per-cycle
// disposition (Commit D, June 2026). One of Enqueued, AlreadyScheduled,
// or Rejected. Lives in monitor/ports.go so it can be a port method
// return type without leaking ledger internals.
type EnqueueOutcome string

const (
	// OutcomeEnqueued: this cycle's TryReserve INSERT won the
	// (channel_id, video_id) race AND the durable youtube_clip.extract
	// job was successfully emitted. Only Enqueued increments the
	// canonical VideosEnqueued counter.
	OutcomeEnqueued EnqueueOutcome = "enqueued"

	// OutcomeAlreadyScheduled: this cycle's TryReserve INSERT lost
	// the race to a previous cycle's INSERT for the same
	// (channel_id, video_id). No job is emitted; the existing ledger
	// row's discovered_at still advances the cycle-end watermark.
	OutcomeAlreadyScheduled EnqueueOutcome = "already_scheduled"

	// OutcomeRejected: this cycle passed TryReserve but the post-INSERT
	// path failed (EnqueueExtract returned a non-nil error, the
	// MaxVideosPerRun slot was already filled, the semantic score was
	// below threshold, etc.). Persisted to youtube_discoveries.outcome.
	OutcomeRejected EnqueueOutcome = "rejected"
)

// ── MonitorDownloaderPort (yt-dlp slice, PR-4 DateAfter) ───────────────

// MonitorDownloaderPort is the minimum-surface slice of
// *downloader.YTDLPDownloader that the channel monitor needs.
//
//   - ListChannelVideos — used by checkChannel to enumerate a channel's
//     videos (the first step of every scheduler tick). The request
//     shape is the canonical downloader.ListChannelVideosRequest;
//     checkChannel fills DateAfter from
//     ResolveDateAfter(channel.LastCursor, channel.LookbackDays) so the
//     next cycle starts at the previous cycle's high-water mark when
//     the LastCursor is parseable, or falls back to LookbackDays when
//     the cursor is empty. Failure here is the primary signal that
//     drives the exponential backoff in scheduler.nextCheckTime.
//
// PR-4 deprecation note: the legacy ListChannel(url, limit) surface is
// REMOVED per Commit 3/6. ListChannelVideos is a strict superset (the
// ListChannelVideosRequest.ChannelURL + PlaylistEnd fields reproduce
// the old arguments; DateAfter is the new field). Tests + stubs
// must be updated to call ListChannelVideos. The downloader's
// *YTDLPDownloader.ListChannel method remains in the infra package for
// any callers outside the monitor surface (the bulk of the codebase
// routes through the YouTube pipeline, not the monitor), so the
// removal here is bounded to MonitorDownloaderPort.
//
//   - Path — historically used by semantic_matcher to build the yt-dlp
//     command line for transcript download. After Step 9, this call site
//     moves to the YTDLPSubtitleAdapter concrete (next commit). The port
//     keeps Path() because the new adapter will need it.
//
// The concrete *downloader.YTDLPDownloader already implements both methods; tests
// inject a stub that satisfies the same interface so unit tests can
// drive the failure path without spawning a real subprocess.
type MonitorDownloaderPort interface {
	ListChannelVideos(ctx context.Context, req downloader.ListChannelVideosRequest) ([]downloader.VideoInfo, error)
	Path() string
}

// ── TranscriptProvider (Step 9 new port) ──────────────────────────────────

// TranscriptProvider abstracts the "given a YouTube URL, return the
// plain-text transcript for the video" capability. The concrete adapter
// (next commit: YTDLPSubtitleAdapter at internal/application/transcripts/) owns:
//
//   - the os/exec invocation of yt-dlp with --write-auto-subs
//   - the temp-file lifecycle (MkdirTemp + defer RemoveAll)
//   - VTT regex/file parsing (regexRemoveVTTHeader, regexRemoveXMLTags)
//   - the 8000-char truncation guard before returning
//
// Per AGENTS.md Pattern 8, this port is consumed through the analyzer flow
// and the analyzer.go orchestrator owns the lifecycle. Returning a non-nil
// error short-circuits the semantic-score path (treated as "should
// skip this video", not as a retryable failure).
type TranscriptProvider interface {
	// GetTranscript returns the concatenated plain-text transcript for the
	// given videoURL. Returns an error if the subtitles are unavailable, the
	// subprocess fails, or the transcript is too short (< 10 words).
	GetTranscript(ctx context.Context, videoURL string) (transcript string, err error)

	// Fetch (Commit G, June 2026) returns the structured transcript.Document
	// (entries + text + duration + language + source). The orchestrator
	// (analyzeVideo) calls Fetch ONCE per video; the returned Document is
	// both the scored transcript (legacy flow) AND the windowed sampling
	// source for the new AnalyzeFull one-shot flow. Subsumes the
	// GetTranscript + GetTimedTranscript pair so the underlying yt-dlp
	// subprocess runs at most once per video.
	Fetch(ctx context.Context, videoURL string) (transcript.Document, error)
}

// ── VideoAnalyzer (Step 9 new port) ──────────────────────────────────────

// VideoAnalyzer abstracts the "given a transcript + channel config,
// score relevance, classify category, and extract best segments" capability.
// The concrete adapter (next commit: OllamaAnalyzer at
// internal/application/semantic/) owns:
//
//   - Ollama SimpleGenerate invocations (3 prompt templates)
//   - JSON response parsing (primary + markdown fallback via jsonRegexFind)
//   - score clamping / matched-keyword selection
//   - segment duration validation (10s .. 60s clamp)
//   - (Step 9 commit 2, June 2026: chapters fallback dropped; see ollama_analyzer.go header)
//   - OllamaModel selection from cfg.External.OllamaModel (default: "gemma4:e2b")
//
// Three methods, not three ports, because the concrete adapter shares
// Ollama client init, retry, and JSON merge — splitting into 3 ports
// would mean 3× the wiring for zero production benefit.
type VideoAnalyzer interface {
	// Score returns the semantic relevance of the transcript against the
	// keywords. Returns (score 0-100, the single best-matched keyword,
	// error). Errors here abort the per-video analyze path; they are NOT
	// retryable (already retried inside the Ollama adapter).
	Score(ctx context.Context, transcript string, keywords []string) (score int, matchedKeyword string, err error)

	// Classify picks a Drive group / category for the given title.
	// Returns (category, error). The fallback value is used if no LLM-driven
	// classification succeeds.
	Classify(ctx context.Context, title string, fallback string) (category string, err error)

	// FindSegments extracts up to maxSegments from the transcript.
	// The segmentPrompt customizes the "what makes a good clip here" guidance.
	// Returns nil if no segments meet the duration threshold (10s..60s).
	//
	// The videoURL argument is required because the analyzer constructs
	// its prompt from the timed VTT entries (which carry the [MM:SS]
	// markers Ollama needs to output timestamped segments); without the
	// URL, the analyzer cannot re-fetch the VTT. analyzer.go orchestrator
	// passes the same videoURL it already uses for GetTranscript + Score.
	FindSegments(ctx context.Context, videoURL string, transcript string, prompt string, maxSegments int) (segments []ytdomain.Segment, err error)

	// AnalyzeFull (Commit G, June 2026) is the canonical one-shot Analysis
	// entry point. Subsumes Score + Classify + FindSegments into a single
	// Ollama JSON call that returns {relevance_score, matched_keyword,
	// category, segments[]} via windowed sampling on the supplied
	// TranscriptDocument. Implementations MUST:
	//
	//   - return ErrLLMResponseInvalid when the JSON parse fails
	//     (primary + markdown fallback both fail, OR response is missing
	//     one of the 4 required fields)
	//   - bound Ollama concurrency via a pkg/concurrent.Semaphore with
	//     width = cfg.Concurrency.MaxConcurrentOllamaCalls
	//     (configurable; default 1 per the GPU-tuning PR in June 2026)
	//   - apply the score gate via opts.MinScore; segments are STILL
	//     returned when the score is below threshold so the caller can
	//     decide on the exact metric for diagnostics
	//
	// Unbound stub implementations return ErrAnalyzeFullNotImplemented
	// so the orchestrator can fall back to the legacy 3-call flow.
	// This staged-rollout shape keeps test fixtures operational
	// without the OneShot JSON path being live.
	AnalyzeFull(ctx context.Context, doc transcript.Document, opts AnalyzeOptions) (Analysis, error)
}

// ── JobEnqueuer (Step 9 new port) ────────────────────────────────────────

// JobEnqueuer abstracts the "given an analysis result, emit a
// youtube_clip.extract job + persist the channel cursor" capability.
// The concrete adapter (this commit: unbound stub; next commit: real
// *jobtools.Service binding inside monitor) owns:
//
//   - marshaling the ExtractRequest payload
//   - JobActiveKey construction ("channel_sync_<videoID>")
//   - jobsSvc.Enqueue invocation
//   - channelsSvc.UpdateCursor invocation
//   - metrics observation (ChannelMonitor* Prometheus counters)
//
// Splitting this port out of the analysis path lets the analyzer
// actually skip the enqueue logic entirely when no segments are found —
// useful for tests that drive the "transcript found, score OK, no
// segments" regression.
type JobEnqueuer interface {
	// EnqueueExtract emits the durable job for the given video + analysis.
	// Returns an error if marshal/enqueue/cursor-update fails; the caller
	// logs + swallows (the channel-monitor's contract: best-effort per
	// video, retry on next scheduler tick via the cursor).
	EnqueueExtract(ctx context.Context, req EnqueueExtractRequest) error
}

// EnqueueExtractRequest is the canonical payload shape the JobEnqueuer
// receives. Replaces the ETP-specific ExtractRequest type the
// pre-Step-9 monitor built inline at enqueueClipExtract time.
type EnqueueExtractRequest struct {
	VideoID       string
	Title         string
	URL           string
	Group         string // Drive / category group
	DriveFolderID string
	Segments      []ytdomain.Segment
	Channel       channels.Channel // back-ref for channel-level metrics + cursor
}

// Analysis is what analyzer.go returns when asked about one video.
// The Fields are the minimum the enqueue step reads: when Segments is
// empty, the enqueue step is a no-op. When Segments is
// non-empty, Group + ChannelHandle drive the Drive subfolder + metrics.
type Analysis struct {
	Score          int
	MatchedKeyword string
	Category       string
	Segments       []ytdomain.Segment
}

// ── Commit G (June 2026): single-shot Analysis port + typed sentinels ───

// ErrLLMResponseInvalid is returned by VideoAnalyzer.AnalyzeFull when
// the LLM response fails JSON validation (primary parse + markdown
// fallback both fail, or response is missing required fields like
// relevance_score / matched_keyword / category / segments[]).
//
// Patterns previously used in OllamaAnalyzer.Score / FindSegments used
// fmt.Errorf-wrapped "parse ollama response: …" strings. The typed
// sentinel lets the orchestrator (analyzeVideo) classify the failure
// without string-matching: errors.Is(err, monitor.ErrLLMResponseInvalid).
var ErrLLMResponseInvalid = errors.New("monitor: malformed LLM JSON response")

// ErrAnalyzeFullNotImplemented is returned by the unbound VideoAnalyzer
// stub's AnalyzeFull method. The orchestrator (analyzeVideo) uses
// errors.Is(err, ErrAnalyzeFullNotImplemented) to detect a non-
// upgraded analyzer and fall back to the legacy 3-call flow
// (Score + Classify + FindSegments) for staged rollout.
//
// Sentinel shape mirrors godlike/zero-baseline: defined once at the
// port surface; the stub returns it via the same path. Tests
// compile-time pin the stub to satisfy VideoAnalyzer so a signature
// drift becomes a build failure, not a runtime missing-method panic.
var ErrAnalyzeFullNotImplemented = errors.New("monitor: AnalyzeFull not implemented (fall back to legacy 3-call path)")

// AnalyzeOptions is the input shape for VideoAnalyzer.AnalyzeFull.
// Replaces the 3 positional keyword/fallback/maxSegments/segmentPrompt
// argument lists spread across the legacy Score/Classify/FindSegments
// signatures. Held as a struct because the one-shot prompt assembly
// reads 5+ fields and collisions on positional arg order are too easy
// to introduce silently.
//
// Zero-values are NOT "do nothing" — MinScore 0 means "score gate
// is disabled", MaxSegments 0 means "no segment cap (use legacy
// default 3)", SegmentPrompt "" means "use the default focus
// instruction".
type AnalyzeOptions struct {
	// SemanticKeywords is the channel's semantic-bias keywords (may
	// be empty → score gate disabled, all videos pass to FindSegments).
	SemanticKeywords []string

	// CategoryFallback is the Drive-group / category seed for the
	// LLM classify step when the LLM-driven selection fails. Should
	// not be empty in production (defaults to channel.Category at
	// the orchestrator level). Mirrors Classify's legacy fallback
	// parameter.
	CategoryFallback string

	// MaxSegments is the segment cap (max 0 = legacy default 3).
	MaxSegments int

	// SegmentPrompt is the focus instruction override. Empty = use
	// the canonical default (story beats / arguments / revelations /
	// jokes / surprises / strong emotional turns).
	SegmentPrompt string

	// MinScore is the relevance-score gate (legacy default 60 when
	// channel.MinSemanticScore is unset). 0 disables the gate.
	MinScore int
}

// ── Unbound placeholder stubs (this commit only) ──────────────────────────

// NewUnboundVideoAnalyzer returns a VideoAnalyzer that surfaces a loud
// failure on every call. This is the Step 9 placeholder until the
// OllamaAnalyzer concrete lands in internal/application/semantic/.
func NewUnboundVideoAnalyzer() VideoAnalyzer {
	return &unboundVideoAnalyzer{
		err: errors.New("monitor: video analyzer not wired (P1 follow-up installs the OllamaAnalyzer at internal/application/semantic/)"),
	}
}

type unboundVideoAnalyzer struct{ err error }

func (u *unboundVideoAnalyzer) Score(_ context.Context, _ string, _ []string) (int, string, error) {
	return 0, "", u.err
}
func (u *unboundVideoAnalyzer) Classify(_ context.Context, _, _ string) (string, error) {
	return "", u.err
}
func (u *unboundVideoAnalyzer) FindSegments(_ context.Context, _, _, _ string, _ int) ([]ytdomain.Segment, error) {
	return nil, u.err
}

// AnalyzeFull on the unbound stub returns ErrAnalyzeFullNotImplemented
// so the orchestrator (analyzeVideo) can detect a non-upgraded analyzer
// and fall back to the legacy 3-call path without throwing a loud
// "Loud failure on every call" panic per the diagnostic-style stub.
func (u *unboundVideoAnalyzer) AnalyzeFull(_ context.Context, _ transcript.Document, _ AnalyzeOptions) (Analysis, error) {
	return Analysis{}, ErrAnalyzeFullNotImplemented
}

// NewUnboundJobEnqueuer returns a JobEnqueuer that surfaces a loud
// failure on every call. This is the Step 9 placeholder until the
// concrete *jobtools.Service binding is built in the next commit
// (the binding itself is straightforward: EnqueueExtract marshals
// ExtractRequest, calls jobsSvc.Enqueue with ActiveKey
// "channel_sync_<videoID>", and updates the channel cursor).
func NewUnboundJobEnqueuer() JobEnqueuer {
	return &unboundJobEnqueuer{
		err: errors.New("monitor: job enqueuer not wired (P1 follow-up installs the binding in internal/app/lifecycle.go or via the next PR-PORTS-2 commit)"),
	}
}

type unboundJobEnqueuer struct{ err error }

func (u *unboundJobEnqueuer) EnqueueExtract(_ context.Context, _ EnqueueExtractRequest) error {
	return u.err
}

// ── Compile-time assertions (Pattern 0 invariant) ─────────────────────────

// Production compile-time assertion: *downloader.YTDLPDownloader must
// satisfy MonitorDownloaderPort. Signature drift becomes a build failure
// here, not a runtime panic — the canonical Pattern 0 invariant from
// AGENTS.md godlike/06 §"Database and config ownership".
var _ MonitorDownloaderPort = (*downloader.YTDLPDownloader)(nil)

// Compile-time assertions: every unbound placeholder must satisfy its
// own port. These intentionally fail to compile if the NextPageOffset
// port methods change signature without also updating the unbound stub.
var _ VideoAnalyzer = (*unboundVideoAnalyzer)(nil)
var _ JobEnqueuer = (*unboundJobEnqueuer)(nil)

// ── CategoryChannelsPort (Commit G migration adapter, June 2026) ────────

// CategoryChannelsPort decouples monitor from internal/application/channels
// (the Service internal). Pattern 0 invariant: monitor never imports the
// channels.Service directly \u2014 it consumes this typed port that exposes
// only the methods checkChannel / recordCheckOutcome / processVideo
// actually need. The concrete adapter (a future commit, or the existing
// channels.Service wrapped via CategoryChannelsAdapter) implements this
// surface; tests inject a stub.
//
// Migration rationale: the pre-Commit-G monitor held *channels.Service
// directly, leaking the Service's full method canon (Upsert, Delete,
// ClaimDue, ListAll, ListCategories, etc.) into the monitor package
// even though monitor only reads 3 methods. The Port narrows the
// surface; future Policy / WorkerPort / CallerIdPort additions will
// follow the same Pattern 0.
//
// Methods exposed:
//   - ListEnabled: the canonical pre-tick channel enumeration
//   - MarkChecked: persists the post-tick outcome (Success + NextCheckAt
//   - LastError). Replaces channels.Service.MarkChecked.
//   - UpdateCursor: persists the cycle-end watermark. Replaces
//     channels.Service.UpdateCursor.
//
// Note: an `Enabled: true` Filter list is intentional. ListEnabled()
// replaces the pre-Port *channels.Service.ListEnabled() call.
type CategoryChannelsPort interface {
	ListEnabled(ctx context.Context) ([]*asset.CategoryChannel, error)
	MarkChecked(ctx context.Context, cmd channels.MarkCheckedCommand) error
	UpdateCursor(ctx context.Context, cmd channels.UpdateCursorCommand) error
}

// YoutubeDiscoveriesPort is the typed surface the channel monitor reads
// against the youtube_discoveries ledger (table created in
// migrations/sqlite/114_youtube_discoveries_v2.sql; v2 RETIRES the
// 113 schema via clean-break table swap. Infra adapter in
// internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go).
//
// The ledger implements the canonical leader-election-by-INSERT dedupe
// pattern + the Commit 3/6 retryable state machine (P1 #5 + #6 + #7):
//   - The per-video worker calls TryReserve BEFORE EnqueueExtract.
//     The UNIQUE(channel_id, video_id, policy_version) constraint means
//     only one goroutine's INSERT effects a row insert; the rest get
//     won=false and classify outcome as AlreadyScheduled.
//   - policyVersion differentiates ledger rows so a policy_version bump
//     (e.g. transcript segmentation logic v2) produces a fresh row
//     alongside the historical v1 row — both coexist for audit, only
//     the new policy_version is in the active TryReserve+drain loop.
//   - retryable rejections (transient timeout / 429) carry
//     next_retry_at + attempt_count and re-enter TryReserve when
//     next_retry_at <= now (the canonical retry-eligibility rule).
//   - On a successful EnqueueExtract, the same worker calls
//     MarkEnqueued to flip the row's state to 'enqueued'.
//   - On a rejected path (MarkRejected(retryable=true) for transient
//     errors, retryable=false for terminal), the row is recorded with
//     the rejection_reason + last_error.
//   - At cycle end, the defer in checkChannel reads MaxDiscoveredAt
//     (state-filtered watermark) and persists it as
//     category_channels.last_cursor.
//
// The port is the typed projection of the SQLite adapter; tests inject
// a stub that counts TryReserve/MarkEnqueued/MarkRejected invocations
// for the 5-videos × 2-invocations dedupe contract.
type YoutubeDiscoveriesPort interface {
	// TryReserve performs the leader-election INSERT with
	// ON CONFLICT(channel_id, video_id, policy_version) DO NOTHING
	// RETURNING id. Returns:
	//   - (id, won=true,  attempt+1, nil) on a fresh win → caller
	//     proceeds to emit a durable job.
	//   - (id, won=true,  attempt+1, nil) on a retryable-lease reclaim
	//     OR a retryable-retry path → caller proceeds to re-emit on
	//     the same ledger row (attempt_count incremented).
	//   - (id, won=false, attempt, nil) on already-scheduled (terminal
	//     state: 'enqueued' / 'completed' / etc.) → caller classifies
	//     OutcomeAlreadyScheduled and skips broker emit.
	// policyVersion is required ("" defaults to "v1"). Empty
	// channelID+videoID is a hard validation error.
	TryReserve(ctx context.Context, channelID, videoID, policyVersion, sourceURL, title, discoveredAt string) (id string, won bool, attempt int, err error)

	// MarkEnqueued flips the row from pending → enqueued. Idempotent
	// on repeat (a row with state='enqueued' stays 'enqueued').
	MarkEnqueued(ctx context.Context, id, enqueuedAt string) error

	// MarkRejected records an explicit rejection outcome. retryable=true
	// → state='rejected_retryable', next_retry_at = now + exponential
	// backoff(attempt_count), attempt_count+=1, last_error pinned.
	// retryable=false → state='rejected_terminal', last_error pinned,
	// no retry. Caller (enqueue.go) computes retryable from a typed
	// isTransientErr predicate, so the repository stays pure
	// (no domain error knowledge leaked into persistence).
	MarkRejected(ctx context.Context, id, rejectionReason string, retryable bool) error

	// MaxDiscoveredAt returns the largest discovered_at for the
	// channel across ALL terminal states (enqueued, completed,
	// already_scheduled, rejected_terminal, rejected_retryable).
	// Excludes 'pending'/'analyzing' so an in-progress cycle's partial
	// row doesn't leak a non-monotonic watermark. Cycle-end
	// canonical-write path. Empty string for an empty ledger.
	MaxDiscoveredAt(ctx context.Context, channelID string) (string, error)
}
