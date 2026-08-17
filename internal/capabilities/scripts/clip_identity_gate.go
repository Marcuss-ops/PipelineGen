// Package scriptgeneration — clip_identity_gate.go owns the pure
// scene↔clip identity certification invariant: a scene whose narration names
// one or more PERSON entities must be visually bound to clips whose subject
// identity actually features those persons. It closes the "Tom Holland /
// Adam Sandler" class of error — the wrong clip rendered under a scene that
// narrates someone else — without touching audio/video duration logic.
package scriptgeneration

import (
	"fmt"
	"strings"
)

// ClipIdentityMismatch is one report-only finding from the scene↔clip
// identity gate: a scene whose narration names a person but whose bound clips
// do not feature that person.
type ClipIdentityMismatch struct {
	SceneID      string   `json:"scene_id"`
	Persons      []string `json:"persons"`
	ClipIdentity []string `json:"clip_identity,omitempty"`
	Reason       string   `json:"reason"` // ClipIdentityReasonMissing | ClipIdentityReasonMismatch
}

const (
	// ClipIdentityReasonMissing — the scene names persons but its clips carry
	// no subject identity at all (Speakers/MentionedPeople/Subject empty).
	ClipIdentityReasonMissing = "missing_identity"
	// ClipIdentityReasonMismatch — the clips carry a subject identity, but it
	// does not include one of the narrated persons.
	ClipIdentityReasonMismatch = "mismatch"
)

// AuditSceneClipIdentity is the report-only surface of the scene↔clip
// identity gate. It returns every mismatch without blocking, so callers can
// surface a metric/warning before the gate is promoted to fail-closed. The
// enforcement spelling (ValidateSceneClipIdentity) derives from these same
// findings.
func AuditSceneClipIdentity(result GenerateResult) []ClipIdentityMismatch {
	var mismatches []ClipIdentityMismatch
	for _, scene := range result.Scenes {
		clips := sceneClipsForIdentity(scene)
		if len(clips) == 0 {
			continue
		}
		persons := scenePersonEntities(scene)
		if len(persons) == 0 {
			continue
		}
		identity := clipIdentityItems(clips)
		if len(identity) == 0 {
			mismatches = append(mismatches, ClipIdentityMismatch{SceneID: scene.ID, Persons: persons, Reason: ClipIdentityReasonMissing})
			continue
		}
		for _, person := range persons {
			if !identityMatchesPerson(person, identity) {
				mismatches = append(mismatches, ClipIdentityMismatch{SceneID: scene.ID, Persons: []string{person}, ClipIdentity: identity, Reason: ClipIdentityReasonMismatch})
			}
		}
	}
	return mismatches
}

// ValidateSceneClipIdentity is the fail-closed spelling of the scene↔clip
// identity gate. It fails on the first mismatch so a wrong or missing clip
// identity is never silently rendered. Callers that want report-only
// semantics use AuditSceneClipIdentity and surface the findings themselves.
func ValidateSceneClipIdentity(result GenerateResult) error {
	mismatches := AuditSceneClipIdentity(result)
	if len(mismatches) == 0 {
		return nil
	}
	m := mismatches[0]
	switch m.Reason {
	case ClipIdentityReasonMissing:
		return fmt.Errorf("scene %s narrates persons %v but its clips carry no subject identity (speakers/mentioned_people/subject)", m.SceneID, m.Persons)
	default:
		person := ""
		if len(m.Persons) > 0 {
			person = m.Persons[0]
		}
		return fmt.Errorf("scene %s narrates person %q but its clips' subject identity %v does not include them", m.SceneID, person, m.ClipIdentity)
	}
}

// clipIdentityMismatchSceneIDs returns the scene IDs of the mismatches in
// order, for observability (warning logs).
func clipIdentityMismatchSceneIDs(mismatches []ClipIdentityMismatch) []string {
	ids := make([]string, 0, len(mismatches))
	for _, m := range mismatches {
		ids = append(ids, m.SceneID)
	}
	return ids
}

// sceneClipsForIdentity returns the clips bound to a scene, preferring the
// multi-clip Clips slice and falling back to the single Clip alias — the same
// canonical precedence used across the timeline compiler.
func sceneClipsForIdentity(scene Scene) []*ClipReference {
	clips := scene.Clips
	if len(clips) == 0 && scene.Clip != nil {
		clips = []*ClipReference{scene.Clip}
	}
	return clips
}

// scenePersonEntities extracts the canonical PERSON names a scene narrates
// from its deterministic annotations. PERSON entities are always primary
// (see entity_annotations.go); other kinds are not subjects for this gate.
func scenePersonEntities(scene Scene) []string {
	if scene.Annotations == nil {
		return nil
	}
	var persons []string
	for _, entity := range scene.Annotations.PrimaryEntities {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "PERSON") {
			continue
		}
		name := strings.TrimSpace(entity.CanonicalName)
		if name == "" {
			name = strings.TrimSpace(entity.Text)
		}
		if name != "" {
			persons = append(persons, name)
		}
	}
	return persons
}

// clipIdentityItems flattens the subject identity of the given clips into a
// single list: every Speaker, every MentionedPerson, and each clip's Subject.
func clipIdentityItems(clips []*ClipReference) []string {
	var items []string
	for _, clip := range clips {
		if clip == nil {
			continue
		}
		items = append(items, clip.Speakers...)
		items = append(items, clip.MentionedPeople...)
		if subject := strings.TrimSpace(clip.Subject); subject != "" {
			items = append(items, subject)
		}
	}
	return items
}

// identityMatchesPerson reports whether a scene PERSON name matches any item
// of the clips' subject identity.
func identityMatchesPerson(person string, identity []string) bool {
	p := normalizeIdentityName(person)
	if p == "" {
		return false
	}
	for _, item := range identity {
		if identityNamesOverlap(p, normalizeIdentityName(item)) {
			return true
		}
	}
	return false
}

// normalizeIdentityName lowercases, trims, and collapses whitespace so person
// and subject names compare deterministically.
func normalizeIdentityName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

// identityNamesOverlap reports whether two normalized names refer to the same
// person. Exact equality wins; otherwise all tokens of the shorter name must
// occur, in order, within the longer name. This lets "Pacquiao" match
// "Manny Pacquiao" while rejecting token-prefix false positives such as
// "Tom" vs "Tommy".
func identityNamesOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	af := strings.Fields(a)
	bf := strings.Fields(b)
	if len(af) == 0 || len(bf) == 0 {
		return false
	}
	short, long := af, bf
	if len(af) > len(bf) {
		short, long = bf, af
	}
	i := 0
	for _, token := range long {
		if i < len(short) && token == short[i] {
			i++
		}
	}
	return i == len(short)
}
