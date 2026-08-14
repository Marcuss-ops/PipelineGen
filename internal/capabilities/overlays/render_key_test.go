package overlays

import "testing"

func TestRenderKeyStableAndChangesWithInputs(t *testing.T) {
	p := OverlayPlan{SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v", Width: 1920, Height: 1080, FPS: 30}
	i := OverlayItem{ID: "o", TemplateID: "entity-card@1", StartMs: 10, EndMs: 20, Text: "Ada", AssetRefs: []OverlayAssetRef{{SHA256: "ABC"}}}
	a, b := RenderKey(p, i), RenderKey(p, i)
	if a == "" || a != b {
		t.Fatalf("render key not stable: %q %q", a, b)
	}
	i.Text = "Grace"
	if RenderKey(p, i) == a {
		t.Fatal("render key did not change when text changed")
	}
}

func TestOverlayPlanValidateComputesMissingKeys(t *testing.T) {
	p := &OverlayPlan{SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v", Width: 1920, Height: 1080, FPS: 30, Items: []OverlayItem{{ID: "o", TemplateID: "card", StartMs: 0, EndMs: 1000}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Items[0].RenderKey == "" {
		t.Fatal("missing render key was not computed")
	}
}

func TestValidateResultRejectsStalePlan(t *testing.T) {
	p := &OverlayPlan{SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v", Width: 1920, Height: 1080, FPS: 30, Items: []OverlayItem{{ID: "o", TemplateID: "card", StartMs: 0, EndMs: 1000}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	r := RenderResult{SchemaVersion: SchemaVersionResult, PlanID: p.PlanID, PlanFingerprint: "stale", OverlayID: "o", RenderKey: p.Items[0].RenderKey}
	if err := ValidateResultForPlan(*p, r); err == nil {
		t.Fatal("stale plan result was accepted")
	}
}
