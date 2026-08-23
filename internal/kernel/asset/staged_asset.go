// Package asset — StagedAsset DTO.
//
// StagedAsset is the internal DTO for staged source files used
// throughout the Stock pipeline. It carries the result of a
// successful staging call: the on-disk path, size, and source
// metadata needed by downstream processing steps.
package asset

// StagedAsset carries the result of a successful staging call.
// The file at LocalPath is ready for subsequent processing (cut,
// transcode, upload). Bytes is the on-disk size.
//
// SourceID is the canonical locator that produced this staged file
// (e.g. the YouTube URL, Artlist m3u8, stock clip URL). Callers use
// it to correlate staged files with their originating ClipPlan
// entries when multiple sources are staged in one orchestrator run.
//
// DurationSec is the probed source duration in seconds, populated
// when known (e.g. yt-dlp --print-duration at staging time, or the
// ffprobe fallback in step_extract_clips). When > 0, downstream
// consumers (stock.extract_clips in particular) use it as the
// pre-cut validation surface for clip EndSec bounds checking.
// When 0 the downstream step must fall back to its own probe or
// skip the bounds check.
type StagedAsset struct {
	LocalPath   string
	Bytes       int64
	SourceID    string
	DurationSec float64
}
