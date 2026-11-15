package rustexec

import (
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func projectSemanticLinkedEntities(texts map[string]string, entities []semanticEngineLinkedEntity) ([]scriptgen.SemanticIdentity, error) {
	projected := make([]scriptgen.SemanticIdentity, 0, len(entities))
	seenIDs := make(map[string]struct{}, len(entities))
	seenSurfaceKeys := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		id := strings.TrimSpace(entity.LocalID)
		if id == "" || strings.TrimSpace(entity.SurfaceKey) == "" || strings.TrimSpace(entity.CanonicalSurface) == "" || strings.TrimSpace(entity.EntityType) == "" {
			return nil, fmt.Errorf("linked entity has an empty local id, surface key, canonical surface, or entity type")
		}
		if _, duplicate := seenIDs[id]; duplicate {
			return nil, fmt.Errorf("linked entity local id %q is repeated", id)
		}
		if _, duplicate := seenSurfaceKeys[entity.SurfaceKey]; duplicate {
			return nil, fmt.Errorf("linked entity surface key %q is repeated", entity.SurfaceKey)
		}
		seenIDs[id] = struct{}{}
		seenSurfaceKeys[entity.SurfaceKey] = struct{}{}

		status := strings.TrimSpace(entity.LinkStatus)
		if status != "linked" && status != "ambiguous" && status != "unlinked" {
			return nil, fmt.Errorf("linked entity %q has unsupported link status %q", id, status)
		}
		role := strings.TrimSpace(entity.Role)
		if role != "named_entity" && role != "visual_concept" {
			return nil, fmt.Errorf("linked entity %q has unsupported role %q", id, role)
		}
		canonicalID := ""
		if entity.CanonicalID != nil {
			canonicalID = strings.TrimSpace(*entity.CanonicalID)
		}
		gloss := ""
		if entity.DisambiguationGloss != nil {
			gloss = strings.TrimSpace(*entity.DisambiguationGloss)
		}
		if status == "linked" {
			if canonicalID == "" || strings.TrimSpace(entity.CanonicalName) == "" || gloss == "" {
				return nil, fmt.Errorf("linked entity %q omits linked identity metadata", id)
			}
		} else if canonicalID != "" || gloss != "" || len(entity.NegativeConstraints) != 0 {
			return nil, fmt.Errorf("unresolved entity %q carries linked-only metadata", id)
		}

		mentions := make([]scriptgen.SemanticIdentityMention, 0, len(entity.Mentions))
		seenMentions := make(map[string]struct{}, len(entity.Mentions))
		canonicalMention := false
		for _, mention := range entity.Mentions {
			source, ok := texts[mention.SceneID]
			if !ok || mention.Surface == "" || !groundedSpan(source, mention.StartByte, mention.EndByte, mention.Surface) {
				return nil, fmt.Errorf("linked entity %q mention %q is not grounded in requested scenes", id, mention.Surface)
			}
			key := fmt.Sprintf("%s\x1f%d\x1f%d\x1f%s", mention.SceneID, mention.StartByte, mention.EndByte, mention.Surface)
			if _, duplicate := seenMentions[key]; duplicate {
				return nil, fmt.Errorf("linked entity %q repeats a source mention", id)
			}
			seenMentions[key] = struct{}{}
			canonicalMention = canonicalMention || mention.Surface == entity.CanonicalSurface
			mentions = append(mentions, scriptgen.SemanticIdentityMention{
				SceneID: mention.SceneID, Surface: mention.Surface, Start: mention.StartByte, End: mention.EndByte,
			})
		}
		if len(mentions) == 0 || !canonicalMention {
			return nil, fmt.Errorf("linked entity %q has no grounded canonical mention", id)
		}
		if len(entity.SearchIntents) > 3 {
			return nil, fmt.Errorf("linked entity %q has %d search intents, maximum is 3", id, len(entity.SearchIntents))
		}
		intents := make([]scriptgen.SemanticSearchIntent, 0, len(entity.SearchIntents))
		for index, intent := range entity.SearchIntents {
			kind := strings.TrimSpace(intent.Kind)
			query := strings.TrimSpace(intent.Query)
			language := strings.TrimSpace(intent.Lang)
			if (kind != "identity" && kind != "contextual") || query == "" || language == "" {
				return nil, fmt.Errorf("linked entity %q has an invalid search intent", id)
			}
			if index == 0 && role == "named_entity" && kind != "identity" {
				return nil, fmt.Errorf("named entity %q must begin with an identity search intent", id)
			}
			if role == "visual_concept" && kind == "identity" {
				return nil, fmt.Errorf("visual concept %q cannot carry an identity search intent", id)
			}
			intents = append(intents, scriptgen.SemanticSearchIntent{Kind: kind, Query: query, Lang: language})
		}
		projected = append(projected, scriptgen.SemanticIdentity{
			ID: id, CanonicalSurface: entity.CanonicalSurface, Kind: entity.EntityType, Mentions: mentions,
			SurfaceKey: entity.SurfaceKey, CanonicalID: canonicalID, LinkStatus: status,
			CanonicalName: entity.CanonicalName, Role: role, DisambiguationGloss: gloss,
			NegativeConstraints: append([]string(nil), entity.NegativeConstraints...), SearchIntents: intents,
		})
	}
	return projected, nil
}

func validateLinkedEntityCoverage(scenes []semanticEngineSceneManifest, linked []scriptgen.SemanticIdentity) error {
	expected := make(map[string]semanticEngineIdentity)
	for _, scene := range scenes {
		for _, identity := range scene.Identities {
			id := strings.TrimSpace(identity.ID)
			if id == "" {
				return fmt.Errorf("scene %q has an identity with an empty id", scene.SceneID)
			}
			if previous, ok := expected[id]; ok {
				if previous.CanonicalSurface != identity.CanonicalSurface || previous.Kind != identity.Kind {
					return fmt.Errorf("identity %q changes surface or type across scene projections", id)
				}
			} else {
				expected[id] = identity
			}
		}
	}
	actual := make(map[string]scriptgen.SemanticIdentity, len(linked))
	for _, identity := range linked {
		actual[identity.ID] = identity
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("manifest has %d linked entities for %d scene identities", len(actual), len(expected))
	}
	for id, identity := range expected {
		linkedEntity, ok := actual[id]
		if !ok {
			return fmt.Errorf("scene identity %q is missing from the linked entity projection", id)
		}
		if linkedEntity.CanonicalSurface != identity.CanonicalSurface {
			return fmt.Errorf("linked entity %q canonical surface does not match scene identity", id)
		}
	}
	return nil
}
