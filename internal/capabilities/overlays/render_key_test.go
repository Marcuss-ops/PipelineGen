package overlays

import "testing"

func TestRenderKeyStableAndChangesWithInputs(t *testing.T) {
	p := OverlayPlan{SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1}
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

func TestRenderKeyTracksRenderingFieldsAndIgnoresProducerIdentity(t *testing.T) {
	p := OverlayPlan{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, RendererVersion: "r1"}
	base := OverlayItem{ID: "o", SceneID: "s1", TemplateID: "card", StartMs: 1, EndMs: 2,
		StartUS: 1001, DurationUS: 999, Text: "hello", Params: map[string]any{"font_family": "inter"},
		AssetRefs: []OverlayAssetRef{{SHA256: "abc"}}, CaptionFontFamily: "inter_bold"}
	key := RenderKey(p, base)
	if key == "" {
		t.Fatal("empty render key")
	}

	mutations := map[string]func(*OverlayPlan, *OverlayItem){
		"composite child caption font": func(_ *OverlayPlan, i *OverlayItem) {
			i.ImageLayers = []OverlayImageLayer{{ID: "child", AssetID: "asset", Caption: "Ada", CaptionFontFamily: "poppins"}}
		},
		"composite child caption color": func(_ *OverlayPlan, i *OverlayItem) {
			i.ImageLayers = []OverlayImageLayer{{ID: "child", AssetID: "asset", Caption: "Ada", CaptionColor: "#AABBCC"}}
		},
		"template":          func(_ *OverlayPlan, i *OverlayItem) { i.TemplateID = "other" },
		"timing":            func(_ *OverlayPlan, i *OverlayItem) { i.DurationUS++ },
		"font params":       func(_ *OverlayPlan, i *OverlayItem) { i.Params["font_family"] = "bolder" },
		"asset identity":    func(_ *OverlayPlan, i *OverlayItem) { i.AssetRefs[0].SHA256 = "def" },
		"caption font":      func(_ *OverlayPlan, i *OverlayItem) { i.CaptionFontFamily = "inter" },
		"render dimensions": func(p *OverlayPlan, _ *OverlayItem) { p.Width++ },
		"renderer version":  func(p *OverlayPlan, _ *OverlayItem) { p.RendererVersion = "r2" },
	}
	for name, mutate := range mutations {
		p2, i2 := p, base
		i2.Params = map[string]any{"font_family": "inter"}
		i2.AssetRefs = append([]OverlayAssetRef(nil), base.AssetRefs...)
		mutate(&p2, &i2)
		if got := RenderKey(p2, i2); got == key {
			t.Errorf("%s mutation left render key unchanged", name)
		}
	}
	base.SceneID = "another-scene"
	base.EntityRef = &OverlayEntityRef{EntityID: "producer-only"}
	if got := RenderKey(p, base); got != key {
		t.Errorf("producer-only metadata changed render key: %s != %s", got, key)
	}
}

func TestOverlayPlanFingerprintRecomputedAfterRenderMutation(t *testing.T) {
	p := &OverlayPlan{SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Items: []OverlayItem{{ID: "o", TemplateID: "card", StartMs: 0, EndMs: 1000}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	sealed := p.Fingerprint
	p.Items[0].Text = "changed"
	p.Items[0].RenderKey = ""
	if err := p.Validate(); err == nil {
		t.Fatal("stale fingerprint accepted after render mutation")
	}
	p.Fingerprint = ""
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Fingerprint == sealed {
		t.Fatal("fingerprint did not change after render mutation")
	}
}

func TestOverlayPlanValidateComputesMissingKeys(t *testing.T) {
	p := &OverlayPlan{SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, Items: []OverlayItem{{ID: "o", TemplateID: "card", StartMs: 0, EndMs: 1000}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Items[0].RenderKey == "" {
		t.Fatal("missing render key was not computed")
	}
}

func TestValidateResultRejectsStalePlan(t *testing.T) {
	p := &OverlayPlan{SchemaVersion: SchemaVersionPlan, PlanID: "p", VideoID: "v", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, Items: []OverlayItem{{ID: "o", TemplateID: "card", StartMs: 0, EndMs: 1000}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	r := RenderResult{SchemaVersion: SchemaVersionResult, PlanID: p.PlanID, PlanFingerprint: "stale", OverlayID: "o", RenderKey: p.Items[0].RenderKey}
	if err := ValidateResultForPlan(*p, r); err == nil {
		t.Fatal("stale plan result was accepted")
	}
}
