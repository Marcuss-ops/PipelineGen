package rustexec

import (
	"fmt"
	"strings"
	"unicode"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func projectSemanticBullets(source string, bullets []semanticEngineCandidate) []scriptgen.BulletCandidate {
	out := make([]scriptgen.BulletCandidate, 0, len(bullets))
	for _, bullet := range bullets {
		// NO EVIDENCE -> NO CANDIDATE: the Rust side already enforced this,
		// and the Go boundary re-checks it before the text can reach a plan.
		if bullet.Evidence == "" || bullet.Text != bullet.Evidence {
			continue
		}
		if !groundedSpan(source, bullet.StartByte, bullet.EndByte, bullet.Evidence) {
			continue
		}
		out = append(out, scriptgen.BulletCandidate{
			Text: bullet.Text, SentenceIndex: bullet.SentenceIndex, Start: bullet.StartByte, End: bullet.EndByte,
			Score: bullet.Score, ID: bullet.ID, Kind: bullet.Kind, Evidence: bullet.Evidence, Provenance: bullet.Provenance,
		})
	}
	return out
}

func projectSemanticEntities(source string, candidates []semanticEngineCandidate) ([]scriptgen.SemanticEntityCandidate, error) {
	out := make([]scriptgen.SemanticEntityCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ID == "" || candidate.Kind == "" || candidate.Evidence == "" ||
			!groundedSpan(source, candidate.StartByte, candidate.EndByte, candidate.Evidence) {
			return nil, fmt.Errorf("entity candidate %q is not grounded in source_text", candidate.Text)
		}
		out = append(out, scriptgen.SemanticEntityCandidate{
			SemanticTextCandidate: scriptgen.SemanticTextCandidate{
				ID: candidate.ID, Text: candidate.Text, Kind: candidate.Kind,
				Evidence: candidate.Evidence, Provenance: candidate.Provenance,
				SentenceIndex: candidate.SentenceIndex, Start: candidate.StartByte, End: candidate.EndByte, Score: candidate.Score,
			},
			IdentityID: candidate.ID,
		})
	}
	return out, nil
}

func projectSemanticEntityFacts(source string, facts []semanticEngineEntityFact) ([]scriptgen.SemanticEntityFact, error) {
	out := make([]scriptgen.SemanticEntityFact, 0, len(facts))
	for _, fact := range facts {
		if fact.ID == "" || fact.SubjectEntityID == "" || fact.Predicate == "" || fact.Value == "" ||
			fact.Evidence == "" || !groundedSpan(source, fact.StartByte, fact.EndByte, fact.Value) ||
			!groundedSpan(source, fact.EvidenceStartByte, fact.EvidenceEndByte, fact.Evidence) {
			return nil, fmt.Errorf("entity fact %q is not grounded in source_text", fact.ID)
		}
		if fact.Status != "asserted" && fact.Status != "negated" && fact.Status != "uncertain" {
			return nil, fmt.Errorf("entity fact %q has invalid status %q", fact.ID, fact.Status)
		}
		if fact.Importance < 0 || fact.Importance > 1 {
			return nil, fmt.Errorf("entity fact %q has importance %.3f outside [0,1]", fact.ID, fact.Importance)
		}
		entry := scriptgen.SemanticEntityFact{
			ID: fact.ID, SubjectEntityID: fact.SubjectEntityID, Predicate: fact.Predicate,
			Value: fact.Value, ValueType: fact.ValueType, Status: fact.Status,
			Evidence: fact.Evidence, SentenceIndex: fact.SentenceIndex, Start: fact.StartByte,
			End: fact.EndByte, EvidenceStart: fact.EvidenceStartByte, EvidenceEnd: fact.EvidenceEndByte,
		}
		if fact.NormalizedValue != nil {
			entry.NormalizedValue = *fact.NormalizedValue
		}
		if fact.TargetEntityID != nil {
			entry.TargetEntityID = *fact.TargetEntityID
		}
		if fact.Unit != nil {
			entry.Unit = *fact.Unit
		}
		if fact.Operator != nil {
			entry.Operator = *fact.Operator
		}
		if fact.NormalizedDate != nil {
			entry.NormalizedDate = *fact.NormalizedDate
		}
		entry.Importance = fact.Importance
		entry.VisualConceptIDs = append([]string(nil), fact.VisualConceptIDs...)
		out = append(out, entry)
	}
	return out, nil
}

func projectSemanticText(source string, candidates []semanticEngineCandidate) ([]scriptgen.SemanticTextCandidate, error) {
	out := make([]scriptgen.SemanticTextCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ID == "" || candidate.Evidence == "" ||
			!groundedSpan(source, candidate.StartByte, candidate.EndByte, candidate.Evidence) {
			return nil, fmt.Errorf("candidate %q is not grounded in source_text", candidate.Text)
		}
		out = append(out, scriptgen.SemanticTextCandidate{
			ID: candidate.ID, Text: candidate.Text, Kind: candidate.Kind,
			Evidence: candidate.Evidence, Provenance: candidate.Provenance,
			SentenceIndex: candidate.SentenceIndex, Start: candidate.StartByte, End: candidate.EndByte, Score: candidate.Score,
		})
	}
	return out, nil
}

func projectSemanticImportantSegments(source string, items []semanticEngineImportantSegment) ([]scriptgen.SemanticImportantSegment, error) {
	out := make([]scriptgen.SemanticImportantSegment, 0, len(items))
	for _, item := range items {
		if item.ID == "" || item.Kind != "important_segment" || item.SentenceStart < 0 || item.SentenceEnd < item.SentenceStart ||
			item.Score < 0 || item.Score > 1 || item.VisualMentions < 0 || item.MeasurementMentions < 0 ||
			!groundedSpan(source, item.StartByte, item.EndByte, item.Evidence) {
			return nil, fmt.Errorf("important segment %q is invalid or not grounded", item.ID)
		}
		out = append(out, scriptgen.SemanticImportantSegment{ID: item.ID, Kind: item.Kind, Evidence: item.Evidence,
			SentenceStart: item.SentenceStart, SentenceEnd: item.SentenceEnd, Start: item.StartByte, End: item.EndByte,
			Score: item.Score, VisualMentions: item.VisualMentions, MeasurementMentions: item.MeasurementMentions})
	}
	return out, nil
}

func projectSemanticQueryComponents(source string, items []semanticEngineQueryComponent) ([]scriptgen.SemanticQueryComponent, error) {
	out := make([]scriptgen.SemanticQueryComponent, 0, len(items))
	for _, item := range items {
		if item.Kind == "" || item.ComponentType == "" || !groundedSpan(source, item.StartByte, item.EndByte, item.Text) {
			return nil, fmt.Errorf("query component %q is invalid or not grounded", item.Text)
		}
		out = append(out, scriptgen.SemanticQueryComponent{Kind: item.Kind, ComponentType: item.ComponentType, Text: item.Text, Start: item.StartByte, End: item.EndByte})
	}
	return out, nil
}

func componentsWithin(items []scriptgen.SemanticQueryComponent, start, end int) bool {
	for i, item := range items {
		if item.Start < start || item.End > end {
			return false
		}
		for _, other := range items[:i] {
			if item.Start < other.End && other.Start < item.End {
				return false
			}
		}
	}
	return true
}

func projectSemanticComposedImageQueries(source string, items []semanticEngineComposedImageQuery) ([]scriptgen.SemanticComposedImageQuery, error) {
	out := make([]scriptgen.SemanticComposedImageQuery, 0, len(items))
	for _, item := range items {
		components, err := projectSemanticQueryComponents(source, item.Components)
		if err != nil {
			return nil, err
		}
		if item.ID == "" || item.Kind != "composed_image_query" || len(components) < 2 || item.SentenceIndex < 0 ||
			!groundedSpan(source, item.StartByte, item.EndByte, item.Query) ||
			!componentsWithin(components, item.StartByte, item.EndByte) {
			return nil, fmt.Errorf("composed query %q is invalid or not grounded", item.ID)
		}
		out = append(out, scriptgen.SemanticComposedImageQuery{ID: item.ID, Kind: item.Kind, Query: item.Query, Start: item.StartByte, End: item.EndByte, SentenceIndex: item.SentenceIndex, Components: components})
	}
	return out, nil
}

func projectSemanticContextualImageQueries(source string, items []semanticEngineContextualImageQuery) ([]scriptgen.SemanticContextualImageQuery, error) {
	out := make([]scriptgen.SemanticContextualImageQuery, 0, len(items))
	for _, item := range items {
		components, err := projectSemanticQueryComponents(source, item.Components)
		if err != nil {
			return nil, err
		}
		joined := make([]string, 0, len(components))
		for _, component := range components {
			joined = append(joined, component.Text)
		}
		if item.ID == "" || item.Kind != "contextual_image_query" || len(components) < 2 || item.SentenceIndex < 0 || item.Query != strings.Join(joined, " ") ||
			!groundedSpan(source, item.EvidenceStartByte, item.EvidenceEndByte, item.Evidence) ||
			!componentsWithin(components, item.EvidenceStartByte, item.EvidenceEndByte) {
			return nil, fmt.Errorf("contextual query %q is invalid or not grounded", item.ID)
		}
		out = append(out, scriptgen.SemanticContextualImageQuery{ID: item.ID, Kind: item.Kind, Query: item.Query, Evidence: item.Evidence,
			EvidenceStart: item.EvidenceStartByte, EvidenceEnd: item.EvidenceEndByte, SentenceIndex: item.SentenceIndex, Components: components})
	}
	return out, nil
}

func projectSemanticIdentities(texts map[string]string, identities []semanticEngineIdentity) ([]scriptgen.SemanticIdentity, error) {
	out := make([]scriptgen.SemanticIdentity, 0, len(identities))
	for _, identity := range identities {
		if identity.ID == "" || identity.Kind == "" || identity.CanonicalSurface == "" {
			return nil, fmt.Errorf("identity has missing id, type, or canonical surface")
		}
		projected := scriptgen.SemanticIdentity{ID: identity.ID, CanonicalSurface: identity.CanonicalSurface, Kind: identity.Kind}
		for _, mention := range identity.Mentions {
			source, ok := texts[mention.SceneID]
			if !ok || mention.Surface == "" || !groundedSpan(source, mention.StartByte, mention.EndByte, mention.Surface) {
				return nil, fmt.Errorf("identity %q mention %q is not grounded in requested scenes", identity.ID, mention.Surface)
			}
			projected.Mentions = append(projected.Mentions, scriptgen.SemanticIdentityMention{
				SceneID: mention.SceneID, Surface: mention.Surface, Start: mention.StartByte, End: mention.EndByte,
			})
		}
		out = append(out, projected)
	}
	return out, nil
}

func projectSemanticRelations(source string, relations semanticEngineRelationView) (scriptgen.SemanticRelationView, error) {
	out := scriptgen.SemanticRelationView{
		CoMentions:        make([]scriptgen.SemanticCoMention, 0, len(relations.CoMentions)),
		AssertedRelations: make([]scriptgen.SemanticAssertedRelation, 0, len(relations.AssertedRelations)),
	}
	for _, relation := range relations.CoMentions {
		if relation.LeftID == "" || relation.RightID == "" || relation.LeftID == relation.RightID ||
			relation.Evidence == "" || !strings.Contains(source, relation.Evidence) {
			return out, fmt.Errorf("co-mention is not grounded in source_text")
		}
		out.CoMentions = append(out.CoMentions, scriptgen.SemanticCoMention{
			LeftID: relation.LeftID, RightID: relation.RightID, Evidence: relation.Evidence, SentenceIndex: relation.SentenceIndex,
		})
	}
	for _, relation := range relations.AssertedRelations {
		if relation.SourceID == "" || relation.TargetID == "" || relation.SourceID == relation.TargetID ||
			relation.Evidence == "" || !strings.Contains(source, relation.Evidence) ||
			!validRelationStatus(relation.Status) {
			return out, fmt.Errorf("asserted relation is invalid or not grounded in source_text")
		}
		predicate := ""
		if relation.Predicate != nil {
			predicate = *relation.Predicate
		}
		out.AssertedRelations = append(out.AssertedRelations, scriptgen.SemanticAssertedRelation{
			SourceID: relation.SourceID, TargetID: relation.TargetID, Predicate: predicate,
			Status: relation.Status, Evidence: relation.Evidence, SentenceIndex: relation.SentenceIndex,
		})
	}
	return out, nil
}

func validRelationStatus(status string) bool {
	return status == "Asserted" || status == "Negated" || status == "Uncertain" ||
		status == "asserted" || status == "negated" || status == "uncertain"
}

func projectSemanticProposals(source string, proposals []semanticEngineProposal) ([]scriptgen.SemanticVisualProposal, error) {
	out := make([]scriptgen.SemanticVisualProposal, 0, len(proposals))
	seenIDs := make(map[string]struct{}, len(proposals))
	for _, proposal := range proposals {
		if proposal.ProposalType == "" || proposal.Evidence == "" ||
			!groundedSpan(source, proposal.StartByte, proposal.EndByte, proposal.Evidence) {
			return nil, fmt.Errorf("visual proposal %q is not grounded in source_text", proposal.ProposalType)
		}
		// The id is the join key of the search-feedback handshake: a proposal
		// without a stable unique id could never report its retrieval outcome.
		id := strings.TrimSpace(proposal.ID)
		if id == "" {
			return nil, fmt.Errorf("visual proposal %q has no stable id", proposal.ProposalType)
		}
		if _, duplicate := seenIDs[id]; duplicate {
			return nil, fmt.Errorf("visual proposal id %q is repeated", id)
		}
		seenIDs[id] = struct{}{}
		projected := scriptgen.SemanticVisualProposal{
			ID:   id,
			Type: proposal.ProposalType, SuggestedVisual: proposal.SuggestedVisual,
			Status: proposal.Status, Evidence: proposal.Evidence, EntityIDs: append([]string(nil), proposal.EntityIDs...),
			TemporalSurfaces:       append([]string(nil), proposal.TemporalSurfaces...),
			VisualQuerySuggestions: append([]string(nil), proposal.VisualQuerySuggestions...), Score: proposal.Score,
			Start: proposal.StartByte, End: proposal.EndByte,
		}
		for _, query := range projected.VisualQuerySuggestions {
			if !groundedQuery(source, query) {
				return nil, fmt.Errorf("visual query %q is not grounded in source_text", query)
			}
		}
		for _, item := range proposal.ChartItems {
			if item.Text == "" || !groundedSpan(source, item.StartByte, item.EndByte, item.Text) {
				return nil, fmt.Errorf("proposal chart item %q is not grounded in source_text", item.Text)
			}
			projected.ChartItems = append(projected.ChartItems, scriptgen.SemanticChartItem{
				Label: item.Label, Text: item.Text, Kind: item.Kind, Value: item.Value, Start: item.StartByte, End: item.EndByte,
			})
		}
		out = append(out, projected)
	}
	return out, nil
}

// scriptGroundingText is the whole-script grounding surface. An
// `exact_mention` query names an identity, and a composition anchored in one
// scene may mention an identity the script introduces in another scene (the
// engine's consolidation groups facts across scenes). Requiring the anchor
// scene to contain the name rejects valid cross-scene compositions; requiring
// the script to contain it still rejects invented surfaces.
func scriptGroundingText(texts map[string]string) string {
	if len(texts) == 0 {
		return ""
	}
	builder := strings.Builder{}
	for _, text := range texts {
		builder.WriteString(text)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func groundedQuery(source, query string) bool {
	if strings.TrimSpace(query) == "" {
		return false
	}
	for _, token := range strings.Fields(query) {
		token = strings.TrimFunc(token, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
		if token != "" && !strings.Contains(strings.ToLower(source), strings.ToLower(token)) {
			return false
		}
	}
	return true
}

func projectSemanticGroups(currentSceneID string, texts map[string]string, groups []semanticEngineGroup) ([]scriptgen.SemanticGroup, error) {
	out := make([]scriptgen.SemanticGroup, 0, len(groups))
	for _, group := range groups {
		if group.ID == "" || group.Title == "" || len(group.Members) < 2 ||
			!validSemanticGroupType(group.GroupType) {
			return nil, fmt.Errorf("group %q has invalid identity, type, or fewer than two members", group.ID)
		}
		if group.TargetCount != nil && *group.TargetCount < len(group.Members) {
			return nil, fmt.Errorf("group %q has more members than its target count", group.ID)
		}
		if group.Complete != nil && *group.Complete && group.TargetCount != nil && len(group.Members) != *group.TargetCount {
			return nil, fmt.Errorf("group %q is marked complete with %d of %d members", group.ID, len(group.Members), *group.TargetCount)
		}
		projected := scriptgen.SemanticGroup{ID: group.ID, Type: group.GroupType, Title: group.Title,
			TargetCount: group.TargetCount, Complete: group.Complete}
		if group.RankingBasis != nil {
			projected.RankingBasis = *group.RankingBasis
		}
		seenRanks := make(map[int]struct{})
		for _, member := range group.Members {
			sceneID := member.SceneID
			if sceneID == "" {
				sceneID = currentSceneID
			}
			source, requested := texts[sceneID]
			if !requested {
				return nil, fmt.Errorf("group %q references unrequested scene %q", group.ID, sceneID)
			}
			if member.CandidateID == "" || member.Text == "" || member.Evidence == "" ||
				!groundedSpan(source, member.StartByte, member.EndByte, member.Evidence) {
				return nil, fmt.Errorf("group %q member %q is not grounded in source_text", group.ID, member.CandidateID)
			}
			// Fact-backed groups carry a paraphrased label plus the verbatim
			// evidence sentence; source-derived groups must keep the two equal.
			if member.Text != member.Evidence && group.GroupType != "step_sequence" && group.GroupType != "fact_comparison" {
				return nil, fmt.Errorf("group %q member %q text is not the verbatim evidence", group.ID, member.CandidateID)
			}
			if member.Rank != nil {
				if *member.Rank < 1 {
					return nil, fmt.Errorf("group %q has invalid rank %d", group.ID, *member.Rank)
				}
				if _, duplicate := seenRanks[*member.Rank]; duplicate {
					return nil, fmt.Errorf("group %q repeats rank %d", group.ID, *member.Rank)
				}
				seenRanks[*member.Rank] = struct{}{}
			}
			if group.GroupType == "ranking" && member.Rank == nil {
				return nil, fmt.Errorf("ranking group %q has an unranked member", group.ID)
			}
			entry := scriptgen.SemanticGroupMember{CandidateID: member.CandidateID, SceneID: sceneID, Rank: member.Rank,
				SentenceIndex: member.SentenceIndex, Text: member.Text, Evidence: member.Evidence,
				Start: member.StartByte, End: member.EndByte}
			if member.TemporalSurface != nil {
				entry.TemporalSurface = *member.TemporalSurface
			}
			if member.NormalizedDate != nil {
				entry.NormalizedDate = *member.NormalizedDate
			}
			projected.Members = append(projected.Members, entry)
		}
		out = append(out, projected)
	}
	return out, nil
}

// projectSemanticMicroSummaries carries the fact-backed phrases across the
// worker boundary. The paraphrased Text is preserved for provenance, but the
// span that planning may act on is always the verbatim Evidence sentence.
func projectSemanticMicroSummaries(source string, summaries []semanticEngineMicroSummary) ([]scriptgen.SemanticMicroSummary, error) {
	out := make([]scriptgen.SemanticMicroSummary, 0, len(summaries))
	for _, summary := range summaries {
		if summary.ID == "" || summary.Text == "" || summary.Evidence == "" ||
			len(summary.EvidenceFactIDs) == 0 || !validMicroSummaryKind(summary.Kind) {
			return nil, fmt.Errorf("micro summary %q has missing id, kind, text, evidence, or fact references", summary.ID)
		}
		if !groundedSpan(source, summary.StartByte, summary.EndByte, summary.Evidence) {
			return nil, fmt.Errorf("micro summary %q evidence is not grounded in source_text", summary.ID)
		}
		entry := scriptgen.SemanticMicroSummary{
			ID: summary.ID, Kind: summary.Kind, Text: summary.Text,
			EvidenceFactIDs: append([]string(nil), summary.EvidenceFactIDs...),
			SentenceIndex:   summary.SentenceIndex, Evidence: summary.Evidence,
			Start: summary.StartByte, End: summary.EndByte, Score: summary.Score,
		}
		if summary.SubjectEntityID != nil {
			entry.SubjectEntityID = *summary.SubjectEntityID
		}
		if summary.NormalizedDate != nil {
			entry.NormalizedDate = *summary.NormalizedDate
		}
		out = append(out, entry)
	}
	return out, nil
}

func validMicroSummaryKind(kind string) bool {
	switch kind {
	case "micro_summary", "fact_comparison", "step_sequence", "timeline_summary":
		return true
	default:
		return false
	}
}

func validSemanticGroupType(kind string) bool {
	switch kind {
	case "chronological", "process", "comparison", "fact_comparison", "step_sequence", "enumeration", "cause_effect", "ranking":
		return true
	default:
		return false
	}
}

func projectSemanticCharts(source string, proposals []semanticEngineProposal) ([]scriptgen.ChartValueCandidate, error) {
	out := make([]scriptgen.ChartValueCandidate, 0)
	for _, proposal := range proposals {
		if proposal.ProposalType != "chart" {
			continue
		}
		for _, item := range proposal.ChartItems {
			// A chart item without verbatim evidence is a contract violation:
			// silently dropping it would hide a broken extractor.
			if item.Text == "" || !groundedSpan(source, item.StartByte, item.EndByte, item.Text) {
				return nil, fmt.Errorf("chart item %q is not grounded in source_text", item.Text)
			}
			out = append(out, scriptgen.ChartValueCandidate{Text: item.Text, Value: item.Value})
		}
	}
	return out, nil
}

func projectSemanticTemporal(source string, facts []semanticEngineTemporal) ([]scriptgen.TemporalFactCandidate, error) {
	out := make([]scriptgen.TemporalFactCandidate, 0, len(facts))
	for _, fact := range facts {
		if fact.Kind != "DATE" {
			return nil, fmt.Errorf("temporal fact %q has kind %q", fact.Surface, fact.Kind)
		}
		if !groundedSpan(source, fact.StartByte, fact.EndByte, fact.Surface) || fact.Evidence != fact.Surface {
			return nil, fmt.Errorf("temporal fact %q is not grounded in source_text", fact.Surface)
		}
		candidate := scriptgen.TemporalFactCandidate{
			Text: fact.Surface, Precision: fact.Precision,
			Start: fact.StartByte, End: fact.EndByte, Ambiguous: fact.Ambiguous,
		}
		if fact.NormalizedDate != nil {
			candidate.NormalizedDate = *fact.NormalizedDate
		}
		if fact.Era != nil {
			candidate.Era = *fact.Era
		}
		out = append(out, candidate)
	}
	return out, nil
}

func projectSemanticNumeric(source string, facts []semanticEngineNumeric) ([]scriptgen.NumericFactCandidate, error) {
	out := make([]scriptgen.NumericFactCandidate, 0, len(facts))
	for _, fact := range facts {
		if !visualNumericKinds[fact.Kind] {
			return nil, fmt.Errorf("numeric fact %q has unknown kind %q", fact.Surface, fact.Kind)
		}
		if !groundedSpan(source, fact.StartByte, fact.EndByte, fact.Surface) || fact.Evidence != fact.Surface {
			return nil, fmt.Errorf("numeric fact %q is not grounded in source_text", fact.Surface)
		}
		candidate := scriptgen.NumericFactCandidate{
			Text: fact.Surface, Kind: fact.Kind, Start: fact.StartByte, End: fact.EndByte,
		}
		if fact.Value != nil {
			if !isFinite(*fact.Value) {
				return nil, fmt.Errorf("numeric fact %q has a non-finite value", fact.Surface)
			}
			candidate.Value = *fact.Value
			candidate.HasValue = true
		}
		out = append(out, candidate)
	}
	return out, nil
}

// groundedSpan checks that evidence is the exact UTF-8 slice at [start, end).
func groundedSpan(source string, start, end int, evidence string) bool {
	return start >= 0 && end > start && end <= len(source) && source[start:end] == evidence
}
