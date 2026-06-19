package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// MediaInfo holds the probed metadata for a media file.
type MediaInfo struct {
	// Duration of the media file.
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
	// HasVideo is true when at least one video stream is present.
	HasVideo bool
	// HasAudio is true when at least one audio stream is present.
	HasAudio bool
}

// ffprobeOutput is the minimal JSON structure we parse from ffprobe output.
type ffprobeOutput struct {
	Format  ffprobeFormat   `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
	BitRate  string `json:"bit_rate"`
}

type ffprobeStream struct {
	CodecType     string `json:"codec_type"` // "video" | "audio"
	CodecName     string `json:"codec_name"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	AvgFrameRate  string `json:"avg_frame_rate"` // e.g. "30000/1001" or "25/1"
}

// Probe interrogates a media file using ffprobe and returns its MediaInfo.
// ffprobe must be on the same PATH as ffmpeg; if not found, an error is returned.
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

	out, err := platform.Run(ctx, probePath, args, platform.ExecOptions{
		Timeout:        2 * time.Minute,
		CombinedOutput: false,
	})
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var result ffprobeOutput
	if err := json.Unmarshal([]byte(out.Stdout), &result); err != nil {
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

	// Parse streams
	for _, s := range result.Streams {
		switch s.CodecType {
		case "video":
			info.HasVideo = true
			info.VideoCodec = s.CodecName
			if s.Width > 0 {
				info.Width = s.Width
			}
			if s.Height > 0 {
				info.Height = s.Height
			}
			// Parse avg_frame_rate (e.g. "30000/1001" or "25/1" or "30")
			if s.AvgFrameRate != "" && info.FPS == 0 {
				if fps := parseFrameRate(s.AvgFrameRate); fps > 0 {
					info.FPS = fps
				}
			}
		case "audio":
			info.HasAudio = true
			info.AudioCodec = s.CodecName
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
