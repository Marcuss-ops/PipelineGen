// Package usecase — text_track_payload.go is the payload-side leaf of
// the text-track 6-file split. It owns ONLY the raw input-extraction
// step for the priority-1 source; it does NOT normalise language
// codes and does NOT assemble the ResolvedTextBundle provenance.
//
// AGENTS.md / godlike/06 SSOT split (July 2026): the orchestrator
// (text_track_resolver.go) is the SOLE canonical site for:
//   - asset.Normalize() calls (BCP-47 normalisation)
//   - ResolvedTextBundle provenance assembly (SourceType, IsOriginal,
//     Provider, ModelName, ModelVersion, Confidence,
//     SourceLanguageCode)
//
// This leaf is intentionally a one-helper file so the orchestrator's
// priority-1 path stays declarative without inlining a for-loop on
// payload texts. Callers receive the first payload LocalizedClipText
// whose Transcript is non-empty; the orchestrator then runs
// asset.Normalize on its LanguageCode and composes the bundle.
//
// godlike/07 honest lock: the leaf returns (zero, false) when no
// payload text has a non-empty Transcript — NEVER a nil pointer
// wrapper, NEVER a typed error. The orchestrator surfaces this as
// (nil, nil) per the chain's "miss" semantics.
package usecase

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// resolveRawPayload returns the first LocalizedClipText whose
// Transcript is non-empty. It is the SOLE canonical payload-priority
// raw extractor (godlike/06 SSOT) and does NOT touch language codes
// or bundle provenance — both stay in the orchestrator.
//
// Returns (zero, false) when no payload text carries a non-empty
// Transcript; the orchestrator treats this as "priority 1 missed",
// short-circuits to (nil, nil), and tries priority 2.
func resolveRawPayload(payloadTexts []youtubetypes.LocalizedClipText) (youtubetypes.LocalizedClipText, bool) {
	for _, t := range payloadTexts {
		if t.Transcript == "" {
			continue
		}
		return t, true
	}
	return youtubetypes.LocalizedClipText{}, false
}
