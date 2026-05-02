package ffmpeg

import (
	"encoding/json"
	"fmt"
)

type probeOutput struct {
	Streams []struct {
		CodecName string  `json:"codec_name"`
		Width     int     `json:"width"`
		Height    int     `json:"height"`
		RFrameRate string `json:"r_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func parseProbeOutput(data []byte) (*MediaInfo, error) {
	var output probeOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	info := &MediaInfo{}

	if output.Format.Duration != "" {
		fmt.Sscanf(output.Format.Duration, "%f", &info.Duration)
	}

	if len(output.Streams) > 0 {
		stream := output.Streams[0]
		info.Width = stream.Width
		info.Height = stream.Height
		info.Codec = stream.CodecName
		fmt.Sscanf(stream.RFrameRate, "%f", &info.FPS)
	}

	return info, nil
}
