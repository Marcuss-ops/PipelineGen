// Package usecase — text_track_persistence.go is the persistence-
// side leaf of the text-track 6-file split. It owns ONLY the
// canonical row-factory helpers that the orchestrator's Save /
// SaveMany / MaterializePayloadTexts paths delegate to after
// normalisation + provenance composition.
//
// AGENTS.md / godlike/06 SSOT split (July 2026): the orchestrator
// (text_track_resolver.go) is the SOLE canonical site for:
//   - asset.Normalize() calls (BCP-47 normalisation)
//   - per-source IsOriginal derivation
//   - per-source provenance field assembly (ModelName, ModelVersion,
//     Provider, Confidence mapping)
//
// This leaf is intentionally a row-factory-only file: no BCP-47
// rules, no per-source decision logic, no policy gates. Helpers
// here either:
//   - convert a(n already-orchestrator-composed) bundle into a
//     single detail.TextTrack row (bundleToTextTracks),
//   - convert a database row back to a bundle for the chain to
//     use (cdbRowToBundle),
//   - pull a default scalar (sourceOrProvided, sourceLangOf,
//     confidencePtr).
//
// godlike/07 honest lock: bundleToTextTracks returns nil for the
// empty-text defensive guard so the orchestrator's
// Save / SaveMany paths do not surface a "transcript==\"\"" row.
// cdbRowToBundle does NOT re-normalise — the orchestrator already
// normalised the language BEFORE the DB probe, so the row's
// LanguageCode is canonical on the way out.
package usecase

import (
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// bundleToTextTracks converts a non-empty bundle into a single
// TextTrack row of the requested kind. Returns nil for empty bundle
// or empty plaintext. Computes canonical hashes via the factory.
//
// godlike/06 SSOT: the orchestrator already ran asset.Normalize on
// bundle.LanguageCode, so the helper does NOT re-derive. The
// "und"-on-empty fallback is a defensive guard that mirrors the
// canonical hash factory input contract.
func bundleToTextTracks(clipID string, bundle *detail.ResolvedTextBundle, kind detail.TextTrackKind) []detail.TextTrack {
	if bundle == nil || bundle.IsEmpty() || bundle.PlainText == "" {
		return nil
	}
	lang := bundle.LanguageCode
	if lang == "" {
		lang = "und"
	}
	srcLang := bundle.SourceLanguageCode
	if srcLang == "" {
		srcLang = lang
	}
	hash := detail.TextHash(bundle.PlainText, lang, kind)
	return []detail.TextTrack{{
		AssetID:            clipID,
		LanguageCode:       lang,
		TextKind:           kind,
		TextContent:        bundle.PlainText,
		SourceType:         bundle.SourceType,
		SourceLanguageCode: srcLang,
		IsOriginal:         bundle.IsOriginal,
		Provider:           bundle.Provider,
		ModelName:          bundle.ModelName,
		ModelVersion:       bundle.ModelVersion,
		TextHash:           hash,
		SourceVersion:      detail.SourceVersion(hash, srcLang, lang, bundle.Provider, bundle.ModelName, bundle.ModelVersion, ""),
		Confidence:         bundle.Confidence,
		Status:             detail.TextTrackReady,
	}}
}

// cdbRowToBundle converts a (DB) TextTrack row into the resolver's
// bundle shape. The orchestrator's priority-2 path uses this
// helper to project the resolved-by-preferred-languages row up
// into the same shape that payload / subtitles / Whisper produce,
// so downstream consumers see a single canonical shape.
//
// godlike/06 SSOT: the orchestrator already normalised the language
// before the DB probe (per centralisation discipline), so this
// helper does NOT re-normalise. The DB-canonical LanguageCode
// flows through verbatim.
func cdbRowToBundle(row detail.TextTrack) *detail.ResolvedTextBundle {
	return &detail.ResolvedTextBundle{
		LanguageCode:       row.LanguageCode,
		SourceLanguageCode: row.SourceLanguageCode,
		PlainText:          row.TextContent,
		Cues:               nil,
		SourceType:         row.SourceType,
		IsOriginal:         row.IsOriginal,
		Provider:           row.Provider,
		ModelName:          row.ModelName,
		ModelVersion:       row.ModelVersion,
		Confidence:         row.Confidence,
	}
}

// sourceOrProvided returns the parsed TextTrackSource, defaulting
// to TextSourceProvided when the input is empty. The orchestrator's
// MaterializePayloadTexts path uses this as the SSOT "empty source
// = provided" default (godlike/07 honest lock: a missing source is
// NEVER silently propagated; it is categorised as "provided" since
// the orchestrator's caller supplied the literal payload).
func sourceOrProvided(s string) detail.TextTrackSource {
	if s == "" {
		return detail.TextSourceProvided
	}
	return detail.TextTrackSource(s)
}

// sourceLangOf returns the source language code for a
// LocalizedClipText. The rule is:
//   - If t.SourceLanguageCode is set, return it (canonical explicit
//     form; the caller has already BCP-47-normalised it).
//   - Otherwise, if isOriginal is true, return fallback (the implied
//     "this is the original language" — the orchestrator decides what
//     "isOriginal" means per context).
//   - Otherwise, return empty.
//
// godlike/06 SSOT: the orchestrator (text_track_resolver.go) is the
// SOLE canonical site for the composite isOriginal decision (which
// mixes raw t.IsOriginal with srcType-derived "is-original" sources
// in MaterializePayloadTexts). ResolveOriginal calls this helper
// with the raw `t.IsOriginal` because its own IsOriginal field is
// derived by a separate cascade (t.IsOriginal || srcLang == "" ||
// srcLang == lang) that produces its own field value.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE 6-file split rationale (Fase 2.e):
// the third parameter makes the derivation rule explicit and removes
// the inline-rule duplication that previously sat inside
// MaterializePayloadTexts.
func sourceLangOf(t youtubetypes.LocalizedClipText, fallback string, isOriginal bool) string {
	if t.SourceLanguageCode != "" {
		return t.SourceLanguageCode
	}
	if isOriginal {
		return fallback
	}
	return ""
}

// confidencePtr returns nil when c is zero (so a missing confidence
// stays "unset" instead of being persisted with a non-nil pointer to
// 0). The orchestrator's MaterializePayloadTexts / Save paths
// pass the resulting pointer to the row factory (TextTrack.Confidence
// is *float64).
func confidencePtr(c float64) *float64 {
	if c == 0 {
		return nil
	}
	return &c
}
