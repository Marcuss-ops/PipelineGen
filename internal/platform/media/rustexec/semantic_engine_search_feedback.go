package rustexec

import (
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// searchQueryWidening is the documented ladder order: the exact identity is
// tried first, then a bounded source phrase, then the full source sentence.
// A co-mentioned composition may repeat `exact_mention` once per name, then
// its own source sentence.
var searchQueryWidening = map[string]int{
	"exact_mention":   0,
	"context_phrase":  1,
	"source_sentence": 2,
}

// projectSemanticSearchFeedback validates the whole-script asset-search
// handshake and projects it into the scripts contract. It fails closed: an
// entry that points at an unknown scene or proposal, a query that is not a
// verbatim source slice, a ladder that widens backwards, a query that the
// proposal itself never suggested, or a status the engine must not write
// (anything but `pending`) is a contract violation, not a planner decision.
func projectSemanticSearchFeedback(
	texts map[string]string,
	wire *semanticEngineSearchFeedback,
	bundles map[string]*scriptgen.SemanticCandidateBundle,
) (*scriptgen.SemanticSearchFeedback, error) {
	if wire == nil {
		return nil, nil
	}
	proposalByScene := make(map[string]map[string]*scriptgen.SemanticVisualProposal, len(bundles))
	for sceneID, bundle := range bundles {
		byID := make(map[string]*scriptgen.SemanticVisualProposal, len(bundle.Proposals))
		for index := range bundle.Proposals {
			proposal := &bundle.Proposals[index]
			byID[proposal.ID] = proposal
		}
		proposalByScene[sceneID] = byID
	}

	scriptText := scriptGroundingText(texts)
	feedback := &scriptgen.SemanticSearchFeedback{Entries: make([]scriptgen.SemanticSearchFeedbackEntry, 0, len(wire.Entries))}
	seen := make(map[string]struct{}, len(wire.Entries))
	for _, entry := range wire.Entries {
		proposalID := strings.TrimSpace(entry.ProposalID)
		sceneID := strings.TrimSpace(entry.SceneID)
		if proposalID == "" || sceneID == "" {
			return nil, fmt.Errorf("semantic search feedback: entry without proposal or scene id")
		}
		source, ok := texts[sceneID]
		if !ok {
			return nil, fmt.Errorf("semantic search feedback: entry %q references unknown scene %q", proposalID, sceneID)
		}
		proposals, ok := proposalByScene[sceneID]
		if !ok {
			return nil, fmt.Errorf("semantic search feedback: entry %q has no analysed scene %q", proposalID, sceneID)
		}
		proposal, ok := proposals[proposalID]
		if !ok {
			return nil, fmt.Errorf("semantic search feedback: entry %q references unknown proposal in scene %q", proposalID, sceneID)
		}
		if _, duplicate := seen[proposalID]; duplicate {
			return nil, fmt.Errorf("semantic search feedback: duplicate entry for proposal %q", proposalID)
		}
		seen[proposalID] = struct{}{}
		if entry.Status != "pending" {
			return nil, fmt.Errorf("semantic search feedback: engine must emit pending, got %q for %q", entry.Status, proposalID)
		}
		if entry.Retry != nil {
			return nil, fmt.Errorf("semantic search feedback: engine must not report a retry for %q", proposalID)
		}
		if len(entry.Queries) == 0 {
			return nil, fmt.Errorf("semantic search feedback: proposal %q has an empty ladder", proposalID)
		}
		suggested := make(map[string]struct{}, len(proposal.VisualQuerySuggestions))
		for _, suggestion := range proposal.VisualQuerySuggestions {
			suggested[suggestion] = struct{}{}
		}
		queries := make([]scriptgen.SemanticSearchQuery, 0, len(entry.Queries))
		seenQueries := make(map[string]struct{}, len(entry.Queries))
		previousKind := -1
		for _, query := range entry.Queries {
			kind := strings.TrimSpace(query.Kind)
			text := strings.TrimSpace(query.Text)
			order, known := searchQueryWidening[kind]
			if !known {
				return nil, fmt.Errorf("semantic search feedback: unknown query kind %q for %q", kind, proposalID)
			}
			if order < previousKind {
				return nil, fmt.Errorf("semantic search feedback: ladder for %q widens backwards at %q", proposalID, kind)
			}
			previousKind = order
			// An exact identity mention may be introduced by a different scene
			// than the proposal's anchor scene; source phrases and sentences
			// must remain verbatim slices of their own scene.
			grounding := source
			if kind == "exact_mention" {
				grounding = scriptText
			}
			if text == "" || !groundedQuery(grounding, text) {
				return nil, fmt.Errorf("semantic search feedback: query %q for %q is not a source slice", text, proposalID)
			}
			if _, ok := suggested[text]; !ok {
				return nil, fmt.Errorf("semantic search feedback: query %q for %q was never suggested by the proposal", text, proposalID)
			}
			if _, duplicate := seenQueries[text]; duplicate {
				return nil, fmt.Errorf("semantic search feedback: proposal %q repeats query %q", proposalID, text)
			}
			seenQueries[text] = struct{}{}
			queries = append(queries, scriptgen.SemanticSearchQuery{
				Kind: kind, Text: text, Origin: query.Origin, Illustrative: query.Illustrative,
			})
		}
		feedback.Entries = append(feedback.Entries, scriptgen.SemanticSearchFeedbackEntry{
			ProposalID: proposalID,
			SceneID:    sceneID,
			Queries:    queries,
			Status:     "pending",
		})
	}
	return feedback, nil
}
