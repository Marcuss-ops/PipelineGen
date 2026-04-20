package youtube

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (b *YtDlpBackend) parseSearchOutput(output string) []SearchResult {
	var results []SearchResult

	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.Split(line, searchPrintDelimiter)
		if len(parts) < 8 {
			continue
		}

		viewCount, _ := strconv.ParseInt(parts[4], 10, 64)
		durationSec, _ := strconv.Atoi(parts[5])

		results = append(results, SearchResult{
			ID:         parts[0],
			Title:      parts[1],
			Channel:    parts[2],
			ChannelID:  parts[3],
			Views:      viewCount,
			Duration:   time.Duration(durationSec) * time.Second,
			UploadDate: parts[6],
			Thumbnail:  parts[7],
			URL:        fmt.Sprintf("https://www.youtube.com/watch?v=%s", parts[0]),
		})
	}

	return results
}

// vttToText converts VTT subtitle format to plain text
func (b *YtDlpBackend) vttToText(vttContent string) string {
	lines := strings.Split(vttContent, "\n")
	var textParts []string

	for _, line := range lines {
		// Skip VTT metadata and timestamps
		if strings.Contains(line, "WEBVTT") ||
			strings.Contains(line, "-->") ||
			strings.TrimSpace(line) == "" {
			continue
		}

		// Skip line numbers
		if _, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			continue
		}

		// Clean HTML tags
		line = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(line, "")
		line = strings.TrimSpace(line)

		if line != "" {
			textParts = append(textParts, line)
		}
	}

	return strings.Join(textParts, " ")
}
