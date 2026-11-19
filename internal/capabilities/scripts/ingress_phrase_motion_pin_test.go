package scriptgeneration

import (
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestIngressPhraseMotionPinsSurvive(t *testing.T) {
	raw := []byte(`{"correlation_id":"ingress-phrase-pin","items":[{` +
		`"id":"probe","project":"probe","title":"Probe","language":"it",` +
		`"short_phrase_motion":"phrase_apple_clean_01_blur_soft_reveal",` +
		`"source":{"type":"text","topic":"Probe","source_text":"Probe text for ingress."}` +
		`}]}`)
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Items) != 1 ||
		env.Items[0].ShortPhraseMotion != "phrase_apple_clean_01_blur_soft_reveal" {
		t.Fatalf("envelope lost the phrase motion pin: %+v", env.Items)
	}
	req, err := BuildGenerateRequest(&env, "ingress-phrase-pin")
	if err != nil {
		t.Fatal(err)
	}
	if req.ShortPhraseMotion != "phrase_apple_clean_01_blur_soft_reveal" {
		t.Fatalf("request lost the phrase motion pin: short=%q", req.ShortPhraseMotion)
	}
}
