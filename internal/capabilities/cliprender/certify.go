package cliprender

// certify.go owns the CERTIFIED OUTPUT FACTS and their projection into the
// contract gate.
//
// Why it exists (clip.render audit 2026-09-13, finding P1 "double
// certification"): the render boundary probes the output it produced and
// certifies the artifact; PipelineGen then probed the downloaded file again
// with the weaker Rust probe (which cannot report codec profile/level,
// timebase, SAR, colour, field order, GOP interval, channel layout or audio
// bitrate). Those dimensions were silently SKIPPED by ValidateContract, so a
// violating render passed.
//
// RenderingGen is now the SINGLE certification owner of the output. Its
// certified facts travel with the artifact as `output_facts`, are validated
// against the resolved contract by this package, and the redundant local
// `RustOutputProber` pass (and the `OutputProber` port that existed only to
// feed it) were DELETED in the same audit. The downloaded bytes are still
// verified: the download computes SHA-256 while streaming and checks it
// against the queue's certified digest, so a corrupt artifact fails before it
// can ever reach this gate.
//
// `OutputFacts` is populated from the wire's output_facts object by the
// RenderingGen adapter. A nil `RenderOutcome.Facts` is a legacy boundary that
// certified only the flat summary; the projection then uses the flat fields
// and leaves dimensions it does not carry unset (never guessed).

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUncertifiedOutput is returned when a render outcome carries no certified
// structural facts at all. Fail-closed: an output nobody certified is never
// validated into existence.
var ErrUncertifiedOutput = errors.New("clip.render: rendered output is uncertified")

// OutputFacts is the COMPLETE structural certification of a rendered artifact,
// as probed and certified by the RenderingGen boundary. It mirrors the queue
// wire's output_facts object field-for-field.
//
// It is the certification owner for every dimension of the output contract.
// Zero/empty means "the boundary did not report this dimension" and is never
// treated as a violation.
type OutputFacts struct {
	Container        string `json:"container,omitempty"`
	HasVideo         bool   `json:"has_video,omitempty"`
	VideoStreams     int    `json:"video_streams,omitempty"`
	VideoCodec       string `json:"video_codec,omitempty"`
	VideoProfile     string `json:"video_profile,omitempty"`
	VideoLevel       string `json:"video_level,omitempty"`
	PixelFormat      string `json:"pixel_format,omitempty"`
	Width            int    `json:"width,omitempty"`
	Height           int    `json:"height,omitempty"`
	FPSNum           int    `json:"fps_num,omitempty"`
	FPSDen           int    `json:"fps_den,omitempty"`
	VideoTimeBaseNum int    `json:"video_timebase_num,omitempty"`
	VideoTimeBaseDen int    `json:"video_timebase_den,omitempty"`
	AudioTimeBaseNum int    `json:"audio_timebase_num,omitempty"`
	AudioTimeBaseDen int    `json:"audio_timebase_den,omitempty"`
	SARNum           int    `json:"sar_num,omitempty"`
	SARDen           int    `json:"sar_den,omitempty"`
	ColorRange       string `json:"color_range,omitempty"`
	ColorSpace       string `json:"color_space,omitempty"`
	ColorTransfer    string `json:"color_transfer,omitempty"`
	ColorPrimaries   string `json:"color_primaries,omitempty"`
	FieldOrder       string `json:"field_order,omitempty"`
	KeyframeInterval int    `json:"keyframe_interval,omitempty"`
	StartPTS         int64  `json:"start_pts,omitempty"`
	HasAudio         bool   `json:"has_audio,omitempty"`
	AudioStreams     int    `json:"audio_streams,omitempty"`
	AudioCodec       string `json:"audio_codec,omitempty"`
	AudioProfile     string `json:"audio_profile,omitempty"`
	SampleRate       int    `json:"sample_rate,omitempty"`
	Channels         int    `json:"channels,omitempty"`
	ChannelLayout    string `json:"channel_layout,omitempty"`
	AudioBitrate     string `json:"audio_bitrate,omitempty"`
}

// RenderOutcomeFacts resolves the effective certified fact set for an outcome.
// The complete `Facts` object wins; a legacy outcome falls back to its flat
// certified summary (container/codec/profile/pixel format/streams/geometry).
// Returns nil when nothing at all was certified.
func RenderOutcomeFacts(outcome *RenderOutcome) *OutputFacts {
	if outcome == nil {
		return nil
	}
	if outcome.Facts != nil {
		return outcome.Facts
	}
	if outcome.Container == "" && outcome.VideoCodec == "" && outcome.VideoProfile == "" &&
		outcome.PixelFormat == "" && outcome.AudioStreams == 0 && outcome.Width == 0 {
		return nil
	}
	hasVideo := outcome.VideoCodec != "" || outcome.Width > 0 || outcome.Height > 0
	videoStreams := 0
	if hasVideo {
		videoStreams = 1
	}
	return &OutputFacts{
		Container:    outcome.Container,
		HasVideo:     hasVideo,
		VideoStreams: videoStreams,
		VideoCodec:   outcome.VideoCodec,
		VideoProfile: outcome.VideoProfile,
		PixelFormat:  outcome.PixelFormat,
		Width:        int(outcome.Width),
		Height:       int(outcome.Height),
		FPSNum:       int(outcome.FPSNum),
		FPSDen:       int(outcome.FPSDen),
		HasAudio:     outcome.AudioStreams > 0,
		AudioStreams: outcome.AudioStreams,
	}
}

// OutputProbeFromCertified projects the certified fact set into the
// capability-owned OutputProbe the contract gate consumes.
//
// Fail-closed: an outcome that certified NOTHING is a typed error, never an
// empty probe that would make ValidateContract skip every dimension. The
// container and codec profile are normalized to the canonical contract values
// (ffprobe reports the MP4 family as "mov,mp4,..." and the profile as "High"
// while the contract declares "mp4" and "high").
func OutputProbeFromCertified(outcome *RenderOutcome) (*OutputProbe, error) {
	certified := RenderOutcomeFacts(outcome)
	if certified == nil {
		return nil, fmt.Errorf("%w: the producing boundary certified no structural facts", ErrUncertifiedOutput)
	}
	hasVideo := certified.HasVideo || certified.VideoStreams > 0 || certified.VideoCodec != "" || certified.Width > 0
	videoStreams := certified.VideoStreams
	if hasVideo && videoStreams == 0 {
		videoStreams = 1
	}
	hasAudio := certified.HasAudio || certified.AudioStreams > 0
	audioStreams := certified.AudioStreams
	if hasAudio && audioStreams == 0 {
		audioStreams = 1
	}
	probe := &OutputProbe{
		Container:        normalizeContainer(certified.Container),
		HasVideo:         hasVideo,
		VideoStreams:     videoStreams,
		VideoCodec:       certified.VideoCodec,
		VideoProfile:     normalizeVideoProfile(certified.VideoProfile),
		VideoLevel:       certified.VideoLevel,
		PixelFormat:      certified.PixelFormat,
		Width:            certified.Width,
		Height:           certified.Height,
		FPSNum:           certified.FPSNum,
		FPSDen:           certified.FPSDen,
		VideoTimeBaseNum: certified.VideoTimeBaseNum,
		VideoTimeBaseDen: certified.VideoTimeBaseDen,
		AudioTimeBaseNum: certified.AudioTimeBaseNum,
		AudioTimeBaseDen: certified.AudioTimeBaseDen,
		SARNum:           certified.SARNum,
		SARDen:           certified.SARDen,
		ColorRange:       certified.ColorRange,
		ColorSpace:       certified.ColorSpace,
		ColorTransfer:    certified.ColorTransfer,
		ColorPrimaries:   certified.ColorPrimaries,
		FieldOrder:       certified.FieldOrder,
		KeyframeInterval: certified.KeyframeInterval,
		StartPTS:         certified.StartPTS,
		HasAudio:         hasAudio,
		AudioStreams:     audioStreams,
		AudioCodec:       certified.AudioCodec,
		AudioProfile:     certified.AudioProfile,
		SampleRate:       certified.SampleRate,
		Channels:         certified.Channels,
		ChannelLayout:    certified.ChannelLayout,
		AudioBitrate:     certified.AudioBitrate,
	}
	if probe.FPSNum > 0 && probe.FPSDen > 0 {
		probe.FPS = float64(probe.FPSNum) / float64(probe.FPSDen)
	}
	return probe, nil
}

// normalizeContainer projects an ffprobe format_name family onto the
// canonical contract container. ffprobe reports MP4 as the compatible family
// "mov,mp4,m4a,3gp,3g2,mj2", and the certified artifact carries that string
// verbatim.
func normalizeContainer(raw string) string {
	container := strings.TrimSpace(raw)
	if i := strings.IndexByte(container, ','); i >= 0 {
		container = strings.TrimSpace(container[:i])
	}
	if container == "mov" && strings.Contains(raw, "mp4") {
		container = "mp4"
	}
	return container
}

// normalizeVideoProfile lowercases a codec profile so ffprobe casing ("High")
// compares equal to the canonical contract value ("high").
func normalizeVideoProfile(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
