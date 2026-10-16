package scriptgeneration

import (
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestIngressPhraseMotionFamilySurvives pins the end-to-end ingress chain for
// the phrase motion family: envelope JSON → BuildGenerateRequest. The r46
// debug run proved every link must be covered — a family dropped anywhere
// between payload and planner silently collapses long phrases onto the
// Apple-heavy default catalog walk.
func TestIngressPhraseMotionFamilySurvives(t *testing.T) {
	raw := []byte(`{"correlation_id":"ingress-family-probe","items":[{` +
		`"id":"probe","project":"probe","title":"Probe","language":"it",` +
		`"phrase_motion_family":"modern_product_v1",` +
		`"animation_counts":{"important_phrase":10,"short_important_phrase":5,"maps":1},` +
		`"source":{"type":"text","topic":"Probe","source_text":"Probe text for ingress."},` +
		`"script_params":{"target_words":50,"min_words":10},` +
		`"output":{"save_to_db":false,"extract_entities":false,"generate_metadata":false}` +
		`}]}`)
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Items) != 1 || env.Items[0].PhraseMotionFamily != "modern_product_v1" {
		t.Fatalf("envelope lost phrase_motion_family: %+v", env.Items)
	}
	req, err := BuildGenerateRequest(&env, "ingress-family-probe")
	if err != nil {
		t.Fatal(err)
	}
	if req.PhraseMotionFamily != "modern_product_v1" {
		t.Fatalf("BuildGenerateRequest lost phrase_motion_family: %q", req.PhraseMotionFamily)
	}
}
