package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/core/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/service/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/service/scriptcore"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// jobPayloadCatalogScript is the runtime payload for catalog-first script generation.
// Reuses the same fields as jobPayloadClipScript since the pipeline after clip selection is identical.
type jobPayloadCatalogScript struct {
	ClipIDs          []string `json:"clip_ids"`
	Title            string   `json:"title"`
	OutputName       string   `json:"output_name"`
	Language         string   `json:"language"`
	Tone             string   `json:"tone"`
	Model            string   `json:"model"`
	TargetWords      int      `json:"target_words"`
	Duration         int      `json:"duration"`
	TranscriptPolicy string   `json:"transcript_policy"`
	OrderingStrategy string   `json:"ordering_strategy"`
	CreateDoc        bool     `json:"create_doc"`
	SaveToDB         bool     `json:"save_to_db"`
	ForceRefresh     bool     `json:"force_refresh"`

	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`
}

// HandleCatalogScriptGenerateJob processes a background script.generate_from_catalog job.
//
// Pipeline:
//  1. Hydrate selected clips → build evidence cards
//  2. Narrative planning (LLM step 1)
//  3. Build source text from evidence + plan
//  4. Generate script through common engine (WriteScript)
//  5. Assemble result with catalog_report metadata
func (h *ScriptFlowHandler) HandleCatalogScriptGenerateJob(ctx context.Context, job *domainjob.Job, tools *jobservice.JobTools) (map[string]any, error) {
	h.log.Info("handling script.generate_from_catalog job", zap.String("job_id", job.ID))

	clipSvc := h.clipSourceBuilder
	if clipSvc == nil {
		return nil, fmt.Errorf("clip source builder not initialized")
	}
	if h.engine == nil {
		return nil, fmt.Errorf("script engine not initialized")
	}

	var payload jobPayloadCatalogScript
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse job payload: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Loading %d clips selected from catalog", len(payload.ClipIDs)))
	}

	opts := &scriptcore.ClipGenerationOptions{
		Language:         payload.Language,
		Tone:             payload.Tone,
		Title:            payload.Title,
		Model:            payload.Model,
		TargetWords:      payload.TargetWords,
		TranscriptPolicy: payload.TranscriptPolicy,
		OrderingStrategy: payload.OrderingStrategy,
	}
	if payload.MinQualityScore != nil {
		opts.MinQualityScore = *payload.MinQualityScore
	}
	if payload.MinTranscriptWords != nil {
		opts.MinTranscriptWords = *payload.MinTranscriptWords
	}

	if tools.Progress != nil {
		tools.Progress(15, "Hydrating clips and building evidence cards")
	}

	// Step 1-3: BuildClipContext = hydrate + validate + narrative plan + source text
	pack, plan, sourceText, err := clipSvc.BuildClipContext(ctx, payload.ClipIDs, opts)
	if err != nil {
		return nil, fmt.Errorf("clip context building failed: %w", err)
	}

	// Compute source fingerprint for memory gate cache key
	sourceFingerprint := clipSvc.ComputeFingerprint(payload.ClipIDs, pack, opts, scriptcore.NewFingerprintContext(opts.Model, opts.Model))

	if tools.Progress != nil {
		tools.Progress(50, "Generating script via common engine (MemoryGate, normalization)...")
	}

	// Step 4: Generate script through the common engine
	writeResult, err := h.engine.WriteScript(ctx, scriptcore.WriteScriptRequest{
		Plan: &scriptcore.ScriptGenerationPlan{
			Title:       plan.Title,
			Topic:       plan.Title,
			Language:    opts.Language,
			Tone:        opts.Tone,
			Model:       opts.Model,
			Mode:        gemmamemory.ModeClipToScript,
			SourceText:  sourceText,
			TargetWords: opts.TargetWords,
			UseMemory:   !payload.ForceRefresh,
			SaveToDB:    payload.SaveToDB,
			Prompt:      sourceFingerprint,
		},
		Topic:       plan.Title,
		Title:       plan.Title,
		Language:    opts.Language,
		Tone:        opts.Tone,
		Model:       opts.Model,
		Mode:        gemmamemory.ModeClipToScript,
		SourceText:  sourceText,
		MinWords:    opts.TargetWords,
		Prompt:      sourceFingerprint,
		UseMemory:   !payload.ForceRefresh,
		SaveToDB:    payload.SaveToDB,
		SaveTimeout: 60,
		Type:        opts.Type, // PR3: structural strategy
		ClipPack:    pack,      // PR3: enable quality gate
	})
	if err != nil {
		return nil, fmt.Errorf("script generation failed: %w", err)
	}

	wordCount := textutil.CountWords(writeResult.Script)

	if tools.Progress != nil {
		tools.Progress(90, "Finalizing...")
	}

	// Build result with structured output
	result := map[string]any{
		"ok":           true,
		"script_id":    writeResult.ScriptID,
		"title":        plan.Title,
		"script":       writeResult.Script,
		"word_count":   wordCount,
		"language":     opts.Language,
		"mode":         "catalog_first",
		"cache_status": writeResult.CacheStatus,
		"clip_coverage": map[string]any{
			"requested": len(payload.ClipIDs),
			"accepted":  len(pack.Clips),
			"used":      len(plan.OrderedClips),
			"excluded":  len(pack.ExcludedClips),
		},
		"narrative_arc":      plan.NarrativeArc,
		"warnings":           plan.Warnings,
		"sections_count":     len(plan.OrderedClips),
		"source_fingerprint": sourceFingerprint,
		"narrative_plan":     plan,
	}

	// Add excluded clips
	if len(pack.ExcludedClips) > 0 {
		excluded := make([]map[string]any, 0, len(pack.ExcludedClips))
		for _, ec := range pack.ExcludedClips {
			excluded = append(excluded, map[string]any{
				"clip_id": ec.ClipID,
				"reason":  ec.ExcludeReason,
			})
		}
		result["excluded_clips"] = excluded
	}

	if tools.Progress != nil {
		tools.Progress(100, "Catalog-first generation completed")
	}

	return result, nil
}
