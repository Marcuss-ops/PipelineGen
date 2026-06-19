package batch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	"go.uber.org/zap"
)

// ── Phase: Parallel Web Search ───────────────────────────────────────────────

type searchFuture struct {
	topic      string
	sourceText string
	ch         chan searchResultWithContext
}

type searchResultWithContext struct {
	context string
	results []client.SearchResult
}

// parallelBatchWebSearch runs parallel web searches for each batch item.
// Returns workItems (expanded via source resolution + splitting), research sources,
// and splitItemCount.
func (s *BatchService) parallelBatchWebSearch(ctx context.Context, req *GenerateBatchRequest, batchItems []BatchTopic) ([]batchWorkItem, []scripts.ScriptResearchSource, int, error) {
	webSearchTimeout := 15 * time.Second
	if s.cfg != nil && s.cfg.External.WebSearchTimeoutSeconds > 0 {
		webSearchTimeout = time.Duration(s.cfg.External.WebSearchTimeoutSeconds) * time.Second
	}

	var futures []searchFuture
	var ws *client.WebSearcher
	if s.generator != nil && s.generator.GetClient() != nil {
		ws = s.generator.GetClient().WebSearcher()
	}
	if ws != nil {
		webSearchConcurrency := 4
		if s.cfg != nil {
			webSearchConcurrency = s.cfg.Scripts.WithDefaults().BatchWebSearchConcurrency
		}
		sem := make(chan struct{}, webSearchConcurrency)
		for _, bt := range batchItems {
			topicClean := strings.TrimSpace(bt.Topic)
			if topicClean == "" {
				continue
			}
			ch := make(chan searchResultWithContext, 1)
			futures = append(futures, searchFuture{topic: topicClean, sourceText: bt.SourceText, ch: ch})
			concurrent.SafeGoFunc("batch-websearch-"+topicClean, struct {
				T string
				C chan searchResultWithContext
			}{T: topicClean, C: ch}, func(arg struct {
				T string
				C chan searchResultWithContext
			}) {
				sem <- struct{}{}
				defer func() { <-sem }()
				searchCtx, searchCancel := context.WithTimeout(ctx, webSearchTimeout)
				defer searchCancel()
				results, err := ws.Search(searchCtx, arg.T)
				if err != nil {
					arg.C <- searchResultWithContext{}
					return
				}
				arg.C <- searchResultWithContext{
					context: client.FormatContext(results),
					results: results,
				}
			})
		}
	} else {
		for _, bt := range batchItems {
			topicClean := strings.TrimSpace(bt.Topic)
			if topicClean == "" {
				continue
			}
			ch := make(chan searchResultWithContext, 1)
			ch <- searchResultWithContext{}
			futures = append(futures, searchFuture{topic: topicClean, sourceText: bt.SourceText, ch: ch})
		}
	}

	var workItems []batchWorkItem
	var researchSources []scripts.ScriptResearchSource
	splitItemCount := 0
	targetWordsPerChapter := effectiveTargetWords(req)
	for _, future := range futures {
		topicClean := future.topic
		searchStart := time.Now()
		res := searchResultWithContext{}
		select {
		case res = <-future.ch:
		case <-ctx.Done():
			return nil, nil, 0, fmt.Errorf("generation timed out or was cancelled during web search")
		}
		searchEnd := time.Now()
		webContext := res.context

		// Convert raw search results to research sources
		for _, sr := range res.results {
			researchSources = append(researchSources, scripts.ScriptResearchSource{
				Query:      topicClean,
				URL:        sr.URL,
				Title:      sr.Title,
				Snippet:    sr.Content,
				SourceType: "web",
			})
		}

		resolvedSourceText := strings.TrimSpace(future.sourceText)
		sourceOrigin := "inline_text"
		if resolvedSourceText != "" {
			if normalizedSourceText, normalizedOrigin, sourceErr := ResolveBatchSourceText(ctx, s.cfg, resolvedSourceText); sourceErr == nil && strings.TrimSpace(normalizedSourceText) != "" {
				resolvedSourceText = strings.TrimSpace(normalizedSourceText)
				sourceOrigin = normalizedOrigin
			} else if sourceErr != nil {
				s.log.Warn("failed to normalize batch source text", zap.String("topic", topicClean), zap.Error(sourceErr))
				if strings.TrimSpace(future.sourceText) == "" {
					sourceOrigin = "empty"
				} else if isYouTubeSourceURL(future.sourceText) {
					sourceOrigin = "youtube_url_fallback"
				} else {
					sourceOrigin = "inline_text_fallback"
				}
			}
		}

		items := buildBatchWorkItems(topicClean, resolvedSourceText, sourceOrigin, webContext, searchStart, searchEnd, targetWordsPerChapter)
		if len(items) > 1 {
			splitItemCount += len(items) - 1
			s.log.Info("batch source split into items", zap.String("topic", topicClean), zap.Int("items", len(items)), zap.String("source_origin", sourceOrigin))
		}
		workItems = append(workItems, items...)
	}

	return workItems, researchSources, splitItemCount, nil
}
