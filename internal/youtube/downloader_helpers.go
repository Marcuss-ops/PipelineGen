package youtube

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseSearchOutput converte l'output yt-dlp in SearchResult
func (d *Downloader) parseSearchOutput(output string) []SearchResult {
	var results []SearchResult
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		if line == "" || !strings.Contains(line, "|") {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}

		result := SearchResult{
			ID:        strings.TrimSpace(parts[0]),
			Title:     strings.TrimSpace(parts[1]),
			URL:       fmt.Sprintf("https://www.youtube.com/watch?v=%s", strings.TrimSpace(parts[0])),
		}

		if len(parts) > 2 {
			result.Channel = strings.TrimSpace(parts[2])
		}
		if len(parts) > 3 {
			result.ChannelID = strings.TrimSpace(parts[3])
			result.ChannelURL = fmt.Sprintf("https://www.youtube.com/channel/%s", result.ChannelID)
		}
		if len(parts) > 4 {
			viewStr := strings.TrimSpace(parts[4])
			if viewCount, err := strconv.ParseInt(viewStr, 10, 64); err == nil {
				result.Views = viewCount
			}
		}
		if len(parts) > 5 {
			durStr := strings.TrimSpace(parts[5])
			if durSec, err := strconv.Atoi(durStr); err == nil && durSec > 0 {
				result.Duration = time.Duration(durSec) * time.Second
			}
		}
		if len(parts) > 6 {
			dateStr := strings.TrimSpace(parts[6])
			if len(dateStr) == 8 {
				result.UploadDate = fmt.Sprintf("%s-%s-%s", dateStr[:4], dateStr[4:6], dateStr[6:8])
			}
		}
		if len(parts) > 7 {
			thumb := strings.TrimSpace(parts[7])
			if thumb != "" && thumb != "NA" {
				result.Thumbnail = thumb
			} else {
				result.Thumbnail = fmt.Sprintf("https://img.youtube.com/vi/%s/hqdefault.jpg", result.ID)
			}
		} else {
			result.Thumbnail = fmt.Sprintf("https://img.youtube.com/vi/%s/hqdefault.jpg", result.ID)
		}

		if result.ID != "" {
			results = append(results, result)
		}
	}

	return results
}
