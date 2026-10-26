package rustexec

import (
	"fmt"
	"sort"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// projectSemanticConsolidation validates and projects the script-level
// selective production view. It fails closed: a candidate that points at an
// unknown scene, identity, fact or group, or whose evidence/query is not a
// verbatim source slice, never reaches planning.
func projectSemanticConsolidation(
	texts map[string]string,
	wire *semanticEngineConsolidation,
	bundles map[string]*scriptgen.SemanticCandidateBundle,
) (*scriptgen.SemanticVisualConsolidation, error) {
	if wire == nil {
		// Older workers do not carry the projection. The raw per-scene
		// collections remain usable; only the new selective view is absent.
		return nil, nil
	}
	if wire.Schema != "visual_consolidation.v1" {
		return nil, fmt.Errorf("semantic consolidation: unsupported schema %q", wire.Schema)
	}

	knownEntities := make(map[string]struct{})
	knownGroups := make(map[string]struct{})
	rawProposals := 0
	for _, bundle := range bundles {
		for _, identity := range bundle.Identities {
			knownEntities[identity.ID] = struct{}{}
		}
		for _, group := range bundle.Groups {
			knownGroups[group.ID] = struct{}{}
		}
		rawProposals += len(bundle.Proposals)
	}

	scriptText := scriptGroundingText(texts)
	canonicalFacts := make(map[string]struct{}, len(wire.Facts))
	for _, fact := range wire.Facts {
		if fact.ID == "" {
			return nil, fmt.Errorf("semantic consolidation: canonical fact without id")
		}
		if _, duplicate := canonicalFacts[fact.ID]; duplicate {
			return nil, fmt.Errorf("semantic consolidation: duplicate canonical fact %q", fact.ID)
		}
		canonicalFacts[fact.ID] = struct{}{}
	}

	candidates := make([]scriptgen.SemanticProductionCandidate, 0, len(wire.ProductionCandidates))
	candidateByID := make(map[string]int, len(wire.ProductionCandidates))
	dedupKeys := make(map[string]struct{}, len(wire.ProductionCandidates))
	mediaAssets := make(map[string]struct{})
	productionQueries := 0
	for _, wireCandidate := range wire.ProductionCandidates {
		if wireCandidate.ID == "" || wireCandidate.Recipe == "" || wireCandidate.AssetKey == "" || wireCandidate.DedupKey == "" {
			return nil, fmt.Errorf("semantic consolidation: candidate identity is incomplete: %+v", wireCandidate)
		}
		if wireCandidate.Rank <= 0 {
			return nil, fmt.Errorf("semantic consolidation: candidate %q has invalid rank %d", wireCandidate.ID, wireCandidate.Rank)
		}
		if _, duplicate := candidateByID[wireCandidate.ID]; duplicate {
			return nil, fmt.Errorf("semantic consolidation: duplicate candidate id %q", wireCandidate.ID)
		}
		if _, duplicate := dedupKeys[wireCandidate.DedupKey]; duplicate {
			return nil, fmt.Errorf("semantic consolidation: duplicate candidate dedup key %q", wireCandidate.DedupKey)
		}
		dedupKeys[wireCandidate.DedupKey] = struct{}{}
		source, ok := texts[wireCandidate.SceneID]
		if !ok {
			return nil, fmt.Errorf("semantic consolidation: candidate %q references unknown scene %q", wireCandidate.ID, wireCandidate.SceneID)
		}
		if !groundedSpan(source, wireCandidate.StartByte, wireCandidate.EndByte, wireCandidate.Evidence) {
			return nil, fmt.Errorf("semantic consolidation: candidate %q evidence is not grounded in %q", wireCandidate.ID, wireCandidate.SceneID)
		}
		for _, entityID := range wireCandidate.EntityIDs {
			if _, ok := knownEntities[entityID]; !ok {
				return nil, fmt.Errorf("semantic consolidation: candidate %q references unknown identity %q", wireCandidate.ID, entityID)
			}
		}
		for _, groupID := range wireCandidate.GroupIDs {
			if _, ok := knownGroups[groupID]; !ok {
				return nil, fmt.Errorf("semantic consolidation: candidate %q references unknown group %q", wireCandidate.ID, groupID)
			}
		}
		for _, factID := range wireCandidate.CoveredFactIDs {
			if _, ok := canonicalFacts[factID]; !ok {
				return nil, fmt.Errorf("semantic consolidation: candidate %q covers unknown fact %q", wireCandidate.ID, factID)
			}
		}
		queries := make([]scriptgen.SemanticSearchQuery, 0, len(wireCandidate.Queries))
		seenQueries := make(map[string]struct{}, len(wireCandidate.Queries))
		for _, query := range wireCandidate.Queries {
			// Exact identity mentions are grounded against the whole script:
			// the consolidation may anchor a composition in the scene that
			// states the fact while the identity it searches is introduced by
			// another scene. Source slices keep the stricter scene grounding.
			grounding := source
			if strings.TrimSpace(query.Origin) == "literal" || query.Origin == "" {
				if strings.TrimSpace(query.Kind) == "exact_mention" {
					grounding = scriptText
				}
				if strings.TrimSpace(query.Text) == "" || !groundedQuery(grounding, query.Text) {
					return nil, fmt.Errorf("semantic consolidation: candidate %q query %q is not grounded", wireCandidate.ID, query.Text)
				}
			} else if !groundedContextQuery(source, query) {
				return nil, fmt.Errorf("semantic consolidation: candidate %q query %q is not grounded", wireCandidate.ID, query.Text)
			}
			if _, duplicate := seenQueries[query.Text]; duplicate {
				return nil, fmt.Errorf("semantic consolidation: candidate %q repeats query %q", wireCandidate.ID, query.Text)
			}
			seenQueries[query.Text] = struct{}{}
			projectedQuery := scriptgen.SemanticSearchQuery{
				Kind: query.Kind, Text: query.Text, Origin: query.Origin,
				Illustrative: query.Illustrative,
			}
			if query.Evidence != nil {
				projectedQuery.Evidence = *query.Evidence
			}
			if query.EvidenceStartByte != nil {
				projectedQuery.EvidenceStartByte = *query.EvidenceStartByte
			}
			if query.EvidenceEndByte != nil {
				projectedQuery.EvidenceEndByte = *query.EvidenceEndByte
			}
			if query.TemplateID != nil {
				projectedQuery.TemplateID = *query.TemplateID
			}
			projectedQuery.DerivedTerms = append(projectedQuery.DerivedTerms, query.DerivedTerms...)
			for _, component := range query.Components {
				projectedQuery.Components = append(projectedQuery.Components, scriptgen.SemanticSearchQueryComponent{
					Kind: component.Kind, ComponentType: component.ComponentType, Text: component.Text,
					StartByte: component.StartByte, EndByte: component.EndByte,
				})
			}
			queries = append(queries, projectedQuery)
		}
		if wireCandidate.RequiresMedia && len(queries) == 0 {
			return nil, fmt.Errorf("semantic consolidation: media candidate %q has no query", wireCandidate.ID)
		}
		if !wireCandidate.RequiresMedia && len(queries) != 0 {
			return nil, fmt.Errorf("semantic consolidation: non-media candidate %q carries external queries", wireCandidate.ID)
		}
		if wireCandidate.RequiresMedia {
			mediaAssets[wireCandidate.AssetKey] = struct{}{}
		}
		productionQueries += len(queries)
		projected := scriptgen.SemanticProductionCandidate{
			ID: wireCandidate.ID, Recipe: wireCandidate.Recipe, Rank: wireCandidate.Rank,
			Priority: wireCandidate.Priority, SceneID: wireCandidate.SceneID,
			SceneIDs:  append([]string(nil), wireCandidate.SceneIDs...),
			EntityIDs: append([]string(nil), wireCandidate.EntityIDs...),
			FactIDs:   append([]string(nil), wireCandidate.FactIDs...),
			GroupIDs:  append([]string(nil), wireCandidate.GroupIDs...),
			Title:     wireCandidate.Title, Evidence: wireCandidate.Evidence,
			Start: wireCandidate.StartByte, End: wireCandidate.EndByte,
			SuggestedVisual:  wireCandidate.SuggestedVisual,
			TemporalSurfaces: append([]string(nil), wireCandidate.TemporalSurfaces...),
			AssetKey:         wireCandidate.AssetKey, Queries: queries,
			RequiresMedia: wireCandidate.RequiresMedia, DedupKey: wireCandidate.DedupKey,
			CoveredFactIDs: append([]string(nil), wireCandidate.CoveredFactIDs...),
			Rationale:      append([]string(nil), wireCandidate.Rationale...),
		}
		for _, item := range wireCandidate.ChartItems {
			if !groundedSpan(source, item.StartByte, item.EndByte, item.Text) {
				return nil, fmt.Errorf("semantic consolidation: candidate %q chart item is not grounded", wireCandidate.ID)
			}
			projected.ChartItems = append(projected.ChartItems, scriptgen.SemanticChartItem{
				Label: item.Label, Text: item.Text, Kind: item.Kind, Value: item.Value,
				Start: item.StartByte, End: item.EndByte,
			})
		}
		candidateByID[projected.ID] = len(candidates)
		candidates = append(candidates, projected)
	}

	facts := make([]scriptgen.SemanticConsolidatedFact, 0, len(wire.Facts))
	for _, fact := range wire.Facts {
		if fact.Evidence == "" {
			return nil, fmt.Errorf("semantic consolidation: fact %q has no evidence", fact.ID)
		}
		grounded := false
		for _, sceneID := range fact.SceneIDs {
			source, ok := texts[sceneID]
			if !ok {
				return nil, fmt.Errorf("semantic consolidation: fact %q references unknown scene %q", fact.ID, sceneID)
			}
			if strings.Contains(source, fact.Evidence) {
				grounded = true
			}
		}
		if !grounded {
			return nil, fmt.Errorf("semantic consolidation: fact %q evidence is not grounded in any of its scenes", fact.ID)
		}
		if fact.Representation == nil {
			if strings.TrimSpace(derefString(fact.UncoveredReason)) == "" {
				return nil, fmt.Errorf("semantic consolidation: fact %q is neither represented nor explained", fact.ID)
			}
		} else {
			candidateIndex, ok := candidateByID[fact.Representation.CandidateID]
			if !ok {
				return nil, fmt.Errorf("semantic consolidation: fact %q references unknown candidate %q", fact.ID, fact.Representation.CandidateID)
			}
			candidate := &candidates[candidateIndex]
			covered := false
			for _, id := range candidate.CoveredFactIDs {
				if id == fact.ID {
					covered = true
					break
				}
			}
			if !covered {
				return nil, fmt.Errorf("semantic consolidation: candidate %q does not declare fact %q", candidate.ID, fact.ID)
			}
		}
		projected := scriptgen.SemanticConsolidatedFact{
			ID: fact.ID, FactKey: fact.FactKey,
			FactIDs:         append([]string(nil), fact.FactIDs...),
			SubjectEntityID: fact.SubjectEntityID, Predicate: fact.Predicate,
			NormalizedValue: derefString(fact.NormalizedValue),
			NormalizedDate:  derefString(fact.NormalizedDate),
			Status:          fact.Status, Importance: fact.Importance,
			SceneIDs:      append([]string(nil), fact.SceneIDs...),
			SentenceIndex: fact.SentenceIndex, Evidence: fact.Evidence,
			Start: fact.StartByte, End: fact.EndByte,
			EvidenceStart: fact.EvidenceStartByte, EvidenceEnd: fact.EvidenceEndByte,
			UncoveredReason: derefString(fact.UncoveredReason),
		}
		if fact.Representation != nil {
			projected.Representation = &scriptgen.SemanticFactRepresentation{
				CandidateID: fact.Representation.CandidateID, Recipe: fact.Representation.Recipe,
			}
		}
		facts = append(facts, projected)
	}

	dates := make([]scriptgen.SemanticConsolidatedDate, 0, len(wire.Dates))
	for _, date := range wire.Dates {
		if date.NormalizedDate == "" {
			return nil, fmt.Errorf("semantic consolidation: date without normalized value")
		}
		projected := scriptgen.SemanticConsolidatedDate{
			NormalizedDate: date.NormalizedDate,
			Count:          date.Count,
			Surfaces:       append([]string(nil), date.Surfaces...),
		}
		for _, span := range date.Spans {
			source, ok := texts[span.SceneID]
			if !ok || !groundedSpan(source, span.StartByte, span.EndByte, span.Surface) {
				return nil, fmt.Errorf("semantic consolidation: date span %q is not grounded", span.Surface)
			}
			projected.Spans = append(projected.Spans, scriptgen.SemanticConsolidatedDateSpan{
				SceneID: span.SceneID, Surface: span.Surface, Start: span.StartByte, End: span.EndByte,
			})
		}
		dates = append(dates, projected)
	}

	references := make([]scriptgen.SemanticReferenceNumber, 0, len(wire.ReferenceNumbers))
	for _, reference := range wire.ReferenceNumbers {
		source, ok := texts[reference.SceneID]
		if !ok || !groundedSpan(source, reference.StartByte, reference.EndByte, reference.Surface) {
			return nil, fmt.Errorf("semantic consolidation: reference number %q is not grounded", reference.Surface)
		}
		references = append(references, scriptgen.SemanticReferenceNumber{
			SceneID: reference.SceneID, Surface: reference.Surface, Reason: reference.Reason,
			Start: reference.StartByte, End: reference.EndByte,
		})
	}

	links := make([]scriptgen.SemanticEventLink, 0, len(wire.EventLinks))
	for _, link := range wire.EventLinks {
		if link.Kind != "precedes" {
			return nil, fmt.Errorf("semantic consolidation: unsupported event link kind %q", link.Kind)
		}
		if _, ok := knownGroups[link.GroupID]; !ok {
			return nil, fmt.Errorf("semantic consolidation: event link references unknown group %q", link.GroupID)
		}
		source, ok := texts[link.SceneID]
		if !ok || !groundedSpan(source, link.StartByte, link.EndByte, link.Evidence) {
			return nil, fmt.Errorf("semantic consolidation: event link %q is not grounded", link.ID)
		}
		links = append(links, scriptgen.SemanticEventLink{
			ID: link.ID, GroupID: link.GroupID, SceneID: link.SceneID,
			FromCandidateID: link.FromCandidateID, ToCandidateID: link.ToCandidateID,
			Kind: link.Kind, Evidence: link.Evidence, Start: link.StartByte, End: link.EndByte,
		})
	}

	scenes := make([]scriptgen.SemanticConsolidationSceneStats, 0, len(wire.Scenes))
	for _, scene := range wire.Scenes {
		if _, ok := texts[scene.SceneID]; !ok {
			return nil, fmt.Errorf("semantic consolidation: scene stats reference unknown scene %q", scene.SceneID)
		}
		scenes = append(scenes, scriptgen.SemanticConsolidationSceneStats{
			SceneID: scene.SceneID, RawProposals: scene.RawProposals,
			ProductionCandidates: scene.ProductionCandidates, RawQueries: scene.RawQueries,
			CanonicalFacts: scene.CanonicalFacts, CoveredFacts: scene.CoveredFacts,
			TimelineCandidates: scene.TimelineCandidates,
		})
	}

	if wire.Metrics.ProductionCandidates != len(candidates) ||
		wire.Metrics.CanonicalFacts != len(facts) ||
		wire.Metrics.CoveredFacts+wire.Metrics.UncoveredFacts != len(facts) ||
		wire.Metrics.ProductionQueries != productionQueries ||
		wire.Metrics.EstimatedSearchJobs != len(mediaAssets) ||
		wire.Inventory.Proposals != rawProposals ||
		wire.Metrics.RawProposals != rawProposals {
		return nil, fmt.Errorf("semantic consolidation: metrics are inconsistent with the manifest")
	}
	if wire.Inventory.CanonicalFacts != len(facts) || wire.Inventory.Proposals != rawProposals {
		return nil, fmt.Errorf("semantic consolidation: inventory is inconsistent with the manifest")
	}

	return &scriptgen.SemanticVisualConsolidation{
		Schema: wire.Schema,
		Inventory: scriptgen.SemanticConsolidationInventory{
			EntityMentions: wire.Inventory.EntityMentions, UniqueIdentities: wire.Inventory.UniqueIdentities,
			NamedIdentities: wire.Inventory.NamedIdentities, Facts: wire.Inventory.Facts,
			CanonicalFacts: wire.Inventory.CanonicalFacts, Proposals: wire.Inventory.Proposals,
			SearchEntries: wire.Inventory.SearchEntries, Queries: wire.Inventory.Queries,
			TemporalFacts: wire.Inventory.TemporalFacts, CanonicalDates: wire.Inventory.CanonicalDates,
			MicroSummaries: wire.Inventory.MicroSummaries, CanonicalMicroSummaries: wire.Inventory.CanonicalMicroSummaries,
			Groups: wire.Inventory.Groups, Timelines: wire.Inventory.Timelines,
		},
		ProductionCandidates: candidates,
		Facts:                facts,
		Dates:                dates,
		ReferenceNumbers:     references,
		EventLinks:           links,
		Scenes:               scenes,
		Metrics: scriptgen.SemanticConsolidationMetrics{
			RawProposals: wire.Metrics.RawProposals, ProductionCandidates: wire.Metrics.ProductionCandidates,
			RawSearchEntries: wire.Metrics.RawSearchEntries, ProductionSearchCandidates: wire.Metrics.ProductionSearchCandidates,
			RawQueries: wire.Metrics.RawQueries, ProductionQueries: wire.Metrics.ProductionQueries,
			DuplicateEntityMentions:   wire.Metrics.DuplicateEntityMentions,
			DuplicateFactsMerged:      wire.Metrics.DuplicateFactsMerged,
			DuplicateQueriesRemoved:   wire.Metrics.DuplicateQueriesRemoved,
			DuplicateProposalsRemoved: wire.Metrics.DuplicateProposalsRemoved,
			EstimatedSearchJobs:       wire.Metrics.EstimatedSearchJobs,
			SharedAssetReuses:         wire.Metrics.SharedAssetReuses,
			CanonicalFacts:            wire.Metrics.CanonicalFacts,
			CoveredFacts:              wire.Metrics.CoveredFacts,
			UncoveredFacts:            wire.Metrics.UncoveredFacts,
			ReferenceNumbers:          wire.Metrics.ReferenceNumbers,
		},
	}, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func groundedContextQuery(source string, query semanticEngineQuery) bool {
	if (query.Origin != "derived_from_source_context" && query.Origin != "derived_from_semantic_template") || query.Evidence == nil ||
		query.EvidenceStartByte == nil || query.EvidenceEndByte == nil ||
		!groundedSpan(source, *query.EvidenceStartByte, *query.EvidenceEndByte, *query.Evidence) ||
		len(query.Components) < 2 {
		return false
	}
	components := append([]semanticEngineQueryComponent(nil), query.Components...)
	sort.Slice(components, func(i, j int) bool { return components[i].StartByte < components[j].StartByte })
	parts := make([]string, 0, len(components))
	for _, component := range components {
		if component.StartByte < *query.EvidenceStartByte || component.EndByte > *query.EvidenceEndByte ||
			!groundedSpan(source, component.StartByte, component.EndByte, component.Text) {
			return false
		}
		parts = append(parts, strings.TrimSpace(component.Text))
	}
	if query.Origin == "derived_from_source_context" {
		return strings.Join(parts, " ") == query.Text
	}
	if query.TemplateID == nil || strings.TrimSpace(*query.TemplateID) == "" || !query.Illustrative || len(query.DerivedTerms) == 0 {
		return false
	}
	groundedTerms := strings.Fields(strings.Join(parts, " "))
	groundedTerms = append(groundedTerms, strings.Fields(strings.Join(query.DerivedTerms, " "))...)
	queryTerms := strings.Fields(query.Text)
	for index := range groundedTerms {
		groundedTerms[index] = strings.ToLower(groundedTerms[index])
	}
	for index := range queryTerms {
		queryTerms[index] = strings.ToLower(queryTerms[index])
	}
	sort.Strings(groundedTerms)
	sort.Strings(queryTerms)
	return strings.Join(groundedTerms, "\x00") == strings.Join(queryTerms, "\x00")
}
