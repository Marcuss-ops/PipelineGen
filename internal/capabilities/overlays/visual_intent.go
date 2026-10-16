// Package overlays — visual_intent.go owns the VisualIntent type and the
// VisualIntentResolver: the deterministic bridge from the semantic index to
// the visual layer.
//
// Pipeline position:
//
//	SemanticItem → VisualIntentResolver → VisualIntent
//	             → DeterministicPresetSampler → (preset, animation)
//
// A VisualIntent answers COSA mostrare (Kind + PresetFamily), QUANDO
// (StartUS + DurationUS), con quale Priorità, e a quale asset è legato. It is
// deliberately independent of the concrete Chronon preset: the family names a
// group of presets, and the actual (preset, animation) selection is deferred
// to the DeterministicPresetSampler (whose candidate lists are the Chronon
// VisualPresetRegistry value space, ADR-029).
//
// The resolver consumes a neutral input (never importing the entities package:
// the dependency direction is entities → overlays, never the reverse), and
// maps the canonical SemanticType spelling to its (kind, family, priority)
// through ONE frozen table — no scattered switches.
package overlays

import (
	"strings"

	capanimreg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
)

// VisualIntentKind is the coarse visual category of an intent (image card vs
// text callout vs number card vs ...). It is coarser than PresetFamily: one
// kind may span several families (e.g. IntentKindImportantNumber covers money,
// number, percentage and statistic). The IntentKind prefix keeps this
// vocabulary distinct from the OverlayKind constants (registry.go).
type VisualIntentKind string

const (
	IntentKindEntityImage      VisualIntentKind = "ENTITY_IMAGE"
	IntentKindImportantText    VisualIntentKind = "IMPORTANT_TEXT"
	IntentKindImportantNumber  VisualIntentKind = "IMPORTANT_NUMBER"
	IntentKindQuoteCard        VisualIntentKind = "QUOTE_CARD"
	IntentKindDateBadge        VisualIntentKind = "DATE_BADGE"
	IntentKindRankingBadge     VisualIntentKind = "RANKING_BADGE"
	IntentKindOrganizationCard VisualIntentKind = "ORGANIZATION_CARD"
	IntentKindLocationCard     VisualIntentKind = "LOCATION_CARD"
	IntentKindTitleCard        VisualIntentKind = "TITLE_CARD"
	IntentKindEventBadge       VisualIntentKind = "EVENT_BADGE"
)

// PresetFamily is the fine-grained sampling family of an intent. It is the
// key the DeterministicPresetSampler seeds on, and the bridge to the
// semantic_role → Chronon preset mapping (SemanticOverlayResolver).
type PresetFamily string

const (
	FamilyPersonImage     PresetFamily = "person_image"
	FamilyOrganization    PresetFamily = "organization"
	FamilyLocation        PresetFamily = "location"
	FamilyDate            PresetFamily = "date"
	FamilyMoney           PresetFamily = "money"
	FamilyNumber          PresetFamily = "number"
	FamilyPercentage      PresetFamily = "percentage"
	FamilyImportantPhrase PresetFamily = "important_phrase"
	FamilyQuote           PresetFamily = "quote"
	FamilyClaim           PresetFamily = "claim"
	FamilyStatistic       PresetFamily = "statistic"
	FamilyRanking         PresetFamily = "ranking"
	FamilyTitle           PresetFamily = "title"
	FamilyEvent           PresetFamily = "event"
	FamilyImageEntity     PresetFamily = "image_entity"
)

// VisualIntent is the resolved visual intention for one semantic item. All
// timing is integer microseconds (never floats), mirroring SemanticItem.
type VisualIntent struct {
	// IntentID is the stable id of this intent (derived from scene+semantic id).
	IntentID string `json:"intent_id"`
	// SemanticID is the id of the source SemanticItem.
	SemanticID string `json:"semantic_id"`
	// SceneID is the id of the scene the item belongs to.
	SceneID string `json:"scene_id"`
	// Kind is the coarse visual category (ENTITY_IMAGE, IMPORTANT_TEXT, ...).
	Kind VisualIntentKind `json:"kind"`
	// StartUS is the item's start in integer microseconds.
	StartUS int64 `json:"start_us"`
	// DurationUS is the on-screen duration in integer microseconds.
	DurationUS int64 `json:"duration_us"`
	// Priority is the editorial priority used by the future VisualScheduler
	// (higher wins: IMPORTANT_PHRASE=100 ... ORGANIZATION=50).
	Priority int `json:"priority"`
	// PresetFamily names the sampling family (person_image, money, ...).
	PresetFamily PresetFamily `json:"preset_family"`
	// AssetID is the optional resolved asset this intent renders (populated by
	// the future Entity Media Resolver).
	AssetID string `json:"asset_id,omitempty"`
	// EntityID is the optional canonical entity id this intent is about
	// (e.g. "person:floyd-mayweather-jr"). It is the cooldown key used by the
	// VisualScheduler; empty for non-entity intents.
	EntityID string `json:"entity_id,omitempty"`
}

// VisualIntentInput is the neutral projection of a SemanticItem the resolver
// consumes. It carries the semantic type as a plain string (the canonical
// SemanticType spelling) so overlays never imports entities.
type VisualIntentInput struct {
	SemanticID string
	SceneID    string
	// Type is the canonical SemanticType spelling ("PERSON", "MONEY", ...).
	Type    string
	StartUS int64
	EndUS   int64
	// AssetID is the optional resolved asset id (media resolver, upstream).
	AssetID string
	// EntityID is the optional canonical entity id (cooldown key, upstream).
	EntityID string
}

// visualIntentEntry is one frozen semantic-type → (kind, family, priority)
// editorial mapping.
type visualIntentEntry struct {
	Kind     VisualIntentKind
	Family   PresetFamily
	Priority int
}

// visualIntentTable is the typed projection of the registry-owned
// SemanticType → visual-intent table (`visual_intents` in
// internal/capabilities/animreg/animation_registry.v1.json): the same JSON that
// owns the preset families owns the editorial priority of every semantic type.
// Priorities follow the editorial table (IMPORTANT_PHRASE=100, MONEY=90,
// PERSON=80, QUOTE=75, DATE=60, ORGANIZATION=50); value artifacts sit between
// phrase and person, badges near quote, lower-thirds below.
var visualIntentTable = loadVisualIntentTable()

// loadVisualIntentTable projects the registry table into the typed vocabulary.
// A registry with an empty table is a load failure upstream (animreg rejects
// it), so an empty table here means the JSON was bypassed: fail closed instead
// of silently inventing no intent for every semantic type.
func loadVisualIntentTable() map[string]visualIntentEntry {
	specs := capanimreg.VisualIntents()
	table := make(map[string]visualIntentEntry, len(specs))
	for semanticType, spec := range specs {
		table[semanticType] = visualIntentEntry{
			Kind:     VisualIntentKind(spec.Kind),
			Family:   PresetFamily(spec.Family),
			Priority: spec.Priority,
		}
	}
	if len(table) == 0 {
		panic("overlays: animation registry defines no visual intents")
	}
	return table
}

// VisualIntentResolver maps a semantic item to its visual intent. It is
// stateless and safe for concurrent use.
type VisualIntentResolver struct{}

// Resolve returns the VisualIntent for the input, or ok=false when the
// semantic type is not part of the canonical vocabulary (fail-closed: an
// intent is never invented for an unknown type). Timing is carried verbatim
// (DurationUS = EndUS - StartUS); the resolver never re-derives or estimates
// timing. The intent id is deterministic: "intent-" + scene + "-" + semantic.
func (VisualIntentResolver) Resolve(in VisualIntentInput) (VisualIntent, bool) {
	entry, ok := visualIntentTable[strings.ToUpper(strings.TrimSpace(in.Type))]
	if !ok {
		return VisualIntent{}, false
	}
	return VisualIntent{
		IntentID:     "intent-" + in.SceneID + "-" + in.SemanticID,
		SemanticID:   in.SemanticID,
		SceneID:      in.SceneID,
		Kind:         entry.Kind,
		StartUS:      in.StartUS,
		DurationUS:   in.EndUS - in.StartUS,
		Priority:     entry.Priority,
		PresetFamily: entry.Family,
		AssetID:      in.AssetID,
		EntityID:     in.EntityID,
	}, true
}

// DefaultVisualIntentResolver is the process-wide resolver. Every call site
// resolves through this single instance so the type→intent mapping is uniform.
var DefaultVisualIntentResolver = VisualIntentResolver{}
