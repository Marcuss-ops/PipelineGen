// Package scriptgeneration — semantics_aliases.go is the facade over the
// shared visual-entity vocabulary and its pure projections.
//
// VisualEntity is part of scripts/contract because the wire shape is shared
// with the VidRush subpackage and the NER FFI boundary; the pure helpers are in
// scripts/semantics. The root keeps naming them unqualified, so every existing
// call site in this package (and the tests that exercise it) stays unchanged
// while the definitions move out of the cycle.
package scriptgeneration

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/semantics"
)

// VisualEntity is the source-grounded entity shared by the scene pipeline and
// the VidRush enrichment.
type VisualEntity = contract.VisualEntity

var (
	// findEntitySpan locates the first case-sensitive (then case-insensitive)
	// occurrence of value in text and returns its rune span.
	findEntitySpan = semantics.FindEntitySpan
	// advanceRunes returns the byte offset n runes after start.
	advanceRunes = semantics.AdvanceRunes
	// groundImportantPhrases keeps only selected phrases that are grounded in
	// the source and do not overlap a named entity.
	groundImportantPhrases = semantics.GroundImportantPhrases
	// entityRuneSpans resolves the rune spans of the grounded entity surfaces.
	entityRuneSpans = semantics.EntityRuneSpans
	// translatedSpecialNames keeps the typed, source-grounded special names of
	// a translated scene.
	translatedSpecialNames = semantics.TranslatedSpecialNames
	// limitTranslatedVisualEntities ranks entities for translation.
	limitTranslatedVisualEntities = semantics.LimitTranslatedVisualEntities
	// isNamedVisualEntity reports whether a type is a named entity surface.
	isNamedVisualEntity = semantics.IsNamedVisualEntity
	// normalizeVisualEntityTypes folds each entity type onto the canonical kind.
	normalizeVisualEntityTypes = semantics.NormalizeVisualEntityTypes
	// limitVisualIdentityEntities caps only imageable named identities.
	limitVisualIdentityEntities = semantics.LimitVisualIdentityEntities
	// deduplicateVisualEntities keeps the first span of each grounded surface.
	deduplicateVisualEntities = semantics.DeduplicateVisualEntities
	// filterOverlayCategoryEntities drops entities the overlay lane cannot use.
	filterOverlayCategoryEntities = semantics.FilterOverlayCategoryEntities
	// hasAdditionalEntityCategory reports whether the policy selected value
	// categories beside the generic entity surface.
	hasAdditionalEntityCategory = semantics.HasAdditionalEntityCategory
)
