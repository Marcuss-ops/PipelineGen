// Package youtube — subtitles_fallback.go is the fallback sentinel
// leaf of the 5-file subtitle split. It owns ONLY the canonical
// "no usable subtitles for this clip" decision helpers + the
// (nil, nil) signal the application-layer orchestrator interprets
// as "fall through to Whisper".
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The 5-level acquisition chain lives in
//     internal/application/youtube/usecase/text_track_resolver.go
//     (commit c0bae1612). This leaf does NOT call Whisper (which
//     is in internal/capabilities/transcripts — the capability
//     layer, not infrastructure). The infra-level "fallback" here
//     is purely a DECISION signal: given the subtitle fetch + parse
//     outcome, return a sentinel that the orchestrator interprets
//     as the chain-fall-through signal.
//
// godlike/07 no-fake-availability invariant: the sentinel
// (nil, nil) is the only canonical "no subs available" surface —
// no typed error, no fake bundle, no default BCP-47 "en". The
// orchestrator then decides whether to surface
// asset.ErrLanguageUndeterminable (RequireLanguageCertainty=true)
// or simply fall through to Whisper (RequireLanguageCertainty=false).
//
// The leaf's job is to keep the facade's intent visible: every
// point where the chain "knows" there's no subtitle-related
// translation goes through triggerWhisperFallback so a future
// maintainer reading subtitles.go::FetchSegmentSubtitles sees
// the fallback decision explicitly named (not lost in a typed-
// error cascade).
package youtube

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// triggerWhisperFallback is the canonical (nil, nil) sentinel that
// the application-layer orchestrator (text_track_resolver.go
// priority chain) interprets as "fall through to Whisper". The
// function is intentionally a no-op shim so a future maintainer
// reading subtitles.go::FetchSegmentSubtitles sees the fallback
// decision explicitly named at the call site — the orchestrator's
// priority discipline is preserved (godlike/06 SSOT) without
// importing the Whisper port here.
func triggerWhisperFallback() (*detail.ResolvedTextBundle, error) {
	return nil, nil
}

// isVttMissing reports whether the expected cached VTT file is
// missing on disk. Used by the facade to detect the "yt-dlp
// succeeded but wrote nothing" outcome.
func isVttMissing(vttPath string) bool {
	_, statErr := os.Stat(vttPath)
	return statErr != nil
}

// isContentEmpty reports whether the parsed subtitle result has no
// usable content for the requested window. Used by the facade to
// detect the "VTT is present but has no cues/plain for [startSec,
// endSec]" outcome. The two checks are decoupled because the
// rolling-cue dedup may strip cues but leave plain text, or
// vice-versa (rare but theoretically possible for malformed VTT).
func isContentEmpty(cues int, plain string) bool {
	return cues == 0 && plain == ""
}
