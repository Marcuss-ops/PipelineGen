package batch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// ── Phase: Pipelined Web Search → Chapter Generation ──────────────────────────
//
// pipelineWebSearchAndChapters overlaps the web search phase and the
// chapter generation phase of /api/script/generate-batch. The previous
// pipeline ran them strictly serially:
//
//  1. parallelBatchWebSearch:   wait for ALL web searches to complete.
//  2. generateBatchChapters:   then start M chapter generations.
//
// On a 10-topic batch with 3s avg web search and 30s avg chapter gen,
// the serial total was 10/4*3s + 10/3*30s = 7.5s + 100s = 107.5s of
// wall time.
//
// The pipelined version below starts the chapter generation for topic
// T the moment the web search for T completes, while subsequent web
// searches keep running. The chapter worker pool reuses the extracted
// per-chapter helper generateSingleChapterFromWorkItem, so the chapter
// generation logic is byte-identical to the serial version.
//
// Trade-off: the order in which chapter results land in the `results`
// slice is driven by the order in which web searches complete (which is
// usually still topic order because SearXNG responses are similar in
// duration, but is no longer guaranteed). The input order of topics is
// preserved within each topic — i.e. if topic 5 splits into 3 work
// items, they still appear in the same order they would have in the
// serial pipeline. The caller (ExecuteBatchGeneration) only reads
// results[i] for merging, so a global reordering only changes the
// chapter sequence in the merged Google Doc — which is acceptable for
// the wall-time win. If strict input-order is required, callers should
// use parallelBatchWebSearch + generateBatchChapters (both still
// present and untouched).
//
// This function is added ALONGSIDE the existing parallelBatchWebSearch
// and generateBatchChapters, which remain intact for fallback.
//
// Returns:
//   - results:        ordered list of genChapterResult, one per workItem
//     (ordered by global idx, which is web-search-
//     completion order, see trade-off above)
//   - failedChapters: topic names of chapters that failed (LLM errors)
//   - failedCount:    count of failed chapters
//   - splitItemCount: number of additional work items produced by source
//     splitting inside the web search phase
//   - err:            non-nil only on a fatal error (ctx cancelled
//     before any work began)
func (s *BatchService) pipelineWebSearchAndChapters(
	ctx context.Context,
	req *GenerateBatchRequest,
	batchItems []BatchTopic,
	channelID string,
	guidelinesBlock string,
	targetWordsPerChapter int,
	onProgress func(int, string),
) ([]*genChapterResult, []string, int, int, error) {
	if onProgress != nil {
		onProgress(2, fmt.Sprintf("Starting pipelined web search + chapter generation for %d items", len(batchItems)))
	}
	if len(batchItems) == 0 {
		return nil, nil, 0, 0, fmt.Errorf("either items/topics list or outline_topic is required")
	}
	select {
	case <-ctx.Done():
		return nil, nil, 0, 0, ctx.Err()
	default:
	}

	// Read concurrency from config, falling back to the previous
	// hard-coded defaults of 4 web searches and 3 chapters.
	webSearchConcurrency := 4
	chapterConcurrency := 3
	if s.cfg != nil {
		s := s.cfg.Scripts.WithDefaults()
		webSearchConcurrency = s.BatchWebSearchConcurrency
		chapterConcurrency = s.BatchChapterConcurrency
	}

	// Use a timeout-aware copy of ctx so the per-web-search call
	// honours request cancellation. The chapter helper has its own
	// 10-minute per-chapter context.
	webSearchTimeout := 15 * time.Second
	if s.cfg != nil && s.cfg.External.WebSearchTimeoutSeconds > 0 {
		webSearchTimeout = time.Duration(s.cfg.External.WebSearchTimeoutSeconds) * time.Second
	}

	// Resolve the web searcher once. If the generator or its client
	// isn't wired up, the per-topic web search is skipped (webContext
	// stays empty) but the rest of the pipeline still runs.
	var ws *client.WebSearcher
	if s.generator != nil && s.generator.GetClient() != nil {
		ws = s.generator.GetClient().WebSearcher()
	}

	// indexedWork pairs a workItem with its global index in the flat
	// results slice. We need a global index because each web search
	// may produce 0..N workItems via source splitting, and the
	// results slice must be indexed by the workItem's position in
	// the global flat list.
	type indexedWork struct {
		globalIdx int
		workItem  batchWorkItem
	}
	workCh := make(chan indexedWork, 32)

	// Shared state guarded by mutexes. We use a dynamic slice for
	// results (we don't know the final length until all web searches
	// complete) and atomic ops for the simple counters.
	var (
		resultsMu      sync.Mutex
		results        []*genChapterResult
		failedChMu     sync.Mutex
		failedChapters []string
		failedCount    atomic.Int32
		splitCountMu   sync.Mutex
		splitItemCount int
	)

	// Web search workers: one goroutine per topic, capped at
	// webSearchConcurrency by a semaphore. Each worker does the same
	// work as parallelBatchWebSearch but, instead of appending to a
	// shared slice, pushes the resulting workItems to workCh.
	webSem := make(chan struct{}, webSearchConcurrency)
	var webWg sync.WaitGroup
	idxCounter := atomic.Int64{}

	for i, bt := range batchItems {
		topicClean := strings.TrimSpace(bt.Topic)
		if topicClean == "" {
			continue
		}
		webWg.Add(1)
		concurrent.SafeGoFunc("pipeline-websearch-"+topicClean, struct {
			Idx   int
			Topic string
			St    string
		}{Idx: i, Topic: topicClean, St: bt.SourceText}, func(arg struct {
			Idx   int
			Topic string
			St    string
		}) {
			defer webWg.Done()
			webSem <- struct{}{}
			defer func() { <-webSem }()

			searchCtx, searchCancel := context.WithTimeout(ctx, webSearchTimeout)
			defer searchCancel()

			webContext := ""
			if ws != nil {
				if searchResults, searchErr := ws.Search(searchCtx, arg.Topic); searchErr == nil {
					webContext = client.FormatContext(searchResults)
				}
			}
			searchEnd := time.Now()

			// Source resolution (same logic as parallelBatchWebSearch).
			resolvedSourceText := strings.TrimSpace(arg.St)
			sourceOrigin := "inline_text"
			if resolvedSourceText != "" {
				if normalizedSourceText, normalizedOrigin, sourceErr := ResolveBatchSourceText(ctx, s.cfg, resolvedSourceText); sourceErr == nil && strings.TrimSpace(normalizedSourceText) != "" {
					resolvedSourceText = strings.TrimSpace(normalizedSourceText)
					sourceOrigin = normalizedOrigin
				} else if sourceErr != nil {
					if isYouTubeSourceURL(arg.St) {
						sourceOrigin = "youtube_url_fallback"
					} else {
						sourceOrigin = "inline_text_fallback"
					}
				}
			}

			items := buildBatchWorkItems(arg.Topic, resolvedSourceText, sourceOrigin, webContext, time.Now(), searchEnd, targetWordsPerChapter)
			if len(items) > 1 {
				splitCountMu.Lock()
				splitItemCount += len(items) - 1
				splitCountMu.Unlock()
			}

			// Push each workItem with a fresh global idx. The order of
			// workItems within a topic is preserved (and the order of
			// topics is also preserved in the typical case because
			// SearXNG response times are similar — see trade-off note
			// in the function header).
			for _, it := range items {
				gi := idxCounter.Add(1) - 1 // 0-based global index
				select {
				case workCh <- indexedWork{globalIdx: int(gi), workItem: it}:
				case <-ctx.Done():
					return
				}
			}
		})
	}

	// Closer: when all web search goroutines have finished, close
	// workCh so the chapter workers can drain and exit. This is the
	// classic "sender closes, receiver drains" Go channel pattern.
	concurrent.SafeGo("pipeline-workCh-closer", func() {
		webWg.Wait()
		close(workCh)
	})

	// Chapter workers: `chapterConcurrency` goroutines consume from
	// workCh, each calling the extracted per-chapter helper. The
	// helper handles its own semaphore (chapterSem) internally to cap
	// concurrent LLM calls — the chapterSem here is shared across
	// workers and gates concurrent LLM calls to chapterConcurrency.
	chapterSem := make(chan struct{}, chapterConcurrency)
	var chapterWg sync.WaitGroup
	for w := 0; w < chapterConcurrency; w++ {
		chapterWg.Add(1)
		concurrent.SafeGo("pipeline-chapter-worker", func() {
			defer chapterWg.Done()
			for iw := range workCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				content, timing, genErr := s.generateSingleChapterFromWorkItem(
					ctx, chapterSem, req, iw.workItem, iw.globalIdx, len(batchItems),
					channelID, guidelinesBlock, targetWordsPerChapter,
				)
				if genErr != nil {
					failedCount.Add(1)
					failedChMu.Lock()
					failedChapters = append(failedChapters, iw.workItem.topic)
					failedChMu.Unlock()
				}
				resultsMu.Lock()
				// Grow the slice to fit this globalIdx if needed.
				for len(results) <= iw.globalIdx {
					results = append(results, nil)
				}
				results[iw.globalIdx] = &genChapterResult{
					scriptContent: content,
					part: generatedPart{
						topic:   iw.workItem.topic,
						content: content,
						timing:  timing,
					},
				}
				resultsMu.Unlock()
			}
		})
	}

	chapterWg.Wait()
	if onProgress != nil {
		onProgress(100, "Pipelined web search + chapter generation completed")
	}
	return results, failedChapters, int(failedCount.Load()), splitItemCount, nil
}
