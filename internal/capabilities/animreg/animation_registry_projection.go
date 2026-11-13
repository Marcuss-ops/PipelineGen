package animreg

// AnimationCandidates is the package-level read-only projection used by
// planners and render adapters. All animation groups query this same registry.
func AnimationCandidates(group string) []string {
	return canonicalAnimationRegistry.Candidates(group)
}

func AnimationSelectable(id, group string) bool {
	return canonicalAnimationRegistry.Selectable(id, group)
}

func AnimationRendererSupported(id string) bool {
	return canonicalAnimationRegistry.RendererSupported(id)
}

func AnimationRandomCandidates(group string) []string {
	return canonicalAnimationRegistry.RandomCandidates(group)
}

func AnimationIsRetired(id string) bool { return canonicalAnimationRegistry.IsRetired(id) }

// AnimationAutomaticallySelectable is the package-level projection used by the
// producer pools.
func AnimationAutomaticallySelectable(id string) bool {
	return canonicalAnimationRegistry.AutomaticallySelectable(id)
}

func RetiredAnimationCandidates(group string) []string {
	return canonicalAnimationRegistry.RetiredCandidates(group)
}

// AnimationEntryFamilies returns the registry families of one animation id.
// The second result is false when the id is unknown to the registry.
func AnimationEntryFamilies(id string) ([]string, bool) {
	return canonicalAnimationRegistry.FamiliesOf(id)
}

func AnimationWordCount(id string) (int, bool) { return canonicalAnimationRegistry.WordCount(id) }

func AnimationLaneCompatible(id, lane string) bool {
	return canonicalAnimationRegistry.LaneCompatible(id, lane)
}

func AnimationTargetCandidates(target string) []string {
	return canonicalAnimationRegistry.TargetCandidates(target)
}

func AnimationSupportsTarget(id, target string) bool {
	return canonicalAnimationRegistry.SupportsTarget(id, target)
}

func AnimationMapTypeBasemap(mapType string) (string, bool) {
	return canonicalAnimationRegistry.MapTypeBasemap(mapType)
}

func AnimationRegistryMapTypeDefault() string {
	if canonicalAnimationRegistry == nil {
		return ""
	}
	return canonicalAnimationRegistry.mapTypeDefault
}

func AnimationSupportsMapType(mapType string) bool {
	return canonicalAnimationRegistry.SupportsMapType(mapType)
}

func AnimationMapCompositionCandidates(pinCount int) []string {
	return canonicalAnimationRegistry.MapCompositionEligible(pinCount)
}

// ItemMotionGroups is the package-level projection of the registry-owned
// overlay-kind → motion-group mapping.
func ItemMotionGroups(kind string) []string {
	return canonicalAnimationRegistry.ItemMotionGroups(kind)
}

func ItemDefaultMotionGroups() []string {
	return canonicalAnimationRegistry.ItemDefaultMotionGroups()
}

func ItemNumberMotionGroups(template string) []string {
	return canonicalAnimationRegistry.ItemNumberMotionGroups(template)
}

func ItemChartGroups() []string {
	return canonicalAnimationRegistry.ItemChartGroups()
}

func ItemChartParams() []string {
	return canonicalAnimationRegistry.ItemChartParams()
}

// VisualIntents is the package-level projection of the registry-owned
// semantic-type → visual-intent table.
func VisualIntents() map[string]VisualIntentSpec {
	return canonicalAnimationRegistry.VisualIntents()
}

// Canonical returns the process-wide registry loaded from the embedded JSON.
func Canonical() *AnimationRegistry { return canonicalAnimationRegistry }
