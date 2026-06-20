package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func (s *BatchService) ExecuteBatchGeneration(ctx context.Context, req *GenerateBatchRequest, onProgress func(int, string)) (BatchGenerateResponse, error) {
	scriptsCfg := config.ScriptsConfig{}
	if s.cfg != nil {
		scriptsCfg = s.cfg.Scripts.WithDefaults()
	}
	docTitle := strings.TrimSpace(req.DocTitle)
	if docTitle == "" {
		docTitle = "Untitled Batch Script"
	}
	channelID := req.ChannelID
	if channelID == "" {
		channelID = scriptsCfg.BatchChannelID
	}
	effectiveFolderID := strings.TrimSpace(req.DriveFolderID)
	if effectiveFolderID == "" {
		effectiveFolderID = "1sBj1OqF-bRuQmIzqExwYD38AildZvBM5"
	}

	guidelinesBlock := strings.TrimSpace(req.Guidelines)
	targetWordsPerChapter := effectiveTargetWords(req)

	if onProgress != nil {
		onProgress(2, "Initializing outline generation")
	}
	batchItems := normalizedBatchItems(req)
	if len(batchItems) == 0 && strings.TrimSpace(req.OutlineTopic) != "" {
		if err := s.generateBatchOutline(ctx, req); err != nil {
			return BatchGenerateResponse{}, err
		}
		batchItems = normalizedBatchItems(req)
	}
	if len(batchItems) == 0 {
		return BatchGenerateResponse{}, fmt.Errorf("either items/topics list or outline_topic is required")
	}

	workItems, researchSources, splitItemCount, err := s.parallelBatchWebSearch(ctx, req, batchItems)
	if err != nil {
		return BatchGenerateResponse{}, err
	}
	if len(workItems) == 0 {
		return BatchGenerateResponse{}, fmt.Errorf("no work items after source resolution")
	}

	if onProgress != nil {
		chapterConcurrency := 3
		if s.cfg != nil {
			chapterConcurrency = s.cfg.Scripts.WithDefaults().BatchChapterConcurrency
		}
		onProgress(5, fmt.Sprintf("Generating %d chapters in parallel (concurrency=%d)...", len(workItems), chapterConcurrency))
	}
	results, failedChapters, failedChapterCount, genErr := s.generateBatchChapters(ctx, req, workItems, channelID, guidelinesBlock, targetWordsPerChapter, onProgress)
	if genErr != nil {
		return BatchGenerateResponse{}, genErr
	}

	topicToSections := make(map[string][]int)
	for idx, res := range results {
		if res == nil || res.part.topic == "" {
			continue
		}
		topicToSections[res.part.topic] = append(topicToSections[res.part.topic], idx+1)
	}
	for i := range researchSources {
		if sections, ok := topicToSections[researchSources[i].Query]; ok && len(sections) > 0 {
			sectionsJSON, _ := json.Marshal(sections)
			researchSources[i].UsedInSections = string(sectionsJSON)
		}
	}

	generatedParts, mergedScriptStr, timings, sections := mergeBatchResults(docTitle, results, req.NoChapters, req.Language)

	if onProgress != nil {
		onProgress(85, "Running coherence pass on merged script...")
	}
	coherentScript, coherenceErr := s.coherencePass(ctx, req, mergedScriptStr)
	if coherenceErr != nil {
		s.log.Warn("coherence pass failed, using merged script as-is", zap.Error(coherenceErr))
	} else if coherentScript != "" && coherentScript != mergedScriptStr {
		mergedScriptStr = coherentScript
		generatedParts = rebuildGeneratedPartsFromMergedScript(docTitle, coherentScript, req.NoChapters, req.Language)
		sections = make([]scripts.ScriptSectionRecord, 0, len(generatedParts))
		for idx, part := range generatedParts {
			status := "completed"
			if part.timing.Status == "failed" {
				status = "failed"
			}
			sections = append(sections, scripts.ScriptSectionRecord{
				SectionType:  "item",
				SectionTitle: part.topic,
				Content:      part.content,
				SortOrder:    idx + 1,
				WordCount:    part.timing.WordCount,
				Status:       status,
			})
		}
		if len(timings) > 0 {
			coherenceTiming := chapterTiming{
				Topic:           "coherence_pass",
				Status:          "completed",
				WordCount:       textutil.CountWords(coherentScript),
				TargetWordCount: textutil.CountWords(coherentScript),
			}
			timings = append(timings, coherenceTiming)
		}
	}

	if onProgress != nil {
		onProgress(88, "Running global QA pass on merged script...")
	}
	qaScript, qaErr := s.qaPass(ctx, req, mergedScriptStr)
	if qaErr != nil {
		s.log.Warn("qa pass failed, using script as-is", zap.Error(qaErr))
	} else if qaScript != "" && qaScript != mergedScriptStr {
		mergedScriptStr = qaScript
		generatedParts = rebuildGeneratedPartsFromMergedScript(docTitle, qaScript, req.NoChapters, req.Language)
		sections = make([]scripts.ScriptSectionRecord, 0, len(generatedParts))
		for idx, part := range generatedParts {
			status := "completed"
			if part.timing.Status == "failed" {
				status = "failed"
			}
			sections = append(sections, scripts.ScriptSectionRecord{
				SectionType:  "item",
				SectionTitle: part.topic,
				Content:      part.content,
				SortOrder:    idx + 1,
				WordCount:    part.timing.WordCount,
				Status:       status,
			})
		}
		if len(timings) > 0 {
			qaTiming := chapterTiming{
				Topic:           "qa_pass",
				Status:          "completed",
				WordCount:       textutil.CountWords(qaScript),
				TargetWordCount: textutil.CountWords(qaScript),
			}
			timings = append(timings, qaTiming)
		}
	}

	cleanScript := textutil.CleanForVoiceover(mergedScriptStr)

	if onProgress != nil {
		onProgress(92, "Creating Google Doc...")
	}
	docURL, docID := s.createBatchDoc(ctx, docTitle, generatedParts, req.NoChapters, req.Language, effectiveFolderID)

	if req.Voiceover && s.voService != nil {
		if onProgress != nil {
			onProgress(94, "Spawning async voiceover for base language...")
		}
		s.log.Info("batch generation: spawning async voiceover for base language", zap.String("lang", req.Language))
		voFilename := fmt.Sprintf("%s_%s.mp3", docTitle, req.Language)
		voFolderID := strings.TrimSpace(s.cfg.Drive.VoiceoverFolder())
		if voFolderID == "" {
			voFolderID = effectiveFolderID
		}
		s.spawnBatchVoiceover(ctx, cleanScript, req.Language, docTitle, voFolderID, voFilename)
	}

	if onProgress != nil {
		onProgress(95, "Translating...")
	}
	translations, failedLanguages := s.translateBatch(ctx, req, generatedParts, docTitle, effectiveFolderID)

	if onProgress != nil {
		onProgress(97, "Saving script to database...")
	}
	outlineSections := make([]scripts.ScriptOutlineSectionRecord, 0, len(batchItems))
	for i, item := range batchItems {
		purpose := ""
		keyPointsJSON := "[]"
		emotionalRole := ""

		if i < len(req.Chapters) {
			chPlan := req.Chapters[i]
			purpose = chPlan.Purpose
			emotionalRole = chPlan.EmotionalRole
			if kpBytes, err := json.Marshal(chPlan.KeyPoints); err == nil {
				keyPointsJSON = string(kpBytes)
			}
		}

		outlineSections = append(outlineSections, scripts.ScriptOutlineSectionRecord{
			SectionIndex:  i + 1,
			Title:         item.Topic,
			Purpose:       purpose,
			TargetWords:   targetWordsPerChapter,
			KeyPointsJSON: keyPointsJSON,
			EmotionalRole: emotionalRole,
		})
	}

	generationLogs := make([]scripts.ScriptGenerationLog, 0, len(timings))
	for _, t := range timings {
		phase := "generate"
		if t.Topic != "" {
			if t.Topic == "coherence_pass" || t.Topic == "qa_pass" {
				phase = t.Topic
			} else {
				phase = "generate_" + t.Topic
			}
		}
		errStr := ""
		if t.Status == "failed" {
			errStr = "generation failed"
		}
		generationLogs = append(generationLogs, scripts.ScriptGenerationLog{
			Phase:       phase,
			Model:       req.Model,
			OutputWords: t.WordCount,
			DurationMs:  t.GenerationDurationMS,
			RetryCount:  t.RetryCount,
			CacheStatus: t.CacheStatus,
			Error:       errStr,
		})
	}

	_ = s.saveBatchScript(ctx, req, &batchDBRecord{
		docTitle:           docTitle,
		mergedScript:       cleanScript,
		docURL:             docURL,
		docID:              docID,
		effectiveFolderID:  effectiveFolderID,
		guidelinesBlock:    guidelinesBlock,
		timings:            timings,
		sections:           sections,
		translations:       translations,
		failedChapters:     failedChapters,
		failedChapterCount: failedChapterCount,
		splitItemCount:     splitItemCount,
		batchItems:         batchItems,
		promptVersion:      req.PromptVersion,
		editorPromptVer:    req.EditorPromptVersion,
		qaPromptVersion:    req.QAPromptVersion,
		chapterStructure:   chapterStructureJSON(req),
		targetWords:        targetWordsPerChapter,
		noChapters:         req.NoChapters,
		outlineSections:    outlineSections,
		generationLogs:     generationLogs,
	}, researchSources)

	if onProgress != nil {
		onProgress(100, "Completed!")
	}

	return buildBatchResponse(
		req, docTitle, cleanScript, docURL,
		translations, guidelinesBlock,
		targetWordsPerChapter, splitItemCount, batchItems,
		timings, failedChapters, failedChapterCount, failedLanguages,
	), nil
}
