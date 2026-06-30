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
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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
// the pre-Step-9 7-parameter signature (cfg, clipsRepo, channelsSvc,
// log, youtubeSvc, ollamaClient, ytdlp) which exposed concrete Ollama
// and yt-dlp dependencies. With this struct, the only concrete
// dependencies the monitor holds are domain-shaped (cfg + repos +
// services + log); all AI/VTT/subprocess concerns cross through ports.
type CompositionDeps struct {
	// Cfg drives monitor-level defaults (CheckInterval, MaxConcurrentChannelChecks,
	// the global OllamaModel for fallback). The per-channel fields live in
	// channels.Channel + ChannelConfig; the monitor reads what it needs.
	Cfg *config.Config

	// ClipsRepo is kept for forward-compat with the channels cursor view
	// (currently unused by the scheduler; reserved for future per-clip
	// discovery flows).
	ClipsRepo *assets.ClipsRepository

	// ChannelsSvc is the canonical authority for category_channels.
	// ClaimDue / GetByID / MarkChecked / UpdateCursor flow through here.
	ChannelsSvc *channels.Service

	// YoutubeSvc is the per-channel youtube clip service. Currently held
	// for forward-compat; not yet consumed by the discovery or analyzer
	// paths. The asset-side enrichment goes through VideoAnalyzer.FindSegments
	// instead.
	YoutubeSvc *youtube.Service

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
// VideosEnqueued = number of jobs posted onto the broker
// (i.e. extracted + segments + accepted by the per-channel MaxVideosPerRun
// budget).
//
// VideosSkipped = number of entries that did NOT become a job, split sum:
// already-processed ActiveKey, below min_views, exceeded max duration,
// title-keyword miss, semantic-budget reached, Ollama score below threshold.
// The per-reason breakdown lives in the structured log stream
// (Debug-level lines on processVideo).
type ChannelCheckResult struct {
	VideosDiscovered int
	VideosEnqueued   int
	VideosSkipped    int
}

// ── MonitorDownloaderPort (the legacy yt-dlp slice) ──────────────────────

// MonitorDownloaderPort is the minimum-surface slice of
// *downloader.YTDLPDownloader that the channel monitor needs.
//
//   - ListChannel — used by checkChannel to enumerate a channel's
//     videos (the first step of every scheduler tick). Failure here is
//     the primary signal that drives the exponential backoff in
//     scheduler.nextCheckTime.
//   - Path — historically used by semantic_matcher to build the yt-dlp
//     command line for transcript download. After Step 9, this call site
//     moves to the YTDLPSubtitleAdapter concrete (next commit). The port
//     keeps Path() because the new adapter will need it.
//
// The concrete *downloader.YTDLPDownloader already implements both methods; tests
// inject a stub that satisfies the same interface so unit tests can
// drive the failure path without spawning a real subprocess.
type MonitorDownloaderPort interface {
	ListChannel(ctx context.Context, channelURL string, limit int) ([]downloader.VideoInfo, error)
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
//   - chapter-fallback logic via yt-dlp metadata (subtitles miss → chapters)
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
	FindSegments(ctx context.Context, transcript string, prompt string, maxSegments int) (segments []ytdomain.Segment, err error)
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

// ── Unbound placeholder stubs (this commit only) ──────────────────────────

// NewUnboundTranscriptProvider returns a TranscriptProvider that surfaces a
// loud failure on every call. This is the Step 9 placeholder until the
// YTDLPSubtitleAdapter concrete lands in internal/application/transcripts/.
//
// The error is deliberately typesafe (operators can grep the operator
// log for "transcript provider not wired" to identify this exact gap).
func NewUnboundTranscriptProvider() TranscriptProvider {
	return &unboundTranscriptProvider{
		err: errors.New("monitor: transcript provider not wired (P1 follow-up installs the YTDLPSubtitleAdapter at internal/application/transcripts/)"),
	}
}

type unboundTranscriptProvider struct{ err error }

func (u *unboundTranscriptProvider) GetTranscript(_ context.Context, _ string) (string, error) {
	return "", u.err
}

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
func (u *unboundVideoAnalyzer) FindSegments(_ context.Context, _, _ string, _ int) ([]ytdomain.Segment, error) {
	return nil, u.err
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
var _ TranscriptProvider = (*unboundTranscriptProvider)(nil)
var _ VideoAnalyzer = (*unboundVideoAnalyzer)(nil)
var _ JobEnqueuer = (*unboundJobEnqueuer)(nil)
