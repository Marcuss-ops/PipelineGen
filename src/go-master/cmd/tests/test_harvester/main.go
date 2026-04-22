// TestHarvester - Utility per testare il harvesting di clip
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"velox/go-master/internal/adapters"
	"velox/go-master/internal/harvester"
	"velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

var (
	keyword    = flag.String("keyword", "", "Search keyword")
	since      = flag.String("since", "", "Start date (YYYY-MM-DD)")
	minViews   = flag.Int64("minViews", 0, "Minimum views")
	maxResults = flag.Int("maxResults", 10, "Maximum results")
)

func main() {
	flag.Parse()

	logger.Init("info", "json")
	log := logger.Get()
	defer logger.Sync()

	ctx := context.Background()

	if *keyword == "" {
		fmt.Println("Usage: go run ./cmd/test_harvester -keyword='Floyd Mayweather' -since=2024-01-01 -minViews=100000 -maxResults=5")
		*keyword = "Floyd Mayweather interview"
	}

	ytDlpPath := os.Getenv("YT_DLP_PATH")
	if ytDlpPath == "" {
		ytDlpPath = "yt-dlp"
	}

	log.Info("Starting harvester test", zap.String("keyword", *keyword))

	ytCfg := &youtube.Config{Backend: "ytdlp", YtDlpPath: ytDlpPath}
	ytClient, err := youtube.NewClient("ytdlp", ytCfg)
	if err != nil {
		log.Error("Failed to create YouTube client", zap.Error(err))
		return
	}

	testSearch(ctx, ytClient, log)

	log.Info("Test completed")
}

func testSearch(ctx context.Context, ytClient youtube.Client, log *zap.Logger) {
	log.Info("Testing YouTube search with timeframe")

	timeframe := "month"
	if *since != "" {
		if t, err := time.Parse("2006-01-02", *since); err == nil {
			months := time.Since(t).Hours() / 24 / 30
			switch {
			case months < 1:
				timeframe = "week"
			case months < 6:
				timeframe = "month"
			case months < 12:
				timeframe = "year"
			default:
				timeframe = ""
			}
		}
	}

	log.Info("Searching",
		zap.String("keyword", *keyword),
		zap.String("timeframe", timeframe),
		zap.Int64("min_views", *minViews),
	)

	ytAdapter := adapters.NewYouTubeSearcherAdapter(ytClient)
	harvestOpts := &harvester.SearchOptions{
		MaxResults: *maxResults,
		SortBy:     "viewCount",
		Timeframe:  timeframe,
	}

	results, err := ytAdapter.Search(ctx, *keyword, harvestOpts)
	if err != nil {
		log.Error("Search failed", zap.Error(err))
		return
	}

	log.Info("Search results", zap.Int("count", len(results)))

	for i, r := range results {
		if *minViews > 0 && r.Views < *minViews {
			continue
		}
		fmt.Printf("%d. %s (views: %d, dur: %ds, channel: %s)\n",
			i+1, r.Title, r.Views, r.Duration, r.Channel)
	}
	fmt.Println()
}
