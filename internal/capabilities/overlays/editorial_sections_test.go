package overlays

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEditorialSectionsHaveStableOrderAndDistinctIDs(t *testing.T) {
	sections := EditorialSections()
	want := []EditorialSectionID{
		EditorialSectionEntityTextImage,
		EditorialSectionSingleImage,
		EditorialSectionImageStack,
		EditorialSectionImportantPhrase,
		EditorialSectionShortPhrase,
		EditorialSectionNumbersAndDates,
		EditorialSectionMap,
		EditorialSectionOther,
	}
	if len(sections) != len(want) {
		t.Fatalf("sections=%d, want %d: %+v", len(sections), len(want), sections)
	}
	seen := make(map[EditorialSectionID]bool, len(sections))
	for i, section := range sections {
		if section.ID != want[i] || section.Order <= 0 || section.Title == "" {
			t.Errorf("section[%d]=%+v, want id %q and nonempty title/order", i, section, want[i])
		}
		if seen[section.ID] {
			t.Errorf("duplicate section id %q", section.ID)
		}
		seen[section.ID] = true
		if i > 0 && sections[i-1].Order >= section.Order {
			t.Errorf("sections are not ordered: %d before %d", sections[i-1].Order, section.Order)
		}
	}
	sections[0].Title = "mutated"
	if EditorialSections()[0].Title == "mutated" {
		t.Fatal("caller mutated the canonical section catalog")
	}
}

func TestEditorialSectionForItemUsesRenderShapeNotDuplicateKinds(t *testing.T) {
	cases := []struct {
		name string
		item OverlayItem
		want EditorialSectionID
	}{
		{"portrait with caption", OverlayItem{Kind: "entity_image", TemplateID: "image_popup", EntityCaption: "Ada Lovelace", AssetRefs: []OverlayAssetRef{{AssetID: "portrait"}}}, EditorialSectionEntityTextImage},
		{"entity image and text", OverlayItem{Kind: "entity_image", TemplateID: "image_popup", Text: "Ada Lovelace", AssetRefs: []OverlayAssetRef{{AssetID: "portrait"}}}, EditorialSectionEntityTextImage},
		{"single portrait image without caption", OverlayItem{Kind: "entity_image", TemplateID: "image_popup", AssetRefs: []OverlayAssetRef{{AssetID: "portrait"}}}, EditorialSectionSingleImage},
		{"single image", OverlayItem{Kind: "entity_image", TemplateID: "image_popup", AssetRefs: []OverlayAssetRef{{AssetID: "image"}}}, EditorialSectionSingleImage},
		{"single generic image", OverlayItem{Kind: "image", TemplateID: "IMAGE_OVERLAY", AssetRefs: []OverlayAssetRef{{AssetID: "image"}}}, EditorialSectionSingleImage},
		{"two image layers", OverlayItem{Kind: "entity_image", TemplateID: "image_popup", ImageLayers: []OverlayImageLayer{{ID: "a"}, {ID: "b"}}}, EditorialSectionImageStack},
		{"three image layers", OverlayItem{Kind: "entity_image", ImageLayers: []OverlayImageLayer{{ID: "a"}, {ID: "b"}, {ID: "c"}}, AssetRefs: []OverlayAssetRef{{AssetID: "a"}, {AssetID: "b"}, {AssetID: "c"}}}, EditorialSectionImageStack},
		{"four image layers", OverlayItem{Kind: "entity_image", ImageLayers: []OverlayImageLayer{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}, AssetRefs: []OverlayAssetRef{{AssetID: "a"}, {AssetID: "b"}, {AssetID: "c"}, {AssetID: "d"}}}, EditorialSectionImageStack},
		{"five image layers", OverlayItem{Kind: "entity_image", ImageLayers: []OverlayImageLayer{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}, AssetRefs: []OverlayAssetRef{{AssetID: "a"}, {AssetID: "b"}, {AssetID: "c"}, {AssetID: "d"}, {AssetID: "e"}}}, EditorialSectionImageStack},
		{"short phrase", OverlayItem{Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Una frase breve"}, EditorialSectionShortPhrase},
		{"important phrase", OverlayItem{Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Una frase editoriale lunga di sei parole"}, EditorialSectionImportantPhrase},
		{"phrase at six words", OverlayItem{Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Uno due tre quattro cinque sei"}, EditorialSectionImportantPhrase},
		{"phrase beyond six words", OverlayItem{Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Uno due tre quattro cinque sei sette"}, EditorialSectionImportantPhrase},
		{"date", OverlayItem{Kind: "number", TemplateID: "TIMELINE_DATE_CARD", Text: "2026"}, EditorialSectionNumbersAndDates},
		{"semantic date", OverlayItem{Kind: "date", TemplateID: "DATE", Text: "October 6, 2026"}, EditorialSectionNumbersAndDates},
		{"metric", OverlayItem{Kind: "number", TemplateID: "METRIC_STAT_CARD", Text: "42"}, EditorialSectionNumbersAndDates},
		{"percent", OverlayItem{Kind: "number", TemplateID: "PERCENT", Text: "42%"}, EditorialSectionNumbersAndDates},
		{"map", OverlayItem{Kind: "map", TemplateID: "MAP", AssetRefs: []OverlayAssetRef{{AssetID: "basemap"}}}, EditorialSectionMap},
		{"other", OverlayItem{Kind: "quote", TemplateID: "QUOTE", Text: "Citazione"}, EditorialSectionOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EditorialSectionForItem(tc.item); got != tc.want {
				t.Fatalf("EditorialSectionForItem()=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestEditorialSectionsDoNotEnterRenderWireOrFingerprint(t *testing.T) {
	plan := OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        "editorial-wire", VideoID: "editorial-wire", Width: 1280, Height: 720, FPSNum: 24, FPSDen: 1,
		Items: []OverlayItem{{ID: "phrase", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "A short phrase", StartMs: 0, EndMs: 1000}},
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	fingerprint := plan.Fingerprint
	renderKey := plan.Items[0].RenderKey
	if got := EditorialSectionForItem(plan.Items[0]); got != EditorialSectionShortPhrase {
		t.Fatalf("phrase section=%q, want short phrase", got)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if plan.Fingerprint != fingerprint || plan.Items[0].RenderKey != renderKey {
		t.Fatalf("derived editor sections changed rendering identity: fingerprint %q->%q render_key %q->%q", fingerprint, plan.Fingerprint, renderKey, plan.Items[0].RenderKey)
	}
	wire, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"editorial_section"`, `"editorial_sections"`, `"short_important_phrase"`} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatalf("editor-only section metadata %q leaked into overlay-plan.v1: %s", forbidden, wire)
		}
	}
}

func TestOrganizeEditorialPlanPlacesEveryItemExactlyOnce(t *testing.T) {
	plan := OverlayPlan{Items: []OverlayItem{
		{ID: "portrait", Kind: "entity_image", TemplateID: "image_popup", EntityCaption: "Ada", AssetRefs: []OverlayAssetRef{{AssetID: "portrait"}}},
		{ID: "image", Kind: "image", TemplateID: "IMAGE_OVERLAY", AssetRefs: []OverlayAssetRef{{AssetID: "image"}}},
		{ID: "stack", Kind: "entity_image", TemplateID: "image_popup", ImageLayers: []OverlayImageLayer{{ID: "a"}, {ID: "b"}}},
		{ID: "phrase", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "La storia cambia"},
		{ID: "headline", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Una storia editoriale molto importante oggi"},
		{ID: "date", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", Text: "2026"},
		{ID: "map", Kind: "map", TemplateID: "MAP"},
		{ID: "quote", Kind: "quote", TemplateID: "QUOTE", Text: "Testo"},
	}}
	sections := OrganizeEditorialPlan(plan)
	if len(sections) != len(EditorialSections()) {
		t.Fatalf("organized section count=%d, want stable catalog size %d", len(sections), len(EditorialSections()))
	}
	wantBySection := map[EditorialSectionID][]string{
		EditorialSectionEntityTextImage: {"portrait"},
		EditorialSectionSingleImage:     {"image"},
		EditorialSectionImageStack:      {"stack"},
		EditorialSectionShortPhrase:     {"phrase"},
		EditorialSectionImportantPhrase: {"headline"},
		EditorialSectionNumbersAndDates: {"date"},
		EditorialSectionMap:             {"map"},
		EditorialSectionOther:           {"quote"},
	}
	counts := make(map[string]int, len(plan.Items))
	for _, section := range sections {
		gotIDs := make([]string, 0, len(section.Items))
		for _, item := range section.Items {
			gotIDs = append(gotIDs, item.ID)
			counts[item.ID]++
		}
		wantIDs := wantBySection[section.ID]
		if len(gotIDs) != len(wantIDs) {
			t.Errorf("section %q items=%v, want %v", section.ID, gotIDs, wantIDs)
			continue
		}
		for i := range gotIDs {
			if gotIDs[i] != wantIDs[i] {
				t.Errorf("section %q item order=%v, want %v", section.ID, gotIDs, wantIDs)
				break
			}
		}
	}
	for _, item := range plan.Items {
		if counts[item.ID] != 1 {
			t.Errorf("plan item %q appears %d times across editor sections", item.ID, counts[item.ID])
		}
	}
	for id, count := range counts {
		if count != 1 {
			t.Errorf("unknown or duplicated plan item %q appears %d times", id, count)
		}
	}
	for _, section := range sections {
		if _, exists := wantBySection[section.ID]; !exists {
			t.Errorf("unexpected section %q", section.ID)
		}
	}
}
