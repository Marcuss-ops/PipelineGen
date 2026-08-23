package media

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
// Frozen values for VELOX_ASSEMBLY_READY_V1 (2026-08-23, unico SSOT):
//
//	container mp4, h264 high 4.0 yuv420p, 1920x1080, 24/1, 1/1 SAR,
//	timebase video 1/90000 audio 1/48000, tv/bt709 progressive,
//	GOP 48 b_frames 0 closed_gop, 1 video +1 audio (video,audio) start_pts 0,
//	AAC-LC 48000 2ch stereo 128k.
//
// Audio profile SSOT: audio.DefaultAudioProfile() in internal/capabilities/audio
// (aac, LC, 48000 Hz, 2 channels, stereo, 128k). This contract MUST stay
// in sync with that profile; MuxFinalAudioCopy gates on the same values.
const (
	AssemblyMediaContractID      = "VELOX_ASSEMBLY_READY_V1"
	AssemblyMediaContractVersion = 1

	// Legacy alias kept for source compatibility during migration.
	AssemblyReadyVideoContractID = AssemblyMediaContractID
	AssemblyReadyVideoVersion    = AssemblyMediaContractVersion
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
	PixelFormat  string `json:"pixel_format"`

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
func DefaultAssemblyMediaContract() AssemblyMediaContract {
	return AssemblyMediaContract{
		ID:                 AssemblyMediaContractID,
		Version:            AssemblyMediaContractVersion,
		AssemblyReady:      true,
		Container:          "mp4",
		VideoCodec:         "h264",
		VideoProfile:       "high",
		VideoLevel:         "4.0",
		PixelFormat:        "yuv420p",
		Width:              1920,
		Height:             1080,
		FPS:                FrameRate{Num: 24, Den: 1},
		VideoTimeBase:      Rational{Num: 1, Den: 90000},
		AudioTimeBase:      Rational{Num: 1, Den: 48000},
		SAR:                Rational{Num: 1, Den: 1},
		ColorRange:         "tv",
		ColorSpace:         "bt709",
		ColorTransfer:      "bt709",
		ColorPrimaries:     "bt709",
		FieldOrder:         "progressive",
		KeyframeInterval:   48,
		BFrames:            0,
		ClosedGOP:          true,
		AudioCodec:         "aac",
		AudioProfile:       "LC",
		AudioSampleRate:    48000,
		AudioChannels:      2,
		AudioChannelLayout: "stereo",
		AudioBitrate:       "128k",
		VideoStreams:       1,
		AudioStreams:       1,
		StartPTS:           0,
	}
}

// DefaultAssemblyReadyVideoContract returns the frozen v1 contract.
// Deprecated: use DefaultAssemblyMediaContract().
func DefaultAssemblyReadyVideoContract() AssemblyMediaContract {
	return DefaultAssemblyMediaContract()
}

// ValidateExact checks every dimension exactly (no float epsilon for FPS).
func (c AssemblyMediaContract) ValidateExact() error {
	if c.ID != AssemblyMediaContractID {
		return fmt.Errorf("contract id %q != %q", c.ID, AssemblyMediaContractID)
	}
	if c.Container != "mp4" || c.VideoCodec != "h264" || c.VideoProfile != "high" || c.PixelFormat != "yuv420p" {
		return fmt.Errorf("video codec/container mismatch: %s/%s/%s/%s", c.Container, c.VideoCodec, c.VideoProfile, c.PixelFormat)
	}
	if c.Width != 1920 || c.Height != 1080 {
		return fmt.Errorf("geometry %dx%d != 1920x1080", c.Width, c.Height)
	}
	if !c.FPS.Equal(FrameRate{Num: 24, Den: 1}) {
		return fmt.Errorf("fps %d/%d != 24/1", c.FPS.Num, c.FPS.Den)
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
	if c.VideoLevel != "4.0" {
		return fmt.Errorf("level %q != 4.0", c.VideoLevel)
	}
	if c.AudioCodec != "aac" || c.AudioProfile != "LC" || c.AudioSampleRate != 48000 || c.AudioChannels != 2 || c.AudioChannelLayout != "stereo" || c.AudioBitrate != "128k" {
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
	VideoCodec         string    `json:"video_codec"`
	VideoProfile       string    `json:"video_profile"`
	VideoLevel         string    `json:"video_level"`
	PixelFormat        string    `json:"pixel_format"`
	Width              int       `json:"width"`
	Height             int       `json:"height"`
	SAR                Rational  `json:"sar"`
	FPS                FrameRate `json:"fps"`
	VideoTimeBase      Rational  `json:"video_time_base"`
	AudioTimeBase      Rational  `json:"audio_time_base"`
	ColorRange         string    `json:"color_range"`
	ColorSpace         string    `json:"color_space"`
	ColorTransfer      string    `json:"color_transfer"`
	ColorPrimaries     string    `json:"color_primaries"`
	FieldOrder         string    `json:"field_order"`
	AudioCodec         string    `json:"audio_codec"`
	AudioProfile       string    `json:"audio_profile"`
	AudioSampleRate    int       `json:"audio_sample_rate"`
	AudioChannels      int       `json:"audio_channels"`
	AudioChannelLayout string    `json:"channel_layout"`
	VideoStreams       int       `json:"video_streams"`
	AudioStreams       int       `json:"audio_streams"`
	StreamOrder        string    `json:"stream_order"` // e.g. "v:0,a:1"
}

// Fingerprint returns sha256 of canonical JSON.
func (s StreamSignature) Fingerprint() string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h[:])
}

// FromContract builds the expected signature from the frozen contract.
func StreamSignatureFromContract(c AssemblyMediaContract) StreamSignature {
	return StreamSignature{
		VideoCodec:         c.VideoCodec,
		VideoProfile:       c.VideoProfile,
		VideoLevel:         c.VideoLevel,
		PixelFormat:        c.PixelFormat,
		Width:              c.Width,
		Height:             c.Height,
		SAR:                c.SAR,
		FPS:                c.FPS,
		VideoTimeBase:      c.VideoTimeBase,
		AudioTimeBase:      c.AudioTimeBase,
		ColorRange:         c.ColorRange,
		ColorSpace:         c.ColorSpace,
		ColorTransfer:      c.ColorTransfer,
		ColorPrimaries:     c.ColorPrimaries,
		FieldOrder:         c.FieldOrder,
		AudioCodec:         c.AudioCodec,
		AudioProfile:       c.AudioProfile,
		AudioSampleRate:    c.AudioSampleRate,
		AudioChannels:      c.AudioChannels,
		AudioChannelLayout: c.AudioChannelLayout,
		VideoStreams:       1,
		AudioStreams:       1,
		StreamOrder:        "v:0,a:1",
	}
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
func DefaultCompositionContract() CompositionContract {
	return CompositionContract{
		WatermarkApplied: true,
		SubtitlesBurned:  true,
		OverlayApplied:   false,
		SlowZoom:         false,
		ScaleMode:        "cover",
	}
}

// Validate checks the composition fields are populated with valid values.
func (c CompositionContract) Validate() error {
	if c.ScaleMode != "" && c.ScaleMode != "cover" && c.ScaleMode != "contain" && c.ScaleMode != "fill" && c.ScaleMode != "none" {
		return fmt.Errorf("composition scale_mode %q not in {cover, contain, fill, none}", c.ScaleMode)
	}
	return nil
}

// StreamSignatureFromProbe builds the actual signature from a probed output.
// It is the canonical "probe → signature" derivation consumed by the
// AssemblyCompatibilityGate before concat -c copy.
func StreamSignatureFromProbe(p ProbeFacts) StreamSignature {
	return StreamSignature{
		VideoCodec:         p.VideoCodec,
		VideoProfile:       p.VideoProfile,
		VideoLevel:         p.VideoLevel,
		PixelFormat:        p.PixelFormat,
		Width:              p.Width,
		Height:             p.Height,
		SAR:                Rational{Num: p.SARNum, Den: p.SARDen},
		FPS:                FrameRate{Num: p.FPSNum, Den: p.FPSDen},
		VideoTimeBase:      Rational{Num: p.VideoTimeBaseNum, Den: p.VideoTimeBaseDen},
		AudioTimeBase:      Rational{Num: p.AudioTimeBaseNum, Den: p.AudioTimeBaseDen},
		ColorRange:         p.ColorRange,
		ColorSpace:         p.ColorSpace,
		ColorTransfer:      p.ColorTransfer,
		ColorPrimaries:     p.ColorPrimaries,
		FieldOrder:         p.FieldOrder,
		AudioCodec:         p.AudioCodec,
		AudioProfile:       p.AudioProfile,
		AudioSampleRate:    p.AudioSampleRate,
		AudioChannels:      p.Channels,
		AudioChannelLayout: p.AudioChannelLayout,
		VideoStreams:       p.VideoStreams,
		AudioStreams:       p.AudioStreams,
		StreamOrder:        "v:0,a:1",
	}
}
