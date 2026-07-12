// Package ffmpeg — probe_test.go: Fase 9 Commit 1 test for the
// MediaInfo extension (PixelFormat, FormatName, VideoStreamCount,
// StreamCount).
//
// godlike/06 SSOT: this test pins the CANONICAL wire shape of the
// MediaInfo struct after the Fase 9 data-shape extension. The
// ffprobe JSON contract tested here is what the per-guard
// validators (Fase 9 Commits 2-8) consume.
//
// godlike/07 fail-closed: the test exercises the parsing through
// the actual Probe() code path (not a hand-rolled mock of
// MediaInfo), so a future regression in the JSON → struct mapping
// surfaces as a test failure here, not as a silent
// validator-bypass in production.
package ffmpeg

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// fakeRunner is a ProcessRunner mock that returns a canned JSON
// payload on the next Run call. Phase 9 Commit 1 only exercises
// the ffprobe branch (Probe), so the canned payload is a realistic
// ffprobe JSON output.
type fakeRunner struct {
	stdout string
	err    error
}

func (f *fakeRunner) Run(ctx context.Context, name string, args []string, opts process.Options) (*process.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &process.Result{Stdout: f.stdout}, nil
}

// TestProbe_PopulatesFase9Fields_PinsDataShape locks the wire shape
// of the MediaInfo struct after the Fase 9 data-shape extension.
// The mock runner returns a known ffprobe JSON that exercises:
//   - format.format_name (container)
//   - streams[0].pix_fmt (pixel format)
//   - 2 video streams + 1 audio stream (StreamCount=3, VideoStreamCount=2)
//   - All existing fields (Duration, Width, Height, FPS, Codecs)
func TestProbe_PopulatesFase9Fields_PinsDataShape(t *testing.T) {
	canned := `{
		"format": {
			"duration": "12.345",
			"bit_rate": "4500000",
			"format_name": "mov,mp4,m4a,3gp,3g2,mj2"
		},
		"streams": [
			{
				"codec_type": "video",
				"codec_name": "h264",
				"width": 1920,
				"height": 1080,
				"avg_frame_rate": "30/1",
				"pix_fmt": "yuv420p"
			},
			{
				"codec_type": "video",
				"codec_name": "h264",
				"width": 1280,
				"height": 720,
				"avg_frame_rate": "30/1",
				"pix_fmt": "yuv420p"
			},
			{
				"codec_type": "audio",
				"codec_name": "aac",
				"avg_frame_rate": "0/0"
			}
		]
	}`

	p := NewProcessor("ffmpeg").WithRunner(&fakeRunner{stdout: canned})
	info, err := p.Probe(context.Background(), "/dev/null")
	require.NoError(t, err)
	require.NotNil(t, info)

	// Pre-existing fields — regression-pins the JSON keys ffprobe
	// emits have NOT drifted from the parser expectations.
	assert.Equal(t, "h264", info.VideoCodec, "VideoCodec must be the FIRST video stream's codec")
	assert.Equal(t, "aac", info.AudioCodec, "AudioCodec must be the audio stream's codec")
	assert.Equal(t, 1920, info.Width, "Width must be the FIRST video stream's width")
	assert.Equal(t, 1080, info.Height, "Height must be the FIRST video stream's height")
	assert.Equal(t, 30.0, info.FPS, "FPS must be the FIRST video stream's avg_frame_rate parsed as 30/1")
	assert.True(t, info.HasVideo, "HasVideo must be true when at least one video stream is present")
	assert.True(t, info.HasAudio, "HasAudio must be true when at least one audio stream is present")
	assert.Equal(t, int64(4500000), info.BitRate, "BitRate must parse from format.bit_rate as int64")

	// Fase 9 NEW fields — the focus of this commit. Each must
	// round-trip from the ffprobe JSON to MediaInfo correctly.
	assert.Equal(t, "mov,mp4,m4a,3gp,3g2,mj2", info.FormatName,
		"Fase 9: FormatName must round-trip from format.format_name verbatim (Fase 9 commit 6 split-on-comma will extract the canonical entry)")
	assert.Equal(t, "yuv420p", info.PixelFormat,
		"Fase 9: PixelFormat must be the FIRST video stream's pix_fmt (subsequent streams are ignored)")
	assert.Equal(t, 3, info.StreamCount,
		"Fase 9: StreamCount must count every stream regardless of codec_type (2 video + 1 audio = 3)")
	assert.Equal(t, 2, info.VideoStreamCount,
		"Fase 9: VideoStreamCount must count ONLY streams with codec_type=video")
}

// TestProbe_AudioOnly_NoVideoFields pins the audio-only case: when
// the file has only an audio stream, all video-related fields
// (Width, Height, FPS, VideoCodec, PixelFormat, VideoStreamCount)
// are zero/empty while FormatName and StreamCount are populated.
func TestProbe_AudioOnly_NoVideoFields(t *testing.T) {
	canned := `{
		"format": {
			"duration": "180.000",
			"bit_rate": "128000",
			"format_name": "mp3"
		},
		"streams": [
			{
				"codec_type": "audio",
				"codec_name": "mp3",
				"avg_frame_rate": "0/0"
			}
		]
	}`

	p := NewProcessor("ffmpeg").WithRunner(&fakeRunner{stdout: canned})
	info, err := p.Probe(context.Background(), "/dev/null")
	require.NoError(t, err)
	require.NotNil(t, info)

	// Audio-only invariants: video fields are zero/empty; audio
	// fields are populated; container+stream count are populated.
	assert.Equal(t, "mp3", info.FormatName, "Fase 9: FormatName populates even for audio-only files")
	assert.Equal(t, 1, info.StreamCount, "Fase 9: StreamCount counts the single audio stream")
	assert.Equal(t, 0, info.VideoStreamCount, "Fase 9: VideoStreamCount=0 for audio-only files (Fase 9 commit 8 guard: '≥1 video stream' fails for audio-only)")
	assert.Equal(t, "", info.PixelFormat, "Fase 9: PixelFormat empty for audio-only (no video stream to source it from)")
	assert.Equal(t, "", info.VideoCodec, "Fase 9: VideoCodec empty for audio-only")
	assert.Equal(t, 0, info.Width, "Fase 9: Width=0 for audio-only")
	assert.Equal(t, 0, info.Height, "Fase 9: Height=0 for audio-only")
	assert.Equal(t, 0.0, info.FPS, "Fase 9: FPS=0 for audio-only")
	assert.False(t, info.HasVideo, "Fase 9: HasVideo=false for audio-only")
	assert.True(t, info.HasAudio, "Fase 9: HasAudio=true for audio-only")
	assert.Equal(t, "mp3", info.AudioCodec)
}

// TestProbe_EmptyFormatName pins the edge case where ffprobe
// returns no format_name (e.g. a malformed input). FormatName
// must be the empty string (NOT a panic, NOT a default value
// like "unknown").
func TestProbe_EmptyFormatName(t *testing.T) {
	canned := `{
		"format": {
			"duration": "0",
			"bit_rate": "0"
		},
		"streams": []
	}`

	p := NewProcessor("ffmpeg").WithRunner(&fakeRunner{stdout: canned})
	info, err := p.Probe(context.Background(), "/dev/null")
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, "", info.FormatName, "Fase 9: FormatName is the empty string when ffprobe omits it (validator's job to fail-closed on empty)")
	assert.Equal(t, 0, info.StreamCount, "Fase 9: StreamCount=0 for the no-streams case (validator's job to require >=1)")
	assert.Equal(t, 0, info.VideoStreamCount)
	assert.False(t, info.HasVideo)
	assert.False(t, info.HasAudio)
	assert.Equal(t, time.Duration(0), info.Duration, "Fase 9: Duration=0 for malformed input (Fase 9 commit 3 guard: duration>0 will fail-closed)")
}

// TestParseMediaInfoJSON_Direct pins the JSON-parsing contract in
// isolation (no process.Run dependency). This catches future
// drift in the ffprobe JSON wire shape faster than the
// ProcessRunner-based tests above.
func TestParseMediaInfoJSON_Direct(t *testing.T) {
	raw := `{
		"format": {"format_name": "matroska,webm"},
		"streams": [
			{"codec_type": "video", "codec_name": "vp9", "pix_fmt": "yuv420p", "width": 3840, "height": 2160, "avg_frame_rate": "60000/1001"},
			{"codec_type": "audio", "codec_name": "opus"}
		]
	}`

	var out ffprobeOutput
	require.NoError(t, json.Unmarshal([]byte(raw), &out))

	assert.Equal(t, "matroska,webm", out.Format.FormatName)
	require.Len(t, out.Streams, 2)
	assert.Equal(t, "vp9", out.Streams[0].CodecName)
	assert.Equal(t, "yuv420p", out.Streams[0].PixFmt)
	assert.Equal(t, "opus", out.Streams[1].CodecName)
	assert.Equal(t, "", out.Streams[1].PixFmt, "non-video streams have empty PixFmt")
	assert.Equal(t, "60000/1001", out.Streams[0].AvgFrameRate)
}
