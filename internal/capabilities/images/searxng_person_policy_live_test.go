package images

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TestSearXNGPersonPolicyLive measures the curated instance through the real
// provider: for each of the 13 audited PERSON queries it reports how many
// candidates survive the engine policy and the source deny-list, and fails if a
// denied host still reaches the caller. It is opt-in because it needs a live
// SearXNG.
//
//	SEARXNG_LIVE_POLICY=1 SEARXNG_URL=http://127.0.0.1:8080 \
//	  go test ./internal/capabilities/images -run TestSearXNGPersonPolicyLive -v -count=1 -timeout 6m
func TestSearXNGPersonPolicyLive(t *testing.T) {
	if os.Getenv("SEARXNG_LIVE_POLICY") != "1" {
		t.Skip("set SEARXNG_LIVE_POLICY=1 to measure the live SearXNG image policy")
	}
	searxngURL := strings.TrimSpace(os.Getenv("SEARXNG_URL"))
	if searxngURL == "" {
		searxngURL = "http://127.0.0.1:8080"
	}
	queries := []string{
		"Mike Tyson 1991", "Chiara Poggi", "Alberto Stasi", "Michael Burry investor",
		"George Washington portrait", "José María Aznar", "Conan O'Brien", "Michael B. Jordan",
		"李小龙 Bruce Lee", "宮崎駿 Hayao Miyazaki", "Elon Musk", "Steve Jobs Stanford 2005",
		"Gianluca Ferretti ingegnere",
	}
	service := &ImageStorageService{
		cfg:    &config.Config{External: config.ExternalConfig{SearxngURL: searxngURL}},
		client: &http.Client{Timeout: 25 * time.Second},
		log:    zap.NewNop(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	total, deniedHits, empty := 0, 0, 0
	for _, query := range queries {
		got := service.searchSearXNGImagesMany(ctx, query, 6)
		if len(got) == 0 {
			empty++
		}
		total += len(got)
		for _, candidate := range got {
			if searxImageSourceDenied(candidate.PageURL) || searxImageSourceDenied(candidate.PreviewURL) {
				deniedHits++
				t.Errorf("%q returned denied source %q", query, candidate.PreviewURL)
			}
		}
		t.Logf("%-38s candidates=%d", query, len(got))
	}
	t.Logf("TOTAL candidates=%d empty_entities=%d denied=%d", total, empty, deniedHits)
	if total == 0 {
		t.Fatal("the curated instance returned no candidates at all")
	}
	if empty == len(queries) {
		t.Fatal("every query was empty")
	}
}
