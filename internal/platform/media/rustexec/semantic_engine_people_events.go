package rustexec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	semanticscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
)

func projectSemanticPersonRoles(source string, identities []semanticEngineIdentity, roles []semanticEnginePersonRole) ([]semanticscript.SemanticPersonRole, error) {
	identityIDs := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		identityIDs[identity.ID] = struct{}{}
	}
	out := make([]semanticscript.SemanticPersonRole, 0, len(roles))
	for _, role := range roles {
		if _, ok := identityIDs[role.PersonID]; !ok || role.ID == "" || role.RoleCode == "" || role.RoleSurface == "" ||
			!groundedSpan(source, role.StartByte, role.EndByte, role.RoleSurface) || !evidenceContainsSpan(source, role.Evidence, role.StartByte, role.EndByte) {
			return nil, fmt.Errorf("role %q lacks a grounded person or exact role span", role.ID)
		}
		if role.Gender != "masculine" && role.Gender != "feminine" && role.Gender != "unknown" {
			return nil, fmt.Errorf("role %q has invalid gender %q", role.ID, role.Gender)
		}
		if !oneOf(role.Rank, "head", "deputy", "member", "former", "acting") ||
			!oneOf(role.Tense, "current", "former", "appointed", "resigned", "unknown") ||
			!oneOf(role.Status, "asserted", "negated", "uncertain") {
			return nil, fmt.Errorf("role %q has invalid rank, tense, or status", role.ID)
		}
		if role.OrganizationID != nil {
			if _, ok := identityIDs[*role.OrganizationID]; !ok {
				return nil, fmt.Errorf("role %q references missing organization %q", role.ID, *role.OrganizationID)
			}
		}
		if role.PlaceID != nil {
			if _, ok := identityIDs[*role.PlaceID]; !ok {
				return nil, fmt.Errorf("role %q references missing place identity %q", role.ID, *role.PlaceID)
			}
		}
		var temporal *semanticscript.SemanticRoleTemporal
		if role.Temporal != nil {
			if !groundedSpan(source, role.Temporal.StartByte, role.Temporal.EndByte, role.Temporal.Surface) {
				return nil, fmt.Errorf("role %q has ungrounded temporal evidence", role.ID)
			}
			temporal = &semanticscript.SemanticRoleTemporal{
				Surface: role.Temporal.Surface, NormalizedDate: derefString(role.Temporal.NormalizedDate),
				Precision: role.Temporal.Precision, Kind: role.Temporal.Kind, Era: derefString(role.Temporal.Era),
				Start: role.Temporal.StartByte, End: role.Temporal.EndByte, Ambiguous: role.Temporal.Ambiguous,
			}
		}
		out = append(out, semanticscript.SemanticPersonRole{
			ID: role.ID, PersonID: role.PersonID, RoleCode: role.RoleCode, RoleSurface: role.RoleSurface,
			Gender: role.Gender, Rank: role.Rank, OrganizationID: derefString(role.OrganizationID),
			PlaceID: derefString(role.PlaceID), Tense: role.Tense, Status: role.Status,
			Evidence: role.Evidence, Start: role.StartByte, End: role.EndByte,
			SentenceIndex: role.SentenceIndex, Temporal: temporal,
		})
	}
	return out, nil
}

func projectSemanticPersonAges(source string, identities []semanticEngineIdentity, ages []semanticEnginePersonAgeFact) ([]semanticscript.SemanticPersonAgeFact, error) {
	identityIDs := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		identityIDs[identity.ID] = struct{}{}
	}
	out := make([]semanticscript.SemanticPersonAgeFact, 0, len(ages))
	for _, age := range ages {
		_, identityOK := identityIDs[age.PersonID]
		surface := sourceSlice(source, age.StartByte, age.EndByte)
		value, parseErr := strconv.Atoi(surface)
		validValue := parseErr == nil && value == age.Age
		if parseErr != nil {
			validValue = spelledAgeSpan(surface) && hasAgeContext(source, age.StartByte, age.EndByte)
		}
		if !identityOK || age.Age < 0 || age.Age > 120 || !validValue ||
			!evidenceContainsSpan(source, age.Evidence, age.StartByte, age.EndByte) {
			return nil, fmt.Errorf("age fact for %q is ungrounded or invalid", age.PersonID)
		}
		out = append(out, semanticscript.SemanticPersonAgeFact{
			PersonID: age.PersonID, Age: age.Age, AsOf: derefString(age.AsOf),
			DerivedBirthYear: age.DerivedBirthYear, Start: age.StartByte, End: age.EndByte,
			SentenceIndex: age.SentenceIndex, Evidence: age.Evidence,
		})
	}
	return out, nil
}

func spelledAgeSpan(surface string) bool {
	if surface == "" {
		return false
	}
	for _, r := range surface {
		if !unicode.IsLetter(r) && !unicode.IsMark(r) && !unicode.IsSpace(r) && r != '-' && r != '\u2011' {
			return false
		}
	}
	return true
}

func hasAgeContext(source string, start, end int) bool {
	// The typed age is resolved by the Rust worker; at this boundary we still
	// require the exact word span to sit next to an explicit age unit or cue.
	lo, hi := max(0, start-64), min(len(source), end+64)
	context := strings.ToLower(source[lo:hi])
	for _, marker := range []string{"anni", "anno", "years old", "year old", "years", "aged", "age", "ans", "jahre", "jahr", "años", "año", "anos", "ano", "лет", "года", "год", "yaşında", "yasında", "tahun", "umurnya"} {
		if strings.Contains(context, marker) {
			return true
		}
	}
	return false
}

func projectSemanticPersonRelations(source string, identities []semanticEngineIdentity, relations []semanticEnginePersonRelation) ([]semanticscript.SemanticPersonRelation, error) {
	identityIDs := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		identityIDs[identity.ID] = struct{}{}
	}
	out := make([]semanticscript.SemanticPersonRelation, 0, len(relations))
	for _, relation := range relations {
		_, fromOK := identityIDs[relation.FromID]
		_, toOK := identityIDs[relation.ToID]
		if !fromOK || !toOK || relation.FromID == relation.ToID || relation.Kind == "" ||
			!oneOf(relation.Status, "asserted", "negated", "uncertain") ||
			!evidenceContainsSpan(source, relation.Evidence, relation.StartByte, relation.EndByte) {
			return nil, fmt.Errorf("person relation %q -> %q is ungrounded or invalid", relation.FromID, relation.ToID)
		}
		out = append(out, semanticscript.SemanticPersonRelation{
			FromID: relation.FromID, ToID: relation.ToID, Kind: relation.Kind, Status: relation.Status,
			Start: relation.StartByte, End: relation.EndByte, SentenceIndex: relation.SentenceIndex,
			Evidence: relation.Evidence,
		})
	}
	return out, nil
}

func projectSemanticEvents(texts map[string]string, scenes []semanticEngineSceneManifest, entities []semanticEngineLinkedEntity, events []semanticEngineEventFrame) ([]semanticscript.SemanticEventFrame, error) {
	sceneByID := make(map[string]semanticEngineSceneManifest, len(scenes))
	for _, scene := range scenes {
		sceneByID[scene.SceneID] = scene
	}
	entityIDs := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		entityIDs[entity.LocalID] = struct{}{}
	}
	out := make([]semanticscript.SemanticEventFrame, 0, len(events))
	seen := make(map[string]semanticEngineEventFrame, len(events))
	for _, event := range events {
		if event.ID == "" || event.EventType == "" || event.Trigger == "" ||
			!oneOf(event.Status, "asserted", "negated", "uncertain") ||
			len(event.Evidence) == 0 {
			return nil, fmt.Errorf("event %q has an invalid trigger or status", event.ID)
		}
		if previous, exists := seen[event.ID]; exists {
			// The Rust worker can classify the same grounded event under more
			// than one event_type (for example "won" is offered as both
			// "election" and "match_competition_result"). That is one event
			// with two candidate labels, not two facts: keep the first label
			// and collapse the repeat. A duplicate whose grounding actually
			// differs is a real identity conflict and stays fail-closed.
			if !sameSemanticEventGrounding(previous, event) {
				return nil, fmt.Errorf("duplicate event id %q with conflicting frames: first=%s second=%s", event.ID, semanticEventFrameJSON(previous), semanticEventFrameJSON(event))
			}
			continue
		}
		seen[event.ID] = event
		evidence := make([]semanticscript.SemanticEventEvidence, 0, len(event.Evidence))
		mentionedScenes := make(map[string]struct{}, len(event.Evidence))
		triggerGrounded := false
		for _, item := range event.Evidence {
			text, ok := texts[item.SceneID]
			if !ok || !groundedSpan(text, item.StartByte, item.EndByte, item.Text) {
				return nil, fmt.Errorf("event %q has ungrounded evidence for scene %q", event.ID, item.SceneID)
			}
			mentionedScenes[item.SceneID] = struct{}{}
			triggerGrounded = triggerGrounded || groundedSpan(text, event.TriggerStartByte, event.TriggerEndByte, event.Trigger)
			evidence = append(evidence, semanticscript.SemanticEventEvidence{SceneID: item.SceneID, Text: item.Text, Start: item.StartByte, End: item.EndByte})
		}
		if !triggerGrounded {
			return nil, fmt.Errorf("event %q trigger is not grounded in its evidence scenes", event.ID)
		}
		for _, sceneID := range event.SceneIDs {
			if _, ok := sceneByID[sceneID]; !ok {
				return nil, fmt.Errorf("event %q references unknown scene %q", event.ID, sceneID)
			}
			if _, ok := mentionedScenes[sceneID]; !ok {
				return nil, fmt.Errorf("event %q lacks evidence for scene %q", event.ID, sceneID)
			}
		}
		if len(event.SceneIDs) == 0 || len(mentionedScenes) != len(event.SceneIDs) {
			return nil, fmt.Errorf("event %q scene ids do not match its evidence", event.ID)
		}
		participants := make([]semanticscript.SemanticEventParticipant, 0, len(event.Participants))
		for _, participant := range event.Participants {
			if participant.Role == "" || participant.EntityID == "" {
				return nil, fmt.Errorf("event %q has an incomplete participant", event.ID)
			}
			if _, ok := entityIDs[participant.EntityID]; !ok {
				return nil, fmt.Errorf("event %q references missing participant %q", event.ID, participant.EntityID)
			}
			participants = append(participants, semanticscript.SemanticEventParticipant{Role: participant.Role, EntityID: participant.EntityID})
		}
		var placeID string
		if event.PlaceID != nil {
			placeID = *event.PlaceID
			found := false
			for sceneID := range mentionedScenes {
				for _, place := range sceneByID[sceneID].Places {
					found = found || place.ID == placeID
				}
			}
			if !found {
				return nil, fmt.Errorf("event %q references missing place %q", event.ID, placeID)
			}
		}
		var eventTime *semanticscript.SemanticEventTime
		if event.Time != nil {
			eventTime = &semanticscript.SemanticEventTime{
				FactIndex: event.Time.FactIndex, Date: derefString(event.Time.NormalizedDate), Derived: event.Time.Derived,
			}
		}
		out = append(out, semanticscript.SemanticEventFrame{
			ID: event.ID, EventType: event.EventType, Trigger: event.Trigger, Status: event.Status, PlaceID: placeID,
			TriggerStart: event.TriggerStartByte, TriggerEnd: event.TriggerEndByte,
			SceneIDs: append([]string(nil), event.SceneIDs...), Participants: participants, Evidence: evidence,
			ConfidenceBasis: append([]string(nil), event.ConfidenceBasis...),
			SourceFactIDs:   append([]string(nil), event.SourceFactIDs...), Time: eventTime,
		})
	}
	return out, nil
}

// sameSemanticEventGrounding reports whether two frames describe the same
// grounded event. event_type is deliberately excluded: it is a classification
// label, and the engine may emit the same span/evidence with more than one
// candidate label. Every field that proves WHAT happened and WHERE it is
// grounded must match.
func sameSemanticEventGrounding(a, b semanticEngineEventFrame) bool {
	return a.Trigger == b.Trigger &&
		a.TriggerStartByte == b.TriggerStartByte &&
		a.TriggerEndByte == b.TriggerEndByte &&
		a.Status == b.Status &&
		reflect.DeepEqual(a.Participants, b.Participants) &&
		reflect.DeepEqual(a.PlaceID, b.PlaceID) &&
		reflect.DeepEqual(a.Time, b.Time) &&
		reflect.DeepEqual(a.SceneIDs, b.SceneIDs) &&
		reflect.DeepEqual(a.Evidence, b.Evidence)
}

// semanticEventFrameJSON renders a conflicting frame for the operator without
// letting an oversized evidence list swamp the log line.
func semanticEventFrameJSON(frame semanticEngineEventFrame) string {
	raw, err := json.Marshal(frame)
	if err != nil {
		return "<unencodable>"
	}
	const limit = 3000
	if len(raw) > limit {
		return string(raw[:limit]) + "..."
	}
	return string(raw)
}

func evidenceContainsSpan(source, evidence string, start, end int) bool {
	if evidence == "" || start < 0 || end <= start || end > len(source) || !groundedSpan(source, start, end, source[start:end]) {
		return false
	}
	for offset := 0; ; {
		index := strings.Index(source[offset:], evidence)
		if index < 0 {
			return false
		}
		evidenceStart := offset + index
		evidenceEnd := evidenceStart + len(evidence)
		if start >= evidenceStart && end <= evidenceEnd {
			return true
		}
		offset = evidenceStart + 1
		if offset >= len(source) {
			return false
		}
	}
}

func sourceSlice(source string, start, end int) string {
	if start < 0 || end <= start || end > len(source) {
		return ""
	}
	return source[start:end]
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
