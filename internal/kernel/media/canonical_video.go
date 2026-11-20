package media

import (
	"strconv"
	"strings"
)

// The canonical H.264 encoder lane. These values are not tuning knobs: every
// one of them lands in the emitted SPS/PPS or VUI and therefore in
// CanonicalVideoExtradataSHA256. A lane that changes codec, preset, quality,
// reference count, B-frame policy, GOP, profile/level or colour tagging emits a
// different avcC and can no longer be packet-copied next to the certified
// segments.
//
// This block is the ONE declaration shared by the local assemblers
// (clipmerge, overlayjoin) and the final-job fragment certifier. The stock
// cutter and RenderingGen keep their own measured argument lists because they
// run in other processes; both are pinned to the same bytes by
// CanonicalVideoExtradataSHA256 and the cross-repo mirror tests.
const (
	CanonicalVideoCodec          = "libx264"
	CanonicalVideoPreset         = "veryfast"
	CanonicalVideoCRF            = 23
	CanonicalVideoCodecProfile   = "high"
	CanonicalVideoCodecLevel     = "4.0"
	CanonicalVideoPixelFormat    = "yuv420p"
	CanonicalVideoGOP            = 48
	CanonicalVideoTimescale      = 90000
	CanonicalVideoColorRange     = "tv"
	CanonicalVideoColorSpace     = "bt709"
	CanonicalVideoColorTransfer  = "bt709"
	CanonicalVideoColorPrimaries = "bt709"
	// CanonicalVideoX264Params pins the scene-cut and open-GOP policy that the
	// libx264 preset itself would otherwise choose. Without it a preset change
	// keeps the nominal profile while changing the SPS/PPS.
	CanonicalVideoX264Params = "scenecut=0:open-gop=0:keyint=48:min-keyint=48"
)

// CanonicalVideoEncoderArgs is the exact ffmpeg video-encoder argument block
// that reproduces CanonicalVideoExtradataSHA256 at 1920x1080@24.
//
// The list is deliberately a function, not a package variable: a caller must
// not be able to mutate the shared recipe. Callers append their own input,
// filters, audio mapping and output path around it.
func CanonicalVideoEncoderArgs() []string {
	gop := strconv.Itoa(CanonicalVideoGOP)
	return []string{
		"-c:v", CanonicalVideoCodec,
		"-preset", CanonicalVideoPreset,
		"-crf", strconv.Itoa(CanonicalVideoCRF),
		"-refs", "1",
		"-profile:v", CanonicalVideoCodecProfile,
		"-level:v", CanonicalVideoCodecLevel,
		"-pix_fmt", CanonicalVideoPixelFormat,
		"-g", gop,
		"-keyint_min", gop,
		"-sc_threshold", "0",
		"-bf", "0",
		"-flags", "+cgop",
		"-x264-params", CanonicalVideoX264Params,
		"-colorspace", CanonicalVideoColorSpace,
		"-color_primaries", CanonicalVideoColorPrimaries,
		"-color_trc", CanonicalVideoColorTransfer,
		"-color_range", CanonicalVideoColorRange,
		"-video_track_timescale", strconv.Itoa(CanonicalVideoTimescale),
	}
}

// IsCanonicalVideoEncoder reports whether a resolved encoder policy names the
// canonical lane. Case and surrounding whitespace are ignored because the
// policy crosses JSON and YAML boundaries; the quality is exact because the
// certified bytes were measured at CRF 23.
func IsCanonicalVideoEncoder(codec, preset string, crf int) bool {
	return strings.EqualFold(strings.TrimSpace(codec), CanonicalVideoCodec) &&
		strings.EqualFold(strings.TrimSpace(preset), CanonicalVideoPreset) &&
		crf == CanonicalVideoCRF
}
