// Package monitor — monitor_ports.go: Pattern 0 port surface
// (per AGENTS.md / godlike/06 §"Database and config ownership"):
//
// ChannelMonitor consumed only the slice of *downloader.YTDLPDownloader
// it actually needs (ListChannel for video discovery in checkChannel;
// Path for the transcribed-subtitles exec in semantic_matcher). Holding
// them as typed ports keeps the production wiring honest
// (channel monitor ⇄ yt-dlp via an interface boundary) and unlocks
// test-doubles that fail fake-ytdlp without spinning up a real
// subprocess. The cross-capability audit (Wave A) flagged the
// missing-port coupling here; this file closes Blocco 1 of that
// audit's port-extraction for *read-time* calls (claim/check).
//
// Compile-time assertion (`var _ Port = (*Concrete)(nil)`) lives at
// the bottom: signature drift between port surface and concrete
// becomes a build failure per Pattern 0.
package monitor

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

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

// MonitorDownloaderPort is the minimum-surface slice of
// *downloader.YTDLPDownloader that the channel monitor needs.
//
//   - ListChannel — used by checkChannel to enumerate a channel's
//     videos (the first step of every scheduler tick). Failure here is
//     the primary signal that drives the exponential backoff in
//     scheduler.nextCheckTime.
//   - Path — used by semantic_matcher to build the yt-dlp command line
//     for transcript download. Kept inside the port so the monitor
//     owns no business knowledge of yt-dlp subprocess invocation
//     beyond reading its binary path.
//
// The concrete *YTDLPDownloader already implements both methods; tests
// inject a stub that satisfies the same interface so unit tests can
// drive the failure path without spawning a real subprocess.
type MonitorDownloaderPort interface {
	ListChannel(ctx context.Context, channelURL string, limit int) ([]downloader.VideoInfo, error)
	Path() string
}

// Compile-time assertion: any signature drift between MonitorDownloaderPort
// and *downloader.YTDLPDownloader becomes a build failure here, not a
// runtime panic in production. Pattern 0 invariant.
var _ MonitorDownloaderPort = (*downloader.YTDLPDownloader)(nil)
