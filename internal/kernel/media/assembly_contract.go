package media

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// AssemblyReadyVideoContract is the single SSOT for assembly-ready video.
// Every clip that enters the concat demuxer + -c copy assembler MUST satisfy
// this contract exactly. Intermediate artifacts (ProRes, alpha, proxies)
// declare assembly_ready=false and never enter the assembler.
//
// Frozen values for VELOX_ASSEMBLY_READY_V1 (2026-08-23, unico SSOT):
//   container mp4, h264 high 4.0 yuv420p, 1920x1080, 24/1, 1/1 SAR,
//   timebase video 1/90000 audio 1/48000, tv/bt709 progressive,
//   GOP 48 b_frames 0 closed_gop, 1 video +1 audio (video,audio) start_pts 0,
//   AAC-LC 48000 2ch stereo 128k, watermark/subtitles già incorporati.
//
// Audio profile SSOT: audio.DefaultAudioProfile() in internal/capabilities/audio
// (aac, LC, 48000 Hz, 2 channels, stereo, 128k). This contract MUST stay
// in sync with that profile; MuxFinalAudioCopy gates on the same values.
const (
	AssemblyReadyVideoContractID = "VELOX_ASSEMBLY_READY_V1"
	AssemblyReadyVideoVersion    = 1
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

// VideoContract is the full assembly-ready video contract.
type VideoContract struct {
	ID      string `json:"id"`
	Version int    `json:"version"`

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

	KeyframeInterval int `json:"keyframe_interval"` // GOP

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

// DefaultAssemblyReadyVideoContract returns the frozen v1 contract.
func DefaultAssemblyReadyVideoContract() VideoContract {
	return VideoContract{
		ID:                 AssemblyReadyVideoContractID,
		Version:            AssemblyReadyVideoVersion,
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

// ValidateExact checks every dimension exactly (no float epsilon for FPS).
func (c VideoContract) ValidateExact() error {
	if c.ID != AssemblyReadyVideoContractID {
		return fmt.Errorf("contract id %q != %q", c.ID, AssemblyReadyVideoContractID)
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
	return nil
}

// StreamSignature is the exact ffprobe-derived signature for copy-gate.
type StreamSignature struct {
	VideoCodec     string   `json:"video_codec"`
	VideoProfile   string   `json:"video_profile"`
	VideoLevel     string   `json:"video_level"`
	PixelFormat    string   `json:"pixel_format"`
	Width          int      `json:"width"`
	Height         int      `json:"height"`
	SAR            Rational `json:"sar"`
	FPS            FrameRate `json:"fps"`
	VideoTimeBase  Rational `json:"video_time_base"`
	AudioTimeBase  Rational `json:"audio_time_base"`
	ColorRange     string   `json:"color_range"`
	ColorSpace     string   `json:"color_space"`
	ColorTransfer  string   `json:"color_transfer"`
	ColorPrimaries string   `json:"color_primaries"`
	FieldOrder     string   `json:"field_order"`
	AudioCodec     string   `json:"audio_codec"`
	AudioProfile   string   `json:"audio_profile"`
	AudioSampleRate int     `json:"audio_sample_rate"`
	AudioChannels   int     `json:"audio_channels"`
	AudioChannelLayout string `json:"channel_layout"`
	VideoStreams int `json:"video_streams"`
	AudioStreams int `json:"audio_streams"`
	StreamOrder  string `json:"stream_order"` // e.g. "v:0,a:1"
}

// Fingerprint returns sha256 of canonical JSON.
func (s StreamSignature) Fingerprint() string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h[:])
}

// FromContract builds the expected signature from the frozen contract.
func StreamSignatureFromContract(c VideoContract) StreamSignature {
	return StreamSignature{
		VideoCodec:     c.VideoCodec,
		VideoProfile:   c.VideoProfile,
		VideoLevel:     c.VideoLevel,
		PixelFormat:    c.PixelFormat,
		Width:          c.Width,
		Height:         c.Height,
		SAR:            c.SAR,
		FPS:            c.FPS,
		VideoTimeBase:  c.VideoTimeBase,
		AudioTimeBase:  c.AudioTimeBase,
		ColorRange:     c.ColorRange,
		ColorSpace:     c.ColorSpace,
		ColorTransfer:  c.ColorTransfer,
		ColorPrimaries: c.ColorPrimaries,
		FieldOrder:     c.FieldOrder,
		AudioCodec:     c.AudioCodec,
		AudioProfile:   c.AudioProfile,
		AudioSampleRate: c.AudioSampleRate,
		AudioChannels:   c.AudioChannels,
		AudioChannelLayout: c.AudioChannelLayout,
		VideoStreams: 1,
		AudioStreams: 1,
		StreamOrder:  "v:0,a:1",
	}
}
