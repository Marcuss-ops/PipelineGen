package rustexec

import (
	"fmt"
	"strings"
	"unicode/utf8"

	semantic "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"
)

func projectSemanticLinkedEntities(texts map[string]string, entities []semanticEngineLinkedEntity) ([]semantic.SemanticIdentity, error) {
	projected := make([]semantic.SemanticIdentity, 0, len(entities))
	seenIDs := make(map[string]struct{}, len(entities))
	seenSurfaceKeys := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		id, status, role, canonicalID, gloss, err := validateLinkedEntityHeader(entity, seenIDs, seenSurfaceKeys)
		if err != nil {
			return nil, err
		}
		mentions, err := projectEntityMentions(entity, texts)
		if err != nil {
			return nil, err
		}
		if len(entity.SearchIntents) > 3 {
			return nil, fmt.Errorf("linked entity %q has %d search intents, maximum is 3", id, len(entity.SearchIntents))
		}
		if len(entity.LinkCandidates) > 3 {
			return nil, fmt.Errorf("linked entity %q has %d link candidates, maximum is 3", id, len(entity.LinkCandidates))
		}
		winnerHasEvidence, candidateIDs, err := validateEntityLinkCandidates(entity, id, canonicalID, texts)
		if err != nil {
			return nil, err
		}
		if entity.LinkRule != "" {
			_, winnerCandidatePresent := candidateIDs[canonicalID]
			if status == "linked" && (!winnerHasEvidence || !winnerCandidatePresent) {
				return nil, fmt.Errorf("linked entity %q has no evidenced winning link candidate", id)
			}
		}
		intents, err := projectEntitySearchIntents(entity, id, role)
		if err != nil {
			return nil, err
		}
		projected = append(projected, semantic.SemanticIdentity{
			ID: id, CanonicalSurface: entity.CanonicalSurface, Kind: entity.EntityType, Mentions: mentions,
			SurfaceKey: entity.SurfaceKey, CanonicalID: canonicalID, LinkStatus: status,
			CanonicalName: entity.CanonicalName, Role: role, DisambiguationGloss: gloss,
			NegativeConstraints: append([]string(nil), entity.NegativeConstraints...), SearchIntents: intents,
			LinkCandidates: projectEntityLinkCandidates(entity), LinkMargin: entity.LinkMargin, LinkRule: entity.LinkRule,
			EditorialDecision: true, EditorialSurface: entity.EditorialSurface, EditorialType: entity.EditorialType,
			PromptEcho: entity.PromptEcho, VisualAdmission: entity.VisualAdmission,
		})
	}
	return projected, nil
}

func validateLinkedEntityHeader(
	entity semanticEngineLinkedEntity,
	seenIDs map[string]struct{},
	seenSurfaceKeys map[string]struct{},
) (id, status, role, canonicalID, gloss string, err error) {
	id = strings.TrimSpace(entity.LocalID)
	if id == "" || strings.TrimSpace(entity.SurfaceKey) == "" || strings.TrimSpace(entity.CanonicalSurface) == "" || strings.TrimSpace(entity.EntityType) == "" {
		return "", "", "", "", "", fmt.Errorf("linked entity has an empty local id, surface key, canonical surface, or entity type")
	}
	if _, duplicate := seenIDs[id]; duplicate {
		return "", "", "", "", "", fmt.Errorf("linked entity local id %q is repeated", id)
	}
	if _, duplicate := seenSurfaceKeys[entity.SurfaceKey]; duplicate {
		return "", "", "", "", "", fmt.Errorf("linked entity surface key %q is repeated", entity.SurfaceKey)
	}
	seenIDs[id] = struct{}{}
	seenSurfaceKeys[entity.SurfaceKey] = struct{}{}

	status = strings.TrimSpace(entity.LinkStatus)
	if status != "linked" && status != "ambiguous" && status != "unlinked" {
		return "", "", "", "", "", fmt.Errorf("linked entity %q has unsupported link status %q", id, status)
	}
	role = strings.TrimSpace(entity.Role)
	if role != "named_entity" && role != "visual_concept" {
		return "", "", "", "", "", fmt.Errorf("linked entity %q has unsupported role %q", id, role)
	}
	if entity.CanonicalID != nil {
		canonicalID = strings.TrimSpace(*entity.CanonicalID)
	}
	if entity.DisambiguationGloss != nil {
		gloss = strings.TrimSpace(*entity.DisambiguationGloss)
	}
	if status == "linked" {
		if canonicalID == "" || strings.TrimSpace(entity.CanonicalName) == "" || gloss == "" {
			return "", "", "", "", "", fmt.Errorf("linked entity %q omits linked identity metadata", id)
		}
	} else if canonicalID != "" || gloss != "" || len(entity.NegativeConstraints) != 0 {
		return "", "", "", "", "", fmt.Errorf("unresolved entity %q carries linked-only metadata", id)
	}
	return id, status, role, canonicalID, gloss, nil
}

func projectEntityMentions(entity semanticEngineLinkedEntity, texts map[string]string) ([]semantic.SemanticIdentityMention, error) {
	mentions := make([]semantic.SemanticIdentityMention, 0, len(entity.Mentions))
	seenMentions := make(map[string]struct{}, len(entity.Mentions))
	canonicalMention := false
	for _, mention := range entity.Mentions {
		source, ok := texts[mention.SceneID]
		if !ok || mention.Surface == "" || !groundedSpan(source, mention.StartByte, mention.EndByte, mention.Surface) {
			return nil, fmt.Errorf("linked entity %q mention %q is not grounded in requested scenes", entity.LocalID, mention.Surface)
		}
		key := fmt.Sprintf("%s\x1f%d\x1f%d\x1f%s", mention.SceneID, mention.StartByte, mention.EndByte, mention.Surface)
		if _, duplicate := seenMentions[key]; duplicate {
			return nil, fmt.Errorf("linked entity %q repeats a source mention", entity.LocalID)
		}
		seenMentions[key] = struct{}{}
		canonicalMention = canonicalMention || mention.Surface == entity.CanonicalSurface
		mentions = append(mentions, semantic.SemanticIdentityMention{
			SceneID: mention.SceneID, Surface: mention.Surface, Start: mention.StartByte, End: mention.EndByte,
		})
	}
	if len(mentions) == 0 || !canonicalMention {
		return nil, fmt.Errorf("linked entity %q has no grounded canonical mention", entity.LocalID)
	}
	return mentions, nil
}

func validateEntityLinkCandidates(
	entity semanticEngineLinkedEntity,
	id, canonicalID string,
	texts map[string]string,
) (bool, map[string]struct{}, error) {
	candidateIDs := make(map[string]struct{}, len(entity.LinkCandidates))
	winnerHasEvidence := false
	for _, candidate := range entity.LinkCandidates {
		candidateID := strings.TrimSpace(candidate.KBID)
		if candidateID == "" || strings.TrimSpace(candidate.Name) == "" {
			return false, nil, fmt.Errorf("linked entity %q has a candidate without an id or name", id)
		}
		if _, duplicate := candidateIDs[candidateID]; duplicate {
			return false, nil, fmt.Errorf("linked entity %q repeats candidate %q", id, candidateID)
		}
		candidateIDs[candidateID] = struct{}{}
		if len(candidate.Features) > 6 {
			return false, nil, fmt.Errorf("linked entity %q candidate %q has more than six features", id, candidateID)
		}
		for _, feature := range candidate.Features {
			if strings.TrimSpace(feature.Name) == "" {
				return false, nil, fmt.Errorf("linked entity %q candidate %q has an unnamed feature", id, candidateID)
			}
			for _, span := range feature.Spans {
				source, ok := texts[span.SceneID]
				if !ok || span.StartByte < 0 || span.EndByte <= span.StartByte || span.EndByte > len(source) ||
					!utf8.ValidString(source[:span.StartByte]) || !utf8.ValidString(source[:span.EndByte]) {
					return false, nil, fmt.Errorf("linked entity %q feature span is outside grounded scene text", id)
				}
				evidence := source[span.StartByte:span.EndByte]
				matched := false
				for _, term := range feature.Terms {
					if strings.EqualFold(evidence, term) {
						matched = true
						break
					}
				}
				if !matched {
					return false, nil, fmt.Errorf("linked entity %q feature span is not verbatim evidence", id)
				}
				if candidateID == canonicalID {
					winnerHasEvidence = true
				}
			}
		}
	}
	return winnerHasEvidence, candidateIDs, nil
}

func projectEntitySearchIntents(entity semanticEngineLinkedEntity, id, role string) ([]semantic.SemanticSearchIntent, error) {
	identityIntents := make([]semantic.SemanticSearchIntent, 0, 1)
	contextualIntents := make([]semantic.SemanticSearchIntent, 0, len(entity.SearchIntents))
	for _, intent := range entity.SearchIntents {
		kind := strings.TrimSpace(intent.Kind)
		query := strings.TrimSpace(intent.Query)
		language := strings.TrimSpace(intent.Lang)
		if (kind != "identity" && kind != "contextual") || query == "" || language == "" {
			return nil, fmt.Errorf("linked entity %q has an invalid search intent", id)
		}
		if role == "visual_concept" && kind == "identity" {
			return nil, fmt.Errorf("visual concept %q cannot carry an identity search intent", id)
		}
		projected := semantic.SemanticSearchIntent{Kind: kind, Query: query, Lang: language}
		if kind == "identity" {
			identityIntents = append(identityIntents, projected)
			continue
		}
		contextualIntents = append(contextualIntents, projected)
	}
	// Intent ordering is repaired at this boundary instead of failing the
	// whole run. A named entity's anchor is its identity query; when a
	// worker reports the contextual queries first, the identity is moved
	// back to the front. When the anchor is missing entirely (a long or
	// punctuated surface the worker could not turn into a query) the
	// contextual queries are dropped: a co-mention without its subject
	// must not become a standalone search.
	intents := make([]semantic.SemanticSearchIntent, 0, len(entity.SearchIntents))
	if role == "named_entity" {
		if len(identityIntents) > 0 {
			intents = append(intents, identityIntents...)
			intents = append(intents, contextualIntents...)
		}
	} else {
		intents = append(intents, identityIntents...)
		intents = append(intents, contextualIntents...)
	}
	return intents, nil
}

func projectEntityLinkCandidates(entity semanticEngineLinkedEntity) []semantic.SemanticLinkCandidate {
	linkCandidates := make([]semantic.SemanticLinkCandidate, 0, len(entity.LinkCandidates))
	for _, candidate := range entity.LinkCandidates {
		features := make([]semantic.SemanticLinkFeature, 0, len(candidate.Features))
		for _, feature := range candidate.Features {
			features = append(features, semantic.SemanticLinkFeature{
				Name: feature.Name, Value: feature.Value, Terms: append([]string(nil), feature.Terms...),
				Spans: append([]semantic.SemanticLinkEvidenceSpan(nil), feature.Spans...),
			})
		}
		linkCandidates = append(linkCandidates, semantic.SemanticLinkCandidate{
			KBID: candidate.KBID, Name: candidate.Name, Score: candidate.Score, Features: features,
		})
	}
	return linkCandidates
}

func validateLinkedEntityCoverage(scenes []semanticEngineSceneManifest, linked []semantic.SemanticIdentity) error {
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
	actual := make(map[string]semantic.SemanticIdentity, len(linked))
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
