// Package deliveryburn owns the delivery-time ffmpeg/ffprobe execution used by
// the velox burn-subtitles command. It lives under internal/platform/media so
// raw media-binary execution stays inside the canonical media capability, and
// it receives the encoder policy from the caller instead of embedding a codec.
package deliveryburn

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Stream is one ffprobe stream projection.
type Stream struct {
	CodecType string
	CodecName string
	Width     int
	Height    int
}

// MediaInfo is the probe result the delivery burn needs.
type MediaInfo struct {
	DurationSeconds float64
	Streams         []Stream
}

type ffprobeDoc struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

// ProbeMedia runs the canonical ffprobe invocation for a delivery artifact.
func ProbeMedia(ctx context.Context, path string) (MediaInfo, error) {
	var doc ffprobeDoc
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", path).Output()
	if err != nil {
		return MediaInfo{}, err
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return MediaInfo{}, err
	}
	info := MediaInfo{DurationSeconds: parseSeconds(doc.Format.Duration)}
	for _, stream := range doc.Streams {
		info.Streams = append(info.Streams, Stream{
			CodecType: stream.CodecType,
			CodecName: stream.CodecName,
			Width:     stream.Width,
			Height:    stream.Height,
		})
	}
	return info, nil
}

func parseSeconds(value string) float64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return seconds
}

// SampleFrame decodes one RGB24 frame at the given timestamp.
func SampleFrame(ctx context.Context, path string, at float64, width, height int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-loglevel", "error",
		"-ss", strconv.FormatFloat(at, 'f', 3, 64),
		"-i", path,
		"-frames:v", "1",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	want := width * height * 3
	if len(out) != want {
		return nil, fmt.Errorf("decoded %d bytes, want %d", len(out), want)
	}
	return out, nil
}

// BurnOptions is the delivery burn request. Codec/Preset/CRF are supplied by
// the canonical encoder policy, never by this adapter.
type BurnOptions struct {
	WorkDir    string
	ASSName    string
	InputPath  string
	OutputPath string
	Codec      string
	Preset     string
	CRF        int
}

// Burn re-encodes one certified artifact with burned subtitles.
func Burn(ctx context.Context, opts BurnOptions) error {
	if opts.InputPath == "" || opts.OutputPath == "" || opts.WorkDir == "" {
		return fmt.Errorf("delivery burn: input, output and work dir are required")
	}
	if opts.Codec == "" {
		return fmt.Errorf("delivery burn: resolved video codec is required")
	}
	if opts.ASSName == "" {
		opts.ASSName = "subtitles.ass"
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-i", opts.InputPath,
		"-vf", "ass="+opts.ASSName,
		"-c:v", opts.Codec, "-preset", opts.Preset, "-crf", strconv.Itoa(opts.CRF),
		"-pix_fmt", "yuv420p",
		"-c:a", "copy",
		"-movflags", "+faststart",
		opts.OutputPath,
	)
	cmd.Dir = opts.WorkDir
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(combined)))
	}
	return nil
}
