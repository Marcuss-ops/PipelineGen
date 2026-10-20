package rustexec

import (
	"context"
	"encoding/json"
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Tests pin the Go-owned sampler selection policy wiring: the data-driven
// keyword tables, the optional diversity weight, the candidate embedding
// signal, and the pre-bound ledger labels must travel EXACTLY when the
// composition root configures them — and must stay absent (worker V1
// curated defaults) when it does not.

func samplerPolicyFixture() config.SamplerPolicyConfig {
	return config.SamplerPolicyConfig{
		MismatchedSubjects: []string{"goalkeeper", "boxing"},
		CompatibleKeywords: []string{"alpine", "mountain"},
		ActionVerbs:        []string{"crossing"},
		ContextKeywords:    []string{"summit"},
		DiversityWeight:    0.05,
	}
}

func TestMediaSamplerPolicyTablesTravelOnSelectAndGroup(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(a *MediaSamplerAdapter) error
		want mediaSamplerSelectionPolicy
	}{
		{
			name: "select",
			run: func(a *MediaSamplerAdapter) error {
				_, _, err := a.SampleScene(context.Background(), "seg", "mountain climb", nil, nil, false)
				return err
			},
		},
		{
			name: "select_group",
			run: func(a *MediaSamplerAdapter) error {
				_, _, err := a.SelectGroup(context.Background(), "seg", "mountain climb", nil, nil, false, 3, nil)
				return err
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := &semanticRunner{output: []byte(`{"results":[],"winner_id":"a"}`)}
			a, _ := NewMediaSamplerWithPolicy(executorForSemantic(r), NewSamplerPolicy(samplerPolicyFixture()))
			runErr := tt.run(a)
			if runErr != nil {
				t.Fatal(runErr)
			}
			var raw map[string]any
			if err := json.Unmarshal(r.input, &raw); err != nil {
				t.Fatal(err)
			}
			// Tables must REPLACE the worker defaults on the wire: present,
			// wholesale, and bound to the configured values.
			tables, ok := raw["mismatched_subjects"].([]any)
			if !ok || len(tables) != 2 {
				t.Fatalf("mismatched_subjects missing on the wire: %s", r.input)
			}
			if _, present := raw["diversity_weight"]; !present {
				t.Fatalf("diversity_weight missing on the wire: %s", r.input)
			}
			for field, want := range map[string][]any{
				"compatible_keywords": {any("alpine"), any("mountain")},
				"action_verbs":        {any("crossing")},
				"context_keywords":    {any("summit")},
			} {
				got, _ := raw[field].([]any)
				if len(got) != len(want) {
					t.Fatalf("%s = %v, want %v", field, got, want)
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("%s[%d] = %v, want %v", field, i, got[i], want[i])
					}
				}
			}
			if weight, _ := raw["diversity_weight"].(float64); weight != 0.05 {
				t.Fatalf("diversity_weight = %v, want 0.05", weight)
			}
			_ = tt.want
		})
	}
}

func TestMediaSamplerEmptyPolicyKeepsWireWithoutTablesOrWeight(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":"a"}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	if _, _, err := a.SampleScene(context.Background(), "seg", "greek salad", nil, nil, false); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(r.input, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"mismatched_subjects", "compatible_keywords", "action_verbs",
		"context_keywords", "diversity_weight",
	} {
		if _, present := raw[field]; present {
			t.Fatalf("%s must stay absent for an empty policy: %s", field, r.input)
		}
	}
}

func TestMediaSamplerConfiguredZeroDiversityWeightDefersToWorkerDefault(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":"a"}`)}
	policy := NewSamplerPolicy(config.SamplerPolicyConfig{DiversityWeight: 0})
	a, _ := NewMediaSamplerWithPolicy(executorForSemantic(r), policy)
	if _, _, err := a.SampleScene(context.Background(), "seg", "greek salad", nil, nil, false); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(r.input, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["diversity_weight"]; present {
		t.Fatalf("configured zero diversity_weight must defer to the worker default: %s", r.input)
	}
}

func TestMediaSamplerCandidateEmbeddingTravelsOnlyWhenInRange(t *testing.T) {
	var wireInput []byte
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":"a"}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	// Capture via the executor capture pattern: run Sample with in-range and
	// out-of-range embeddings and inspect the last wire payload.
	for _, value := range []float64{0.83, 1.4, 0} {
		r.input = nil
		_, err := a.Sample(context.Background(), "seg", "greek salad", nil,
			[]scriptpkg.SegmentAssetCandidate{{
				AssetID: "cand-1", Entity: "salad bowl",
				RelevanceScore: 0.6, EmbeddingSimilarity: value,
			}},
			false)
		if err != nil {
			t.Fatal(err)
		}
		wireInput = r.input
		var candidates []mediaSamplerCandidate
		var wire struct {
			Candidates []mediaSamplerCandidate `json:"candidates"`
		}
		if err := json.Unmarshal(wireInput, &wire); err != nil {
			t.Fatal(err)
		}
		candidates = wire.Candidates
		if len(candidates) != 1 {
			t.Fatalf("candidates=%d for value %v", len(candidates), value)
		}
		emitted := candidates[0].EmbeddingSimilarity != nil
		inRange := value > 0 && value <= 1
		if emitted != inRange {
			t.Fatalf("embedding %v traveled=%v, want %v", value, emitted, inRange)
		}
		if emitted && *candidates[0].EmbeddingSimilarity != float32(value) {
			t.Fatalf("embedding value drift: %v", *candidates[0].EmbeddingSimilarity)
		}
	}
}

func TestMediaSamplerGroupLedgerCarriesTheWinnerLabel(t *testing.T) {
	r := &semanticRunner{output: []byte(`{"results":[],"winner_id":"a","group_ids":["a"]}`)}
	a, _ := NewMediaSamplerAdapter(executorForSemantic(r))
	_, err := a.SampleGroup(context.Background(), "seg", "greek salad", nil,
		[]scriptpkg.SegmentAssetCandidate{{AssetID: "a", Entity: "salad"}},
		false, 3,
		[]scriptports.AssetBinding{{AssetID: "old-asset", SceneID: "scene-00", Label: "greek salad close up"}})
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		BoundAssets []mediaSamplerBoundAsset `json:"bound_assets"`
	}
	if err := json.Unmarshal(r.input, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.BoundAssets) != 1 || raw.BoundAssets[0].Label != "greek salad close up" {
		t.Fatalf("bound label missing on the wire: %s", r.input)
	}
}
