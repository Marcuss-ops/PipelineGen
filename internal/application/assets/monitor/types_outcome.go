package monitor

// SkipReason is a stable, machine-readable reason for why a video was NOT
// enqueued for clip extraction. The set is the single source of truth — no
// magic strings anywhere in discovery / analyzer / enqueue branches; every
// skip path emits one of these constants via the final "channel monitor
// video completed" log line + the channel_monitor_video_outcomes_total
// Prometheus counter (labels: channel, outcome, reason).
//
// Plan: Channel Monitor Blocchi 4+5 (June 2026).
type SkipReason string

const (
	// SkipBelowMinViews — channel.MinViews filter rejected the video.
	SkipBelowMinViews SkipReason = "below_min_views"
	// SkipOverDuration — channel.MaxClipDuration filter rejected the video.
	SkipOverDuration SkipReason = "over_duration"
	// SkipKeywordMismatch — title-keyword filter did not match.
	SkipKeywordMismatch SkipReason = "keyword_mismatch"
	// SkipSemanticRejected — semantic score below channel.MinSemanticScore.
	SkipSemanticRejected SkipReason = "semantic_rejected"
	// SkipSubtitleUnavailable — no VTT/subtitles could be fetched for the video.
	SkipSubtitleUnavailable SkipReason = "subtitle_unavailable"
	// SkipTranscriptTooShort — Whisper/yt-dlp transcript shorter than threshold.
	SkipTranscriptTooShort SkipReason = "transcript_too_short"
	// SkipOllamaFailed — semantic matcher (Ollama) returned an error.
	SkipOllamaFailed SkipReason = "ollama_failed"
	// SkipNoChapters — yt-dlp chapter extraction returned empty.
	SkipNoChapters SkipReason = "chapters_unavailable"
	// SkipNoSegments — segment finder produced no interesting segments.
	SkipNoSegments SkipReason = "no_segments"
	// SkipAlreadyActive — a youtube_clip.extract job is already active for this
	// video (job ActiveKey collision: channel_sync_<videoID>).
	SkipAlreadyActive SkipReason = "already_active"
	// SkipEnqueueFailed — jobs.Service.Enqueue returned an error.
	SkipEnqueueFailed SkipReason = "enqueue_failed"
	// SkipUnknown — defensive sentinel: callers MUST set Reason on every
	// EnqueueOutcome {Enqueued: false}; if a caller forgets, the zero value
	// is "" which leaks into the Prometheus label
	// channel_monitor_video_outcomes_total{reason=""} and the final log line.
	// Use this constant in code paths that legitimately do not know the cause
	// (defensive default), and prefer a typed Skip* over SkipUnknown wherever
	// the cause is identifiable.
	SkipUnknown SkipReason = "unknown"
)

// String makes SkipReason satisfy fmt.Stringer so zap.String("reason", r)
// renders as the constant value (not "<SkipReason Value>").
func (r SkipReason) String() string { return string(r) }

// EnqueueOutcome reports what happened when ChannelMonitor.enqueueClipExtract
// tried to enqueue a youtube_clip.extract job for a single video.
//
// Semantics (Blocco 4 of channel-monitor plan, June 2026):
//   - Enqueued=true  → jobs.Service.Enqueue accepted the job; JobID is set.
//                      The reservation in MaxVideosPerRun MUST be kept.
//   - Enqueued=false → no job was queued (pre-filter, no segments, enqueue
//                      rejected by ActiveKey, etc.); Reason explains why.
//                      The caller MUST release any reservation taken via
//                      tryReserve before calling enqueueClipExtract.
//
// This type replaces the void return so that MaxVideosPerRun represents
// "jobs actually enqueued" — not "videos that entered the analyze path".
type EnqueueOutcome struct {
	Enqueued bool
	JobID    string
	Reason   SkipReason
}
