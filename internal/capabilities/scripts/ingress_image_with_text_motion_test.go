package scriptgeneration

import (
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestIngressImageWithTextMotionPoolSurvives(t *testing.T) {
	raw := []byte(`{"correlation_id":"ingress-image-text-pool","items":[{
		"id":"probe","project":"probe","title":"Probe","language":"it",
		"image_with_text_motions":["image_25d_card_swing"],
		"source":{"type":"text","topic":"Probe","source_text":"Probe text for ingress."}
	}]}`)
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Items) != 1 || len(env.Items[0].ImageWithTextMotions) != 1 ||
		env.Items[0].ImageWithTextMotions[0] != "image_25d_card_swing" {
		t.Fatalf("envelope lost image_with_text_motions: %+v", env.Items)
	}
	req, err := BuildGenerateRequest(&env, "ingress-image-text-pool")
	if err != nil {
		t.Fatal(err)
	}
	if len(req.ImageWithTextMotions) != 1 || req.ImageWithTextMotions[0] != "image_25d_card_swing" {
		t.Fatalf("request lost image_with_text_motions: %+v", req.ImageWithTextMotions)
	}
}
