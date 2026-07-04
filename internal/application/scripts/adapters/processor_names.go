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
// method MUST return one of these constants.
const (
	ProcessorEntities         ProcessorName = "entities"
	ProcessorMetadata         ProcessorName = "metadata"
	ProcessorClipBindings     ProcessorName = "clip_bindings"
	ProcessorStockAssociation ProcessorName = "stock_association"
	ProcessorVoiceover        ProcessorName = "voiceover"
	ProcessorImages           ProcessorName = "images"
	ProcessorDocument         ProcessorName = "document"
	ProcessorPersistence      ProcessorName = "persistence"
)

// CanonicalProcessorNames returns the closed set of all 8 canonical
// postprocessors in their expected execution order:
// entities → metadata → clip_bindings → stock_association →
// voiceover → images → document → persistence.
func CanonicalProcessorNames() []ProcessorName {
	return []ProcessorName{
		ProcessorEntities,
		ProcessorMetadata,
		ProcessorClipBindings,
		ProcessorStockAssociation,
		ProcessorVoiceover,
		ProcessorImages,
		ProcessorDocument,
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
