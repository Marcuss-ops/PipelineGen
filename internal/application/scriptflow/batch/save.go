package batch

import (
	"context"
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/scripts"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// ── Phase: Save to DB ────────────────────────────────────────────────────────

// batchDBRecord holds all data needed to persist a batch script.
type batchDBRecord struct {
	docTitle           string
	mergedScript       string
	docURL             string
	docID              string
	effectiveFolderID  string
	guidelinesBlock    string
	timings            []chapterTiming
	sections           []scripts.ScriptSectionRecord
	translations       map[string]map[string]any
	failedChapters     []string
	failedChapterCount int
	splitItemCount     int
	batchItems         []BatchTopic
	promptVersion      string
	editorPromptVer    string
	qaPromptVersion    string
	chapterStructure   string
	targetWords        int
	noChapters         bool
	outlineSections    []scripts.ScriptOutlineSectionRecord
	generationLogs     []scripts.ScriptGenerationLog
}

// saveBatchScript persists a batch script to the scripts repository.
// Returns the script ID if saved, or 0 when saving is skipped or fails.
func (s *BatchService) saveBatchScript(ctx context.Context, req *GenerateBatchRequest, rec *batchDBRecord, researchSources []scripts.ScriptResearchSource) int64 {
	if !req.SaveToDB || s.scriptsRepo == nil {
		return 0
	}

	metadata := map[string]any{
		"prompt_version":        rec.promptVersion,
		"editor_prompt_version": rec.editorPromptVer,
		"qa_prompt_version":     rec.qaPromptVersion,
		"guidelines":            rec.guidelinesBlock,
		"chapter_structure":     rec.chapterStructure,
		"target_words":          rec.targetWords,
		"target_words_per_item": rec.targetWords,
		"no_chapters":           rec.noChapters,
		"source_preprocessing": map[string]any{
			"original_items": len(rec.batchItems),
			"expanded_items": len(rec.sections),
			"split_items":    rec.splitItemCount,
		},
		"timings":              rec.timings,
		"google_doc_url":       rec.docURL,
		"google_doc_id":        rec.docID,
		"drive_folder_id":      rec.effectiveFolderID,
		"voiceover_status":     "processing",
		"translations":         rec.translations,
		"failed_chapters":      rec.failedChapters,
		"failed_chapter_count": rec.failedChapterCount,
	}
	metadataJSON, marshalErr := json.Marshal(metadata)
	if marshalErr != nil {
		s.log.Warn("failed to marshal batch metadata", zap.Error(marshalErr))
		metadataJSON = []byte("{}")
	}

	version := 1
	if nextVersion, versionErr := s.scriptsRepo.NextVersionForTopic(ctx, rec.docTitle, req.Language, "book"); versionErr != nil {
		s.log.Warn("failed to compute batch version, falling back to 1", zap.Error(versionErr))
	} else {
		version = nextVersion
	}

	ollamaBaseURL := ""
	if s.cfg != nil {
		ollamaBaseURL = s.cfg.External.OllamaURL
	}

	// Clean markdown artifacts from each section so DB content is consistent
	// with the merged script (which is already cleaned via CleanForVoiceover).
	for i := range rec.sections {
		rec.sections[i].Content = textutil.CleanForVoiceover(rec.sections[i].Content)
	}

	scriptID, saveErr := s.scriptsRepo.SaveScript(ctx, &scripts.ScriptRecord{
		Topic:          rec.docTitle,
		Title:          rec.docTitle,
		Duration:       req.Duration,
		Language:       req.Language,
		Template:       req.Tone,
		Mode:           "book",
		Tone:           req.Tone,
		TargetWords:    rec.targetWords,
		FinalWordCount: textutil.CountWords(rec.mergedScript),
		Status:         "completed",
		NarrativeText:  rec.mergedScript,
		FullDocument:   rec.mergedScript,
		MetadataJSON:   string(metadataJSON),
		ModelUsed:      req.Model,
		OllamaBaseURL:  ollamaBaseURL,
		Version:        version,
	}, rec.sections, nil)
	if saveErr != nil {
		s.log.Warn("failed to save batch script to DB", zap.Error(saveErr), zap.String("title", rec.docTitle))
		return 0
	}

	// Save research sources linked to the script
	if len(researchSources) > 0 {
		for i := range researchSources {
			researchSources[i].ScriptID = scriptID
		}
		if srcErr := s.scriptsRepo.SaveResearchSources(ctx, scriptID, researchSources); srcErr != nil {
			s.log.Warn("failed to save research sources", zap.Error(srcErr), zap.Int64("script_id", scriptID))
		}
	}

	// Save outline sections (intermediate step) linked to the script
	if len(rec.outlineSections) > 0 {
		for i := range rec.outlineSections {
			rec.outlineSections[i].ScriptID = scriptID
		}
		if outlineErr := s.scriptsRepo.SaveOutlineSections(ctx, scriptID, rec.outlineSections); outlineErr != nil {
			s.log.Warn("failed to save outline sections", zap.Error(outlineErr), zap.Int64("script_id", scriptID))
		}
	}

	// Save generation logs (per-chapter generation metadata) linked to the script
	if len(rec.generationLogs) > 0 {
		for i := range rec.generationLogs {
			rec.generationLogs[i].ScriptID = scriptID
		}
		for _, logEntry := range rec.generationLogs {
			if logErr := s.scriptsRepo.SaveGenerationLog(ctx, logEntry); logErr != nil {
				s.log.Warn("failed to save generation log", zap.Error(logErr), zap.Int64("script_id", scriptID))
			}
		}
	}

	s.log.Info("batch script saved to DB", zap.Int64("script_id", scriptID), zap.String("title", rec.docTitle))
	return scriptID
}
