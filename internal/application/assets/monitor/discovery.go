// Package monitor — discovery.go: cheap per-video discovery + filter chain
// + ledger-backed dedupe (Commit D, June 2026, PR-D YouTube Channel Monitor
// cutover, Blocco 7).
//
// Step 9 (June 2026): the package is exactly 5 production files
// (scheduler.go, discovery.go, analyzer.go, enqueue.go, ports.go).
// Commit D adds the youtube_discoveries ledger dance without changing
// file count. This file owns:
//
//   - discoverChannelVideos: a thin wrapper over the MonitorDownloaderPort
//     that decides the per-channel PlaylistEnd (DefaultPlaylistEnd fallback).
//   - discoverSearchQueries: a STUB that returns nil.
//   - checkChannel: orchestrates the per-cycle per-channel check. List
//     videos → fan out bounded goroutines → per-video processVideo runs
//     the cheap lexical filter chain + the leader-election INSERT dance.
//     The cycle-end defer (Commit D) reads MAX(discovered_at) from the
//     youtube_discoveries ledger and persists it as
//     category_channels.last_cursor — the column is repurposed from
//     "last video id" to "RFC3339 timestamp of the high-water mark".
//     This replaces the pre-Commit-D per-video UpdateCursor (contract 3
//     best-effort degrade) with a single, durable, monotonic write that
//     survives SQLite transient errors.
//   - processVideo: per-video dispatch. Cheap lexical filters
//     (MinViews / MaxClipDuration / title-keyword) + the AI gate
//     (analyzer.go) + MaxVideosPerRun CAS + the ledger TryReserve dance
//     + EnqueueExtract + outcome classification. The outcome
//     (enqueued | already_scheduled | rejected) increments exactly one
//     of three atomic counters; only enqueued increments VideosEnqueued.
//
// This file NEVER imports os/exec, the OllamaClient, or VTT regex
// helpers — those concerns moved out under the TranscriptProvider /
// VideoAnalyzer / JobEnqueuer ports.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// effectivePlaylistEnd picks the playlist-end limit for the caller's
// yt-dlp invocation: per-channel override > global default > 0 (which
// means "no limit").
func effectivePlaylistEnd(channel channels.Channel, globalDefault int) int {
	if channel.PlaylistEnd > 0 {
		return channel.PlaylistEnd
	}
	if channel.PlaylistEnd == 0 {
		return 0
	}
	return globalDefault
}

// discoverChannelVideos returns the unfiltered list of videos for a
// channel. Pure read call (no AI, no subprocess); cheap lexical filtering
// happens per-video in checkChannel's worker loop. Returns (videos, err)
// where err is non-nil ONLY for yt-dlp infra failures (subprocess / parse).
//
// A nil ytdlp port surfaces as a loud error here rather than a panic
// because nothing further down can succeed without a real ListChannel
// result — failing fast at the discovery layer avoids silent zero-result
// channels that would otherwise be reported as Success=true.
func (m *ChannelMonitor) discoverChannelVideos(ctx context.Context, channel channels.Channel) ([]downloader.VideoInfo, error) {
	if m.ytdlp == nil {
		return nil, fmt.Errorf("discoverChannelVideos: ytdlp port not wired")
	}
	playlistEnd := effectivePlaylistEnd(channel, DefaultPlaylistEnd)
	return m.ytdlp.ListChannel(ctx, channel.ChannelURL, playlistEnd)
}

// QueryResult is the canonical return-type for the (currently stubbed)
// discoverSearchQueries. Defined for cross-Step-9 type stability: any
// future P1 PR re-introducing search-query-driven discovery must
// return QueryResult, not invent a new shape.
type QueryResult struct {
	Query  string
	Videos []downloader.VideoInfo
}

// discoverSearchQueries is a Step 9 STUB. The previous search_queries.go
// (channel-by-search-query discovery) was removed earlier in the working
// tree as part of the Wave A simplification; this stub keeps the signature
// in the type system so a future P1 PR can fill it in without changing
// the public API. Returns (nil, nil) so existing callers (none currently
// — this is purely a future-proofing step) can no-op safely.
func (m *ChannelMonitor) discoverSearchQueries(_ context.Context, _ string) ([]QueryResult, error) {
	m.log.Debug("discoverSearchQueries: stub (search-query-driven discovery not re-introduced in Step 9; see P1 follow-up ticket)")
	return nil, nil
}

// checkChannel runs the per-cycle loop with bounded concurrency.
//
// Returns (ChannelCheckResult, error):
//   - err is non-nil ONLY when the yt-dlp structured listing itself failed
//     (network, parse, subprocess error, etc.). In-process filter
//     rejections (below min_views, title-keyword miss, semantic budget
//     exhaustion, semantic score below threshold, ledger-dedupe loss)
//     count toward VideosAlreadyScheduled / VideosRejected and produce
//     a nil error: the check itself succeeded; those rejections are
//     policy, not infra. Conflating them would trigger a backoff after
//     every policy rejection, which is wrong.
//
//   - VideosSkipped = VideosDiscovered - VideosEnqueued
//     - VideosAlreadyScheduled - VideosRejected: legacy aggregate of
//     "did NOT become a job that we recorded as enqueued". Kept for
//     back-compat with pre-Commit-D scheduler logs; the new counters
//     split it per-outcome for operator observability.
//
// Commit D (June 2026, PR-D YouTube Channel Monitor cutover): the cycle
// end runs a defer that updates category_channels.last_cursor to
// MAX(discovered_at) of the youtube_discoveries ledger for this channel.
// The previous per-video UpdateCursor call (extraction_enqueuer.go
// contract 2 + 3) is REMOVED; the single cycle-end write is durable
// at the table level (no per-row best-effort degrade), so a SQLite
// transient error no longer silently re-discovers the same videos on
// the next cycle.
func (m *ChannelMonitor) checkChannel(ctx context.Context, channel channels.Channel) (ChannelCheckResult, error) {
	videos, err := m.discoverChannelVideos(ctx, channel)
	if err != nil {
		m.log.Error("Failed to fetch channel videos", zap.String("url", channel.ChannelURL), zap.Error(err))
		return ChannelCheckResult{}, fmt.Errorf("list channel %q: %w", channel.ChannelURL, err)
	}

	m.log.Info("Fetched channel videos", zap.String("url", channel.ChannelURL), zap.Int("count", len(videos)))

	// Commit D (June 2026, P1 #10 cycle-end watermark): when the cycle
	// completes (success OR error-wg.Wait still drains), read
	// MAX(discovered_at) and persist it as category_channels.last_cursor.
	// Idempotent and monotonic: re-runs converge on the same value;
	// transient SQLite errors are surfaced to the operator as a real
	// per-cycle failure (was previously a silent contract-3 degrade).
	//
	// The defer ALSO fires when videos is empty so a no-discovery cycle
	// still records the previous watermark (whichever survives the MAX
	// query). This matches the pre-Commit-D UpdateCursor-omitted-when-empty
	// behaviour of processVideo's per-video fan-out.
	cycleNow := time.Now().UTC().Format(time.RFC3339)
	defer m.recordCycleEndWatermark(ctx, channel, cycleNow)

	// Commit A: inner per-video goroutine concurrency comes from the
	// typed MonitorRuntimePolicy (was previously hardcoded to 5 inline).
	// policy.MaxConcurrentVideos bounds the parallel processVideo
	// fan-out per channel.
	concurrency := m.policyOrDefault().MaxConcurrentVideos
	if concurrency <= 0 {
		concurrency = DefaultMonitorRuntimePolicy().MaxConcurrentVideos
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	// Commit D: replace the legacy acceptedCount (single CAS counter)
	// with a typed outcome-counter triple: each video classifies
	// into exactly one of enqueued | already_scheduled | rejected.
	var outcomes outcomeCounters

	for _, video := range videos {
		video := video
		if channel.MaxVideosPerRun > 0 && (outcomes.enqueued.Load()+outcomes.rejected.Load()) >= int32(channel.MaxVideosPerRun) {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() { if r := recover(); r != nil {
				m.log.Error("panic in video processing worker", zap.Any("recover", r), zap.String("video_id", video.ID))
			} }()
			if channel.MaxVideosPerRun > 0 && (outcomes.enqueued.Load()+outcomes.rejected.Load()) >= int32(channel.MaxVideosPerRun) {
				return
			}
			m.processVideo(ctx, video, channel, &outcomes, cycleNow)
		}()
	}
	wg.Wait()

	enqueued := int(outcomes.enqueued.Load())
	rejected := int(outcomes.rejected.Load())
	already := int(outcomes.alreadyScheduled.Load())
	return ChannelCheckResult{
		VideosDiscovered:       len(videos),
		VideosEnqueued:         enqueued,
		VideosAlreadyScheduled: already,
		VideosRejected:         rejected,
		VideosSkipped:          len(videos) - enqueued - already - rejected,
	}, nil
}

// recordCycleEndWatermark persists MAX(discovered_at) for the channel
// as category_channels.last_cursor. Triggered by the defer in
// checkChannel; safe to run on an empty ledger (writes the empty
// string when MAX returns no rows).
//
// Idempotent + monotonic: re-running the cycle for the same channel
// converges on the same value (or strictly advances if new discoveries
// occurred). Transient SQLite errors are logged + swallowed so a
// cursor-write failure does NOT propagate to the caller (the cycle
// itself succeeded; the cursor is best-effort durability).
func (m *ChannelMonitor) recordCycleEndWatermark(ctx context.Context, channel channels.Channel, cycleNow string) {
	if m.discoveries == nil {
		m.log.Debug("recordCycleEndWatermark: discoveries port not wired, skipping cursor update", zap.String("channel_id", channel.ID))
		return
	}
	if m.channelsSvc == nil {
		m.log.Warn("recordCycleEndWatermark: channelsSvc not wired, skipping cursor update", zap.String("channel_id", channel.ID))
		return
	}
	watermark, err := m.discoveries.MaxDiscoveredAt(ctx, channel.ID)
	if err != nil {
		m.log.Warn("recordCycleEndWatermark: MaxDiscoveredAt failed (best-effort cycle-end durability)",
			zap.String("channel_id", channel.ID),
			zap.Error(err))
		return
	}
	if watermark == "" {
		// Empty ledger: don't degrade category_channels.last_cursor to
		// empty (would rewind the cursor for the next cycle's
		// per-row filter). Skip silently.
		m.log.Debug("recordCycleEndWatermark: empty ledger, skipping cursor write",
			zap.String("channel_id", channel.ID),
			zap.String("cycle_now", cycleNow))
		return
	}
	if err := m.channelsSvc.UpdateCursor(ctx, channels.UpdateCursorCommand{
		ID:     channel.ID,
		Cursor: watermark,
	}); err != nil {
		m.log.Warn("recordCycleEndWatermark: UpdateCursor failed (best-effort cycle-end durability)",
			zap.String("channel_id", channel.ID),
			zap.String("watermark", watermark),
			zap.Error(err))
		return
	}
	m.log.Debug("recordCycleEndWatermark: cursor updated",
		zap.String("channel_id", channel.ID),
		zap.String("watermark", watermark))
}

// outcomeCounters is the per-cycle aggregate triple (Commit D):
// committed-to-broker jobs (enqueued), leader-election INSERT losers
// (already_scheduled), and post-INSERT failures (rejected). Each is
// a separate atomic counter so dedupe lost vs. filter-rejected are
// distinguishable in operators' dashboards.
type outcomeCounters struct {
	enqueued         atomic.Int32
	alreadyScheduled atomic.Int32
	rejected         atomic.Int32
}

// processVideo runs the per-video flow:
//   - cheap lexical filter chain (MinViews → MaxClipDuration → title-keyword)
//     — early-returns without consuming a budget slot.
//   - analyzer gate (delegated to analyzer.go via TranscriptProvider +
//     VideoAnalyzer ports). On skip/error, the slot is NOT consumed.
//   - tryReserve (atomic CAS) — only after passing the AI gate.
//   - ledger TryReserve (leader-election INSERT). If !won, return
//     AlreadyScheduled (no broker record this cycle).
//   - enqueueExtract (delegated to enqueue.go via the JobEnqueuer port).
//     On success, MarkEnqueued; on error, MarkRejected.
//   - Outcome counter update: exactly one of three counters incremented.
func (m *ChannelMonitor) processVideo(ctx context.Context, info downloader.VideoInfo, channel channels.Channel, outcomes *outcomeCounters, cycleNow string) {
	videoID := info.ID
	title := info.Title
	m.log.Debug("Found video", zap.String("video_id", videoID), zap.String("title", title))

	// ── Cheap lexical filters (Step 9: stays here) ────────────────────
	if channel.MinViews > 0 && info.Views < int64(channel.MinViews) {
		m.log.Debug("video below min_views, skipping",
			zap.String("video_id", videoID),
			zap.Int64("views", info.Views),
			zap.Int("min_views", channel.MinViews))
		return
	}

	if channel.MaxClipDuration > 0 && info.Duration > float64(channel.MaxClipDuration) {
		m.log.Debug("video exceeds max_clip_duration, skipping",
			zap.String("video_id", videoID),
			zap.Float64("duration_sec", info.Duration),
			zap.Int("max_duration", channel.MaxClipDuration))
		return
	}

	// ── Keyword filter (cheap; runs BEFORE the AI gate) ──────────────
	// Commit D (P1 keyword filter rewrite): decodeJSONStrings now
	// returns (nil, err) on malformed JSON input. The pre-Commit-D
	// behaviour silently dropped the input and treated the channel as
	// keyword-less; the new path surfaces the misconfiguration via the
	// monitor's per-video warn log so an operator notices a broken
	// config without spending a full cycle on it.
	keywords, decodeErr := decodeJSONStrings(channel.Keywords)
	if decodeErr != nil {
		m.log.Warn("decodeJSONStrings: channel.Keywords is malformed JSON; treating channel as keyword-less for this cycle",
			zap.String("channel_id", channel.ID),
			zap.String("raw_keywords", channel.Keywords),
			zap.Error(decodeErr))
		keywords = nil
	}
	if len(keywords) > 0 {
		if !containsAny(title, keywords) {
			m.log.Debug("title keyword no match, skipping",
				zap.String("video_id", videoID),
				zap.Strings("keywords", keywords))
			return
		}
		m.log.Debug("title keyword match", zap.String("video_id", videoID))
	}

	// ── Semantic gate — delegated to analyzer.go (Step 9) ─────────────
	semanticKeywords, decodeErr := decodeJSONStrings(channel.SemanticKeywords)
	if decodeErr != nil {
		m.log.Warn("decodeJSONStrings: channel.SemanticKeywords is malformed JSON; treating channel as semantically-keyword-less for this cycle",
			zap.String("channel_id", channel.ID),
			zap.String("raw_semantic_keywords", channel.SemanticKeywords),
			zap.Error(decodeErr))
		semanticKeywords = nil
	}
	analysis, analyzeErr := m.analyzeVideo(ctx, info, channel, semanticKeywords)
	if analyzeErr != nil {
		m.log.Warn("analyzeVideo failed, skipping video",
			zap.String("video_id", videoID),
			zap.Error(analyzeErr))
		return
	}
	if len(analysis.Segments) == 0 {
		// analyzer.go already logged the skip reason (no segments,
		// score below threshold, transcript miss). Cheap early-out.
		return
	}

	// ── MaxVideosPerRun budget reserve (after AI gate) ────────────────
	if outcomes != nil && channel.MaxVideosPerRun > 0 {
		// Budget now counts post-broker outcomes (enqueued + rejected).
		// A previously-already_scheduled video never consumes a slot
		// because it short-circuited before tryReserve. This matches
		// the pre-Commit-D "consumed on AI gate pass" intent.
		if !tryReserve(&outcomes.enqueued, channel.MaxVideosPerRun) {
			m.log.Debug("max_videos_per_run reached, skipping",
				zap.String("video_id", videoID),
				zap.Int("max", channel.MaxVideosPerRun))
			return
		}
	}

	// ── Per-video Prometheus observation ─────────────────────────────
	channelHandle := extractChannelHandle(channel.ChannelURL)
	if channelHandle == "" {
		channelHandle = "unknown"
	}
	metrics.ChannelMonitorVideosChecked.WithLabelValues(channelHandle).Inc()

	// ── Leader-election INSERT + broker-side dispatch (Commit D) ────────
	// recordDiscoveryAndClassify orchestrates:
	//   ① m.discoveries.TryReserve(ledger INSERT ... ON CONFLICT DO
	//     NOTHING RETURNING id). won=true → proceeds to ②; won=false →
	//     classifies OutcomeAlreadyScheduled and skips the broker side.
	//   ② m.enqueueFromAnalysis (delegated to enqueue.go): runs the
	//     canonical DriveFolderID/Group/Segments resolution + the broker-
	//     side EnqueueExtract + the per-bookkeeping MarkEnqueued /
	//     MarkRejected calls on the ledger row.
	//   ③ On a step-① win + step-② success: outcome=OutcomeEnqueued;
	//     the broker-emitted job is durably recorded AND the ledger row's
	//     outcome flipped to 'enqueued'.
	//   ④ On a step-① win + step-② error: outcome=OutcomeRejected;
	//     ledger row's outcome flipped to 'rejected' with rejection_reason.
	outcome, ledgerID := m.recordDiscoveryAndClassify(ctx, info, channel, analysis, cycleNow)
	switch outcome {
	case OutcomeAlreadyScheduled:
		outcomes.alreadyScheduled.Add(1)
		// Roll back the enqueued-budget CAS so a subsequent cycle's
		// new (channel_id, video_id) pair can claim the slot.
		outcomes.enqueued.Add(-1)
		m.log.Debug("dedupe loss: video already scheduled in a previous cycle; no broker record this cycle",
			zap.String("video_id", videoID),
			zap.String("channel_id", channel.ID))
		return
	case OutcomeRejected:
		// Post-broker rejection: enqueueFromAnalysis returned non-nil.
		// enqueue.go's helper already called m.discoveries.MarkRejected
		// with the rejection reason; here we just roll back the
		// budget CAS so a subsequent cycle's new (channel_id, video_id)
		// pair can claim the slot.
		outcomes.rejected.Add(outcomes.enqueued.Add(-1))
		m.log.Debug("video rejected post-INSERT", zap.String("video_id", videoID))
		return
	case OutcomeEnqueued:
		// Successful flow: enqueueFromAnalysis ran the broker side AND
		// MarkEnqueued flipped the ledger row's outcome to 'enqueued'.
		// The enqueued counter is already incremented via the budget
		// CAS above.
		m.log.Info("video enqueued",
			zap.String("video_id", videoID),
			zap.String("title", title),
			zap.Int("segments", len(analysis.Segments)),
			zap.String("ledger_id", ledgerID))
		return
	}
}

// recordDiscoveryAndClassify runs the leader-election TryReserve dance
// and (when won) delegates the canonical broker-side dance to
// enqueue.go (`m.enqueueFromAnalysis`). The discovery-side responsibility
// is solely the ledger dedupe; the broker-side payload resolution +
// EnqueueExtract dispatch + per-outcome ledger MarkEnqueued / MarkRejected
// all live in enqueue.go to keep a single canonical path for the
// ExtractRequest payload (BLOCKER-1 fix, June 2026: pre-fix the inline
// call here emitted payload-empty ExtractRequests, which is a
// silent-success bug).
//
// Returns (outcome, ledgerID):
//   - (OutcomeAlreadyScheduled, "<id>") when TryReserve loses the race
//     (cycle-end watermark still advances, but no broker emit).
//   - (OutcomeEnqueued, "<id>") when TryReserve wins AND the broker
//     emit succeeded AND enqueue.go's MarkEnqueued flipped the row.
//   - (OutcomeRejected, "<id>") when TryReserve wins but the broker
//     emit failed AND enqueue.go's MarkRejected recorded the reason.
//   - (OutcomeAlreadyScheduled, "") when m.discoveries is nil (defensive;
//     no dedupe possible — flagged loudly so a missing wire surfaces in
//     the per-video warn log rather than silently losing dedupe).
func (m *ChannelMonitor) recordDiscoveryAndClassify(ctx context.Context, info downloader.VideoInfo, channel channels.Channel, analysis Analysis, cycleNow string) (EnqueueOutcome, string) {
	videoID := info.ID
	title := info.Title

	if m.discoveries == nil {
		m.log.Warn("recordDiscoveryAndClassify: discoveries port not wired, classifying as already_scheduled (no dedupe)",
			zap.String("video_id", videoID))
		return OutcomeAlreadyScheduled, ""
	}

	// ① Leader-election INSERT. won=false → previous cycle won, this
	//    cycle classifies already_scheduled and skips the broker side.
	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	id, won, err := m.discoveries.TryReserve(ctx, channel.ID, videoID, videoURL, title, cycleNow)
	if err != nil {
		m.log.Error("recordDiscoveryAndClassify: TryReserve failed, classifying as rejected (ledger error)",
			zap.String("video_id", videoID),
			zap.Error(err))
		return OutcomeRejected, ""
	}
	if !won {
		return OutcomeAlreadyScheduled, id
	}

	// ② Delegate the canonical broker-side dance to enqueue.go.
	// The helper does: DriveFolderID/Group/Segments resolution →
	// m.enqueuer.EnqueueExtract → on success m.discoveries.MarkEnqueued,
	// on error m.discoveries.MarkRejected. Returns nil on the enqueued
	// path; returns the underlying error on the rejected path.
	err = m.enqueueFromAnalysis(ctx, info, channel, analysis, id)
	if err != nil {
		m.log.Warn("recordDiscoveryAndClassify: enqueueFromAnalysis failed",
			zap.String("video_id", videoID),
			zap.String("ledger_id", id),
			zap.Error(err))
		return OutcomeRejected, id
	}
	return OutcomeEnqueued, id
}

// decodeJSONStrings decodes a JSON-encoded string array (as stored in
// the channels.Channel DTO's Keywords / SemanticKeywords fields) into
// a Go []string. Returns ([], nil) for empty/[] input.
//
// Commit D (P1 keyword filter rewrite): on malformed JSON, returns a
// non-nil error with the raw input echoed for diagnostics (capped at
// 200 chars to avoid log flooding). Pre-Commit-D returned nil silently,
// which masked channels-table misconfigurations across full cycles.
// Callers in processVideo (this file) log the error + treat the channel
// as keyword-less for the current cycle; future cycles retry once the
// underlying config is repaired.
func decodeJSONStrings(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil, nil
	}
	sample := s
	if len(sample) > 200 {
		sample = sample[:200] + "…"
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("decodeJSONStrings: malformed JSON %q: %w", sample, err)
	}
	return out, nil
}

// containsAny checks if a string contains any of the keywords (case-
// insensitive after TrimSpace + strings.ToLower on both sides).
//
// Commit D (P1 keyword filter rewrite): replaces the pre-Commit-D
// bespoke O(n*m) ASCII-looper with stdlib primitives:
//   - keywords are pre-lowercased once before the loop,
//   - title is lowercased once before the loop,
//   - matching is `strings.Contains(lowerTitle, lowerKW)`.
//
// This is still O(n*m) but with the inner comparison reduced to a
// singlememchr (Go's strings.Contains). For typical channel-keyword
// list length (<20) and title length (<200 chars) this is dominated
// by the kernel time of an yt-dlp subprocess by many orders of
// magnitude, so the rewrite is observably identical in practice but
// canonical in shape (no hand-rolled loop).
func containsAny(text string, keywords []string) bool {
	if len(text) == 0 || len(keywords) == 0 {
		return false
	}
	lowerText := strings.ToLower(text)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw == "" {
			continue
		}
		if strings.Contains(lowerText, kw) {
			return true
		}
	}
	return false
}
