package rustexec

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"
)

// consolidationScope is the immutable manifest index shared by every
// consolidation check: known identities, group-to-scene ownership and the raw
// proposal count.
type consolidationScope struct {
	knownEntities map[string]struct{}
	groupScenes   map[string]map[string]struct{}
	rawProposals  int
}

// consolidationCandidates is the candidate projection state. It owns the
// duplicate indexes so every check sees the same identity map.
type consolidationCandidates struct {
	items             []scriptgen.SemanticProductionCandidate
	byID              map[string]int
	dedupKeys         map[string]struct{}
	mediaAssets       map[string]struct{}
	productionQueries int
}

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
	if err := validateConsolidationSchema(wire); err != nil {
		return nil, err
	}
	scope, err := indexConsolidationScope(bundles)
	if err != nil {
		return nil, err
	}
	canonicalFacts, err := validateCanonicalFacts(wire.Facts)
	if err != nil {
		return nil, err
	}
	scriptText := scriptGroundingText(texts)
	candidates, err := validateProductionCandidates(wire.ProductionCandidates, texts, scriptText, scope, canonicalFacts)
	if err != nil {
		return nil, err
	}
	facts, err := projectConsolidatedFacts(wire.Facts, texts, candidates.items, candidates.byID)
	if err != nil {
		return nil, err
	}
	dates, err := projectConsolidatedDates(wire.Dates, texts)
	if err != nil {
		return nil, err
	}
	references, err := projectReferenceNumbers(wire.ReferenceNumbers, texts)
	if err != nil {
		return nil, err
	}
	links, err := projectEventLinks(wire.EventLinks, texts, scope.groupScenes)
	if err != nil {
		return nil, err
	}
	scenes, err := projectConsolidationScenes(wire.Scenes, texts)
	if err != nil {
		return nil, err
	}
	if err := validateConsolidationCounts(wire, candidates, facts, scope); err != nil {
		return nil, err
	}
	return projectConsolidationView(wire, candidates.items, facts, dates, references, links, scenes), nil
}

func validateConsolidationSchema(wire *semanticEngineConsolidation) error {
	if wire.Schema != "visual_consolidation.v1" {
		return fmt.Errorf("semantic consolidation: unsupported schema %q", wire.Schema)
	}
	return nil
}

func indexConsolidationScope(bundles map[string]*scriptgen.SemanticCandidateBundle) (*consolidationScope, error) {
	groupScenes, err := indexSemanticGroupScenes(bundles)
	if err != nil {
		return nil, err
	}
	knownEntities := make(map[string]struct{})
	rawProposals := 0
	for _, bundle := range bundles {
		for _, identity := range bundle.Identities {
			knownEntities[identity.ID] = struct{}{}
		}
		rawProposals += len(bundle.Proposals)
	}
	return &consolidationScope{knownEntities: knownEntities, groupScenes: groupScenes, rawProposals: rawProposals}, nil
}

func validateCanonicalFacts(wireFacts []semanticEngineConsolidatedFact) (map[string]struct{}, error) {
	canonicalFacts := make(map[string]struct{}, len(wireFacts))
	for _, fact := range wireFacts {
		if fact.ID == "" {
			return nil, fmt.Errorf("semantic consolidation: canonical fact without id")
		}
		if _, duplicate := canonicalFacts[fact.ID]; duplicate {
			return nil, fmt.Errorf("semantic consolidation: duplicate canonical fact %q", fact.ID)
		}
		canonicalFacts[fact.ID] = struct{}{}
	}
	return canonicalFacts, nil
}

func validateProductionCandidates(
	wireCandidates []semanticEngineProductionCandidate,
	texts map[string]string,
	scriptText string,
	scope *consolidationScope,
	canonicalFacts map[string]struct{},
) (*consolidationCandidates, error) {
	out := &consolidationCandidates{
		items:       make([]scriptgen.SemanticProductionCandidate, 0, len(wireCandidates)),
		byID:        make(map[string]int, len(wireCandidates)),
		dedupKeys:   make(map[string]struct{}, len(wireCandidates)),
		mediaAssets: make(map[string]struct{}),
	}
	for _, wireCandidate := range wireCandidates {
		queries, err := validateProductionCandidate(wireCandidate, out, texts, scriptText, scope, canonicalFacts)
		if err != nil {
			return nil, err
		}
		projected, err := projectProductionCandidate(wireCandidate, queries, texts[wireCandidate.SceneID])
		if err != nil {
			return nil, err
		}
		if wireCandidate.RequiresMedia {
			out.mediaAssets[wireCandidate.AssetKey] = struct{}{}
		}
		out.productionQueries += len(queries)
		out.byID[projected.ID] = len(out.items)
		out.items = append(out.items, projected)
	}
	return out, nil
}

func validateProductionCandidate(
	wireCandidate semanticEngineProductionCandidate,
	out *consolidationCandidates,
	texts map[string]string,
	scriptText string,
	scope *consolidationScope,
	canonicalFacts map[string]struct{},
) ([]generationcontract.SemanticSearchQuery, error) {
	if wireCandidate.ID == "" || wireCandidate.Recipe == "" || wireCandidate.AssetKey == "" || wireCandidate.DedupKey == "" {
		return nil, fmt.Errorf("semantic consolidation: candidate identity is incomplete: %+v", wireCandidate)
	}
	if wireCandidate.Rank <= 0 {
		return nil, fmt.Errorf("semantic consolidation: candidate %q has invalid rank %d", wireCandidate.ID, wireCandidate.Rank)
	}
	if _, duplicate := out.byID[wireCandidate.ID]; duplicate {
		return nil, fmt.Errorf("semantic consolidation: duplicate candidate id %q", wireCandidate.ID)
	}
	if _, duplicate := out.dedupKeys[wireCandidate.DedupKey]; duplicate {
		return nil, fmt.Errorf("semantic consolidation: duplicate candidate dedup key %q", wireCandidate.DedupKey)
	}
	out.dedupKeys[wireCandidate.DedupKey] = struct{}{}
	source, ok := texts[wireCandidate.SceneID]
	if !ok {
		return nil, fmt.Errorf("semantic consolidation: candidate %q references unknown scene %q", wireCandidate.ID, wireCandidate.SceneID)
	}
	if !groundedSpan(source, wireCandidate.StartByte, wireCandidate.EndByte, wireCandidate.Evidence) {
		return nil, fmt.Errorf("semantic consolidation: candidate %q evidence is not grounded in %q", wireCandidate.ID, wireCandidate.SceneID)
	}
	for _, entityID := range wireCandidate.EntityIDs {
		if _, ok := scope.knownEntities[entityID]; !ok {
			return nil, fmt.Errorf("semantic consolidation: candidate %q references unknown identity %q", wireCandidate.ID, entityID)
		}
	}
	for _, groupID := range wireCandidate.GroupIDs {
		declaringScenes, ok := scope.groupScenes[groupID]
		if !ok {
			return nil, fmt.Errorf("semantic consolidation: candidate %q references unknown group %q", wireCandidate.ID, groupID)
		}
		if !candidateOwnsGroupScene(wireCandidate, declaringScenes) {
			return nil, fmt.Errorf("semantic consolidation: candidate %q references group %q of another scene", wireCandidate.ID, groupID)
		}
	}
	for _, factID := range wireCandidate.CoveredFactIDs {
		if _, ok := canonicalFacts[factID]; !ok {
			return nil, fmt.Errorf("semantic consolidation: candidate %q covers unknown fact %q", wireCandidate.ID, factID)
		}
	}
	queries, err := validateCandidateQueries(wireCandidate, source, scriptText)
	if err != nil {
		return nil, err
	}
	if wireCandidate.RequiresMedia && len(queries) == 0 {
		return nil, fmt.Errorf("semantic consolidation: media candidate %q has no query", wireCandidate.ID)
	}
	if !wireCandidate.RequiresMedia && len(queries) != 0 {
		return nil, fmt.Errorf("semantic consolidation: non-media candidate %q carries external queries", wireCandidate.ID)
	}
	return queries, nil
}

func validateCandidateQueries(
	wireCandidate semanticEngineProductionCandidate,
	source string,
	scriptText string,
) ([]generationcontract.SemanticSearchQuery, error) {
	queries := make([]generationcontract.SemanticSearchQuery, 0, len(wireCandidate.Queries))
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
		projectedQuery := generationcontract.SemanticSearchQuery{
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
			projectedQuery.Components = append(projectedQuery.Components, generationcontract.SemanticSearchQueryComponent{
				Kind: component.Kind, ComponentType: component.ComponentType, Text: component.Text,
				StartByte: component.StartByte, EndByte: component.EndByte,
			})
		}
		queries = append(queries, projectedQuery)
	}
	return queries, nil
}

func projectProductionCandidate(
	wireCandidate semanticEngineProductionCandidate,
	queries []generationcontract.SemanticSearchQuery,
	source string,
) (scriptgen.SemanticProductionCandidate, error) {
	projected := scriptgen.SemanticProductionCandidate{
		ID: wireCandidate.ID, Recipe: wireCandidate.Recipe, Rank: wireCandidate.Rank,
		Priority: wireCandidate.Priority, SceneID: wireCandidate.SceneID,
		SceneIDs:  append([]string(nil), wireCandidate.SceneIDs...),
		EntityIDs: append([]string(nil), wireCandidate.EntityIDs...),
		FactIDs:   append([]string(nil), wireCandidate.FactIDs...),
		GroupIDs:  append([]string(nil), wireCandidate.GroupIDs...),
		Title:     wireCandidate.Title, Evidence: wireCandidate.Evidence,
		Start: wireCandidate.StartByte, End: wireCandidate.EndByte,
		VisualPayload:    append(json.RawMessage(nil), wireCandidate.VisualPayload...),
		SuggestedVisual:  wireCandidate.SuggestedVisual,
		TemporalSurfaces: append([]string(nil), wireCandidate.TemporalSurfaces...),
		AssetKey:         wireCandidate.AssetKey, Queries: queries,
		RequiresMedia: wireCandidate.RequiresMedia, DedupKey: wireCandidate.DedupKey,
		CoveredFactIDs: append([]string(nil), wireCandidate.CoveredFactIDs...),
		Rationale:      append([]string(nil), wireCandidate.Rationale...),
	}
	for _, item := range wireCandidate.ChartItems {
		if !groundedSpan(source, item.StartByte, item.EndByte, item.Text) {
			return scriptgen.SemanticProductionCandidate{}, fmt.Errorf("semantic consolidation: candidate %q chart item is not grounded", wireCandidate.ID)
		}
		projected.ChartItems = append(projected.ChartItems, scriptgen.SemanticChartItem{
			Label: item.Label, Text: item.Text, Kind: item.Kind, Value: item.Value,
			Start: item.StartByte, End: item.EndByte,
		})
	}
	return projected, nil
}

func projectConsolidatedFacts(
	wireFacts []semanticEngineConsolidatedFact,
	texts map[string]string,
	candidates []scriptgen.SemanticProductionCandidate,
	candidateByID map[string]int,
) ([]scriptgen.SemanticConsolidatedFact, error) {
	facts := make([]scriptgen.SemanticConsolidatedFact, 0, len(wireFacts))
	for _, fact := range wireFacts {
		if err := validateConsolidatedFact(fact, texts, candidates, candidateByID); err != nil {
			return nil, err
		}
		facts = append(facts, projectConsolidatedFact(fact))
	}
	return facts, nil
}

func validateConsolidatedFact(
	fact semanticEngineConsolidatedFact,
	texts map[string]string,
	candidates []scriptgen.SemanticProductionCandidate,
	candidateByID map[string]int,
) error {
	if fact.Evidence == "" {
		return fmt.Errorf("semantic consolidation: fact %q has no evidence", fact.ID)
	}
	grounded := false
	for _, sceneID := range fact.SceneIDs {
		source, ok := texts[sceneID]
		if !ok {
			return fmt.Errorf("semantic consolidation: fact %q references unknown scene %q", fact.ID, sceneID)
		}
		if strings.Contains(source, fact.Evidence) {
			grounded = true
		}
	}
	if !grounded {
		return fmt.Errorf("semantic consolidation: fact %q evidence is not grounded in any of its scenes", fact.ID)
	}
	if fact.Representation == nil {
		if strings.TrimSpace(derefString(fact.UncoveredReason)) == "" {
			return fmt.Errorf("semantic consolidation: fact %q is neither represented nor explained", fact.ID)
		}
		return nil
	}
	candidateIndex, ok := candidateByID[fact.Representation.CandidateID]
	if !ok {
		return fmt.Errorf("semantic consolidation: fact %q references unknown candidate %q", fact.ID, fact.Representation.CandidateID)
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
		return fmt.Errorf("semantic consolidation: candidate %q does not declare fact %q", candidate.ID, fact.ID)
	}
	return nil
}

func projectConsolidatedFact(fact semanticEngineConsolidatedFact) scriptgen.SemanticConsolidatedFact {
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
	return projected
}

func projectConsolidatedDates(wireDates []semanticEngineConsolidatedDate, texts map[string]string) ([]scriptgen.SemanticConsolidatedDate, error) {
	dates := make([]scriptgen.SemanticConsolidatedDate, 0, len(wireDates))
	for _, date := range wireDates {
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
	return dates, nil
}

func projectReferenceNumbers(wireReferences []semanticEngineReferenceNumber, texts map[string]string) ([]scriptgen.SemanticReferenceNumber, error) {
	references := make([]scriptgen.SemanticReferenceNumber, 0, len(wireReferences))
	for _, reference := range wireReferences {
		source, ok := texts[reference.SceneID]
		if !ok || !groundedSpan(source, reference.StartByte, reference.EndByte, reference.Surface) {
			return nil, fmt.Errorf("semantic consolidation: reference number %q is not grounded", reference.Surface)
		}
		references = append(references, scriptgen.SemanticReferenceNumber{
			SceneID: reference.SceneID, Surface: reference.Surface, Reason: reference.Reason,
			Start: reference.StartByte, End: reference.EndByte,
		})
	}
	return references, nil
}

func projectEventLinks(
	wireLinks []semanticEngineEventLink,
	texts map[string]string,
	groupScenes map[string]map[string]struct{},
) ([]scriptgen.SemanticEventLink, error) {
	links := make([]scriptgen.SemanticEventLink, 0, len(wireLinks))
	for _, link := range wireLinks {
		if link.Kind != "precedes" {
			return nil, fmt.Errorf("semantic consolidation: unsupported event link kind %q", link.Kind)
		}
		declaringScenes, ok := groupScenes[link.GroupID]
		if !ok {
			return nil, fmt.Errorf("semantic consolidation: event link references unknown group %q", link.GroupID)
		}
		if _, ok := declaringScenes[link.SceneID]; !ok {
			return nil, fmt.Errorf("semantic consolidation: event link %q references group %q of another scene", link.ID, link.GroupID)
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
	return links, nil
}

func projectConsolidationScenes(
	wireScenes []semanticEngineConsolidationSceneStats,
	texts map[string]string,
) ([]scriptgen.SemanticConsolidationSceneStats, error) {
	scenes := make([]scriptgen.SemanticConsolidationSceneStats, 0, len(wireScenes))
	for _, scene := range wireScenes {
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
	return scenes, nil
}

func validateConsolidationCounts(
	wire *semanticEngineConsolidation,
	candidates *consolidationCandidates,
	facts []scriptgen.SemanticConsolidatedFact,
	scope *consolidationScope,
) error {
	if wire.Metrics.ProductionCandidates != len(candidates.items) ||
		wire.Metrics.CanonicalFacts != len(facts) ||
		wire.Metrics.CoveredFacts+wire.Metrics.UncoveredFacts != len(facts) ||
		wire.Metrics.ProductionQueries != candidates.productionQueries ||
		wire.Metrics.EstimatedSearchJobs != len(candidates.mediaAssets) ||
		wire.Inventory.Proposals != scope.rawProposals ||
		wire.Metrics.RawProposals != scope.rawProposals {
		return fmt.Errorf("semantic consolidation: metrics are inconsistent with the manifest")
	}
	if wire.Inventory.CanonicalFacts != len(facts) || wire.Inventory.Proposals != scope.rawProposals {
		return fmt.Errorf("semantic consolidation: inventory is inconsistent with the manifest")
	}
	return nil
}

func projectConsolidationView(
	wire *semanticEngineConsolidation,
	candidates []scriptgen.SemanticProductionCandidate,
	facts []scriptgen.SemanticConsolidatedFact,
	dates []scriptgen.SemanticConsolidatedDate,
	references []scriptgen.SemanticReferenceNumber,
	links []scriptgen.SemanticEventLink,
	scenes []scriptgen.SemanticConsolidationSceneStats,
) *scriptgen.SemanticVisualConsolidation {
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
	}
}

// candidateOwnsGroupScene reports whether the candidate is anchored in the
// scene that declares the group. A composition may span scenes, so every
// declared scene counts, not only the primary one.
func candidateOwnsGroupScene(candidate semanticEngineProductionCandidate, declaringScenes map[string]struct{}) bool {
	if _, ok := declaringScenes[candidate.SceneID]; ok {
		return true
	}
	for _, sceneID := range candidate.SceneIDs {
		if _, ok := declaringScenes[sceneID]; ok {
			return true
		}
	}
	return false
}

// indexSemanticGroupScenes maps every declared semantic group to the single
// scene that declares it. A group id shared by two scenes is ambiguous: an
// event link names a group and only the scene of its target member, so the
// link could silently point at another scene's composition. Fail closed
// instead of guessing which scene the group belongs to.
func indexSemanticGroupScenes(bundles map[string]*scriptgen.SemanticCandidateBundle) (map[string]map[string]struct{}, error) {
	index := make(map[string]map[string]struct{}, len(bundles))
	for sceneID, bundle := range bundles {
		if bundle == nil {
			continue
		}
		for _, group := range bundle.Groups {
			if strings.TrimSpace(group.ID) == "" {
				return nil, fmt.Errorf("semantic consolidation: scene %q declares a group without id", sceneID)
			}
			scenes, ok := index[group.ID]
			if !ok {
				index[group.ID] = map[string]struct{}{sceneID: {}}
				continue
			}
			scenes[sceneID] = struct{}{}
			if len(scenes) > 1 {
				return nil, fmt.Errorf("semantic consolidation: group id %q is declared by more than one scene", group.ID)
			}
		}
	}
	return index, nil
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
	// Components are validated in the order the worker sent them: the query
	// text must equal the sent-order join. Composing by source byte order
	// instead would reject a valid subject-first query whenever the subject
	// span sits after the target span in the sentence (e.g. "fascicolo di
	// X"): the same sent-order contract as the per-scene contextual
	// validator.
	components := append([]semanticEngineQueryComponent(nil), query.Components...)
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
