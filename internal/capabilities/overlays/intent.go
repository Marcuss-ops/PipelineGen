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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	Name string `json:"name"`
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
	if strings.TrimSpace(i.Entity.Type) == "" {
		return fmt.Errorf("overlay intent: entity type is required")
	}
	if strings.TrimSpace(i.Entity.CanonicalName) == "" {
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
		Kind        string
		TemplateID  string
		PayloadName string
	}{
		i.IntentID, i.SceneID, i.SceneIndex,
		i.Entity, i.Kind, i.TemplateID, i.Payload.Name,
	}
	b, _ := encodeJSON(flat)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
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
	var intents []OverlayIntent
	for _, scene := range scenes {
		for _, entity := range scene.Entities {
			name := strings.TrimSpace(entity.Name)
			etype := strings.TrimSpace(entity.Type)
			if name == "" || etype == "" {
				continue
			}
			kind := entityOverlayKindForIntent(etype)
			entry, err := registry.Resolve(string(kind))
			if err != nil {
				continue // unknown kind → skip, never invent
			}
			intent := OverlayIntent{
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
			}
			intents = append(intents, intent)
		}
	}
	return intents
}

// entityOverlayKindForIntent maps an NLP entity type to the canonical
// overlay kind. This is the same mapping used by the entity overlay
// resolver (entities/overlay_resolver.go) — the single owner of this
// decision. Kept here as a pure function to avoid importing the entities
// package.
func entityOverlayKindForIntent(entityType string) OverlayKind {
	switch strings.ToUpper(strings.TrimSpace(entityType)) {
	case "PERSON":
		return KindEntityCard
	case "ORG", "ORGANIZATION":
		return KindOrganization
	case "GPE", "PLACE", "LOCATION", "CITY", "COUNTRY":
		return KindLocation
	case "NUMBER", "NUM":
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
