package rustexec

import (
	"fmt"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func validateSemanticScriptViews(texts map[string]string, graph *scriptgen.SemanticStoryGraph, coverage *scriptgen.SemanticVisualCoverage, bundles map[string]*scriptgen.SemanticCandidateBundle) error {
	identities := map[string]bool{}
	for _, bundle := range bundles {
		for _, identity := range bundle.Identities {
			identities[identity.ID] = true
		}
	}
	factFor := func(sceneID, factID string) *scriptgen.SemanticEntityFact {
		if bundle := bundles[sceneID]; bundle != nil {
			for i := range bundle.EntityFacts {
				if bundle.EntityFacts[i].ID == factID {
					return &bundle.EntityFacts[i]
				}
			}
		}
		return nil
	}
	if graph != nil {
		nodes := map[string]scriptgen.SemanticStoryNode{}
		for _, node := range graph.Nodes {
			source, ok := texts[node.SceneID]
			if !ok || node.ID == "" || node.Label == "" || !groundedSpan(source, node.StartByte, node.EndByte, node.Evidence) {
				return fmt.Errorf("story graph node %q is invalid or ungrounded", node.ID)
			}
			if _, duplicate := nodes[node.ID]; duplicate {
				return fmt.Errorf("story graph repeats node %q", node.ID)
			}
			anchor := false
			for _, sceneID := range node.SceneIDs {
				if _, ok := texts[sceneID]; !ok {
					return fmt.Errorf("story graph node %q references unknown scene", node.ID)
				}
				anchor = anchor || sceneID == node.SceneID
			}
			if !anchor {
				return fmt.Errorf("story graph node %q omits its anchor scene", node.ID)
			}
			if node.EntityID == nil || !identities[*node.EntityID] {
				return fmt.Errorf("story graph node %q references unknown identity", node.ID)
			}
			switch node.Kind {
			case "identity":
				groundedMention := false
				for _, identity := range bundles[node.SceneID].Identities {
					if identity.ID != *node.EntityID {
						continue
					}
					for _, mention := range identity.Mentions {
						groundedMention = groundedMention || (mention.SceneID == node.SceneID && mention.Start == node.StartByte && mention.End == node.EndByte && mention.Surface == node.Evidence)
					}
				}
				if !groundedMention {
					return fmt.Errorf("story identity %q is not a source mention", node.ID)
				}
				if node.FactID != nil {
					return fmt.Errorf("story identity %q carries a fact id", node.ID)
				}
			case "fact":
				if node.FactID == nil {
					return fmt.Errorf("story fact %q omits fact id", node.ID)
				}
				fact := factFor(node.SceneID, *node.FactID)
				if fact == nil || fact.SubjectEntityID != *node.EntityID || fact.Evidence != node.Evidence || fact.EvidenceStart != node.StartByte || fact.EvidenceEnd != node.EndByte || node.SentenceIndex == nil || *node.SentenceIndex != fact.SentenceIndex {
					return fmt.Errorf("story fact %q does not match its source fact", node.ID)
				}
			default:
				return fmt.Errorf("story graph node %q has invalid kind", node.ID)
			}
			nodes[node.ID] = node

		}
		edges := map[string]bool{}
		for _, edge := range graph.Edges {
			from, fromOK := nodes[edge.FromID]
			to, toOK := nodes[edge.ToID]
			if edge.ID == "" || edges[edge.ID] || !fromOK || !toOK || edge.FromID == edge.ToID || edge.CrossScene != (from.SceneID != to.SceneID) || !groundedSpan(texts[to.SceneID], edge.StartByte, edge.EndByte, edge.Evidence) {
				return fmt.Errorf("story graph edge %q is invalid or ungrounded", edge.ID)
			}
			if (edge.Kind != "about" && edge.Kind != "chronological") || to.Kind != "fact" || (edge.Kind == "about" && from.Kind != "identity") || (edge.Kind == "chronological" && from.Kind != "fact") || *from.EntityID != *to.EntityID {
				return fmt.Errorf("story graph edge %q has inconsistent endpoints", edge.ID)
			}
			edges[edge.ID] = true
		}
	}
	if coverage != nil {
		total, covered := 0, 0
		scenes := map[string]bool{}
		for _, scene := range coverage.Scenes {
			bundle, ok := bundles[scene.SceneID]
			if !ok || scenes[scene.SceneID] {
				return fmt.Errorf("visual coverage references unknown or duplicate scene %q", scene.SceneID)
			}
			scenes[scene.SceneID] = true
			seen := map[string]bool{}
			for _, item := range scene.Facts {
				fact := factFor(scene.SceneID, item.FactID)
				if fact == nil || seen[item.FactID] || item.Predicate != fact.Predicate || item.SubjectEntityID != fact.SubjectEntityID || item.Importance != fact.Importance || item.SentenceIndex != fact.SentenceIndex || item.Evidence != fact.Evidence || item.StartByte != fact.Start || item.EndByte != fact.End {
					return fmt.Errorf("visual coverage fact %q does not match its source", item.FactID)
				}
				if (item.Status != "covered" && item.Status != "uncovered") || (item.Status == "covered") != (len(item.ProposalTypes) > 0) {
					return fmt.Errorf("visual coverage fact %q has inconsistent status", item.FactID)
				}
				seen[item.FactID] = true
				total++
				if item.Status == "covered" {
					covered++
				}
			}
			if len(seen) != len(bundle.EntityFacts) {
				return fmt.Errorf("visual coverage omits facts in %q", scene.SceneID)
			}
		}
		if len(scenes) != len(bundles) || coverage.TotalFacts != total || coverage.CoveredFacts != covered {
			return fmt.Errorf("visual coverage totals or scenes are incomplete")
		}
	}
	return nil
}
