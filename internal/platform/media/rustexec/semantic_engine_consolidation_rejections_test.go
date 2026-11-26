package rustexec

import (
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// validConsolidationWire is the smallest manifest that passes every
// consolidation check: one grounded candidate, one canonical fact and one
// query. Each rejection case mutates exactly one invariant.
func validConsolidationWire() (*semanticEngineConsolidation, map[string]string, map[string]*scriptgen.SemanticCandidateBundle) {
	texts := map[string]string{"s1": "Alpha beta gamma."}
	bundles := map[string]*scriptgen.SemanticCandidateBundle{
		"s1": {
			IdentityIDs: []string{"ent_alpha"},
			Groups:      []scriptgen.SemanticGroup{{ID: "g1", Type: "chronological"}},
		},
	}
	wire := &semanticEngineConsolidation{
		Schema:    "visual_consolidation.v1",
		Inventory: semanticEngineConsolidationInventory{CanonicalFacts: 1},
		ProductionCandidates: []semanticEngineProductionCandidate{{
			ID: "c1", Recipe: "r1", Rank: 1, SceneID: "s1",
			Evidence: "Alpha", StartByte: 0, EndByte: 5,
			EntityIDs: []string{"ent_alpha"}, GroupIDs: []string{"g1"},
			AssetKey: "asset-1", DedupKey: "dedup-1", RequiresMedia: true,
			Queries:        []semanticEngineQuery{{Kind: "keyword", Text: "Alpha", Origin: "literal"}},
			CoveredFactIDs: []string{"f1"},
		}},
		Facts: []semanticEngineConsolidatedFact{{
			ID: "f1", Evidence: "Alpha", SceneIDs: []string{"s1"},
			Representation: &semanticEngineRepresentation{CandidateID: "c1", Recipe: "r1"},
		}},
		Metrics: semanticEngineConsolidationMetrics{
			ProductionCandidates: 1, CanonicalFacts: 1, CoveredFacts: 1,
			ProductionQueries: 1, EstimatedSearchJobs: 1,
		},
	}
	return wire, texts, bundles
}

func TestProjectSemanticConsolidationFailsClosedOnEveryInvariant(t *testing.T) {
	withSecondGroupScene := func(w *semanticEngineConsolidation, bundles map[string]*scriptgen.SemanticCandidateBundle) {
		bundles["s2"] = &scriptgen.SemanticCandidateBundle{
			Groups: []scriptgen.SemanticGroup{{ID: "g2", Type: "chronological"}},
		}
		w.ProductionCandidates[0].GroupIDs = []string{"g2"}
	}

	cases := []struct {
		name   string
		mutate func(*semanticEngineConsolidation, map[string]string, map[string]*scriptgen.SemanticCandidateBundle)
		want   string
	}{
		{
			name: "unsupported schema",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Schema = "visual_consolidation.v0"
			},
			want: "unsupported schema",
		},
		{
			name: "duplicate canonical fact",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Facts = append(w.Facts, w.Facts[0])
			},
			want: "duplicate canonical fact",
		},
		{
			name: "candidate identity incomplete",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].ID = ""
			},
			want: "candidate identity is incomplete",
		},
		{
			name: "candidate rank is not positive",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].Rank = 0
			},
			want: "invalid rank",
		},
		{
			name: "duplicate candidate id",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				duplicate := w.ProductionCandidates[0]
				duplicate.DedupKey = "dedup-2"
				w.ProductionCandidates = append(w.ProductionCandidates, duplicate)
			},
			want: "duplicate candidate id",
		},
		{
			name: "duplicate candidate dedup key",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				duplicate := w.ProductionCandidates[0]
				duplicate.ID = "c2"
				w.ProductionCandidates = append(w.ProductionCandidates, duplicate)
			},
			want: "duplicate candidate dedup key",
		},
		{
			name: "candidate references unknown scene",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].SceneID = "s2"
			},
			want: "references unknown scene",
		},
		{
			name: "candidate evidence is not grounded",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].Evidence = "Beta"
			},
			want: "evidence is not grounded",
		},
		{
			name: "candidate references unknown identity",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].EntityIDs = []string{"ent_missing"}
			},
			want: "references unknown identity",
		},
		{
			name: "candidate references unknown group",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].GroupIDs = []string{"g_missing"}
			},
			want: "references unknown group",
		},
		{
			name: "candidate references another scene's group",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, bundles map[string]*scriptgen.SemanticCandidateBundle) {
				withSecondGroupScene(w, bundles)
			},
			want: "of another scene",
		},
		{
			name: "candidate covers unknown fact",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].CoveredFactIDs = []string{"f_missing"}
			},
			want: "covers unknown fact",
		},
		{
			name: "candidate query is not grounded",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].Queries[0].Text = "Delta"
			},
			want: "query \"Delta\" is not grounded",
		},
		{
			name: "candidate repeats a query",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].Queries = append(w.ProductionCandidates[0].Queries, w.ProductionCandidates[0].Queries[0])
			},
			want: "repeats query",
		},
		{
			name: "media candidate has no query",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].Queries = nil
			},
			want: "has no query",
		},
		{
			name: "non-media candidate carries queries",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].RequiresMedia = false
			},
			want: "carries external queries",
		},
		{
			name: "fact has no evidence",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Facts[0].Evidence = ""
			},
			want: "has no evidence",
		},
		{
			name: "fact references unknown scene",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Facts[0].SceneIDs = []string{"s2"}
			},
			want: "fact \"f1\" references unknown scene",
		},
		{
			name: "fact evidence is not grounded",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Facts[0].Evidence = "Delta"
			},
			want: "evidence is not grounded in any of its scenes",
		},
		{
			name: "fact is neither represented nor explained",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Facts[0].Representation = nil
				w.Facts[0].UncoveredReason = nil
			},
			want: "neither represented nor explained",
		},
		{
			name: "fact references unknown candidate",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Facts[0].Representation.CandidateID = "c_missing"
			},
			want: "references unknown candidate",
		},
		{
			name: "candidate does not declare the fact it represents",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ProductionCandidates[0].CoveredFactIDs = nil
			},
			want: "does not declare fact",
		},
		{
			name: "date has no normalized value",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Dates = []semanticEngineConsolidatedDate{{}}
			},
			want: "date without normalized value",
		},
		{
			name: "date span is not grounded",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Dates = []semanticEngineConsolidatedDate{{
					NormalizedDate: "2020",
					Spans: []struct {
						SceneID   string `json:"scene_id"`
						Surface   string `json:"surface"`
						StartByte int    `json:"start_byte"`
						EndByte   int    `json:"end_byte"`
					}{{SceneID: "s1", Surface: "Delta", StartByte: 0, EndByte: 5}},
				}}
			},
			want: "date span \"Delta\" is not grounded",
		},
		{
			name: "reference number is not grounded",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.ReferenceNumbers = []semanticEngineReferenceNumber{{
					SceneID: "s1", Surface: "Delta", StartByte: 0, EndByte: 5,
				}}
			},
			want: "reference number \"Delta\" is not grounded",
		},
		{
			name: "event link kind is unsupported",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.EventLinks = []semanticEngineEventLink{{
					ID: "el1", GroupID: "g1", SceneID: "s1", Kind: "follows",
					Evidence: "Alpha", StartByte: 0, EndByte: 5,
				}}
			},
			want: "unsupported event link kind",
		},
		{
			name: "event link references unknown group",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.EventLinks = []semanticEngineEventLink{{
					ID: "el1", GroupID: "g_missing", SceneID: "s1", Kind: "precedes",
					Evidence: "Alpha", StartByte: 0, EndByte: 5,
				}}
			},
			want: "event link references unknown group",
		},
		{
			name: "event link points at another scene's group",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, bundles map[string]*scriptgen.SemanticCandidateBundle) {
				withSecondGroupScene(w, bundles)
				w.ProductionCandidates[0].GroupIDs = []string{"g1"}
				w.EventLinks = []semanticEngineEventLink{{
					ID: "el1", GroupID: "g2", SceneID: "s1", Kind: "precedes",
					Evidence: "Alpha", StartByte: 0, EndByte: 5,
				}}
			},
			want: "of another scene",
		},
		{
			name: "event link is not grounded",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.EventLinks = []semanticEngineEventLink{{
					ID: "el1", GroupID: "g1", SceneID: "s1", Kind: "precedes",
					Evidence: "Delta", StartByte: 0, EndByte: 5,
				}}
			},
			want: "event link \"el1\" is not grounded",
		},
		{
			name: "scene stats reference unknown scene",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Scenes = []semanticEngineConsolidationSceneStats{{SceneID: "s2"}}
			},
			want: "scene stats reference unknown scene",
		},
		{
			name: "metrics are inconsistent",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Metrics.ProductionCandidates = 2
			},
			want: "metrics are inconsistent",
		},
		{
			name: "inventory is inconsistent",
			mutate: func(w *semanticEngineConsolidation, _ map[string]string, _ map[string]*scriptgen.SemanticCandidateBundle) {
				w.Inventory.CanonicalFacts = 2
			},
			want: "inventory is inconsistent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire, texts, bundles := validConsolidationWire()
			tc.mutate(wire, texts, bundles)
			if _, err := projectSemanticConsolidation(texts, wire, bundles); err == nil {
				t.Fatal("expected the consolidation gate to fail closed")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestProjectSemanticConsolidationAcceptsTheGroundedBaseline(t *testing.T) {
	wire, texts, bundles := validConsolidationWire()
	projected, err := projectSemanticConsolidation(texts, wire, bundles)
	if err != nil {
		t.Fatalf("grounded baseline must be accepted: %v", err)
	}
	if len(projected.ProductionCandidates) != 1 || len(projected.Facts) != 1 {
		t.Fatalf("projection = %d candidates / %d facts, want 1/1", len(projected.ProductionCandidates), len(projected.Facts))
	}
}
