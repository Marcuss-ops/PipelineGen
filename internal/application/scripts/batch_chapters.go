package scripts

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// ── Phase: Parallel Chapter Generation ───────────────────────────────────────

// failedChapterMarker returns a localised one-line marker used to flag a
// failed chapter in the merged script when IncludeFailedChapters=true.
// The previous version hard-coded Italian regardless of the request
// language, leaving bilingual artefacts in non-Italian documents.
func failedChapterMarker(language, topic string) string {
	const formatMarker = "%s"
	markers := map[string]string{
		"it": "\n[ATTENZIONE: La generazione del capitolo '" + formatMarker + "' è fallita a causa di un errore dell'LLM.]\n",
		"en": "\n[WARNING: Generation of chapter '" + formatMarker + "' failed due to an LLM error.]\n",
		"es": "\n[ATENCIÓN: La generación del capítulo '" + formatMarker + "' falló por un error del LLM.]\n",
		"fr": "\n[ATTENTION: La génération du chapitre '" + formatMarker + "' a échoué en raison d'une erreur du LLM.]\n",
		"de": "\n[WARNUNG: Die Generierung des Kapitels '" + formatMarker + "' ist aufgrund eines LLM-Fehlers fehlgeschlagen.]\n",
	}
	if m, ok := markers[strings.ToLower(strings.TrimSpace(language))]; ok {
		return fmt.Sprintf(m, topic)
	}
	// Fallback to English for any unsupported language.
	return fmt.Sprintf(markers["en"], topic)
}

// genChapterResult holds the output of a single parallel chapter generation goroutine.
type genChapterResult struct {
	scriptContent string
	part          GeneratedPart
}

// generateBatchChapters runs all work items in parallel with a semaphore (capacity 3).
// Returns results ordered by index, failed chapters list, and failure count.
//
// This delegates every work item to generateSingleChapterFromWorkItem, which is the
// engine-backed unified chapter writer. The duplication between the inline goroutine
// and the helper was eliminated so the writing logic lives in a single place.
func (s *BatchService) generateBatchChapters(
	ctx context.Context,
	req *GenerateBatchRequest,
	workItems []batchWorkItem,
	channelID string,
	guidelinesBlock string,
	targetWordsPerChapter int,
	onProgress func(int, string),
) ([]*genChapterResult, []string, int, error) {

	results := make([]*genChapterResult, len(workItems))
	var chWg sync.WaitGroup
	var chMu sync.Mutex
	var failedChapters []string
	var failedChapterCount int

	chapterConcurrency := 3
	if s.cfg != nil {
		chapterConcurrency = s.cfg.Scripts.WithDefaults().BatchChapterConcurrency
	}
	sem := make(chan struct{}, chapterConcurrency)

	for idx, workItem := range workItems {
		chWg.Add(1)
		go func(idx int, workItem batchWorkItem) {
			defer chWg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.log.Error("panic in parallel chapter generation goroutine",
						zap.Any("recover", r),
						zap.Int("chapter", idx+1),
						zap.String("topic", workItem.topic))
				}
			}()

			select {
			case <-ctx.Done():
				return
			default:
			}

			scriptContent, timing, genErr := s.generateSingleChapterFromWorkItem(
				ctx, sem, req, workItem, idx, len(workItems), channelID, guidelinesBlock, targetWordsPerChapter,
			)

			chMu.Lock()
			defer chMu.Unlock()

			if genErr != nil {
				failedChapterCount++
				failedChapters = append(failedChapters, workItem.topic)
			}

			results[idx] = &genChapterResult{
				scriptContent: scriptContent,
				part: GeneratedPart{
					topic:   workItem.topic,
					content: scriptContent,
					timing:  timing,
				},
			}
		}(idx, workItem)
	}

	chWg.Wait()
	return results, failedChapters, failedChapterCount, nil
}
