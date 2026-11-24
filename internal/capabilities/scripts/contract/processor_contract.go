package contract

// ProcessorName is a typed identifier for a registered postprocessor.
type ProcessorName string

// ProcessorPolicy classifies a postprocessor's failure mode.
type ProcessorPolicy string

const (
	// ProcessorRequired marks a processor whose failure is a hard failure.
	ProcessorRequired ProcessorPolicy = "required"
	// ProcessorBestEffort marks a processor whose failure is a warning.
	ProcessorBestEffort ProcessorPolicy = "best_effort"
)

// Canonical postprocessor names. Each concrete processor's Name() method MUST
// return one of these constants.
const (
	ProcessorClipSearch                  ProcessorName = "clip_search"
	ProcessorMetadata                    ProcessorName = "metadata"
	ProcessorTranslation                 ProcessorName = "translation"
	ProcessorClipBindings                ProcessorName = "clip_bindings"
	ProcessorStockBindings               ProcessorName = "stock_bindings"
	ProcessorAssetLocationReconciliation ProcessorName = "asset_location_reconciliation"
	ProcessorVisualPlanning              ProcessorName = "visual_planning"
	ProcessorVisualSlots                 ProcessorName = "visual_slots"
	ProcessorVoiceover                   ProcessorName = "voiceover"
	ProcessorImages                      ProcessorName = "images"
	ProcessorInternetImages              ProcessorName = "internet_images"
	ProcessorVidRushMaterialization      ProcessorName = "vidrush_materialization"
	ProcessorPersistence                 ProcessorName = "persistence"
	ProcessorNarrationSanitizer          ProcessorName = "narration_sanitizer"
)

// ProcessorDescriptor is the single source of truth for a postprocessor's
// canonical metadata.
type ProcessorDescriptor struct {
	Name   ProcessorName
	Policy ProcessorPolicy
	Order  int
	Active bool
}

var canonicalDescriptors = []ProcessorDescriptor{
	{Name: ProcessorClipSearch, Policy: ProcessorBestEffort, Order: 0, Active: true},
	{Name: ProcessorMetadata, Policy: ProcessorRequired, Order: 2, Active: true},
	{Name: ProcessorTranslation, Policy: ProcessorBestEffort, Order: 3, Active: true},
	{Name: ProcessorClipBindings, Policy: ProcessorBestEffort, Order: 4, Active: true},
	{Name: ProcessorStockBindings, Policy: ProcessorRequired, Order: 5, Active: true},
	{Name: ProcessorVisualPlanning, Policy: ProcessorBestEffort, Order: 6, Active: true},
	{Name: ProcessorVisualSlots, Policy: ProcessorBestEffort, Order: 7, Active: true},
	{Name: ProcessorVoiceover, Policy: ProcessorBestEffort, Order: 8, Active: true},
	{Name: ProcessorImages, Policy: ProcessorBestEffort, Order: 9, Active: true},
	{Name: ProcessorInternetImages, Policy: ProcessorBestEffort, Order: 10, Active: true},
	{Name: ProcessorVidRushMaterialization, Policy: ProcessorBestEffort, Order: 11, Active: true},
	{Name: ProcessorAssetLocationReconciliation, Policy: ProcessorBestEffort, Order: 12, Active: true},
	{Name: ProcessorPersistence, Policy: ProcessorRequired, Order: 13, Active: true},
	{Name: ProcessorNarrationSanitizer, Policy: ProcessorRequired, Order: 14, Active: true},
}

var descriptorByName = func() map[ProcessorName]ProcessorDescriptor {
	m := make(map[ProcessorName]ProcessorDescriptor, len(canonicalDescriptors))
	for _, d := range canonicalDescriptors {
		m[d.Name] = d
	}
	return m
}()

// DefaultPolicyFor returns the canonical default policy for a named processor.
func DefaultPolicyFor(name ProcessorName) ProcessorPolicy {
	if d, ok := descriptorByName[name]; ok {
		return d.Policy
	}
	return ""
}

// CanonicalProcessorNames returns the closed set of active processors in
// canonical execution order.
func CanonicalProcessorNames() []ProcessorName {
	names := make([]ProcessorName, 0, len(canonicalDescriptors))
	for _, d := range canonicalDescriptors {
		if d.Active {
			names = append(names, d.Name)
		}
	}
	return names
}

// RequiredProcessorNames returns the active processors whose default policy is
// required.
func RequiredProcessorNames() []ProcessorName {
	var names []ProcessorName
	for _, d := range canonicalDescriptors {
		if d.Active && d.Policy == ProcessorRequired {
			names = append(names, d.Name)
		}
	}
	return names
}

// ProcessorNamesToStrings converts typed names to plain strings.
func ProcessorNamesToStrings(names []ProcessorName) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = string(n)
	}
	return out
}

// ProcessorNamesFromStrings converts strings to typed names.
func ProcessorNamesFromStrings(names []string) []ProcessorName {
	out := make([]ProcessorName, len(names))
	for i, n := range names {
		out[i] = ProcessorName(n)
	}
	return out
}
