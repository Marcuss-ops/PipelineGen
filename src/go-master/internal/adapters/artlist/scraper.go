package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type ArtlistScraper struct {
	scraperPath string
}

func NewArtlistScraper(scraperPath string) *ArtlistScraper {
	return &ArtlistScraper{scraperPath: scraperPath}
}

func (a *ArtlistScraper) Search(ctx context.Context, input SearchInput) ([]ClipCandidate, error) {
	args := []string{
		"search",
		"--query", input.Query,
		"--limit", fmt.Sprintf("%d", input.Limit),
		"--format", "json",
	}

	cmd := exec.CommandContext(ctx, a.scraperPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("artlist scraper failed: %w", err)
	}

	var results []ClipCandidate
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("failed to parse scraper output: %w", err)
	}

	return results, nil
}
