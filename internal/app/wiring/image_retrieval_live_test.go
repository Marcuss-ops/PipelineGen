package wiring

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	imageadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

type liveImageTopicResult struct {
	query      string
	candidates []scriptpkg.SegmentAssetCandidate
}

// TestLiveImageRetrievalFillsVerifiedTopicTargets is the opt-in retrieval gate.
// It exercises the production provider resolver and guarded downloader, but it
// does not persist files or write to the media catalog.
//
// Run with:
//
//	VELOX_IMAGE_RETRIEVAL_LIVE=1 go test ./internal/app/wiring -run '^TestLiveImageRetrievalFillsVerifiedTopicTargets$' -v -count=1
func TestLiveImageRetrievalFillsVerifiedTopicTargets(t *testing.T) {
	if os.Getenv("VELOX_IMAGE_RETRIEVAL_LIVE") != "1" {
		t.Skip("set VELOX_IMAGE_RETRIEVAL_LIVE=1 to run live image-provider and download gates")
	}
	topics := []struct{ query, class string }{
		{"Donald Trump White House latest 2026 press conference", "person"},
		{"Artemis II NASA Orion Moon mission latest 2026", "event"},
		{"2026 FIFA World Cup stadium latest Mexico City", "event"},
		{"California wildfire firefighters latest 2026", "event"},
		{"NVIDIA AI data center chips latest 2026", "organization"},
	}
	log := zap.NewNop()
	cfg := &config.Config{}
	cfg.External.SearxngURL = strings.TrimSpace(os.Getenv("SEARXNG_URL"))
	if cfg.External.SearxngURL == "" {
		cfg.External.SearxngURL = "http://127.0.0.1:8080"
	}
	service := imagesapp.NewService(imagesapp.ImagesDeps{
		Core:     imagesapp.ImagesCoreDeps{Cfg: cfg, Log: log},
		External: imagesapp.ImagesExternalDeps{RemoteFetch: &http.Client{Timeout: 20 * time.Second}},
	})
	resolver, err := imagesapp.NewImageSearchResolver(
		imagesapp.WithRetrievalBackend(service.RetrievalRegistry()),
		imagesapp.WithImageListRepository(liveEmptyImageRepo{}),
	)
	if err != nil {
		t.Fatalf("image search resolver: %v", err)
	}
	searcher := newInternetImageSearchAdapter(resolver, log)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	providerResolver, ok := resolver.(interface {
		ResolveProvider(string) (imagesapp.ImageSearcher, error)
	})
	if !ok {
		t.Fatal("image resolver does not expose provider diagnostics")
	}
	searx, err := providerResolver.ResolveProvider("searxng")
	if err != nil {
		t.Fatalf("SearXNG provider is not configured: %v", err)
	}
	searxRows, err := searx.Search(ctx, imagesapp.ImageFilter{SubjectID: "Artemis II latest 2026", Limit: 5})
	if err != nil || len(searxRows) == 0 {
		t.Fatalf("SearXNG JSON image query returned %d results: %v", len(searxRows), err)
	}
	t.Logf("SearXNG JSON gate: query returned %d images", len(searxRows))

	results := make([]liveImageTopicResult, len(topics))
	searchStart := time.Now()
	var searches sync.WaitGroup
	for i, topic := range topics {
		searches.Add(1)
		go func(i int, topic struct{ query, class string }) {
			defer searches.Done()
			candidates, searchErr := searcher.SearchImages(ctx, imageadapters.InternetImageSearchRequest{
				SegmentID: fmt.Sprintf("live-topic-%d", i), Query: topic.query, Entity: topic.query,
				QueryClass: topic.class, Language: "en", Limit: 10, ForceRefresh: true,
			})
			if searchErr != nil {
				t.Errorf("search %q: %v", topic.query, searchErr)
				return
			}
			results[i] = liveImageTopicResult{query: topic.query, candidates: candidates}
		}(i, topic)
	}
	searches.Wait()
	t.Logf("search phase: topics=%d candidates=%d elapsed=%s", len(topics), countLiveCandidates(results), time.Since(searchStart).Round(time.Millisecond))

	type downloadTask struct {
		query     string
		candidate scriptpkg.SegmentAssetCandidate
	}
	type downloadResult struct {
		query    string
		provider string
		bytes    int
		err      error
	}
	tasks := make(chan downloadTask, countLiveCandidates(results))
	seen := make(map[string]struct{})
	for rank := 0; ; rank++ {
		added := false
		for _, topic := range results {
			if rank >= len(topic.candidates) {
				continue
			}
			candidate := topic.candidates[rank]
			url := strings.TrimSpace(candidate.SourceURL)
			if url == "" {
				continue
			}
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			tasks <- downloadTask{query: topic.query, candidate: candidate}
			added = true
		}
		if !added {
			break
		}
	}
	close(tasks)

	const maxWorkers = 15
	const targetPerTopic = 3
	verified := make(map[string]int, len(topics))
	inFlight := make(map[string]int, len(topics))
	var stateMu sync.Mutex
	downloads := make([]downloadResult, 0, len(topics)*targetPerTopic)
	attempts := 0
	var workers sync.WaitGroup
	downloadStart := time.Now()
	for worker := 0; worker < maxWorkers; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			client := &http.Client{Timeout: 25 * time.Second}
			for task := range tasks {
				stateMu.Lock()
				if verified[task.query]+inFlight[task.query] >= targetPerTopic {
					stateMu.Unlock()
					continue
				}
				inFlight[task.query]++
				attempts++
				stateMu.Unlock()

				data, mime, downloadErr := imageadapters.DownloadVidRushImageForCandidate(ctx, client, task.candidate, imageadapters.DefaultVidRushImagePolicy())
				if downloadErr == nil {
					_, downloadErr = imageadapters.VerifyVidRushImageBytes(task.candidate, data, mime, imageadapters.DefaultVidRushImagePolicy())
				}
				stateMu.Lock()
				inFlight[task.query]--
				if downloadErr == nil && verified[task.query] < targetPerTopic {
					verified[task.query]++
					downloads = append(downloads, downloadResult{query: task.query, provider: task.candidate.Provider, bytes: len(data)})
				} else if downloadErr != nil {
					downloads = append(downloads, downloadResult{query: task.query, provider: task.candidate.Provider, err: downloadErr})
				}
				stateMu.Unlock()
			}
		}()
	}
	workers.Wait()
	for _, topic := range results {
		t.Logf("verified: query=%q candidates=%d images=%d", topic.query, len(topic.candidates), verified[topic.query])
	}
	verifiedTotal := 0
	for _, count := range verified {
		verifiedTotal += count
	}
	t.Logf("download phase: verified=%d/15 attempts=%d max_concurrency=%d elapsed=%s", verifiedTotal, attempts, maxWorkers, time.Since(downloadStart).Round(time.Millisecond))
	for _, result := range downloads {
		if result.err != nil {
			t.Logf("rejected: query=%q provider=%s reason=%v", result.query, result.provider, result.err)
		}
	}
	if verifiedTotal < 12 {
		t.Fatalf("verified images = %d, want at least 12/15 across five topics", verifiedTotal)
	}
	for _, topic := range topics {
		if verified[topic.query] < 2 {
			t.Fatalf("topic %q produced %d verified images, want at least 2", topic.query, verified[topic.query])
		}
	}
}

func countLiveCandidates(results []liveImageTopicResult) int {
	total := 0
	for _, topic := range results {
		total += len(topic.candidates)
	}
	return total
}
