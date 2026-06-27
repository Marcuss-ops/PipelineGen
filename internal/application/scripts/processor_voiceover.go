// Package scripts — processor_voiceover.go generates voiceovers for
// each scene. Enabled as "voiceover" in the plan's Postprocessors list.
//
// PR 9 (June 2026): the legacy scene-splitters
// (splitScriptIntoSegments / sceneCountFromPlan) were REMOVED.
// The processor now reads scenes directly from
// engineResult.Output.SpecScene.Scenes — the canonical structured
// output from PR 1, validated by PR 6's ValidateAndEnrichSpecScene.
// Each generated voiceover maps to a model-defined scene with
// stable indexes.
//
// Partial failures are collected — the processor does NOT abort on
// first error. No-op when plan has no ClipEvidence or when the model
// output has zero scenes.
package scripts

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// VoiceoverProcessor generates scene voiceovers via VoiceoverService.
// Uses engineResult.Output.SpecScene.Scenes to drive per-scene
// voiceover generation (PR 9 contract).
type VoiceoverProcessor struct {
	gen VoiceoverService
	log *zap.Logger
}

// NewVoiceoverProcessor creates a VoiceoverProcessor.
// gen must be non-nil (enforced at registration time by wire_script.go).
func NewVoiceoverProcessor(gen VoiceoverService, log *zap.Logger) *VoiceoverProcessor {
	return &VoiceoverProcessor{gen: gen, log: log}
}

func (p *VoiceoverProcessor) Name() string { return "voiceover" }

// Policy classifies voiceover as ProcessorBestEffort: a missing
// voiceover service (typed adapter nil at composition time) or a
// runtime TTS failure degrades into a Warning, not a hard failure.
// Voiceover is an auxiliary deliverable; per PR 2 spec: "voiceover =
// configurabile" (best-effort is the safe default). The plan arg is
// accepted for interface uniformity but ignored.
func (p *VoiceoverProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process generates per-scene voiceovers. PR 9 contract: scenes come
// directly from engineResult.Output.SpecScene.Scenes (validated by
// ValidateAndEnrichSpecScene); no paragraph-splitting helper is
// used.
func (p *VoiceoverProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: voiceover processor: VoiceoverService not configured", scriptpkg.ErrPostprocessFailed)
	}

	// PR 9: scenes sourced from canonical typed MSOV1.
	scenes := specScenesFromInput(input)
	if len(scenes) == 0 {
		if p.log != nil {
			p.log.Debug("voiceover processor: no scenes to render (no specscene scenes)",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessResult{}, nil
	}

	if input.Text == "" {
		return &PostProcessResult{}, nil
	}

	language := plan.Language
	if language == "" {
		language = "en"
	}

	voiceovers := make([]SceneVoiceover, 0, len(scenes))
	var warnings []string

	for i, scene := range scenes {
		sceneText := scene.Text
		if sceneText == "" {
			sceneText = fmt.Sprintf("Scene %d", i+1)
		}
		filename := fmt.Sprintf("%s_scene_%d", sanitizeFilename(plan.Title), i+1)

		result, err := p.gen.Generate(ctx, sceneText, language, filename)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("voiceover failed for scene %d: %v", i, err))
			voiceovers = append(voiceovers, SceneVoiceover{SceneIndex: i, Status: "failed"})
			continue
		}

		// VoiceoverService.Generate returns interface{}; production
		// concrete is *voiceover.VoiceoverResult.
		link, path := extractVoiceoverPaths(result)
		status := "completed"
		if link == "" && path == "" {
			status = "empty_result"
		}

		voiceovers = append(voiceovers, SceneVoiceover{
			SceneIndex: i,
			Status:     status,
			Link:       link,
			LocalPath:  path,
		})
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("voiceover processor: partial failures",
			zap.Int("total", len(scenes)),
			zap.Int("failed", len(warnings)),
			zap.Int("succeeded", len(voiceovers)-len(warnings)),
			zap.Strings("warnings", warnings))
	}

	return &PostProcessResult{Voiceovers: voiceovers}, nil
}

// extractVoiceoverPaths extracts DriveLink and Path from a voiceover
// result. Handles both *voiceover.VoiceoverResult (production concrete)
// and map[string]any (test fakes). The VoiceoverService interface returns
// interface{}, so we type-assert to discover the concrete shape.
func extractVoiceoverPaths(result interface{}) (link, path string) {
	if result == nil {
		return "", ""
	}

	// Production path: *voiceover.VoiceoverResult has DriveLink and Path
	// as struct fields (not methods). Direct type assertion.
	if vo, ok := result.(*voiceover.VoiceoverResult); ok {
		return vo.DriveLink, vo.Path
	}

	// Fallback: map[string]any (test fakes).
	if m, ok := result.(map[string]any); ok {
		l, _ := m["drive_link"].(string)
		p, _ := m["path"].(string)
		return l, p
	}
	return "", ""
}

// sanitizeFilename replaces characters unsafe in filenames.
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", " ", "_",
	)
	return strings.ToLower(replacer.Replace(strings.TrimSpace(name)))
}
