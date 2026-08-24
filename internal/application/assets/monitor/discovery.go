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
//   - EnqueueExtract + outcome classification. The outcome
//     (enqueued | already_scheduled | rejected) increments exactly one
//     of three atomic counters; only enqueued increments VideosEnqueued.
//
// This file NEVER imports os/exec, the OllamaClient, or VTT regex
// helpers — those concerns moved out under the TranscriptProvider /
// VideoAnalyzer / JobEnqueuer ports.
package monitor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
)

// effectivePlaylistEnd picks the playlist-end limit for the caller's
// yt-dlp invocation:
//
//   - PlaylistEnd > 0 → explicit per-channel limit (honoured as-is)
//   - PlaylistEnd <= 0 (including DB default 0) → fall back to the
//     global default. Blocco 3d (July 2026): pre-fix, 0 was passed
//     through literally, which yt-dlp interprets as "no limit" — a
//     new channel with the DB default would scan ALL videos instead
//     of the expected DefaultPlaylistEnd (50).
//
// Future: when the schema adds NULL support for the PlaylistEnd
// column, 0 = disabled (skip channel scan). Today every channel
// with a non-positive PlaylistEnd falls through to DefaultPlaylistEnd.
func effectivePlaylistEnd(channel channels.Channel, globalDefault int) int {
	if channel.PlaylistEnd > 0 {
		return channel.PlaylistEnd
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
// ChannelMonitorPolicyVersion is the canonical `policy_version`
// stamped on every youtube_discoveries row produced by the channel
// monitor. Bumping this constant (e.g. "v2_retryable") produces a
// fresh row under UNIQUE(channel_id, video_id, policy_version)
// alongside the historical one — both coexist for audit; only the
// new policy_version participates in the live TryReserve+drain loop.
// Commit 3/6 (June 2026, P1 #5+#6+#7): constant lives here so a
// future Commit 4+/5+ can bump it without touching enqueue.go or the
// repository.
const ChannelMonitorPolicyVersion = "v1"

func (m *ChannelMonitor) discoverChannelVideos(ctx context.Context, channel channels.Channel) ([]VideoInfo, error) {
	if m.ytdlp == nil {
		return nil, fmt.Errorf("discoverChannelVideos: ytdlp port not wired")
	}
	playlistEnd := effectivePlaylistEnd(channel, DefaultPlaylistEnd)
	// FASE 3.7 Commit 1b (2026-07-04): DateAfter is produced by
	// monitor.DateAfterFromCursor (formerly `sqlassets.ResolveDateAfter`
	// — the previous infra-supplied helper was equivalent but
	// imported `internal/platform/sqlite/assets`,
	// violating the FASE 3.7 zero-infra-import commitment in
	// monitor/). The monitor-owned helper preserves the same
	// precedence: LastCursor (RFC3339 truncated to YYYYMMDD) wins
	// over LookbackDays (now - N days formatted as YYYYMMDD). An
	// empty LastCursor + zero LookbackDays → empty DateAfter
	// (yt-dlp's no-filter path). Pass nil for the now-function so
	// the lazy-default clock (time.Now via defaultNowFn) is used.
	dateAfter := DateAfterFromCursor(channel.LastCursor, channel.LookbackDays, nil)
	return m.ytdlp.ListChannelVideos(ctx, ListChannelVideosQuery{
		ChannelURL:  channel.ChannelURL,
		DateAfter:   dateAfter,
		PlaylistEnd: playlistEnd,
	})
}

// checkChannel runs the per-cycle loop with bounded concurrency.
//
// Returns (ChannelCheckResult, error):
//
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
//
//   - VideosAlreadyScheduled - VideosRejected: legacy aggregate of
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
	//
	// Commit 2/6 (PR-C-YouTube-Cutover, Correttezza #1): added
	// budgetUsed (atomic Int32) so the MaxVideosPerRun cap counts
	// the live "reserved" budget slots — not the snapshot of
	// (enqueued + rejected). Pre-Commit-2 the cap regressed on
	// alreadyScheduled outcomes because enqueued was decremented
	// after the leader-election INSERT lost, and the new
	// (enqueued+rejected) aggregate dropped by 1, allowing more
	// parallel goroutines to slip past the gate than the cap
	// intended. budgetUsed is incremented on tryReserve success
	// and decremented on outcome classification (Enqueued keeps
	// the slot; AlreadyScheduled + Rejected release it).
	var outcomes outcomeCounters

	for _, video := range videos {
		video := video
		if channel.MaxVideosPerRun > 0 && outcomes.budgetUsed.Load() >= int32(channel.MaxVideosPerRun) {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					m.log.Error("panic in video processing worker", zap.Any("recover", r), zap.String("video_id", video.ID))
					// Blocco 3a: panic is an infra failure, not a policy rejection.
					outcomes.infraFailures.Add(1)
				}
			}()
			if channel.MaxVideosPerRun > 0 && outcomes.budgetUsed.Load() >= int32(channel.MaxVideosPerRun) {
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
		InfraFailures:          int(outcomes.infraFailures.Load()),
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
//
// Commit 2/6 (PR-C-YouTube-Cutover, Correttezza #1): added
// budgetUsed — the canonical "reserved-slot" counter for the
// MaxVideosPerRun cap. The pre-Commit-2 cap regressed on
// alreadyScheduled outcomes because enqueued was decremented
// after the leader-election INSERT lost; the new
// (enqueued+rejected) aggregate dropped by 1, allowing more
// parallel goroutines to slip past the gate than the cap intended.
// budgetUsed is incremented on tryReserve success and decremented
// on AlreadyScheduled / Rejected outcomes; Enqueued keeps the
// slot. The enqueued / alreadyScheduled / rejected counters stay
// non-cumulative outcome tallies (caller-visible; they don't
// participate in the cap check).
type outcomeCounters struct {
	enqueued         atomic.Int32
	alreadyScheduled atomic.Int32
	rejected         atomic.Int32
	budgetUsed       atomic.Int32
	// infraFailures counts per-video infra errors (panics, analyzer
	// timeouts, SQLite errors, broker failures) within the cycle.
	// Blocco 3a (July 2026).
	infraFailures atomic.Int32
}

// tryReserve is now defined in enqueue.go (pre-existing — the budget
// CAS helper lived there before Commit 2/6). The single source of
// truth for the per-channel budget gate. processVideo in this file
// calls the same function via the package-level scope (enqueue.go
// and discovery.go share the same `monitor` package).

// processVideo, recordDiscoveryAndClassify, decodeJSONStrings, containsAny
// live in discovery_record.go (PR-DISCOVERY-SPLIT, July 2026).
