package handlers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"velox/go-master/pkg/concurrent"
	"velox/go-master/pkg/textutil"

	"go.uber.org/zap"
)

// ── Phase: Translation Pipeline ──────────────────────────────────────────────

// translateBatch runs the full translation pipeline (Phase 1 independent +
// Phase 2 chain-dependent) for all requested languages.
func (h *ScriptFlowHandler) translateBatch(
	ctx context.Context,
	req *GenerateBatchRequest,
	parts []generatedPart,
	docTitle, effectiveFolderID string,
) (map[string]map[string]any, []string) {
	translations := make(map[string]map[string]any)
	var (
		transMu                  sync.Mutex
		transWg                  sync.WaitGroup
		failedLanguages          []string
		translatedChaptersByLang = make(map[string][]string)
		translatedTopicsByLang   = make(map[string][]string)
	)

	translateOneLang := func(lang string, getSource func(pi int, part generatedPart) (sourceText string, topicSource string)) {
		defer transWg.Done()

		select {
		case <-ctx.Done():
			return
		default:
		}

		const maxConsecutiveFailures = 3
		var consecutiveFailures int

		var transDocTitle string
		titleCtx, titleCancel := context.WithTimeout(ctx, 5*time.Minute)
		titleTranslated, titleErr := h.generator.TranslateText(titleCtx, docTitle, lang)
		titleCancel()
		if titleErr == nil && strings.TrimSpace(titleTranslated) != "" {
			transDocTitle = titleTranslated
		} else {
			transDocTitle = docTitle
		}

		h.log.Info("batch generation: translating to language",
			zap.String("lang", lang),
			zap.Int("chapters", len(parts)),
			zap.String("title", transDocTitle),
		)

		var translatedChapters []string
		var translatedTopics []string

		for pi, part := range parts {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if consecutiveFailures >= maxConsecutiveFailures {
				h.log.Warn("batch generation: skipping remaining chapters for language due to consecutive quality gate failures",
					zap.String("lang", lang),
					zap.Int("consecutive_failures", consecutiveFailures),
					zap.Int("remaining_chapters", len(parts)-pi))
				break
			}

			chapterText := strings.TrimSpace(part.content)
			if chapterText == "" {
				continue
			}

			sourceText, topicSource := getSource(pi, part)

			h.log.Info("batch generation: translating chapter",
				zap.String("lang", lang),
				zap.String("topic", topicSource),
				zap.Int("chars", len(sourceText)),
				zap.Int("index", pi),
			)

			transCtx, transCancel := context.WithTimeout(ctx, 10*time.Minute)
			translated, err := h.generator.TranslateText(transCtx, sourceText, lang)
			transCancel()
			if err != nil {
				// Fail-loud: instead of silently publishing untranslated
				// content as if it were translated, mark the chapter as
				// failed for this language and let the consecutive-failure
				// gate eventually drop the whole language. The caller sees
				// the gap explicitly in failedChapters/failedLanguages.
				h.log.Warn("batch generation: chapter translation failed",
					zap.String("lang", lang), zap.String("topic", part.topic), zap.Error(err))
				translated = ""
				consecutiveFailures++
			}

			sourceLang := req.Language
			if err == nil && (strings.TrimSpace(translated) == "" || !looksTranslated(translated, lang, sourceLang)) {
				h.log.Warn("batch generation: chapter translation appears untranslated, retrying with full language name",
					zap.String("lang", lang), zap.String("topic", part.topic))
				langName := textutil.LangFullName(lang)
				retryCtx, retryCancel := context.WithTimeout(ctx, 10*time.Minute)
				translated, err = h.generator.TranslateText(retryCtx, sourceText, langName)
				retryCancel()
				if err != nil || strings.TrimSpace(translated) == "" || !looksTranslated(translated, lang, sourceLang) {
					h.log.Warn("batch generation: chapter translation retry failed",
						zap.String("lang", lang), zap.String("topic", part.topic), zap.Error(err))
					translated = ""
					consecutiveFailures++
				} else {
					consecutiveFailures = 0
				}
			} else if err == nil {
				consecutiveFailures = 0
			}

			translatedChapters = append(translatedChapters, translated)

			topicCtx, topicCancel := context.WithTimeout(ctx, 10*time.Minute)
			topicTranslated, topicErr := h.generator.TranslateText(topicCtx, topicSource, textutil.LangFullName(lang))
			topicCancel()
			if topicErr != nil || strings.TrimSpace(topicTranslated) == "" {
				topicTranslated = topicSource
			}
			translatedTopics = append(translatedTopics, topicTranslated)
		}

		if consecutiveFailures >= maxConsecutiveFailures {
			transMu.Lock()
			failedLanguages = append(failedLanguages, lang)
			transMu.Unlock()
			h.log.Warn("batch generation: language skipped due to quality gate failures",
				zap.String("lang", lang),
				zap.Int("consecutive_failures", consecutiveFailures))
			return
		}

		transMu.Lock()
		translatedChaptersByLang[lang] = translatedChapters
		translatedTopicsByLang[lang] = translatedTopics
		transMu.Unlock()

		var translatedTextBuilder strings.Builder
		translatedTextBuilder.WriteString(fmt.Sprintf("%s\n\n", transDocTitle))
		for i, translated := range translatedChapters {
			chapterLabel := chapterLabelForLang(lang)
			topicTitle := parts[i].topic
			if i < len(translatedTopics) && translatedTopics[i] != "" {
				topicTitle = translatedTopics[i]
			}
			translatedTextBuilder.WriteString(fmt.Sprintf("\n%s %d: %s\n\n", chapterLabel, i+1, topicTitle))
			translatedTextBuilder.WriteString(translated)
			translatedTextBuilder.WriteString("\n\n")
		}
		translatedText := translatedTextBuilder.String()

		var transDocURL string
		if h.docClient != nil {
			translatedParts := make([]generatedPart, len(translatedChapters))
			for i, ch := range translatedChapters {
				topicTitle := parts[i].topic
				if i < len(translatedTopics) && translatedTopics[i] != "" {
					topicTitle = translatedTopics[i]
				}
				translatedParts[i] = generatedPart{topic: topicTitle, content: ch}
			}
			htmlMergedContent := buildBatchGoogleDocHTMLWithTranslations(
				transDocTitle,
				translatedParts, req.NoChapters, lang, nil,
			)
			transDoc, docErr := h.docClient.CreateDoc(ctx,
				transDocTitle,
				htmlMergedContent, effectiveFolderID,
			)
			if docErr == nil {
				transDocURL = transDoc.URL
			} else {
				h.log.Warn("batch generation: failed to create translated Google Doc",
					zap.String("lang", lang), zap.Error(docErr))
			}
		}

		if req.Voiceover && h.voService != nil {
			h.log.Info("batch generation: spawning async voiceover for translated script", zap.String("lang", lang))
			voFilename := fmt.Sprintf("%s_%s.mp3", transDocTitle, lang)
			voFolderID := strings.TrimSpace(h.cfg.Drive.VoiceoverFolder())
			if voFolderID == "" {
				voFolderID = effectiveFolderID
			}
			h.spawnBatchVoiceover(ctx, textutil.CleanForVoiceover(translatedText), lang, transDocTitle, voFolderID, voFilename)
		}

		transMu.Lock()
		translations[lang] = map[string]any{
			"script":           textutil.CleanForVoiceover(translatedText),
			"doc_url":          transDocURL,
			"voiceover_link":   "",
			"voiceover_status": "processing",
		}
		transMu.Unlock()
	}

	// Phase 1: Independent languages in parallel
	var chainDependentLangs []string

	for _, lang := range req.Languages {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang == "" || lang == strings.ToLower(req.Language) {
			continue
		}

		if req.TranslationSourceLang != "" && lang != req.TranslationSourceLang {
			chainDependentLangs = append(chainDependentLangs, lang)
			continue
		}

		transWg.Add(1)
		lang := lang
		concurrent.SafeGo(fmt.Sprintf("translate-batch[%s]", lang), func() {
			translateOneLang(lang, func(pi int, part generatedPart) (string, string) {
				return strings.TrimSpace(part.content), part.topic
			})
		})
	}
	transWg.Wait()

	// Phase 2: Chain-dependent translations (sequential)
	for _, lang := range chainDependentLangs {
		h.log.Info("batch generation: processing chain-dependent translation",
			zap.String("lang", lang),
			zap.String("source_lang", req.TranslationSourceLang),
		)

		transWg.Add(1)
		translateOneLang(lang, func(pi int, part generatedPart) (string, string) {
			sourceLang := req.TranslationSourceLang
			transMu.Lock()
			sc, hasCh := translatedChaptersByLang[sourceLang]
			st, hasTo := translatedTopicsByLang[sourceLang]
			transMu.Unlock()

			if hasCh && pi < len(sc) && strings.TrimSpace(sc[pi]) != "" {
				topic := part.topic
				if hasTo && pi < len(st) && strings.TrimSpace(st[pi]) != "" {
					topic = st[pi]
				}
				return sc[pi], topic
			}
			return strings.TrimSpace(part.content), part.topic
		})
	}

	return translations, failedLanguages
}
