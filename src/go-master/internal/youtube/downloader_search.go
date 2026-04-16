package youtube

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"
	"go.uber.org/zap"
)

// Search cerca video su YouTube
func (d *Downloader) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	args := []string{
		"--flat-playlist",
		"--print", "%(id)s|%(title)s|%(channel)s|%(channel_id)s|%(view_count)s|%(duration)s|%(upload_date)s|%(thumbnail)s",
		fmt.Sprintf("ytsearch%d:%s", maxResults, query),
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp search failed: %w", err)
	}

	return d.parseSearchOutput(string(output)), nil
}

// SearchWithOptions ricerca avanzata con più opzioni
func (d *Downloader) SearchWithOptions(ctx context.Context, query string, maxResults int, searchType string) ([]SearchResult, error) {
	var queries []string

	switch searchType {
	case "interviews":
		queries = []string{
			fmt.Sprintf("%s interview", query),
			fmt.Sprintf("%s talk", query),
			fmt.Sprintf("%s podcast appearance", query),
		}
	case "highlights":
		queries = []string{
			fmt.Sprintf("%s highlights best moments", query),
			fmt.Sprintf("%s viral moments", query),
			fmt.Sprintf("%s top moments", query),
		}
	default:
		queries = []string{query}
	}

	var allResults []SearchResult
	seenIDs := make(map[string]bool)
	resultsPerQuery := maxResults / len(queries)
	if resultsPerQuery < 3 {
		resultsPerQuery = 3
	}

	for _, q := range queries {
		if len(allResults) >= maxResults {
			break
		}

		results, err := d.Search(ctx, q, resultsPerQuery)
		if err != nil {
			logger.Warn("Search query failed", zap.String("query", q), zap.Error(err))
			continue
		}

		for _, r := range results {
			if !seenIDs[r.ID] {
				seenIDs[r.ID] = true
				allResults = append(allResults, r)
			}
		}
	}

	return allResults[:util.Min(len(allResults), maxResults)], nil
}

// GetChannelVideos ottiene video da un canale
func (d *Downloader) GetChannelVideos(ctx context.Context, channelURL string, limit int) ([]LegacySearchResult, error) {
	args := []string{
		"--flat-playlist",
		"--print", "%(id)s|%(title)s|%(channel)s|%(channel_id)s|%(view_count)s|%(duration)s|%(upload_date)s|%(thumbnail)s",
		channelURL,
		"--playlist-end", fmt.Sprintf("%d", limit),
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp channel videos failed: %w", err)
	}

	return d.parseLegacySearchOutput(string(output)), nil
}

// parseLegacySearchOutput converte l'output yt-dlp in LegacySearchResult
func (d *Downloader) parseLegacySearchOutput(output string) []LegacySearchResult {
	var results []LegacySearchResult
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		if line == "" || !strings.Contains(line, "|") {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}

		result := LegacySearchResult{
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
			if viewCount, err := strconv.Atoi(viewStr); err == nil {
				result.ViewCount = viewCount
				result.ViewCountFmt = fmt.Sprintf("%d", viewCount)
			}
		}
		if len(parts) > 5 {
			durStr := strings.TrimSpace(parts[5])
			if durSec, err := strconv.Atoi(durStr); err == nil && durSec > 0 {
				result.DurationSec = durSec
				result.Duration = fmt.Sprintf("%d:%02d", durSec/60, durSec%60)
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

// GetTrending ottiene video trending per regione
func (d *Downloader) GetTrending(ctx context.Context, region string, limit int) ([]SearchResult, error) {
	trendingURL := fmt.Sprintf("https://www.youtube.com/feed/trending?region=%s", region)

	args := []string{
		"--flat-playlist",
		"--print", "%(id)s|%(title)s|%(channel)s|%(channel_id)s|%(view_count)s|%(duration)s|%(upload_date)s|%(thumbnail)s",
		trendingURL,
		"--playlist-end", fmt.Sprintf("%d", limit),
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp trending failed: %w", err)
	}

	return d.parseSearchOutput(string(output)), nil
}

// GetLegacyChannelAnalytics ottiene analytics di un canale
func (d *Downloader) GetLegacyChannelAnalytics(ctx context.Context, channelURL string, limit int) (*LegacyChannelAnalytics, error) {
	videos, err := d.GetChannelVideos(ctx, channelURL, limit)
	if err != nil {
		return nil, err
	}

	analytics := &LegacyChannelAnalytics{}
	analytics.Channel.URL = channelURL
	analytics.Analytics.TotalVideos = len(videos)

	var totalViews int64
	var totalDuration int

	for _, v := range videos {
		totalViews += int64(v.ViewCount)
		totalDuration += v.DurationSec

		if analytics.Channel.ID == "" && v.ChannelID != "" {
			analytics.Channel.ID = v.ChannelID
		}
		if analytics.Channel.Name == "" && v.Channel != "" {
			analytics.Channel.Name = v.Channel
		}
	}

	analytics.Analytics.TotalViews = totalViews
	analytics.Analytics.TotalViewsFmt = fmt.Sprintf("%d", totalViews)

	if len(videos) > 0 {
		analytics.Analytics.AverageViews = int(totalViews) / len(videos)
		analytics.Analytics.AverageViewsFmt = fmt.Sprintf("%d", analytics.Analytics.AverageViews)
		analytics.Analytics.AverageDurationSec = totalDuration / len(videos)
		analytics.Analytics.AverageDuration = fmt.Sprintf("%d:%02d", analytics.Analytics.AverageDurationSec/60, analytics.Analytics.AverageDurationSec%60)
	}

	// Convert to LegacySearchResult for RecentVideos (already LegacySearchResult from parseLegacySearchOutput)
	analytics.RecentVideos = videos

	return analytics, nil
}
