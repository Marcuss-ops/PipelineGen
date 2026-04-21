package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// GetDetailedInfo ottiene informazioni dettagliate di un video
func (d *Downloader) GetDetailedInfo(ctx context.Context, url string) (*LegacyDetailedVideoInfo, error) {
	args := []string{
		"--dump-json",
		"--no-download",
		url,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp info failed: %w", err)
	}

	// Prendi la prima linea JSON valida
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		return d.parseDetailedInfo(raw), nil
	}

	return nil, fmt.Errorf("no valid JSON found")
}

// parseDetailedInfo converte JSON raw in LegacyDetailedVideoInfo
func (d *Downloader) parseDetailedInfo(raw map[string]interface{}) *LegacyDetailedVideoInfo {
	info := &LegacyDetailedVideoInfo{}

	if id, ok := raw["id"].(string); ok {
		info.ID = id
		info.URL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", id)
		info.Thumbnail = fmt.Sprintf("https://img.youtube.com/vi/%s/maxresdefault.jpg", id)
		info.ThumbnailHQ = fmt.Sprintf("https://img.youtube.com/vi/%s/hqdefault.jpg", id)
		info.ThumbnailMax = fmt.Sprintf("https://img.youtube.com/vi/%s/maxresdefault.jpg", id)
	}
	if title, ok := raw["title"].(string); ok {
		info.Title = title
	}
	if desc, ok := raw["description"].(string); ok {
		if len(desc) > 500 {
			info.Description = desc[:500]
		} else {
			info.Description = desc
		}
	}
	if dur, ok := raw["duration"].(float64); ok {
		info.DurationSec = int(dur)
		info.Duration = fmt.Sprintf("%d:%02d", int(dur)/60, int(dur)%60)
	}
	if views, ok := raw["view_count"].(float64); ok {
		info.ViewCount = int64(views)
	}
	if likes, ok := raw["like_count"].(float64); ok {
		info.LikeCount = fmt.Sprintf("%.0f", likes)
	} else {
		info.LikeCount = "N/A"
	}
	if chID, ok := raw["channel_id"].(string); ok {
		info.ChannelID = chID
		info.ChannelURL = fmt.Sprintf("https://www.youtube.com/channel/%s", chID)
	}
	if ch, ok := raw["channel"].(string); ok {
		info.Channel = ch
	}
	if uploadDate, ok := raw["upload_date"].(string); ok {
		if len(uploadDate) == 8 {
			info.UploadDate = fmt.Sprintf("%s-%s-%s", uploadDate[:4], uploadDate[4:6], uploadDate[6:8])
		} else {
			info.UploadDate = uploadDate
		}
	}
	if tags, ok := raw["tags"].([]interface{}); ok {
		for i, t := range tags {
			if i >= 10 {
				break
			}
			if tag, ok := t.(string); ok {
				info.Tags = append(info.Tags, tag)
			}
		}
	}
	if cats, ok := raw["categories"].([]interface{}); ok {
		for _, c := range cats {
			if cat, ok := c.(string); ok {
				info.Categories = append(info.Categories, cat)
			}
		}
	}

	return info
}
