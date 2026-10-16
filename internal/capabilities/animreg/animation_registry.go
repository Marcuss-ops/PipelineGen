package animreg

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed animation_registry.v1.json
var animationRegistryJSON []byte

// AnimationEntry is one selectable or retired animation capability. Motion
// definitions remain owned by ChrononTemplate; this registry owns selection,
// lane, family, and renderer-admission metadata for the whole PipelineGen.
type AnimationEntry struct {
	ID                string         `json:"id"`
	Families          []string       `json:"families"`
	Lanes             []string       `json:"lanes"`
	Status            string         `json:"status"`
	RendererSupported bool           `json:"renderer_supported"`
	Targets           []string       `json:"targets"`
	WordCount         int            `json:"word_count,omitempty"`
	Order             map[string]int `json:"order,omitempty"`
	Random            []string       `json:"random,omitempty"`
	BlockedLanes      []string       `json:"blocked_lanes,omitempty"`
	Kind              string         `json:"kind,omitempty"`
	MinPinCount       int            `json:"min_pin_count,omitempty"`
	MaxPinCount       int            `json:"max_pin_count,omitempty"`
}

// ItemMotionGroupRule is one ordered rule of the number-kind mapping: the first
// rule whose AnyOf token appears in the upper-cased item template wins.
type ItemMotionGroupRule struct {
	AnyOf  []string `json:"any_of"`
	Groups []string `json:"groups"`
}

// VisualIntentSpec is one frozen SemanticType → (kind, family, priority)
// editorial mapping. It lives in the registry because the preset families it
// names are registry families: adding a semantic type is a registry entry, not
// a Go table.
type VisualIntentSpec struct {
	Kind     string `json:"kind"`
	Family   string `json:"family"`
	Priority int    `json:"priority"`
}

// itemMotionGroupsDocument owns the overlay-kind → motion-group mapping, the
// number-kind template rules and the chart upgrade: exactly one place decides
// which registry groups an item of a given kind may sample from.
type itemMotionGroupsDocument struct {
	ByKind              map[string][]string   `json:"by_kind"`
	NumberTemplateRules []ItemMotionGroupRule `json:"number_template_contains"`
	NumberFallback      []string              `json:"number_fallback"`
	ChartParams         []string              `json:"chart_params"`
	ChartGroups         []string              `json:"chart_groups"`
	Default             []string              `json:"default"`
}

type animationRegistryDocument struct {
	Schema           string                      `json:"schema"`
	Version          int                         `json:"version"`
	MapTypeAliases   map[string]string           `json:"map_type_aliases"`
	MapTypeDefault   string                      `json:"map_type_default"`
	ItemMotionGroups itemMotionGroupsDocument    `json:"item_motion_groups"`
	VisualIntents    map[string]VisualIntentSpec `json:"visual_intents"`
	Entries          []AnimationEntry            `json:"entries"`
}

type AnimationRegistry struct {
	entries        []AnimationEntry
	byID           map[string]AnimationEntry
	mapTypeAliases map[string]string
	mapTypeDefault string
	// Pre-indexes built once at load time so every Candidates-level lookup is
	// O(1) instead of a scan over all entries. Each slice is kept in authored
	// order; callers always receive a fresh copy and never mutate the index.
	groupIndex  map[string][]string
	randomIndex map[string][]string
	targetIndex map[string][]string
	// Item/kind and semantic-type mappings are data of the same registry, so a
	// pool and the kind that samples from it can never be renamed apart.
	itemMotionGroups itemMotionGroupsDocument
	visualIntents    map[string]VisualIntentSpec
}

func loadAnimationRegistry(raw []byte) (*AnimationRegistry, error) {
	var doc animationRegistryDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("animation registry: decode: %w", err)
	}
	if doc.Schema != "pipelinegen.animation-registry.v1" || doc.Version != 1 || len(doc.Entries) == 0 {
		return nil, fmt.Errorf("animation registry: unsupported or empty document")
	}
	r := &AnimationRegistry{entries: doc.Entries, byID: make(map[string]AnimationEntry, len(doc.Entries)), mapTypeAliases: doc.MapTypeAliases, mapTypeDefault: doc.MapTypeDefault,
		itemMotionGroups: doc.ItemMotionGroups, visualIntents: doc.VisualIntents,
		groupIndex: make(map[string][]string), randomIndex: make(map[string][]string), targetIndex: make(map[string][]string)}
	const maxOrder = int(^uint(0) >> 1)
	type rankedID struct {
		id    string
		order int
		seq   int
	}
	groupRows := make(map[string][]rankedID)
	for seq, e := range doc.Entries {
		if strings.TrimSpace(e.ID) == "" || (e.Status != "active" && e.Status != "retired") {
			return nil, fmt.Errorf("animation registry: invalid entry id/status %q/%q", e.ID, e.Status)
		}
		if _, exists := r.byID[e.ID]; exists {
			return nil, fmt.Errorf("animation registry: duplicate id %q", e.ID)
		}
		if e.Status == "active" && len(e.Families) == 0 {
			return nil, fmt.Errorf("animation registry: active entry %q has no family", e.ID)
		}
		r.byID[e.ID] = e
		if e.Status != "active" || !e.RendererSupported {
			continue
		}
		for _, group := range e.Families {
			order := maxOrder
			if v, ok := e.Order[group]; ok {
				order = v
			}
			groupRows[group] = append(groupRows[group], rankedID{id: e.ID, order: order, seq: seq})
		}
		for _, group := range e.Random {
			r.randomIndex[group] = append(r.randomIndex[group], e.ID)
		}
		for _, target := range e.Targets {
			r.targetIndex[target] = append(r.targetIndex[target], e.ID)
		}
	}
	for group, rows := range groupRows {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].order != rows[j].order {
				return rows[i].order < rows[j].order
			}
			return rows[i].seq < rows[j].seq
		})
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = row.id
		}
		r.groupIndex[group] = ids
	}
	if r.mapTypeDefault == "" || !r.Selectable(r.mapTypeDefault, "map.basemap") {
		return nil, fmt.Errorf("animation registry: map default %q is not an active basemap", r.mapTypeDefault)
	}
	for alias, id := range r.mapTypeAliases {
		if !r.Selectable(id, "map.basemap") {
			return nil, fmt.Errorf("animation registry: map type alias %q points to unavailable basemap %q", alias, id)
		}
	}
	if err := r.validateItemMotionGroups(); err != nil {
		return nil, err
	}
	if err := r.validateVisualIntents(); err != nil {
		return nil, err
	}
	return r, nil
}

// validateItemMotionGroups is the fail-closed gate for the item-kind mapping:
// every referenced group must be a non-empty registry group, so a rename that
// misses the mapping fails at load instead of silently emptying an item's pool.
func (r *AnimationRegistry) validateItemMotionGroups() error {
	mapping := r.itemMotionGroups
	if len(mapping.ByKind) == 0 || len(mapping.Default) == 0 || len(mapping.ChartGroups) == 0 ||
		len(mapping.ChartParams) == 0 || len(mapping.NumberFallback) == 0 || len(mapping.NumberTemplateRules) == 0 {
		return fmt.Errorf("animation registry: item_motion_groups must define by_kind, number_template_contains, number_fallback, chart_params, chart_groups and default")
	}
	seen := make(map[string]bool)
	check := func(owner string, groups []string) error {
		if len(groups) == 0 {
			return fmt.Errorf("animation registry: item_motion_groups %s has no group", owner)
		}
		for _, group := range groups {
			if seen[group] {
				continue
			}
			seen[group] = true
			if len(r.groupIndex[group]) == 0 {
				return fmt.Errorf("animation registry: item_motion_groups %s references group %q with no active renderer-supported animation", owner, group)
			}
		}
		return nil
	}
	for _, kind := range sortedKeys(mapping.ByKind) {
		if err := check("kind "+kind, mapping.ByKind[kind]); err != nil {
			return err
		}
	}
	for _, rule := range mapping.NumberTemplateRules {
		if len(rule.AnyOf) == 0 {
			return fmt.Errorf("animation registry: item_motion_groups number rule without any_of tokens")
		}
		if err := check("number rule "+strings.Join(rule.AnyOf, "|"), rule.Groups); err != nil {
			return err
		}
	}
	if err := check("number fallback", mapping.NumberFallback); err != nil {
		return err
	}
	if err := check("chart groups", mapping.ChartGroups); err != nil {
		return err
	}
	return check("default", mapping.Default)
}

// validateVisualIntents is the fail-closed gate for the semantic-type table:
// a malformed or non-canonical key would silently drop an intent kind.
func (r *AnimationRegistry) validateVisualIntents() error {
	if len(r.visualIntents) == 0 {
		return fmt.Errorf("animation registry: visual_intents must not be empty")
	}
	for semanticType, spec := range r.visualIntents {
		if strings.TrimSpace(semanticType) == "" || semanticType != strings.ToUpper(semanticType) {
			return fmt.Errorf("animation registry: visual intent key %q must be canonical upper case", semanticType)
		}
		if strings.TrimSpace(spec.Kind) == "" || strings.TrimSpace(spec.Family) == "" {
			return fmt.Errorf("animation registry: visual intent %q must define kind and family", semanticType)
		}
		if spec.Priority <= 0 {
			return fmt.Errorf("animation registry: visual intent %q must have a positive priority", semanticType)
		}
	}
	return nil
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ItemMotionGroups returns the registry groups an item of the given overlay
// kind may sample a motion from (nil when the kind is not authored, so callers
// fall back to the default set).
func (r *AnimationRegistry) ItemMotionGroups(kind string) []string {
	if r == nil {
		return nil
	}
	groups, ok := r.itemMotionGroups.ByKind[strings.ToLower(strings.TrimSpace(kind))]
	if !ok {
		return nil
	}
	return append([]string(nil), groups...)
}

// ItemDefaultMotionGroups is the wide group set used for unauthored kinds.
func (r *AnimationRegistry) ItemDefaultMotionGroups() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.itemMotionGroups.Default...)
}

// ItemNumberMotionGroups maps a number-kind item's template to its groups: the
// first template-substring rule that matches wins, otherwise the fallback set.
func (r *AnimationRegistry) ItemNumberMotionGroups(template string) []string {
	if r == nil {
		return nil
	}
	upper := strings.ToUpper(strings.TrimSpace(template))
	for _, rule := range r.itemMotionGroups.NumberTemplateRules {
		for _, token := range rule.AnyOf {
			if token != "" && strings.Contains(upper, token) {
				return append([]string(nil), rule.Groups...)
			}
		}
	}
	return append([]string(nil), r.itemMotionGroups.NumberFallback...)
}

// ItemChartGroups is the group set for an item that is (or carries) a chart.
func (r *AnimationRegistry) ItemChartGroups() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.itemMotionGroups.ChartGroups...)
}

// ItemChartParams are the item params that upgrade a non-chart kind into the
// chart group set.
func (r *AnimationRegistry) ItemChartParams() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.itemMotionGroups.ChartParams...)
}

// VisualIntents returns the frozen SemanticType → (kind, family, priority)
// table; the returned map is a copy, so callers never mutate the registry.
func (r *AnimationRegistry) VisualIntents() map[string]VisualIntentSpec {
	if r == nil {
		return nil
	}
	out := make(map[string]VisualIntentSpec, len(r.visualIntents))
	for semanticType, spec := range r.visualIntents {
		out[semanticType] = spec
	}
	return out
}

var canonicalAnimationRegistry = func() *AnimationRegistry {
	r, err := loadAnimationRegistry(animationRegistryJSON)
	if err != nil {
		panic(err)
	}
	return r
}()

// Selectable reports whether an id is active, renderer-supported, and belongs
// to the requested group. The same admission rule is used by every selector.
func (r *AnimationRegistry) Selectable(id, group string) bool {
	if r == nil {
		return false
	}
	e, ok := r.byID[id]
	return ok && e.Status == "active" && e.RendererSupported && containsMotion(e.Families, group)
}

func (r *AnimationRegistry) RendererSupported(id string) bool {
	if r == nil {
		return false
	}
	e, ok := r.byID[id]
	return ok && e.Status == "active" && e.RendererSupported
}

// Candidates returns the registry-owned group in authored order. Retired and
// renderer-unsupported entries are excluded from every automatic/explicit pool.
// The lookup reads the pre-built group index (O(1)); the returned slice is a
// fresh copy, so callers never mutate the index.
func (r *AnimationRegistry) Candidates(group string) []string {
	if r == nil {
		return nil
	}
	ids := r.groupIndex[group]
	if ids == nil {
		return nil
	}
	return append([]string(nil), ids...)
}

func (r *AnimationRegistry) RandomCandidates(group string) []string {
	if r == nil {
		return nil
	}
	ids := r.randomIndex[group]
	if ids == nil {
		return nil
	}
	return append([]string(nil), ids...)
}

func (r *AnimationRegistry) WordCount(id string) (int, bool) {
	if r == nil {
		return 0, false
	}
	e, ok := r.byID[id]
	if !ok || e.Status != "active" || !e.RendererSupported || e.WordCount < 1 || !r.Selectable(id, "phrase.short") {
		return 0, false
	}
	return e.WordCount, true
}

func (r *AnimationRegistry) LaneCompatible(id, lane string) bool {
	if r == nil {
		return false
	}
	e, ok := r.byID[id]
	if !ok || e.Status != "active" || !e.RendererSupported {
		return false
	}
	for _, blocked := range e.BlockedLanes {
		if blocked == lane {
			return false
		}
	}
	return true
}

func (r *AnimationRegistry) TargetCandidates(target string) []string {
	if r == nil {
		return nil
	}
	ids := r.targetIndex[target]
	if ids == nil {
		return nil
	}
	return append([]string(nil), ids...)
}

func (r *AnimationRegistry) SupportsTarget(id, target string) bool {
	if r == nil {
		return false
	}
	e, ok := r.byID[id]
	return ok && e.Status == "active" && e.RendererSupported && containsMotion(e.Targets, target)
}

func (r *AnimationRegistry) SupportsMapType(mapType string) bool {
	if strings.EqualFold(strings.TrimSpace(mapType), "random") {
		return len(r.Candidates("map.basemap")) > 0
	}
	_, ok := r.MapTypeBasemap(mapType)
	return ok
}

func (r *AnimationRegistry) IsRetired(id string) bool {
	e, ok := r.byID[id]
	return ok && e.Status == "retired"
}

func (r *AnimationRegistry) RetiredIDs() []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, e := range r.entries {
		if e.Status == "retired" {
			out = append(out, e.ID)
		}
	}
	return out
}

func (r *AnimationRegistry) RetiredCandidates(group string) []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, e := range r.entries {
		if e.Status == "retired" && containsMotion(e.Families, group) {
			out = append(out, e.ID)
		}
	}
	return out
}

func (r *AnimationRegistry) MapTypeBasemap(mapType string) (string, bool) {
	if r == nil {
		return "", false
	}
	key := strings.ToLower(strings.TrimSpace(mapType))
	if key == "random" {
		return "", false
	}
	if key == "" {
		return r.mapTypeDefault, true
	}
	id, ok := r.mapTypeAliases[key]
	return id, ok
}

func (r *AnimationRegistry) MapCompositionEligible(pinCount int) []string {
	out := make([]string, 0)
	for _, id := range r.Candidates("map.composition") {
		e := r.byID[id]
		if e.MinPinCount > 0 && pinCount < e.MinPinCount {
			continue
		}
		if e.MaxPinCount > 0 && pinCount > e.MaxPinCount {
			continue
		}
		out = append(out, id)
	}
	return out
}

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

func RetiredAnimationCandidates(group string) []string {
	return canonicalAnimationRegistry.RetiredCandidates(group)
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

// Entries returns every registry entry (active and retired) in document order.
func (r *AnimationRegistry) Entries() []AnimationEntry {
	if r == nil {
		return nil
	}
	return r.entries
}

// MapTypeDefault returns the basemap id used when no map type is requested.
func (r *AnimationRegistry) MapTypeDefault() string {
	if r == nil {
		return ""
	}
	return r.mapTypeDefault
}

func containsMotion(pool []string, id string) bool {
	for _, candidate := range pool {
		if candidate == id {
			return true
		}
	}
	return false
}
