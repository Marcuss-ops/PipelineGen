package detail

// timed_cues_clip_window.go — canonical rebase of SOURCE-VIDEO cue
// timings onto the CLIP timeline (Sept 2026, PR-SUBS-CLIP-WINDOW).
//
// godlike/06 SSOT (one canonical owner per fact): the source-video →
// clip cue rebase formula lives ONLY here. Every clip-chain caller
// (TextTrackResolver.acquireFromSubtitles, texttracks.Acquire
// priority 3+4) calls this helper and NEVER subtracts startSec inline.
//
// WHY THIS EXISTS (root cause of "downloaded clips ship wrong/absent
// subtitles"): the YouTube caption source is the FULL video's VTT.
// FetchSegmentSubtitles merely WINDOW-filters cues to
// [startSec, endSec] of the source video, so the returned cues keep
// their SOURCE timestamps — while every consumer of a clip's cues
// expects the CLIP timeline (0 = clip start):
//
//   - Whisper (priority 5) transcribes the CUT clip → clip-local cues;
//   - texttracks.validateASSFile rejects an artifact whose last cue end
//     exceeds clipDurationMs (+250ms) → a source-absolute cue set marks
//     the .ass FAILED and it is never published to Drive;
//   - cliprender.trimClipRenderCues drops every cue whose StartMs >=
//     clip duration → a source-absolute cue set compiles to
//     "no cues remain inside clip duration" and the render ships with
//     no subtitles at all.
//
// The two timelines therefore disagree for EVERY clip that does not
// start at 0s of its source video — which is why the symptom was
// intermittent (clips starting at 0s, and Whisper-resolved clips, came
// out fine).
//
// The rebase is deliberately NOT done inside
// SubtitleFetcherAdapter.FetchSegmentSubtitles: GET
// /api/clips/transcript serves source-video windows to operators who
// pick clip boundaries from those very timings (ops/jobs/*/process.json
// "Windows BY HAND from timed transcript"), so the video-level port
// must keep source-video times.

// RebaseCuesForClip rewrites cues acquired on the source-video timeline
// onto the clip timeline and clamps every cue into the clip window.
//
//   - startSec, endSec are the clip boundaries WITHIN THE SOURCE VIDEO
//     (seconds). startSec <= 0 && endSec <= 0 is the canonical
//     "whole video, no window" contract and returns the cues unchanged.
//   - Each cue is shifted by -startSec and clamped to
//     [0, (endSec-startSec)] so a cue that straddles a clip boundary
//     cannot end after the clip does (the +250ms ASS validation
//     tolerance must not be relied on for a systematic overshoot).
//   - Cues that fall entirely outside the window are dropped (they
//     carry no visible text inside the clip).
//
// The input slice is never mutated; a nil/empty input returns the
// input as-is so callers can keep the "no cues" sentinel semantics.
func RebaseCuesForClip(cues []TimedCue, startSec, endSec int) []TimedCue {
	if len(cues) == 0 {
		return cues
	}
	if startSec <= 0 && endSec <= 0 {
		return cues
	}
	shiftMs := int64(0)
	if startSec > 0 {
		shiftMs = int64(startSec) * 1000
	}
	var capMs int64
	if endSec > startSec {
		capMs = int64(endSec-startSec) * 1000
	}
	out := make([]TimedCue, 0, len(cues))
	for _, c := range cues {
		start := c.StartMs - shiftMs
		if start < 0 {
			start = 0
		}
		end := c.EndMs - shiftMs
		if capMs > 0 && end > capMs {
			end = capMs
		}
		if end <= start {
			// Entirely outside the clip window (or collapsed by the
			// clamp): no visible frame range, so no row.
			continue
		}
		out = append(out, TimedCue{StartMs: start, EndMs: end, Text: c.Text})
	}
	return out
}
