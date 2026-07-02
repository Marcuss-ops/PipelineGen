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
//   - ChannelCheckResult + Analysis + EnqueueExtractRequest — DTO types used across files.
//   - The compile-time assertions pinning the concrete YTDLPDownloader
//     satisfies MonitorDownloaderPort.
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

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	transcript "github.com/Marcuss-ops/PipelineGen/internal/domain/transcript"
	assetsdb "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── VideoInfo (monitor-owned DTO) ──────────────────────────────────────

// VideoInfo is the monitor-owned projection of a YouTube video's
// listing metadata. Replaces downloader.VideoInfo so the monitor never
// leaks an infrastructure DTO through its port surfaces or internal
// call paths. The concrete *downloader.YTDLPDownloader maps its native
// []downloader.VideoInfo to []VideoInfo inside the adapter.
type VideoInfo struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Views    int64   `json:"view_count"`
	Duration float64 `json:"duration"`
}

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

// ── ActiveKey (canonical channel-sync prefix) ───────────────────────────

// ActiveKeyPrefix is the canonical job.ActiveKey prefix for
// channel-sync extraction jobs. Per the Channel Monitor Step 9 design
// spec: every durable extraction job enqueued by the Channel Monitor
// uses "channel_sync_<VideoID>" so the broker's per-ActiveKey
// idempotency dedupes across the monitor's per-tick retry window.
//
// Exported so future tooling (admin CLIs, bulk re-enqueue scripts,
// the Wave 15 remote-worker fallback path for channel-sync) can
// re-use the same prefix without duplicating the literal.
//
// Fase 8 (July 2026, Spina Dorsale — monitoradapter consolidation):
// relocated from internal/application/assets/monitor/extraction_enqueuer.go
// to the canonical port surface package (here) so the const + the
// monitor.JobEnqueuer port + the ExtractionSegment alias declaration
// share the same canonical ownership boundary (monitor capability).
const ActiveKeyPrefix = "channel_sync_"

// ── CompositionDeps (the new ctor signature) ─────────────────────────────

// CompositionDeps is the ctor payload for NewChannelMonitor. Replaces
// the pre-Step-9 7-parameter signature which exposed concrete Ollama
// and yt-dlp dependencies. With this struct, the only concrete
// dependencies the monitor holds are domain-shaped (cfg + the channels
// service); all AI/VTT/subprocess concerns cross through ports.
type CompositionDeps struct {
	// Cfg drives monitor-level defaults (CheckInterval, MaxConcurrentChannelChecks,
	// the global OllamaModel for fallback). The per-channel fields live in
	// channels.Channel; the monitor reads what it needs.
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
	// transcript". The concrete adapter (YTDLPSubtitleAdapter at
	// internal/application/transcripts/) owns os/exec + yt-dlp subprocess
	// invocation + temp-file lifecycle + VTT regex parsing.
	Transcript TranscriptProvider

	// Analyzer abstracts "given a transcript, return relevance score +
	// matched keyword + category + best segments". The concrete adapter
	// (OllamaAnalyzer at internal/application/semantic/) owns Ollama
	// SimpleGenerate + JSON parse + category fallback.
	Analyzer VideoAnalyzer

	// Enqueuer abstracts "given an analysis, build the ExtractRequest +
	// emit a job via the broker + update the channel cursor". The
	// concrete adapter (*ExtractionEnqueuer) owns *jobtools.Service +
	// ActiveKey construction + cursor persistence.
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
//
// InfraFailures = count of per-video infra errors (panics, analyzer
// timeouts, SQLite errors, broker failures) within the cycle. Distinct
// from policy rejections; a non-zero InfraFailures signals degraded
// infra health independent of the channel's policy config.
// Blocco 3a (July 2026).
type ChannelCheckResult struct {
	VideosDiscovered       int
	VideosEnqueued         int
	VideosSkipped          int
	VideosAlreadyScheduled int
	VideosRejected         int
	InfraFailures          int
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

	// OutcomeInfraFailure: TryReserve itself failed (SQLite error).
	// Counts as both Rejected + InfraFailure — the video couldn't be
	// processed due to infra, not policy. Blocco 3a (July 2026).
	OutcomeInfraFailure EnqueueOutcome = "infra_failure"
)

// ── MonitorDownloaderPort (yt-dlp slice, PR-4 DateAfter) ───────────────

// MonitorDownloaderPort is the minimum-surface slice of
// *downloader.YTDLPDownloader that the channel monitor needs.
//
//   - ListChannelVideos — used by checkChannel to enumerate a channel's
//     videos (the first step of every scheduler tick). The request
//     shape is the canonical downloader.ListChannelVideosRequest;
//     returns monitor-owned VideoInfo (not downloader.VideoInfo) so
//     the monitor package never leaks infrastructure DTOs.
//
//   - Path — the yt-dlp binary path, consumed by the transcript adapter.
type MonitorDownloaderPort interface {
	ListChannelVideos(ctx context.Context, req downloader.ListChannelVideosRequest) ([]VideoInfo, error)
	Path() string
}

// ── TranscriptProvider (Step 9 port, cutover completed Step 6) ──────────

// TranscriptProvider abstracts the "given a YouTube URL, return the
// structured transcript" capability. The concrete adapter
// (YTDLPSubtitleAdapter at internal/application/transcripts/) owns
// os/exec + yt-dlp subprocess invocation + temp-file lifecycle + VTT
// parsing.
//
// Fetch is the single canonical method. The legacy GetTranscript
// method was removed in Step 6 (June 2026) when the analyzer was
// cut over to the one-shot AnalyzeFull flow.
type TranscriptProvider interface {
	// Fetch returns the structured transcript.Document (entries + text +
	// duration + language + source). Called ONCE per video by the
	// orchestrator (analyzeVideo); the returned Document is passed
	// directly to VideoAnalyzer.AnalyzeFull for one-shot scoring.
	Fetch(ctx context.Context, videoURL string) (transcript.Document, error)
}

// ── VideoAnalyzer (Step 9 port, cutover completed Step 6) ───────────────

// VideoAnalyzer abstracts the "given a transcript document + channel
// config, score relevance, classify category, and extract best segments"
// capability through a single one-shot call. The concrete adapter
// (OllamaAnalyzer at internal/application/semantic/) owns Ollama
// prompt assembly, JSON parsing, and segment duration validation.
//
// The legacy Score / Classify / FindSegments methods were removed in
// Step 6 (June 2026) when the orchestrator was cut over to the
// one-shot AnalyzeFull flow.
type VideoAnalyzer interface {
	// AnalyzeFull is the canonical one-shot Analysis entry point.
	// Subsumes the legacy Score + Classify + FindSegments into a
	// single Ollama JSON call that returns {relevance_score,
	// matched_keyword, category, segments[]} via windowed sampling
	// on the supplied TranscriptDocument. Implementations MUST:
	//
	//   - return ErrLLMResponseInvalid when the JSON parse fails
	//   - bound Ollama concurrency via a pkg/concurrent.Semaphore
	//   - apply the score gate via opts.MinScore; segments are STILL
	//     returned when the score is below threshold so the caller
	//     can decide on the exact metric for diagnostics
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

// ── Fase 8 DTO contract lock (July 2026) ───────────────────────────────────
//
// ExtractionSegment is the canonical monitor-package segment alias
// for ytdomain.Segment (godlike/06 "one canonical owner per fact" —
// the monitor package does NOT define its own segment shape; it
// RELAYS the dto.Segment shape verbatim). JSON-tag shape + field
// count is determined by the dto.Segment struct definition (see
// extraction_intent_test.go::TestExtractionSegment_FieldParityWithYtdomainSegment
// for the audit-pin lock).
//
// Note: a pre-Fase-8 alias `EnqueueExtractSegment` was introduced
// in an earlier draft but proved gratuitous (zero pre-Fase-8
// callers in the codebase per `rg EnqueueExtractSegment`); dropped
// to honour godlike/06 (no orphan aliases — one canonical name per
// type). The sole canonical segment name in monitor is now
// ExtractionSegment.
//
// Analysis.Segments (above) already declares []ytdomain.Segment for
// back-compat — ExtractionSegment is the SAME type via alias, so
// Analysis.Segments assetions stay green without an explicit retype.
type ExtractionSegment = ytdomain.Segment

// ExtractionIntent is the Fase 8 canonical extraction-enqueue
// intent payload. Replaces the legacy EnqueueExtractRequest struct
// shape (which lacked JSON tags) with a wire-stable, snake_case
// JSON tag surface that mirrors ytdomain.Segment's tag style. The
// channel-monitor's bind path (ExtractionIntentAdapter at
// internal/application/youtube/adapters/monitoradapter/) emits this
// shape; the broker's job-handler deserializes it; the bloop of
// lock-test asserts byte-equivalence under json.Marshal.
//
// Wire-format note (godlike/07 honest-limitation, deliberate Fase 8
// break): pre-Fase-8 the same shape was an untagged struct that
// marshalled with default Go names + the embedded Channel; the
// new shape uses snake_case top-level tags + drops Channel via
// `json:"-"`. Any caller that JSON-round-trips the struct (broker
// payload, monitoradapter bind, job-handler decode) sees a different
// wire shape post-Fase-8. The downstream caller must re-bind on
// ExtractionIntent (the apply-leg type-alias preserves compile-time
// compatibility; the wire-format shift is runtime-only). The new
// Channel `json:"-"` gate is symmetric: marshal-side omits the field
// AND unmarshal-side leaves the field at zero-value, so a downstream
// reader cannot observe the legacy Channel state via JSON.
type ExtractionIntent struct {
	VideoID       string             `json:"video_id"`
	Title         string             `json:"title"`
	URL           string             `json:"url"`
	Group         string             `json:"group"`
	DriveFolderID string             `json:"drive_folder_id"`
	Segments      []ExtractionSegment `json:"segments"`
	Channel       channels.Channel   `json:"-"` // opaque in monitor; back-ref only for metrics + cursor
}

// EnqueueExtractRequest is the apply-leg type alias of ExtractionIntent
// (Fase 8 DTO contract lock). Pre-Fase-8 callers used
// EnqueueExtractRequest as a struct literal; post-Fase-8 the same name
// resolves to the canonical ExtractionIntent via the = alias so
// the call-site surface is unchanged. The alias is bidirectional:
// `var _ ExtractionIntent = EnqueueExtractRequest{}` and the inverse
// both compile (see extraction_intent_test.go::TestEnqueueExtractRequest_TypeAliasResolution).
type EnqueueExtractRequest = ExtractionIntent

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

// ErrAnalyzeFullNotImplemented is returned by AnalyzeFull when the
// concrete analyzer has not yet been upgraded to the one-shot JSON
// AnalyzeFull flow (Commit G follow-up, June 2026). The orchestrator
// (analyzeVideo) detects this sentinel and falls back to the legacy
// Score + Classify + FindSegments 3-call path.
var ErrAnalyzeFullNotImplemented = errors.New("monitor: AnalyzeFull not implemented on concrete adapter")

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
// Blocco 3 (July 2026): adds the outbox surface. CommitEnqueueOutbox
// atomically runs MarkEnqueued + INSERT into monitor_enqueue_outbox
// in a single SQLite transaction, eliminating the torn-write path
// between broker emit and ledger update (audit P0 #2). The outbox
// drainer (startOutboxDrainer in scheduler.go) polls
// DrainPendingOutbox and dispatches entries to the durable-jobs
// broker.
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

	// ── Blocco 3 outbox surface ──────────────────────────────────

	// CommitEnqueueOutbox atomically marks the discovery as enqueued
	// AND inserts a pending outbox entry. The outbox drainer picks up
	// the entry and dispatches it to the durable-jobs broker. Returns
	// nil on idempotent retry (duplicate idempotency_key).
	CommitEnqueueOutbox(ctx context.Context, discoveryID, enqueuedAt, idempotencyKey, payloadJSON string) error

	// DrainPendingOutbox returns up to limit pending outbox entries.
	DrainPendingOutbox(ctx context.Context, limit int) ([]assetsdb.OutboxEntry, error)

	// MarkOutboxDispatched marks an outbox entry as dispatched.
	MarkOutboxDispatched(ctx context.Context, outboxID int64, jobID string) error

	// MarkOutboxFailed marks an outbox entry as failed.
	MarkOutboxFailed(ctx context.Context, outboxID int64, errMsg string) error
}
