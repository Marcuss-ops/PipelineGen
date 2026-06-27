// Package scripts — flow helpers that are still invoked after
// PR2 2b/c (June 2026). The historical shape of this file carried
// ~14 helpers (SearchScriptAssets, BuildPhraseClipSuggestions,
// SearchIntroClips, EnrichSpecialNamesWithImages,
// ResolveRecommendedDriveFolder, artlistSearchPhrase, etc.) which
// all depended on ClipServices ports whose packages were removed
// from origin (commit d61068b3). Every one of those helpers
// short-circuited to nil/empty under production wiring.
//
// After PR2 2b/c, ClipServices sheds the 7 dropped ports and
// ScriptInsights sheds the 5 dead fields they used to populate.
// This file is therefore slimmed to the two functions that the
// live pipeline still calls:
//
//   - BuildTextOnlyScriptPlan: invoked by PipelineUseCase.handleClipPathTextOnly
//   - ExtractScriptEntities: invoked by PostGenUseCase.Run when
//     spec.ExtractEntities=true
//
// No local type stubs for the removed realtime/association
// packages are kept: they were referenced only from the dropped
// helpers. The local minInt helper is gone for the same reason.
package scripts

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── ExtractScriptEntities ────────────────────────────────────────────────────

// ExtractScriptEntities extracts entities from a script text and returns
// the JSON-serialized entity analysis.
func ExtractScriptEntities(ctx context.Context, extractor EntityScriptExtractor, script string, model string) (string, error) {
	if extractor == nil {
		return "", nil
	}

	segments := textutil.SplitScriptSentences(script)
	if len(segments) == 0 {
		script = strings.TrimSpace(script)
		if script != "" {
			segments = []string{script}
		}
	}
	if len(segments) > 12 {
		segments = sliceutil.GroupSentences(segments, 4)
	}

	analysis, err := extractor.ExtractEntitiesFromScriptWithModel(ctx, segments, 12, model)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(analysis)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ── BuildTextOnlyScriptPlan ─────────────────────────────────────────────────

// BuildTextOnlyScriptPlan builds a plan for text-only script generation.
func BuildTextOnlyScriptPlan(
	topic, sourceText, guidelines, title, language, tone, model string,
	forceRefresh, saveToDB bool, targetWords int,
	promptVersion, editorPromptVersion, qaPromptVersion string,
) *scriptpkg.ScriptGenerationPlan {
	if topic == "" {
		topic = sourceText
	}
	if title == "" {
		title = topic
	}

	plan := &scriptpkg.ScriptGenerationPlan{
		Title:               title,
		Topic:               topic,
		Language:            language,
		Tone:                tone,
		Model:               model,
		Mode:                "generate",
		UseMemory:           !forceRefresh,
		SaveToDB:            saveToDB,
		TargetWords:         targetWords,
		Prompt:              topic,
		SourceText:          sourceText,
		Guidelines:          guidelines,
		PromptVersion:       promptVersion,
		EditorPromptVersion: editorPromptVersion,
		QAPromptVersion:     qaPromptVersion,
	}
	return plan
}

// _ keeps asset package referenced even if downstream lints think
// it unused (the local EntityScriptExtractor may evolve to require
// explicit FullEntityAnalysis type assertions).
var _ asset.FullEntityAnalysis
