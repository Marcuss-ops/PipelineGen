// Package adapters — processor_names.go defines ProcessorName, the
// typed constants, the canonical ProcessorDescriptor registry, and the
// closed-set helpers used by the postprocessor registry and plan-builders.
//
// The canonical registry below is the SOLE source of truth for every
// postprocessor identifier, default policy, execution order, and
// active flag. Do NOT duplicate this information in sibling files.
package adapters

// ProcessorName is a typed identifier for a registered postprocessor.
type ProcessorName string

// ProcessorPolicy classifies a postprocessor's failure mode for
// composition and preflight decisions.
type ProcessorPolicy string

const (
	// ProcessorRequired marks a processor whose missing-registered
	// status is a composition-level failure (composition refuses
	// to start) AND whose runtime error or empty output causes the
	// overall Run to return a non-nil error.
	ProcessorRequired ProcessorPolicy = "required"

	// ProcessorBestEffort marks a processor whose missing-registered
	// status is a non-fatal warning (composition continues) AND
	// whose runtime error or empty output is a warning rather than
	// a hard failure.
	ProcessorBestEffort ProcessorPolicy = "best_effort"
)

// Canonical postprocessor names. Each concrete processor's Name()
// method MUST return one of these constants. This is the SOLE
// canonical home for every postprocessor identifier in the package
// (godlike/06 SSOT one-owner-per-fact) — do NOT redeclare these in
// sibling files.
const (
	ProcessorEntities       ProcessorName = "entities"
	ProcessorClipSearch     ProcessorName = "clip_search"
	ProcessorMetadata       ProcessorName = "metadata"
	ProcessorTranslation    ProcessorName = "translation"
	ProcessorClipBindings   ProcessorName = "clip_bindings"
	ProcessorStockBindings  ProcessorName = "stock_bindings"
	ProcessorVisualPlanning ProcessorName = "visual_planning"
	ProcessorVoiceover      ProcessorName = "voiceover"
	ProcessorImages         ProcessorName = "images"
	ProcessorInternetImages ProcessorName = "internet_images"
	ProcessorPersistence    ProcessorName = "persistence"
	ProcessorDocument       ProcessorName = "document"
)

// ProcessorDescriptor is the single source of truth for a
// postprocessor's canonical metadata.
type ProcessorDescriptor struct {
	Name   ProcessorName
	Policy ProcessorPolicy
	Order  int
	Active bool
}

// canonicalDescriptors is the authoritative registry of postprocessors
// metadata. The Order field encodes the canonical EXECUTION order
// (the order the registry walks processors in Run()).
var canonicalDescriptors = []ProcessorDescriptor{
	{Name: ProcessorEntities, Policy: ProcessorRequired, Order: 0, Active: true},
	{Name: ProcessorClipSearch, Policy: ProcessorBestEffort, Order: 1, Active: true},
	{Name: ProcessorMetadata, Policy: ProcessorRequired, Order: 2, Active: true},
	{Name: ProcessorTranslation, Policy: ProcessorBestEffort, Order: 3, Active: true},
	{Name: ProcessorClipBindings, Policy: ProcessorBestEffort, Order: 4, Active: true},
	{Name: ProcessorStockBindings, Policy: ProcessorRequired, Order: 5, Active: true},
	{Name: ProcessorVisualPlanning, Policy: ProcessorBestEffort, Order: 6, Active: true},
	{Name: ProcessorVoiceover, Policy: ProcessorBestEffort, Order: 7, Active: true},
	{Name: ProcessorImages, Policy: ProcessorBestEffort, Order: 8, Active: true},
	{Name: ProcessorInternetImages, Policy: ProcessorBestEffort, Order: 9, Active: true},
	{Name: ProcessorPersistence, Policy: ProcessorRequired, Order: 10, Active: true},
	{Name: ProcessorDocument, Policy: ProcessorBestEffort, Order: 11, Active: true},
}

// descriptorByName provides O(1) lookup over canonicalDescriptors.
var descriptorByName = func() map[ProcessorName]ProcessorDescriptor {
	m := make(map[ProcessorName]ProcessorDescriptor, len(canonicalDescriptors))
	for _, d := range canonicalDescriptors {
		m[d.Name] = d
	}
	return m
}()

// DefaultPolicyFor returns the canonical default policy for a named
// postprocessor. Returns empty string for unknown names — callers
// MUST treat unknown names as a hard fail or warn per their own
// classification logic.
func DefaultPolicyFor(name ProcessorName) ProcessorPolicy {
	if d, ok := descriptorByName[name]; ok {
		return d.Policy
	}
	return ""
}

// IsCanonicalProcessor reports whether name is part of the canonical set.
func IsCanonicalProcessor(name ProcessorName) bool {
	_, ok := descriptorByName[name]
	return ok
}

// CanonicalProcessorNames returns the closed set of all active
// canonical postprocessors in their canonical EXECUTION order.
func CanonicalProcessorNames() []ProcessorName {
	names := make([]ProcessorName, 0, len(canonicalDescriptors))
	for _, d := range canonicalDescriptors {
		if !d.Active {
			continue
		}
		names = append(names, d.Name)
	}
	return names
}

// RequiredProcessorNames returns the names of all active canonical
// processors whose default policy is Required.
func RequiredProcessorNames() []ProcessorName {
	var names []ProcessorName
	for _, d := range canonicalDescriptors {
		if !d.Active || d.Policy != ProcessorRequired {
			continue
		}
		names = append(names, d.Name)
	}
	return names
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

// ProcessorNamesFromStrings converts a []string to typed
// []ProcessorName. Non-canonical strings are preserved as typed
// values; callers should validate with IsCanonicalProcessor if needed.
func ProcessorNamesFromStrings(names []string) []ProcessorName {
	out := make([]ProcessorName, len(names))
	for i, n := range names {
		out[i] = ProcessorName(n)
	}
	return out
}
