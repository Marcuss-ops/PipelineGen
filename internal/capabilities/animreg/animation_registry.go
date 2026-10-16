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

type animationRegistryDocument struct {
	Schema         string            `json:"schema"`
	Version        int               `json:"version"`
	MapTypeAliases map[string]string `json:"map_type_aliases"`
	MapTypeDefault string            `json:"map_type_default"`
	Entries        []AnimationEntry  `json:"entries"`
}

type AnimationRegistry struct {
	entries        []AnimationEntry
	byID           map[string]AnimationEntry
	mapTypeAliases map[string]string
	mapTypeDefault string
}

func loadAnimationRegistry(raw []byte) (*AnimationRegistry, error) {
	var doc animationRegistryDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("animation registry: decode: %w", err)
	}
	if doc.Schema != "pipelinegen.animation-registry.v1" || doc.Version != 1 || len(doc.Entries) == 0 {
		return nil, fmt.Errorf("animation registry: unsupported or empty document")
	}
	r := &AnimationRegistry{entries: doc.Entries, byID: make(map[string]AnimationEntry, len(doc.Entries)), mapTypeAliases: doc.MapTypeAliases, mapTypeDefault: doc.MapTypeDefault}
	for _, e := range doc.Entries {
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
	}
	if r.mapTypeDefault == "" || !r.Selectable(r.mapTypeDefault, "map.basemap") {
		return nil, fmt.Errorf("animation registry: map default %q is not an active basemap", r.mapTypeDefault)
	}
	for alias, id := range r.mapTypeAliases {
		if !r.Selectable(id, "map.basemap") {
			return nil, fmt.Errorf("animation registry: map type alias %q points to unavailable basemap %q", alias, id)
		}
	}
	return r, nil
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
func (r *AnimationRegistry) Candidates(group string) []string {
	if r == nil {
		return nil
	}
	type ranked struct {
		id    string
		order int
	}
	rows := make([]ranked, 0)
	for _, e := range r.entries {
		if !r.Selectable(e.ID, group) {
			continue
		}
		order, ok := e.Order[group]
		if !ok {
			order = int(^uint(0) >> 1)
		}
		rows = append(rows, ranked{id: e.ID, order: order})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].order < rows[j].order })
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.id
	}
	return out
}

func (r *AnimationRegistry) RandomCandidates(group string) []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0)
	for _, e := range r.entries {
		if !r.Selectable(e.ID, group) {
			continue
		}
		for _, randomGroup := range e.Random {
			if randomGroup == group {
				out = append(out, e.ID)
				break
			}
		}
	}
	return out
}

func (r *AnimationRegistry) WordCount(id string) (int, bool) {
	if r == nil {
		return 0, false
	}
	e, ok := r.byID[id]
	if !ok || e.Status != "active" || !e.RendererSupported || e.WordCount < 1 || !containsMotion(e.Families, "phrase.short") {
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
	var out []string
	for _, e := range r.entries {
		if e.Status == "active" && e.RendererSupported && containsMotion(e.Targets, target) {
			out = append(out, e.ID)
		}
	}
	return out
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
