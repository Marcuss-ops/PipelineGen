package rustexec

import (
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

const searchFeedbackSource = "Alberto Stasi torna nel racconto del procedimento. La Corte d'Assise conferma la condanna."

func searchFeedbackBundles() map[string]*scriptgen.SemanticCandidateBundle {
	return map[string]*scriptgen.SemanticCandidateBundle{
		"s01": {
			Proposals: []scriptgen.SemanticVisualProposal{{
				ID: "s01_visual_001", Type: "entity_search",
				VisualQuerySuggestions: []string{"Alberto Stasi", "Alberto Stasi torna nel racconto", searchFeedbackSource[:strings.Index(searchFeedbackSource, ".")]},
			}},
		},
	}
}

func searchFeedbackWire(queries ...semanticEngineQuery) *semanticEngineSearchFeedback {
	return &semanticEngineSearchFeedback{Entries: []semanticEngineSearchFeedbackEntry{{
		ProposalID: "s01_visual_001", SceneID: "s01", Queries: queries, Status: "pending",
	}}}
}

// TestSearchFeedbackGroundsExactIdentityAcrossScenes pins the cross-scene
// grounding rule: the consolidation can anchor a composition in one scene
// while the identity it searches is introduced by another. The exact identity
// is script-grounded; source phrases and sentences stay scene-grounded.
func TestSearchFeedbackGroundsExactIdentityAcrossScenes(t *testing.T) {
	bundles := map[string]*scriptgen.SemanticCandidateBundle{
		"s01": {Proposals: []scriptgen.SemanticVisualProposal{{
			ID: "s01_visual_001", Type: "entity_search", VisualQuerySuggestions: []string{"Alberto Stasi"},
		}}},
	}
	feedback, err := projectSemanticSearchFeedback(
		map[string]string{
			"s01": "Il caso è approdato in aula.",
			"s02": "Alberto Stasi parla ai giornalisti.",
		},
		searchFeedbackWire(semanticEngineQuery{Kind: "exact_mention", Text: "Alberto Stasi"}),
		bundles,
	)
	if err != nil {
		t.Fatalf("a cross-scene identity must stay admissible: %v", err)
	}
	if feedback == nil || len(feedback.Entries) != 1 || len(feedback.Entries[0].Queries) != 1 {
		t.Fatalf("feedback = %+v, want the exact identity query", feedback)
	}
}

func TestSearchFeedbackStillRejectsInventedIdentity(t *testing.T) {
	bundles := map[string]*scriptgen.SemanticCandidateBundle{
		"s01": {Proposals: []scriptgen.SemanticVisualProposal{{
			ID: "s01_visual_001", Type: "entity_search", VisualQuerySuggestions: []string{"Mario Rossi"},
		}}},
	}
	_, err := projectSemanticSearchFeedback(
		map[string]string{"s01": "Il caso è approdato in aula.", "s02": "Alberto Stasi parla."},
		searchFeedbackWire(semanticEngineQuery{Kind: "exact_mention", Text: "Mario Rossi"}),
		bundles,
	)
	if err == nil || !strings.Contains(err.Error(), "not a source slice") {
		t.Fatalf("an identity absent from the whole script must be rejected, got %v", err)
	}
}

func TestSearchFeedbackProjectsTheExactSourceLadder(t *testing.T) {
	sentence := searchFeedbackSource[:strings.Index(searchFeedbackSource, ".")]
	feedback, err := projectSemanticSearchFeedback(
		map[string]string{"s01": searchFeedbackSource},
		searchFeedbackWire(
			semanticEngineQuery{Kind: "exact_mention", Text: "Alberto Stasi"},
			semanticEngineQuery{Kind: "context_phrase", Text: "Alberto Stasi torna nel racconto"},
			semanticEngineQuery{Kind: "source_sentence", Text: sentence},
		),
		searchFeedbackBundles(),
	)
	if err != nil {
		t.Fatalf("valid ladder rejected: %v", err)
	}
	if feedback == nil || len(feedback.Entries) != 1 {
		t.Fatalf("feedback not projected: %+v", feedback)
	}
	entry := feedback.Entries[0]
	if entry.ProposalID != "s01_visual_001" || entry.SceneID != "s01" || entry.Status != "pending" || entry.Retry != nil {
		t.Fatalf("entry identity is wrong: %+v", entry)
	}
	if len(entry.Queries) != 3 || entry.Queries[0].Kind != "exact_mention" || entry.Queries[2].Text != sentence {
		t.Fatalf("ladder lost its rungs: %+v", entry.Queries)
	}
}

func TestSearchFeedbackFailsClosedOnADanglingProposal(t *testing.T) {
	wire := searchFeedbackWire(semanticEngineQuery{Kind: "exact_mention", Text: "Alberto Stasi"})
	wire.Entries[0].ProposalID = "s01_visual_999"
	if _, err := projectSemanticSearchFeedback(map[string]string{"s01": searchFeedbackSource}, wire, searchFeedbackBundles()); err == nil {
		t.Fatal("an entry for an unknown proposal must fail closed")
	}
	wire = searchFeedbackWire(semanticEngineQuery{Kind: "exact_mention", Text: "Alberto Stasi"})
	wire.Entries[0].SceneID = "s99"
	if _, err := projectSemanticSearchFeedback(map[string]string{"s01": searchFeedbackSource}, wire, searchFeedbackBundles()); err == nil {
		t.Fatal("an entry for an unknown scene must fail closed")
	}
}

func TestSearchFeedbackRejectsUngroundedOrUndeclaredQueries(t *testing.T) {
	cases := map[string]struct {
		query      semanticEngineQuery
		wantReason string
	}{
		"not_a_source_slice": {query: semanticEngineQuery{Kind: "exact_mention", Text: "Mario Rossi"}, wantReason: "not a source slice"},
		"never_suggested":    {query: semanticEngineQuery{Kind: "context_phrase", Text: "La Corte"}, wantReason: "never suggested"},
		"unknown_kind":       {query: semanticEngineQuery{Kind: "entity_search", Text: "Alberto Stasi"}, wantReason: "unknown query kind"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := projectSemanticSearchFeedback(
				map[string]string{"s01": searchFeedbackSource},
				searchFeedbackWire(testCase.query),
				searchFeedbackBundles(),
			)
			if err == nil || !strings.Contains(err.Error(), testCase.wantReason) {
				t.Fatalf("want %q, got %v", testCase.wantReason, err)
			}
		})
	}
}

func TestSearchFeedbackRejectsABackwardsWideningLadder(t *testing.T) {
	_, err := projectSemanticSearchFeedback(
		map[string]string{"s01": searchFeedbackSource},
		searchFeedbackWire(
			semanticEngineQuery{Kind: "source_sentence", Text: "Alberto Stasi torna nel racconto"},
			semanticEngineQuery{Kind: "exact_mention", Text: "Alberto Stasi"},
		),
		searchFeedbackBundles(),
	)
	if err == nil || !strings.Contains(err.Error(), "widens backwards") {
		t.Fatalf("a narrower rung after a wider one must fail closed, got %v", err)
	}
}

func TestSearchFeedbackRejectsPlannerOwnedStatuses(t *testing.T) {
	wire := searchFeedbackWire(semanticEngineQuery{Kind: "exact_mention", Text: "Alberto Stasi"})
	wire.Entries[0].Status = "satisfied"
	if _, err := projectSemanticSearchFeedback(map[string]string{"s01": searchFeedbackSource}, wire, searchFeedbackBundles()); err == nil {
		t.Fatal("the engine may only emit pending entries")
	}
	wire = searchFeedbackWire(semanticEngineQuery{Kind: "exact_mention", Text: "Alberto Stasi"})
	wire.Entries[0].Retry = &semanticEngineSearchRetry{Action: "skip_visual", Reason: "x"}
	if _, err := projectSemanticSearchFeedback(map[string]string{"s01": searchFeedbackSource}, wire, searchFeedbackBundles()); err == nil {
		t.Fatal("the engine may not report a planner retry")
	}
}
