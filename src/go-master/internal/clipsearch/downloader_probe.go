package clipsearch

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func (d *ClipDownloader) ProbeDurationSeconds(ctx context.Context, filePath string) (float64, error) {
	meta, err := ffprobeFormatMeta(ctx, filePath, "format=duration")
	if err != nil {
		return 0, err
	}
	durationStr := strings.TrimSpace(meta["duration"])
	if durationStr == "" || durationStr == "N/A" {
		return 0, fmt.Errorf("duration unavailable")
	}
	val, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return 0, err
	}
	if val < 0 {
		return 0, fmt.Errorf("negative duration")
	}
	return val, nil
}

func ffprobeStreamMeta(ctx context.Context, filePath, streamSelector, showEntries string) (map[string]string, error) {
	cmd := exec.CommandContext(
		ctx,
		"ffprobe",
		"-v", "error",
		"-select_streams", streamSelector,
		"-show_entries", showEntries,
		"-of", "default=nw=1",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseFFprobeKVOutput(out), nil
}

func ffprobeFormatMeta(ctx context.Context, filePath, showEntries string) (map[string]string, error) {
	cmd := exec.CommandContext(
		ctx,
		"ffprobe",
		"-v", "error",
		"-show_entries", showEntries,
		"-of", "default=nw=1",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseFFprobeKVOutput(out), nil
}

func parseFFprobeKVOutput(raw []byte) map[string]string {
	meta := make(map[string]string)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		if k != "" {
			meta[k] = v
		}
	}
	return meta
}
