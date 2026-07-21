package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// MediaInfo holds the probed metadata for a media file.
//
// Fase 9 (July 2026): the struct is extended with the 4 fields the
// user-spec literal calls out as guards (pix_fmt, container, rate,
// stream number) plus the supporting fields the contract test
// fixtures need. The new fields are populated by the ffprobe
// parser in Probe() below; callers that want the per-guard
// validation (Fase 9 Commits 2-8) read these fields and compare
// against the expected values from ffmpeg.NormalizeOptions /
// config.VideoConfig.
type MediaInfo struct {
	// Duration of the media file. 0 means "no duration reported
	// by ffprobe" (corrupt or empty file).
	Duration time.Duration
	// Width in pixels (video streams only; 0 for audio-only).
	Width int
	// Height in pixels (video streams only; 0 for audio-only).
	Height int
	// FPS is the frame rate of the video stream (0 for audio-only).
	FPS float64
	// BitRate in bits per second.
	BitRate int64
	// VideoCodec name (e.g. "h264", "vp9"). Empty for audio-only files.
	VideoCodec string
	// AudioCodec name (e.g. "aac", "mp3"). Empty for video-only files.
	AudioCodec string
	// SampleRate is the audio sample rate in Hz (e.g. 48000).
	// 0 when no audio stream is present.
	SampleRate int
	// Channels is the number of audio channels (e.g. 2).
	// 0 when no audio stream is present.
	Channels int
	// HasVideo is true when at least one video stream is present.
	HasVideo bool
	// HasAudio is true when at least one audio stream is present.
	HasAudio bool

	// ── Fase 9 fields (added for the post-normalize contract
	//    guards — see probe.go Probe() parsing loop). These fields
	//    are ADDITIVE: existing callers continue to work; only
	//    the per-guard validators added in subsequent commits
	//    consume them. ──

	// PixelFormat is the pixel format of the first video stream
	// (e.g. "yuv420p", "yuvj420p", "yuv422p", "rgb24"). Empty
	// when no video stream is present. Maps to ffprobe's
	// `streams[i].pix_fmt` JSON key.
	PixelFormat string

	// FormatName is the container format reported by ffprobe
	// (e.g. "mov,mp4,m4a,3gp,3g2,mj2" for an .mp4 file, "matroska,webm"
	// for a .mkv, "mp3" for an audio-only file). The validator
	// matches against the FIRST entry (the canonical container
	// for the file). Maps to ffprobe's `format.format_name` JSON
	// key.
	FormatName string

	// VideoStreamCount is the number of streams with
	// codec_type=video. The Fase 9 stream-number guard
	// requires VideoStreamCount >= 1 (per the user spec
	// literal "≥1 stream video").
	VideoStreamCount int

	// StreamCount is the total number of streams of every
	// kind (video + audio + subtitle + data + attachment).
	// Distinct from VideoStreamCount; the validator may
	// also require StreamCount >= 1 (any real media file has
	// at least 1 stream).
	StreamCount int
}

// ffprobeOutput is the minimal JSON structure we parse from ffprobe output.
type ffprobeOutput struct {
	Format  ffprobeFormat   `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
	BitRate  string `json:"bit_rate"`
	// FormatName is the container format reported by ffprobe
	// (e.g. "mov,mp4,m4a,3gp,3g2,mj2" for .mp4, "matroska,webm"
	// for .mkv). The validator matches the FIRST comma-separated
	// entry against the expected container.
	FormatName string `json:"format_name"`
}

type ffprobeStream struct {
	CodecType    string `json:"codec_type"` // "video" | "audio" | "subtitle" | "data" | "attachment"
	CodecName    string `json:"codec_name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	AvgFrameRate string `json:"avg_frame_rate"` // e.g. "30000/1001" or "25/1"
	// PixFmt is the pixel format of a video stream
	// (e.g. "yuv420p", "yuvj420p"). Empty for non-video streams.
	// Maps to ffprobe's `streams[i].pix_fmt` JSON key.
	PixFmt string `json:"pix_fmt"`
	// SampleRate is the audio sample rate in Hz (e.g. "48000").
	SampleRate string `json:"sample_rate"`
	// Channels is the number of audio channels.
	Channels int `json:"channels"`
}

// Probe interrogates a media file using ffprobe and returns its MediaInfo.
// ffprobe must be on the same PATH as ffmpeg; if not found, an error is returned.
//
// godlike/06 SSOT: Probe routes through p.runner (the canonical
// ProcessRunner port) NOT the package-level process.Run function.
// This matches the pattern in Normalize, CutCopy, ExtractFrame,
// GenerateProxy, GenerateStoryboard, RemuxHLS — every Processor
// method funnels through p.runner so tests can inject a fake
// via Processor.WithRunner(). The pre-Fase-9 implementation
// called process.Run directly, which made the parsing path
// un-mockable and forced tests to require a real ffprobe binary
// on PATH (Fase 9 Commit 1 fixes this consistency gap).
func (p *Processor) Probe(ctx context.Context, path string) (*MediaInfo, error) {
	// Derive ffprobe path from the configured ffmpeg path.
	probePath := deriveProbePath(p.path)

	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	}

	res, err := p.runner.Run(ctx, probePath, args, process.Options{
		Timeout:        2 * time.Minute,
		CombinedOutput: false,
	})
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var result ffprobeOutput
	if err := json.Unmarshal([]byte(res.Stdout), &result); err != nil {
		return nil, fmt.Errorf("ffprobe parse: %w", err)
	}

	info := &MediaInfo{}

	// Parse duration
	if d, err := strconv.ParseFloat(result.Format.Duration, 64); err == nil {
		info.Duration = time.Duration(d * float64(time.Second))
	}

	// Parse bitrate
	if br, err := strconv.ParseInt(result.Format.BitRate, 10, 64); err == nil {
		info.BitRate = br
	}

	// Parse format name (container). The first comma-separated
	// entry is the canonical container per ffprobe convention
	// (e.g. "mov,mp4,m4a,3gp,3g2,mj2" -> canonical "mov" for an
	// mp4, or sometimes "mp4" depending on ffprobe version).
	// We keep the FULL format_name string and let the validator
	// (Fase 9 commit 6) split on comma and check the canonical
	// entry against the expected container.
	info.FormatName = result.Format.FormatName

	// Parse streams
	for _, s := range result.Streams {
		// Every stream counts toward StreamCount regardless of
		// codec_type (the validator's "≥1 stream" check uses
		// StreamCount; the validator's "≥1 video stream" check
		// uses VideoStreamCount).
		info.StreamCount++

		switch s.CodecType {
		case "video":
			info.HasVideo = true
			info.VideoStreamCount++
			// Capture the FIRST video stream's codec (the
			// validator in Fase 9 commit 4 compares against the
			// expected codec from NormalizeOptions; later
			// video streams are ignored to match the canonical
			// "ffmpeg -i shows the first video" convention).
			if info.VideoCodec == "" {
				info.VideoCodec = s.CodecName
			}
			// Capture the FIRST video stream's dimensions
			// (Width/Height are single-value fields; multi-stream
			// comparison is a forward-pointer). The pre-Fase-9
			// implementation overwrote on every video stream
			// (last-wins), which broke multi-video-stream files
			// where the first stream is the "primary" stream
			// and subsequent streams are secondary tracks.
			// Fase 9 Commit 1 makes this consistent with the
			// "first wins" semantics of FPS and PixelFormat.
			if info.Width == 0 && s.Width > 0 {
				info.Width = s.Width
			}
			if info.Height == 0 && s.Height > 0 {
				info.Height = s.Height
			}
			// Parse avg_frame_rate (e.g. "30000/1001" or "25/1" or "30")
			if s.AvgFrameRate != "" && info.FPS == 0 {
				if fps := parseFrameRate(s.AvgFrameRate); fps > 0 {
					info.FPS = fps
				}
			}
			// Capture the pixel format of the FIRST video
			// stream (Info.PixelFormat is a single string, not
			// a per-stream array — multi-stream pixel-format
			// comparison is a forward-pointer; the validator
			// in Fase 9 commit 5 compares against the
			// EXPECTED pixel format from config).
			if info.PixelFormat == "" && s.PixFmt != "" {
				info.PixelFormat = s.PixFmt
			}
		case "audio":
			info.HasAudio = true
			info.AudioCodec = s.CodecName
			if info.SampleRate == 0 && s.SampleRate != "" {
				if sr, err := strconv.Atoi(s.SampleRate); err == nil {
					info.SampleRate = sr
				}
			}
			if info.Channels == 0 && s.Channels > 0 {
				info.Channels = s.Channels
			}
		}
	}

	return info, nil
}

// deriveProbePath returns the ffprobe binary path based on the ffmpeg path.
// If ffmpegPath is "ffmpeg" or empty, returns "ffprobe".
// If ffmpegPath is an absolute path, replaces the binary name with "ffprobe".
func deriveProbePath(ffmpegPath string) string {
	if ffmpegPath == "" || ffmpegPath == "ffmpeg" {
		return "ffprobe"
	}
	// Replace the last path component ("ffmpeg") with "ffprobe".
	p := ffmpegPath
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i+1] + "ffprobe"
		}
	}
	return "ffprobe"
}

// parseFrameRate parses an ffprobe avg_frame_rate string (e.g. "30000/1001", "25/1", "30") into a float64.
// Returns 0 if the string cannot be parsed.
func parseFrameRate(s string) float64 {
	if s == "" || s == "0/0" {
		return 0
	}
	// Handle "num/den" format
	for i, c := range s {
		if c == '/' {
			num, err1 := strconv.ParseFloat(s[:i], 64)
			den, err2 := strconv.ParseFloat(s[i+1:], 64)
			if err1 == nil && err2 == nil && den != 0 {
				return num / den
			}
			return 0
		}
	}
	// Plain number
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
