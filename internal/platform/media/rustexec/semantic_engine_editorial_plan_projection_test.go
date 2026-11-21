package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// cannedEditorialPlanRunner answers the full-plan compile with a fixed worker
// response so the adapter projection can be exercised without the binary.
type cannedEditorialPlanRunner struct{ response []byte }

func (r cannedEditorialPlanRunner) Run(context.Context, string, []byte, int64) ([]byte, []byte, error) {
	return r.response, nil, nil
}

const editorialPlanEntityRefResponse = `{
  "ok": true,
  "schema_version": "editorial_plan.v2",
  "plan": {
    "schema_version": "editorial_plan.v2",
    "plan_id": "plan-projection",
    "video_id": "video-projection",
    "language": "it",
    "width": 1920,
    "height": 1080,
    "fps_num": 24,
    "fps_den": 1,
    "duration_ms": 2000,
    "items": [
      {
        "id": "card-ada",
        "scene_id": "s1",
        "entity_id": "ent-ada",
        "entity_ref": {
          "entity_id": "ent-ada",
          "type": "PERSON",
          "name": "Ada Lovelace",
          "canonical_entity_id": "person:ada-lovelace"
        },
        "kind": "entity_image",
        "start_ms": 0,
        "end_ms": 1000,
        "template_id": "image_popup"
      },
      {
        "id": "phrase-1",
        "scene_id": "s1",
        "kind": "text_phrase",
        "start_ms": 1000,
        "end_ms": 2000,
        "template_id": "IMPORTANT_PHRASE",
        "text": "ciao"
      }
    ]
  }
}`

// TestCompileOverlayPlanV2ProjectsPlannerEntityRef keeps the two contracts
// apart: the planner's canonical identity arrives on the v2 response and is
// projected onto the in-memory OverlayItem, while the renderer item keeps its
// exact JSON shape (EntityRef stays out of the overlay-plan.v1 wire).
func TestCompileOverlayPlanV2ProjectsPlannerEntityRef(t *testing.T) {
	executor := &Executor{binaryPath: "stub", runner: cannedEditorialPlanRunner{response: []byte(editorialPlanEntityRefResponse)}}
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"schema_version":"editorial_plan.v2"}`)
	plan, err := adapter.CompileOverlayPlanV2(context.Background(), request)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(plan.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(plan.Items))
	}
	card := plan.Items[0]
	if card.EntityRef == nil {
		t.Fatal("entity_ref was dropped during projection")
	}
	if card.EntityRef.CanonicalEntityID != "person:ada-lovelace" || card.EntityRef.Name != "Ada Lovelace" || card.EntityRef.Type != "PERSON" {
		t.Fatalf("entity_ref projected as %+v", *card.EntityRef)
	}
	if plan.Items[1].EntityRef != nil {
		t.Fatalf("an item without entity_ref must not invent one: %+v", *plan.Items[1].EntityRef)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("projected plan must validate: %v", err)
	}
	wire, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "entity_ref") || strings.Contains(string(wire), "Ada Lovelace") {
		t.Fatalf("overlay-plan.v1 item leaked the producer-only identity: %s", wire)
	}
}
