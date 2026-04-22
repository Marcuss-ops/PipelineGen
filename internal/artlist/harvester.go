package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// DiscoveredClip represents metadata of a clip found during discovery.
type DiscoveredClip struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
	Mp4URL    string `json:"mp4Url"`
	Duration  int    `json:"duration"`
}

// DiscoveryResult represents the full output of a discovery run.
type DiscoveryResult struct {
	Keyword   string           `json:"keyword"`
	Timestamp time.Time        `json:"timestamp"`
	Count     int              `json:"count"`
	Clips     []DiscoveredClip `json:"clips"`
}

// Harvester handles the discovery of new Artlist clips.
type Harvester struct {
	scraperPath string
	outputDir   string
}

// NewHarvester creates a new harvester.
func NewHarvester(scraperPath, outputDir string) *Harvester {
	return &Harvester{
		scraperPath: scraperPath,
		outputDir:   outputDir,
	}
}

// Harvest performs a live search on Artlist and returns metadata for top N results.
// It does NOT download any video files.
func (h *Harvester) Harvest(ctx context.Context, keyword string, maxResults int) (*DiscoveryResult, error) {
	logger.Info("Starting Artlist discovery harvest", 
		zap.String("keyword", keyword), 
		zap.Int("max_results", maxResults),
	)

	// Invocazione dello script Node.js
	cmd := exec.CommandContext(ctx, "node", h.scraperPath, keyword, fmt.Sprintf("%d", maxResults))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("scraper execution failed: %w (output: %s)", err, string(output))
	}

	var result DiscoveryResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse scraper output: %w", err)
	}

	logger.Info("Artlist harvest completed", 
		zap.String("keyword", keyword), 
		zap.Int("found", result.Count),
	)

	return &result, nil
}

// SaveToStaging saves the discovery results to a JSON file for inspection.
func (h *Harvester) SaveToStaging(result *DiscoveryResult) (string, error) {
	if result == nil {
		return "", fmt.Errorf("result is nil")
	}

	filename := fmt.Sprintf("discovery_%s_%d.json", result.Keyword, result.Timestamp.Unix())
	path := filepath.Join(h.outputDir, filename)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}

	return path, nil
}
