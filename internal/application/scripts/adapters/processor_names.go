// Package adapters — processor_names.go defines ProcessorName, the
// typed constant set for postprocessor identifiers. Every
// PostProcessor.Name() returns a ProcessorName; registries and
// plan-builders consume ProcessorName instead of raw strings so
// typos surface as compile-time errors rather than runtime
// "processor not registered" warnings.
//
// AZIONE 3 (July 2026): typed constants replace string-based names
// in generation_plan_builder.go and postprocessor_registry.go.
// CanonicalProcessorNames() provides the closed set for iteration
// and validation.
package adapters

// ProcessorName is a typed identifier for a registered postprocessor.
type ProcessorName string

// Canonical postprocessor names. Each concrete processor's Name()
// method MUST return one of these constants. This is the SOLE
// canonical home for every postprocessor identifier in the package
// (godlike/06 SSOT one-owner-per-fact) — do NOT redeclare these in
// sibling files.
//
// Sprint 1.0: ProcessorDocument was RETIRED from the script
// postprocessor chain. Document creation is now produced by the
// canonical downstream document.generate job
// (internal/application/document/usecase.go), which is the only
// place that owns Google Drive writes for script artefacts. The
// script postprocessor chain produces SpecScene; the document job
// turns SpecScene into a Google Doc.
const (
	ProcessorEntities ProcessorName = "entities"
	// PR-CLIP-SEARCH-WIRING (July 2026): clip_search is the canonical
	// identifier for the ClipSearchProcessor (artlist phrase enrichment,
	// BestEffort policy). Lives here — NOT in processor_clip_search.go —
	// to satisfy godlike/06 SSOT one-canonical-owner-per-fact.
	ProcessorClipSearch       ProcessorName = "clip_search"
	ProcessorMetadata         ProcessorName = "metadata"
	ProcessorClipBindings     ProcessorName = "clip_bindings"
	ProcessorStockAssociation ProcessorName = "stock_association"
	ProcessorVoiceover        ProcessorName = "voiceover"
	ProcessorImages           ProcessorName = "images"
	ProcessorPersistence      ProcessorName = "persistence"
	// PR-TRANSLATE-SCRIPT-SPEC forward-pointer FP2 (2026-08-08):
	// translation postprocessor lives in the canonical SOLE identifier
	// set (godlike/06 SSOT one-canonical-owner-per-fact) — inserted
	// between metadata and clip_bindings in the EXECUTION order so the
	// translated SpecScene is consumed by the downstream ClipBindings
	// pass (localised Drive links + clip titles).
	ProcessorTranslation ProcessorName = "translation"
)

// CanonicalProcessorNames returns the closed set of all 8 canonical
// postprocessors in their canonical EXECUTION order.
//
// IMPORTANT (godlike/07 typed-distinction): this list reflects
// EXECUTION order (the order the registry walks processors when
// Run() is called), NOT REGISTRATION order (the order processors
// are added to the registry at composition time). The two are not
// the same on purpose:
//   - EXECUTION order (this list): entities → clip_search → metadata →
//     translation → clip_bindings → stock_association → voiceover →
//     images → persistence. Persistence at the tail because each
//     processor that mutated scene/payload must run BEFORE the row
//     is locked for replay/retry. Translation slots between
//     metadata and clip_bindings so the translated SpecScene text is
//     visible to the downstream clip_bindings pass.
//   - REGISTRATION order (see registerScriptPostProcessors in
//     internal/app/wire_script_postprocess.go): persistence FIRST so
//     no Drive-write side effect runs before the SQLite row is
//     locked (replay-safe semantics per PR SCRIPTCONTRACT-2026-07-08
//     PR-1, godlike/06 SSOT, fails-closed at composition).
//
// Plan-time execution order is constructed in buildPostprocessorList
// (internal/application/scripts/usecase/generation_plan_builder.go)
// — clip_search is conditionally appended between entities and
// metadata when OutputSpec.ExtractEntities=true; translation is
// conditionally appended between metadata and clip_bindings when
// OutputSpec.TranslateTo != "" (PR-TRANSLATE-SCRIPT-SPEC FP2).
// The closed set here uses position 2 (clip_search) + position 4
// (translation) so the EXECUTION order is consistent with the
// plan-time build even when the corresponding OutputSpec flags
// are false (the closed-set slot is reserved, not optional).
func CanonicalProcessorNames() []ProcessorName {
	return []ProcessorName{
		ProcessorEntities,
		ProcessorClipSearch,
		ProcessorMetadata,
		ProcessorTranslation,
		ProcessorClipBindings,
		ProcessorStockAssociation,
		ProcessorVoiceover,
		ProcessorImages,
		ProcessorPersistence,
	}
}

// ProcessorNamesToStrings converts a typed slice to plain []string.
// Used at the boundary where ResolvedGenerationPlan.Postprocessors
// (which stays []string in the domain layer) receives the output of
// buildPostprocessorList.
func ProcessorNamesToStrings(names []ProcessorName) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = string(n)
	}
	return out
}
