// Package ffmpeg provides the canonical interface for FFmpeg operations.
//
// The implementation lives in internal/platform/ffmpeg/.
// New code should depend on this interface, not the concrete FFmpeg wrapper.
package ffmpeg

import "context"

// Processor is the contract for FFmpeg-based media processing.
type Processor interface {
	Probe(ctx context.Context, filePath string) (*MediaInfo, error)
	Encode(ctx context.Context, inputPath, outputPath string, opts EncodeOptions) error
	ImageToVideo(ctx context.Context, imagePath, outputPath string, duration float64) error
}

// MediaInfo contains metadata from FFprobe.
type MediaInfo struct {
	Duration   float64
	Width      int
	Height     int
	Codec      string
	Format     string
	Bitrate    int64
	FileSize   int64
}

// EncodeOptions controls FFmpeg encoding.
type EncodeOptions struct {
	Codec      string
	Bitrate    string
	Resolution string
	FPS        int
	AudioCodec string
	AudioBitrate string
}
