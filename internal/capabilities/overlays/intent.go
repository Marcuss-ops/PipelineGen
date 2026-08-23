// Package overlays — intent.go owns the OverlayIntent type and the
// EntityOverlayPlanner: the deterministic pre-timing binding of an entity
// occurrence to its resolved template. OverlayIntents are created IMMEDIATELY
// after entity extraction — before TTS, before CanonicalTimeline, before any
// timing is available — so the overlay.prepare path can start template
// resolution and asset prefetch in parallel with audio synthesis.
//
// Ownership split:
//
//	PipelineGen (EntityOverlayPlanner) → owns the intent (what + which template)
//	Chronon                           → owns the pixels (how to render)
//	Timing                            → owns the when (start_us / duration_us)
//
// The OverlayIntent carries entity identity + resolved template_id + payload;
// timing is absent (TimingState = PENDING) until the CanonicalTimeline is
// frozen, at which point the caller promotes the intent into a fully-timed
// OverlayPlan item.
package overlays

import (
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"sort"
	"strings"
)

// OverlayIntentVersion is the schema version of the OverlayIntent. Bump it
// when the JSON shape or semantics change so persisted intents cannot be
// silently misread.
const OverlayIntentVersion = 1

// TimingState declares whether an OverlayIntent has been anchored to the
// final combined timeline. PENDING means the intent was created from entity
// extraction but no timing is available yet; FROZEN means the caller has
// bound start_us / duration_us from the CanonicalTimeline.
type TimingState string

// IntentSource identifies the canonical scene surface that produced an
// intent. It is deliberately semantic and contains no timing information.
type IntentSource string

const (
	IntentSourceEntity          IntentSource = "entity"
	IntentSourceImportantPhrase IntentSource = "important_phrase"
	IntentSourceImportantWord   IntentSource = "important_word"
	IntentSourceEntityImage     IntentSource = "entity_image"
)

const (
	// TimingStatePending means the intent carries no timing. It is the
	// state immediately after entity extraction.
	TimingStatePending TimingState = "PENDING"
	// TimingStateFrozen means the intent has been promoted into a timed
	// overlay (start_us / duration_us are authoritative).
	TimingStateFrozen TimingState = "FROZEN"
)

// EntityBinding is the neutral entity identity carried by an OverlayIntent.
// It carries the NLP entity type and the canonical (normalized) name — never
// the raw extracted text.
type EntityBinding struct {
	Type          string `json:"type"`
	CanonicalName string `json:"canonical_name"`
}

// IntentPayload is the template-specific payload the renderer needs to
// materialize the overlay. For entity cards this is the displayed name;
// for other templates it may carry additional fields.
type IntentPayload struct {
	Name      string            `json:"name,omitempty"`
	Text      string            `json:"text,omitempty"`
	AssetRefs []OverlayAssetRef `json:"asset_refs,omitempty"`
}

// OverlayIntent is the canonical pre-timing entity→template binding. One
// OverlayIntent exists per entity occurrence that should produce a visual
// overlay. It is the SSOT for WHAT appears and WHICH template renders it;
// the WHEN (start_us / duration_us) is added later from the CanonicalTimeline.
//
// OverlayIntents are deterministic: the same scenes + entities + registry
// always produce the same intents in the same order.
type OverlayIntent struct {
	Version     int           `json:"version"`
	IntentID    string        `json:"intent_id"`
	SceneID     string        `json:"scene_id"`
	SceneIndex  int           `json:"scene_index"`
	Entity      EntityBinding `json:"entity"`
	Source      IntentSource  `json:"source"`
	SourceID    string        `json:"source_id,omitempty"`
	SourceText  string        `json:"source_text,omitempty"`
	Kind        string        `json:"kind"`
	TemplateID  string        `json:"template_id"`
	Payload     IntentPayload `json:"payload"`
	TimingState TimingState   `json:"timing_state"`
}

// Validate checks structural invariants on a single intent.
func (i OverlayIntent) Validate() error {
	if i.Version != OverlayIntentVersion {
		return fmt.Errorf("overlay intent: unsupported version %d", i.Version)
	}
	if strings.TrimSpace(i.IntentID) == "" {
		return fmt.Errorf("overlay intent: intent_id is required")
	}
	if strings.TrimSpace(i.SceneID) == "" {
		return fmt.Errorf("overlay intent: scene_id is required")
	}
	source := i.Source
	// Version-1 intents written before the generic insight sources were
	// introduced are entity intents by shape; keep them readable while all
	// newly planned intents carry an explicit source.
	if source == "" && strings.TrimSpace(i.Entity.CanonicalName) != "" {
		source = IntentSourceEntity
	}
	if source == "" {
		return fmt.Errorf("overlay intent: source is required")
	}
	if strings.TrimSpace(i.Entity.Type) == "" && strings.TrimSpace(i.SourceText) == "" {
		return fmt.Errorf("overlay intent: entity or source_text is required")
	}
	if source == IntentSourceEntity && strings.TrimSpace(i.Entity.CanonicalName) == "" {
		return fmt.Errorf("overlay intent: entity canonical_name is required")
	}
	if strings.TrimSpace(i.TemplateID) == "" {
		return fmt.Errorf("overlay intent: template_id is required")
	}
	if i.TimingState != TimingStatePending && i.TimingState != TimingStateFrozen {
		return fmt.Errorf("overlay intent: invalid timing_state %q", i.TimingState)
	}
	return nil
}

// Fingerprint computes a deterministic SHA-256 of the intent's content
// (excluding timing state, which is mutable). Two intents with the same
// entity + scene + template produce the same fingerprint.
func (i OverlayIntent) Fingerprint() string {
	flat := struct {
		IntentID    string
		SceneID     string
		SceneIndex  int
		Entity      EntityBinding
		Source      IntentSource
		SourceID    string
		SourceText  string
		Kind        string
		TemplateID  string
		PayloadName string
		PayloadText string
	}{
		i.IntentID, i.SceneID, i.SceneIndex,
		i.Entity, i.Source, i.SourceID, i.SourceText, i.Kind, i.TemplateID, i.Payload.Name, i.Payload.Text,
	}
	b, _ := encodeJSON(flat)
	h := digest.SHA256Bytes(b)
	return h
}

// ── EntityOverlayPlanner ────────────────────────────────────────────

// EntityOverlayInput is one neutral entity occurrence the planner consumes.
// It is the caller's projection of a domain entity (AnnotatedEntity,
// EntitySource, EntityOccurrence) onto the planner's interface. The
// planner does not import any domain or entity package — it works
// exclusively through this neutral type.
type EntityOverlayInput struct {
	Name       string
	Type       string
	Confidence float64
}

// SceneEntityInput is one scene's entity bundle for the planner.
type SceneEntityInput struct {
	SceneID    string
	SceneIndex int
	Entities   []EntityOverlayInput
	Phrases    []OverlayAnnotationInput
	Words      []OverlayAnnotationInput
	Images     []EntityImageOverlayInput
}

// OverlayAnnotationInput is a scene-local important phrase/word projected
// from canonical insights. Timing is intentionally absent at this phase.
type OverlayAnnotationInput struct {
	ID   string
	Text string
}

// EntityImageOverlayInput binds an already-resolved image asset to an entity.
// Image search/selection remains PipelineGen-owned; renderers only consume
// this deterministic asset reference.
type EntityImageOverlayInput struct {
	EntityName string
	AssetID    string
	URL        string
	SHA256     string
}

// PlanOverlayIntents creates OverlayIntents from per-scene entity bundles.
// It is deterministic: same input → same intents in the same order. The
// template_id is resolved through the provided registry; entities that map
// to an unknown kind are skipped (never invented).
//
// Every returned intent has TimingState = PENDING because no timing is
// available at this point. The caller is responsible for promoting timing
// after the CanonicalTimeline is frozen.
func PlanOverlayIntents(scenes []SceneEntityInput, registry *ChrononOverlayRegistry) []OverlayIntent {
	if len(scenes) == 0 || registry == nil {
		return nil
	}
	resolver := NewTemplateResolver(registry)
	var intents []OverlayIntent
	for _, scene := range scenes {
		for _, entity := range scene.Entities {
			if intent, ok := bindEntityIntent(scene, entity, resolver); ok {
				intents = append(intents, intent)
			}
		}
		entityNames := make(map[string]struct{}, len(scene.Entities))
		for _, entity := range scene.Entities {
			entityNames[strings.ToLower(strings.TrimSpace(entity.Name))] = struct{}{}
		}
		appendAnnotation := func(source IntentSource, kind OverlayKind, annotation OverlayAnnotationInput) {
			text := strings.TrimSpace(annotation.Text)
			if text == "" {
				return
			}
			entry, err := registry.Resolve(string(kind))
			if err != nil {
				return
			}
			id := strings.TrimSpace(annotation.ID)
			if id == "" {
				id = text
			}
			intents = append(intents, OverlayIntent{
				Version: OverlayIntentVersion, IntentID: intentID(scene.SceneID, string(source)+"-"+id),
				SceneID: scene.SceneID, SceneIndex: scene.SceneIndex, Kind: string(kind),
				TemplateID: entry.Template, Source: source, SourceID: id, SourceText: text,
				Payload: IntentPayload{Text: text}, TimingState: TimingStatePending,
			})
		}
		for _, phrase := range scene.Phrases {
			appendAnnotation(IntentSourceImportantPhrase, KindImportantPhrase, phrase)
		}
		for _, word := range scene.Words {
			if _, duplicate := entityNames[strings.ToLower(strings.TrimSpace(word.Text))]; duplicate {
				continue
			}
			appendAnnotation(IntentSourceImportantWord, KindImportantWord, word)
		}
		for _, image := range scene.Images {
			if strings.TrimSpace(image.EntityName) == "" || strings.TrimSpace(image.AssetID) == "" || strings.TrimSpace(image.SHA256) == "" {
				continue
			}
			entry, err := registry.Resolve(string(KindEntityImage))
			if err != nil {
				continue
			}
			intents = append(intents, OverlayIntent{
				Version: OverlayIntentVersion, IntentID: intentID(scene.SceneID, "image-"+image.EntityName),
				SceneID: scene.SceneID, SceneIndex: scene.SceneIndex, Kind: string(KindEntityImage),
				TemplateID: entry.Template, Source: IntentSourceEntityImage, SourceID: image.EntityName,
				SourceText: image.EntityName, Entity: EntityBinding{CanonicalName: image.EntityName},
				Payload:     IntentPayload{Text: image.EntityName, AssetRefs: []OverlayAssetRef{{AssetID: image.AssetID, URL: image.URL, SHA256: image.SHA256}}},
				TimingState: TimingStatePending,
			})
		}
	}
	return intents
}

// EntityTypeToKind is the SINGLE canonical owner of the NLP entity-type →
// overlay-kind translation. Every planner, the entity overlay resolver and
// the overlay-plan compiler map an entity's NLP type through this one
// function instead of scattering switch statements (switch PERSON, switch
// ORG, …) across packages. The kind → template/renderer mapping is owned by
// ChrononOverlayRegistry; this is only the type-vocabulary bridge.
//
//	PERSON → entity_card, ORG/ORGANIZATION → organization,
//	GPE/PLACE/LOCATION/CITY/COUNTRY → location,
//	NUMBER/NUM/CARDINAL/ORDINAL/MONEY/PERCENT → number,
//	QUOTE → quote, PRODUCT → product, LOGO → logo, everything else → concept.
func EntityTypeToKind(entityType string) OverlayKind {
	switch strings.ToUpper(strings.TrimSpace(entityType)) {
	case "PERSON":
		return KindEntityCard
	case "ORG", "ORGANIZATION":
		return KindOrganization
	case "GPE", "PLACE", "LOCATION", "CITY", "COUNTRY":
		return KindLocation
	case "NUMBER", "NUM", "CARDINAL", "ORDINAL", "MONEY", "PERCENT":
		return KindNumber
	case "QUOTE":
		return KindQuote
	case "PRODUCT":
		return KindProduct
	case "LOGO":
		return KindLogo
	default:
		return KindConcept
	}
}

// TemplateResolver resolves an entity's NLP type to its canonical
// template_id through the single entity-type → kind → template chain
// (EntityTypeToKind + ChrononOverlayRegistry). It is the deterministic
// surface the EntityOverlayPlanner uses, so the template_id bound to an
// OverlayIntent is always the registry's canonical template — never a
// hard-coded switch.
type TemplateResolver struct {
	registry *ChrononOverlayRegistry
}

// NewTemplateResolver builds a resolver over the given registry; nil falls
// back to the process-wide DefaultChrononOverlayRegistry.
func NewTemplateResolver(registry *ChrononOverlayRegistry) *TemplateResolver {
	if registry == nil {
		registry = DefaultChrononOverlayRegistry
	}
	return &TemplateResolver{registry: registry}
}

// Resolve maps an entity type to its canonical template_id.
func (t *TemplateResolver) Resolve(entityType string) (string, error) {
	e, err := t.ResolveEntry(entityType)
	if err != nil {
		return "", err
	}
	return e.Template, nil
}

// ResolveEntry maps an entity type to its full canonical overlay entry
// (template + renderer + policies) through EntityTypeToKind + the registry.
func (t *TemplateResolver) ResolveEntry(entityType string) (OverlayEntry, error) {
	if t == nil || t.registry == nil {
		return OverlayEntry{}, fmt.Errorf("template resolver: %w: %q", ErrUnknownOverlayKind, entityType)
	}
	kind := EntityTypeToKind(entityType)
	return t.registry.Resolve(string(kind))
}

// EntityOverlayPlanner is the deterministic pre-timing binding of per-scene
// entities to their resolved templates. It owns OverlayIntent creation;
// Chronon owns the pixels; timing owns the when. The template_id is always
// resolved through the TemplateResolver (single registry owner) so the same
// entity type always binds to the same template — never duplicated switches.
type EntityOverlayPlanner struct {
	resolver *TemplateResolver
}

// NewEntityOverlayPlanner builds a planner over the given resolver; nil
// falls back to the default registry.
func NewEntityOverlayPlanner(resolver *TemplateResolver) *EntityOverlayPlanner {
	if resolver == nil {
		resolver = NewTemplateResolver(nil)
	}
	return &EntityOverlayPlanner{resolver: resolver}
}

// Plan creates the PENDING entity OverlayIntents for the given scenes. It
// is deterministic: same input → same intents in the same order. An entity
// whose kind cannot be resolved is skipped (never invented). Timing is
// absent (PENDING) because no timing is available at this point; the caller
// promotes timing after the CanonicalTimeline is frozen.
func (p *EntityOverlayPlanner) Plan(scenes []SceneEntityInput) []OverlayIntent {
	if len(scenes) == 0 || p == nil || p.resolver == nil {
		return nil
	}
	var intents []OverlayIntent
	for _, scene := range scenes {
		for _, entity := range scene.Entities {
			if intent, ok := bindEntityIntent(scene, entity, p.resolver); ok {
				intents = append(intents, intent)
			}
		}
	}
	return intents
}

// bindEntityIntent resolves one entity occurrence to its canonical template
// and builds the PENDING OverlayIntent. ok=false when the entity name/type
// is empty or its kind cannot be resolved — an overlay is never invented.
func bindEntityIntent(scene SceneEntityInput, entity EntityOverlayInput, resolver *TemplateResolver) (OverlayIntent, bool) {
	name := strings.TrimSpace(entity.Name)
	etype := strings.TrimSpace(entity.Type)
	if name == "" || etype == "" || resolver == nil {
		return OverlayIntent{}, false
	}
	kind := EntityTypeToKind(etype)
	entry, err := resolver.ResolveEntry(etype)
	if err != nil {
		return OverlayIntent{}, false // unknown kind → skip, never invent
	}
	return OverlayIntent{
		Version:    OverlayIntentVersion,
		IntentID:   intentID(scene.SceneID, name),
		SceneID:    scene.SceneID,
		SceneIndex: scene.SceneIndex,
		Entity: EntityBinding{
			Type:          etype,
			CanonicalName: name,
		},
		Kind:        string(kind),
		TemplateID:  entry.Template,
		Payload:     IntentPayload{Name: name},
		TimingState: TimingStatePending,
		Source:      IntentSourceEntity,
	}, true
}

// intentID derives the deterministic, collision-free intent id for one
// entity occurrence: "intent-" + scene id + "-" + normalized name.
func intentID(sceneID, name string) string {
	var b strings.Builder
	b.WriteString("intent-")
	for _, r := range strings.ToLower(sceneID + "-" + name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 7 { // after "intent-" prefix
			b.WriteByte('-')
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// SortIntents sorts intents deterministically: by scene_index, then by
// intent_id. This ensures the same entity set always produces the same
// ordered slice.
func SortIntents(intents []OverlayIntent) {
	sort.Slice(intents, func(i, j int) bool {
		if intents[i].SceneIndex != intents[j].SceneIndex {
			return intents[i].SceneIndex < intents[j].SceneIndex
		}
		return intents[i].IntentID < intents[j].IntentID
	})
}

// encodeJSON is a deterministic JSON encoder (stable field order via
// struct). Used for fingerprinting only.
func encodeJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}
