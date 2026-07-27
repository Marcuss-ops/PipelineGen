package render

// cutter_audio.go is the split-by-capability (Phase 9) CAPABILITY
// MARKER file for the "audio-cut" bucket of the FFmpegCutter
// pipeline.
//
// In the current architecture, audio extraction and muting for the
// stock pipeline are NOT a separate code path. The FFmpegCutter
// (cutter.go) routes the audio requirement through a single boolean
// flag on CutRequest (NoAudio) — when true, the ffmpeg adapter
// adds `-an` to the cut argv so the produced clip has NO audio
// stream; when false, it adds `-c:a aac -ar 48000 -ac 2` so the
// AAC 48 kHz stereo canonical profile is satisfied. The frame-cut
// orchestration that consumes this flag lives in cutter_cut.go.
//
// This file exists to:
//  1. Make the audio-cut capability discoverable in the architecture
//     graph (godlike/07 SSOT for machine-readable ownership).
//  2. Document the parameterized audio handling convention so future
//     contributors don't grep for a non-existent dedicated
//     audio-cut code path inside this package.
//  3. Reserve a home for any future audio-specific extension
//     (e.g., a per-language audio-track selector, a volume
//     normalization pass — currently out of scope but flagged here
//     for SSOT discoverability).
//
// No Go code lives in this file — the marker is intentionally
// documentation-only to mirror the Phase 7 capability-marker pattern
// (cached_search.go + retry_fallback.go in the artlist package) and
// the Phase 9 cutter_probe.go if/when a future extraction needs a
// code home.
