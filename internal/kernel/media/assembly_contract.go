package media

import (
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// AssemblyMediaContract is the SSOT for the technical stream identity.
// It describes the exact codec, resolution, fps, timebase, pixel format,
// SAR, color metadata, GOP, and audio layout of the encoded video stream.
// Every clip in the concat demuxer + -c copy assembler MUST satisfy this
// contract exactly.
//
// This contract is PURELY about stream-level facts — it answers "what
// codec/pixels/audio/timebase are on disk?". It deliberately excludes
// editorial/visual choices (watermark, subtitles, overlay, zoom, scale).
// Those live in the separate CompositionContract.
//
// The AssemblyReady flag distinguishes final clips (assembly_ready=true)
// from intermediate artifacts (ProRes, alpha, proxies — assembly_ready=false).
// Only assembly-ready clips may enter the assembler.
//
// Frozen values for VELOX_ASSEMBLY_READY_V1 (legacy, 2026-08-23, unico SSOT):
//
//	container mp4, h264 high 4.0 yuv420p, 1920x1080, 24/1, 1/1 SAR,
//	timebase video 1/90000 audio 1/48000, tv/bt709 progressive,
//	GOP 48 b_frames 0 closed_gop, 1 video +1 audio (video,audio) start_pts 0,
//	AAC-LC 48000 2ch stereo 128k.
//
// Audio profile SSOT: audio.DefaultAudioProfile() in internal/capabilities/audio
// (aac, LC, 48000 Hz, 2 channels, stereo, 192k). This contract MUST stay
// in sync with that profile; the stock copy renderer gates on the same values.
const (
	// Canonical delivery geometry. Every producer that renders, clamps, or
	// certifies a delivery frame derives these facts from here instead of
	// restating the literals in its own package.
	CanonicalWidth  = 1920
	CanonicalHeight = 1080
	CanonicalFPSNum = 24
	CanonicalFPSDen = 1

	// V1 remains frozen for compatibility with existing assets.
	AssemblyMediaContractID      = "VELOX_ASSEMBLY_READY_V1"
	AssemblyMediaContractVersion = 1

	// V2 is the canonical contract for newly produced assembly-ready clips.
	AssemblyMediaContractV2ID      = "VELOX_ASSEMBLY_READY_V2"
	AssemblyMediaContractV2Version = 2

	// Legacy aliases kept for source compatibility during migration.
	AssemblyReadyVideoContractID = AssemblyMediaContractID
	AssemblyReadyVideoVersion    = AssemblyMediaContractVersion

	// OverlayVideoOnlyProfileID is the current RenderingGen output profile for the
	// final job's opaque overlay compositions (packed manifest timeline):
	// H.264 High 4.0, 1920x1080@24, yuv420p, closed GOP 48, first frame IDR,
	// start PTS 0, and ZERO audio streams. It is a RENDER certification
	// profile, not an assembly contract: an overlay is an input to the
	// assembler, so ValidateExact keeps rejecting it on purpose. The value is
	// declared here so the plan that requests it and the producer that
	// certifies it name the same profile. V2 is the current cache lane for new
	// overlay sources; the fragment producer normalizes any source whose actual
	// avcC is not CanonicalVideoExtradataSHA256, so the artifact profile does
	// not itself promise a parameter-set identity. V1 remains historical.
	OverlayVideoOnlyProfileID = "VELOX_OVERLAY_VIDEO_ONLY_V2"
	OverlayVideoOnlyLegacyID  = "VELOX_OVERLAY_VIDEO_ONLY_V1"

	// CanonicalVideoExtradataSHA256 is the SHA-256 of the exact
	// AVCDecoderConfigurationRecord (the MP4 `avcC` payload: SPS + PPS +
	// their VUI) that every packet-copy assembly input MUST carry. Profile,
	// level, geometry, fps and pixel format are NOT sufficient: three streams
	// can agree on all of them and still carry different SPS/PPS, and a
	// `concat -c copy` writes only the first segment's avcC. The decoder then
	// applies those parameter sets to slices encoded with different ones — the
	// green/rainbow flash defect. This hash is the single bitstream identity
	// the producer, the copy gate, the assembler and the final mux all compare.
	CanonicalVideoExtradataSHA256 = "7201ce80a2b2b32a0c1a89501851099f267c1767d59ff799db744f72032aae5a"

	// CanonicalVideoProfileRevision names the exact encoder lane behind
	// CanonicalVideoExtradataSHA256. Profile and level alone are not a version:
	// the same High 4.0 nominal profile can be produced with different reference
	// counts, cadence, colour VUI or track timescale. Producers embed this
	// revision in their work/cache identity, so changing the encoder contract
	// invalidates previously certified artifacts even when the avcC bytes happen
	// to stay equal. It is a revision of VELOX_ASSEMBLY_READY_V1 (High 4.0),
	// never of the High 4.1 V2 contract.
	CanonicalVideoProfileRevision = "velox-avc1-high40-ref1-bt709-v1"
)

// FrameRate is a rational framerate. Exact equality via cross-multiplication
// (no float, no epsilon). Use FPSFloat() only for logs/UI.
type FrameRate struct {
	Num int `json:"num"`
	Den int `json:"den"`
}

func (f FrameRate) FPSFloat() float64 {
	if f.Den == 0 {
		return 0
	}
	return float64(f.Num) / float64(f.Den)
}

func (f FrameRate) Equal(o FrameRate) bool {
	return f.Num*o.Den == o.Num*f.Den
}

func (f FrameRate) Valid() bool { return f.Num > 0 && f.Den > 0 && f.Num <= 120*f.Den }

// Rational is a generic num/den pair (SAR, timebase).
type Rational struct {
	Num int `json:"num"`
	Den int `json:"den"`
}

func (r Rational) Equal(o Rational) bool { return r.Num*o.Den == o.Num*r.Den }
func (r Rational) Valid() bool           { return r.Num > 0 && r.Den > 0 }

// AssemblyMediaContract is the full technical stream identity.
// It is PURELY about what codec/pixels/audio/timebase is on disk.
// Editorial/visual choices (watermark, subtitles, overlay, zoom, scale)
// live in the separate CompositionContract.
type AssemblyMediaContract struct {
	ID      string `json:"id"`
	Version int    `json:"version"`

	// AssemblyReady is true for final assembly-ready clips.
	// Intermediate artifacts (ProRes, alpha, proxies) set this to false
	// and must never enter the assembler.
	AssemblyReady bool `json:"assembly_ready"`

	Container string `json:"container"`

	VideoCodec   string `json:"video_codec"`
	VideoProfile string `json:"video_profile"`
	VideoLevel   string `json:"video_level"`
	// VideoExtradataSHA256 is the exact SPS/PPS (avcC) identity. It is not a
	// redundant profile field: two High 4.0 streams can carry different
	// parameter sets. Empty is a contract violation, never a wildcard.
	VideoExtradataSHA256 string `json:"video_extradata_sha256"`
	PixelFormat          string `json:"pixel_format"`

	Width  int       `json:"width"`
	Height int       `json:"height"`
	FPS    FrameRate `json:"fps"`

	VideoTimeBase Rational `json:"video_time_base"`
	AudioTimeBase Rational `json:"audio_time_base"`

	SAR            Rational `json:"sar"`
	ColorRange     string   `json:"color_range"`
	ColorSpace     string   `json:"color_space"`
	ColorTransfer  string   `json:"color_transfer"`
	ColorPrimaries string   `json:"color_primaries"`
	FieldOrder     string   `json:"field_order"`

	KeyframeInterval int  `json:"keyframe_interval"` // GOP
	BFrames          int  `json:"b_frames"`          // 0 = no B-frames
	ClosedGOP        bool `json:"closed_gop"`        // must be true for assembly-ready

	AudioCodec         string `json:"audio_codec"`
	AudioProfile       string `json:"audio_profile"`
	AudioSampleRate    int    `json:"audio_sample_rate"`
	AudioChannels      int    `json:"audio_channels"`
	AudioChannelLayout string `json:"audio_channel_layout"`
	AudioBitrate       string `json:"audio_bitrate"`

	VideoStreams int   `json:"video_streams"`
	AudioStreams int   `json:"audio_streams"`
	StartPTS     int64 `json:"start_pts"`
}

// VideoContract is a backward-compatible alias for AssemblyMediaContract.
// Deprecated: use AssemblyMediaContract directly.
type VideoContract = AssemblyMediaContract

// DefaultAssemblyMediaContract returns the frozen v1 contract.
// Deprecated: use DefaultAssemblyMediaContractV2 for newly produced clips.
func DefaultAssemblyMediaContract() AssemblyMediaContract {
	return AssemblyMediaContract{
		ID:                   AssemblyMediaContractID,
		Version:              AssemblyMediaContractVersion,
		AssemblyReady:        true,
		Container:            "mp4",
		VideoCodec:           "h264",
		VideoProfile:         "high",
		VideoLevel:           "4.0",
		VideoExtradataSHA256: CanonicalVideoExtradataSHA256,
		PixelFormat:          "yuv420p",
		Width:                CanonicalWidth,
		Height:               CanonicalHeight,
		FPS:                  FrameRate{Num: CanonicalFPSNum, Den: CanonicalFPSDen},
		VideoTimeBase:        Rational{Num: 1, Den: 90000},
		AudioTimeBase:        Rational{Num: 1, Den: 48000},
		SAR:                  Rational{Num: 1, Den: 1},
		ColorRange:           "tv",
		ColorSpace:           "bt709",
		ColorTransfer:        "bt709",
		ColorPrimaries:       "bt709",
		FieldOrder:           "progressive",
		KeyframeInterval:     48,
		BFrames:              0,
		ClosedGOP:            true,
		AudioCodec:           "aac",
		AudioProfile:         "LC",
		AudioSampleRate:      48000,
		AudioChannels:        2,
		AudioChannelLayout:   "stereo",
		AudioBitrate:         "128k",
		VideoStreams:         1,
		AudioStreams:         1,
		StartPTS:             0,
	}
}

// DefaultAssemblyMediaContractV2 returns the canonical contract for newly
// produced assembly-ready clips. V1 is intentionally left unchanged because
// its contract ID is already present on legacy assets.
func DefaultAssemblyMediaContractV2() AssemblyMediaContract {
	c := DefaultAssemblyMediaContract()
	c.ID = AssemblyMediaContractV2ID
	c.Version = AssemblyMediaContractV2Version
	c.VideoLevel = "4.1"
	c.AudioBitrate = "192k"
	return c
}

// DefaultAssemblyReadyVideoContract returns the frozen v1 contract.
// Deprecated: use DefaultAssemblyMediaContract().
func DefaultAssemblyReadyVideoContract() AssemblyMediaContract {
	return DefaultAssemblyMediaContract()
}

// ValidateExact checks every dimension exactly (no float epsilon for FPS).
func (c AssemblyMediaContract) ValidateExact() error {
	if c.ID != AssemblyMediaContractID && c.ID != AssemblyMediaContractV2ID {
		return fmt.Errorf("contract id %q is not a supported assembly contract", c.ID)
	}
	isV2 := c.ID == AssemblyMediaContractV2ID
	if c.Version != AssemblyMediaContractVersion && c.Version != AssemblyMediaContractV2Version {
		return fmt.Errorf("contract version %d is unsupported", c.Version)
	}
	if c.Container != "mp4" || c.VideoCodec != "h264" || c.VideoProfile != "high" || c.PixelFormat != "yuv420p" {
		return fmt.Errorf("video codec/container mismatch: %s/%s/%s/%s", c.Container, c.VideoCodec, c.VideoProfile, c.PixelFormat)
	}
	if c.Width != CanonicalWidth || c.Height != CanonicalHeight {
		return fmt.Errorf("geometry %dx%d != %dx%d", c.Width, c.Height, CanonicalWidth, CanonicalHeight)
	}
	if !c.FPS.Equal(FrameRate{Num: CanonicalFPSNum, Den: CanonicalFPSDen}) {
		return fmt.Errorf("fps %d/%d != %d/%d", c.FPS.Num, c.FPS.Den, CanonicalFPSNum, CanonicalFPSDen)
	}
	if !c.VideoTimeBase.Equal(Rational{Num: 1, Den: 90000}) {
		return fmt.Errorf("video timebase %d/%d != 1/90000", c.VideoTimeBase.Num, c.VideoTimeBase.Den)
	}
	if !c.AudioTimeBase.Equal(Rational{Num: 1, Den: 48000}) {
		return fmt.Errorf("audio timebase %d/%d != 1/48000", c.AudioTimeBase.Num, c.AudioTimeBase.Den)
	}
	if !c.SAR.Equal(Rational{Num: 1, Den: 1}) {
		return fmt.Errorf("SAR %d/%d != 1/1", c.SAR.Num, c.SAR.Den)
	}
	if c.ColorRange != "tv" || c.ColorSpace != "bt709" || c.ColorTransfer != "bt709" || c.ColorPrimaries != "bt709" {
		return fmt.Errorf("color %s/%s/%s/%s != tv/bt709", c.ColorRange, c.ColorSpace, c.ColorTransfer, c.ColorPrimaries)
	}
	if c.FieldOrder != "progressive" {
		return fmt.Errorf("field_order %q != progressive", c.FieldOrder)
	}
	if c.KeyframeInterval != 48 {
		return fmt.Errorf("GOP %d != 48", c.KeyframeInterval)
	}
	expectedLevel, expectedBitrate := "4.0", "128k"
	if isV2 {
		expectedLevel, expectedBitrate = "4.1", "192k"
	}
	if c.VideoLevel != expectedLevel {
		return fmt.Errorf("level %q != %s", c.VideoLevel, expectedLevel)
	}
	if c.VideoExtradataSHA256 != CanonicalVideoExtradataSHA256 {
		return fmt.Errorf("video extradata %q != canonical %s", c.VideoExtradataSHA256, CanonicalVideoExtradataSHA256)
	}
	if c.AudioCodec != "aac" || c.AudioProfile != "LC" || c.AudioSampleRate != 48000 || c.AudioChannels != 2 || c.AudioChannelLayout != "stereo" || c.AudioBitrate != expectedBitrate {
		return fmt.Errorf("audio %s/%s/%d/%d/%s/%s mismatch", c.AudioCodec, c.AudioProfile, c.AudioSampleRate, c.AudioChannels, c.AudioChannelLayout, c.AudioBitrate)
	}
	if c.VideoStreams != 1 || c.AudioStreams != 1 {
		return fmt.Errorf("stream count video %d audio %d != 1/1", c.VideoStreams, c.AudioStreams)
	}
	if c.StartPTS != 0 {
		return fmt.Errorf("start_pts %d != 0", c.StartPTS)
	}
	if c.BFrames != 0 {
		return fmt.Errorf("b_frames %d != 0", c.BFrames)
	}
	if !c.ClosedGOP {
		return fmt.Errorf("closed_gop must be true")
	}
	if !c.AssemblyReady {
		return fmt.Errorf("assembly_ready must be true for ValidateExact")
	}
	return nil
}

// StreamSignature is the exact ffprobe-derived signature for copy-gate.
type StreamSignature struct {
	VideoCodec           string    `json:"video_codec"`
	VideoProfile         string    `json:"video_profile"`
	VideoLevel           string    `json:"video_level"`
	VideoExtradataSHA256 string    `json:"video_extradata_sha256"`
	PixelFormat          string    `json:"pixel_format"`
	Width                int       `json:"width"`
	Height               int       `json:"height"`
	SAR                  Rational  `json:"sar"`
	FPS                  FrameRate `json:"fps"`
	VideoTimeBase        Rational  `json:"video_time_base"`
	AudioTimeBase        Rational  `json:"audio_time_base"`
	ColorRange           string    `json:"color_range"`
	ColorSpace           string    `json:"color_space"`
	ColorTransfer        string    `json:"color_transfer"`
	ColorPrimaries       string    `json:"color_primaries"`
	FieldOrder           string    `json:"field_order"`
	AudioCodec           string    `json:"audio_codec"`
	AudioProfile         string    `json:"audio_profile"`
	AudioSampleRate      int       `json:"audio_sample_rate"`
	AudioChannels        int       `json:"audio_channels"`
	AudioChannelLayout   string    `json:"channel_layout"`
	VideoStreams         int       `json:"video_streams"`
	AudioStreams         int       `json:"audio_streams"`
	StreamOrder          string    `json:"stream_order"` // e.g. "v:0,a:1"
}

// Fingerprint returns sha256 of canonical JSON.
func (s StreamSignature) Fingerprint() string {
	b, _ := json.Marshal(s)
	return digest.SHA256Bytes(b)
}

// ── CompositionContract ───────────────────────────────────────────────
//
// CompositionContract describes what is VISUALLY in the video — the editorial
// choices applied during rendering. It is intentionally separate from
// AssemblyMediaContract, which describes only the technical stream identity.
//
// You can have the same technical stream (h264, 1920x1080, 24fps) with
// different composition facts (watermark yes/no, subtitles yes/no). The
// assembler gates on both: contract_id + stream_signature for technical
// compatibility, plus CompositionContract for editorial completeness.

type CompositionContract struct {
	WatermarkApplied bool   `json:"watermark_already_applied"`
	SubtitlesBurned  bool   `json:"subtitles_already_burned"`
	OverlayApplied   bool   `json:"overlay_already_applied"`
	SlowZoom         bool   `json:"slow_zoom"`
	ScaleMode        string `json:"scale_mode"` // "cover" | "contain" | "fill" | "none"
}

// DefaultCompositionContract returns the canonical composition contract
// for an assembly-ready clip: watermark applied, subtitles burned, no overlay.

// Validate checks the composition fields are populated with valid values.
