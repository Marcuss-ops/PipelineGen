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
package adapters

import (
	"context"
	"fmt"

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

		// Sanitize the title for use in a filename, then build a
		// scene-stable filename: {title}_{scene_id}_{lang}.mp3.
		// VoiceoverProcessor used a local character-replacer (no .mp3,
		// no path-traversal guard); now delegates to the canonical
		// voiceover.SanitizeBasename which rejects path separators and
		// normalises unsafe characters via textutil.SanitizeFilename.
		sceneID := scene.ID
		if sceneID == "" {
			sceneID = fmt.Sprintf("%d", i+1)
		}
		// Sanitize sceneID too — it comes from model output and must
		// not contain path separators or unsafe filename characters.
		safeSceneID, serr2 := voiceover.SanitizeBasename(sceneID)
		if serr2 != nil {
			safeSceneID = fmt.Sprintf("s%d", i+1)
		}
		safeTitle, serr := voiceover.SanitizeBasename(plan.Title)
		if serr != nil {
			safeTitle = "scene"
		}
		filename := fmt.Sprintf("%s_%s_%s.mp3", safeTitle, safeSceneID, language)

		// Use GenerateWithDestination when the plan carries a
		// voiceover destination (folder_id or resolved group).
		// Otherwise fall back to the default Generate (which
		// honours the configured voiceover folder).
		var result *voiceover.VoiceoverResult
		var voErr error
		if plan.VoiceoverFolderID != "" {
			dest := &voiceover.DestinationRequest{
				FolderID: plan.VoiceoverFolderID,
			}
			result, voErr = p.gen.GenerateWithDestination(ctx, sceneText, language, filename, dest)
		} else {
			if plan.VoiceoverGroup != "" && p.log != nil {
				p.log.Warn("voiceover processor: voiceover_group set but not resolved to folder_id — falling back to default folder",
					zap.String("voiceover_group", plan.VoiceoverGroup))
			}
			result, voErr = p.gen.Generate(ctx, sceneText, language, filename)
		}

		if voErr != nil {
			warnings = append(warnings, fmt.Sprintf("voiceover failed for scene %d: %v", i, voErr))
			voiceovers = append(voiceovers, SceneVoiceover{SceneIndex: i, Status: "failed"})
			continue
		}

		// Step 7 (June 2026) — M2 typed-port remediation: process
		// the typed *voiceover.VoiceoverResult directly. Path and
		// DriveLink are direct struct field reads (no type assertion,
		// no extractVoiceoverPaths). Result is nil-tolerant in case
		// the underlying service returns (nil, nil) — status flips to
		// "empty_result" matching pre-Step-7 behaviour. The "empty_result"
		// sentinel is preserved because the canonical envelope in
		// PostProcessResult.Voiceovers[i].Status needs a distinct value
		// from "completed" (with a real DriveLink/Path pair) AND from
		// "failed" (when Generate returned a non-nil err).
		link, path := "", ""
		if result != nil {
			link = result.DriveLink
			path = result.Path
		}
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

	// fix/voiceover-propagate-warnings (June 2026): propagate per-scene
	// failures to PostProcessResult.Warnings so the canonical
	// GenerationResult.Warnings envelope in the API response reports
	// them. The zap log above stays — it serves operator tail/grep,
	// while the envelope is the client-facing canonical surface.
	return &PostProcessResult{
		Voiceovers: voiceovers,
		Warnings:   warnings,
	}, nil
}

// ── Typed port (production adapter: voiceover.Service) ───────────────────

// VoiceoverService is the canonical port for voiceover generation.
// Production implementations live in internal/application/voiceover/
// (concrete *voiceover.Service); stubs live in adapters/_ in
// test fixtures (processor_voiceover_test.go + processor_images_voiceover_test.go).
//
// Step 7 / PR-VOICEOVER-STREAM-SUPERSESSION-2026-06-28 M2 typed-port
// remediation (June 2026): both methods now return the canonical
// typed *voiceover.VoiceoverResult (NOT interface{}). The Process body
// reads result.Path + result.DriveLink directly — no type assertion,
// no extractVoiceoverPaths helper. Companion back-compat alias
// `domain.VoiceoverResult = domain.Result` lives at
// internal/domain/voiceover/result.go for waves-cross safety during
// Wave 21 PR-G.2 BACKFILL settlement (deadline 2026-07-10).
//
// GenerateWithDestination is needed by VoiceoverProcessor when the
// plan carries a voiceover destination (folder_id or resolved group).
// Both production and test fakes must satisfy it.
type VoiceoverService interface {
	Generate(ctx context.Context, text, lang, filename string) (*voiceover.VoiceoverResult, error)
	GenerateWithDestination(ctx context.Context, text, lang, filename string, dest *voiceover.DestinationRequest) (*voiceover.VoiceoverResult, error)
}

// Compile-time assertion: *voiceover.Service satisfies adapters.VoiceoverService
// directly. Step 9 / PR-VOICEOVER-TYPED-PORT-RECOVERY-PHASE2 / B-3 CUTOVER
// (June 2026): deletes the previous `voiceoverSvcAdapter` wrapper that lived
// in internal/app/wire_script.go. The structural match holds because the
// concrete *voiceover.Service's Generate and GenerateWithDestination methods
// already return the typed *voiceover.VoiceoverResult (post-Step 7 M2 typed
// return). Drift in either side of this contract now fails the BUILD at this
// line instead of silently returning interface{} / panicking at runtime —
// AGENTS.md Pattern 0 (port abstraction layer, June 2026) "compile-time
// assertions catch signature drift at compile, not at first panic runtime".
var _ VoiceoverService = (*voiceover.Service)(nil)
