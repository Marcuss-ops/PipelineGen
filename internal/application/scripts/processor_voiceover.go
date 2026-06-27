// Package scripts — processor_voiceover.go generates voiceovers for
// each scene. Enabled as "voiceover" in the plan's Postprocessors list.
//
// The processor iterates over the plan's ClipEvidence to derive scene
// count and context, generates a voiceover for each scene via
// VoiceoverService, and returns SceneVoiceover results with preserved
// indexes. Partial failures are collected — the processor does NOT
// abort on first error.
//
// No-op when plan has no ClipEvidence (text-only generation).
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
// Uses plan.ClipEvidence to derive scene count and context.
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

// PR 5 (June 2026): signature now takes ProcessInput envelope.
func (p *VoiceoverProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: voiceover processor: VoiceoverService not configured", scriptpkg.ErrPostprocessFailed)
	}

	sceneCount := sceneCountFromPlan(plan)
	if sceneCount == 0 {
		if p.log != nil {
			p.log.Debug("voiceover processor: no scenes (no clip evidence)",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessResult{}, nil
	}

	if input.Text == "" {
		return &PostProcessResult{}, nil
	}

	segments := splitScriptIntoSegments(input.Text, sceneCount)
	language := plan.Language
	if language == "" {
		language = "en"
	}

	voiceovers := make([]SceneVoiceover, 0, sceneCount)
	var warnings []string

	for i := 0; i < sceneCount; i++ {
		sceneText := ""
		filename := fmt.Sprintf("%s_scene_%d", sanitizeFilename(plan.Title), i+1)
		if i < len(segments) {
			sceneText = segments[i]
		}
		if sceneText == "" {
			sceneText = fmt.Sprintf("Scene %d", i+1)
		}

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
			zap.Int("total", sceneCount),
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
