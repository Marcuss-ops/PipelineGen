package animreg

import (
	"reflect"
	"testing"
)

func TestUnsupportedTextMotionIsExcludedFromAutomaticSelection(t *testing.T) {
	registry := Canonical()
	entry, ok := registry.byID["isometric_plane_fold"]
	if !ok {
		t.Fatal("isometric_plane_fold must remain catalogued for auditability")
	}
	if entry.Status != "active" || entry.RendererSupported {
		t.Fatalf("isometric_plane_fold metadata = status %q, renderer_supported=%v; want active but not selectable until its GPU text path is fixed", entry.Status, entry.RendererSupported)
	}
	for _, group := range []string{"phrase.modern_apple", "phrase.all", "phrase.long"} {
		for _, id := range registry.Candidates(group) {
			if id == entry.ID {
				t.Errorf("unsupported motion %q leaked into automatic group %q", id, group)
			}
		}
	}
}

// TestItemMotionGroupsAreRegistryAuthored pins the overlay-kind → group mapping
// to the registry document. The mapping lives in `item_motion_groups.by_kind`,
// never in a Go switch, so an authored kind must resolve to exactly its authored
// groups and an unauthored kind must resolve to nil (callers then fall back to
// the default set).
func TestItemMotionGroupsAreRegistryAuthored(t *testing.T) {
	cases := map[string][]string{
		"text_phrase":      {"phrase.all", "phrase.short", "bullet"},
		"important_phrase": {"phrase.all", "phrase.short", "bullet"},
		"bullet":           {"phrase.all", "phrase.short", "bullet"},
		"chart":            {"data"},
		"data":             {"data"},
		"shape":            {"phrase.all", "image.all", "data"},
		"logo":             {"image.all", "image.lightleak", "phrase.all"},
		"image":            {"image.all", "image.lightleak", "phrase.all"},
		"entity_image":     {"image.all", "image.lightleak", "phrase.all"},
		"image_popup":      {"image.all", "image.lightleak", "phrase.all"},
		"product":          {"image.all", "image.lightleak", "phrase.all"},
		"chapter_card":     {"phrase.all", "bullet"},
		"map":              {"image.map_centered"},
	}
	authored := Canonical().itemMotionGroups.ByKind
	if len(authored) != len(cases) {
		t.Fatalf("registry authors %d item kinds, test pins %d: a kind was added or removed without pinning it here", len(authored), len(cases))
	}
	for kind, want := range cases {
		if got := ItemMotionGroups(kind); !reflect.DeepEqual(got, want) {
			t.Errorf("ItemMotionGroups(%q) = %v, want %v", kind, got, want)
		}
		if got := ItemMotionGroups("  " + kind + " "); !reflect.DeepEqual(got, want) {
			t.Errorf("ItemMotionGroups must normalize the kind spelling, got %v for %q", got, kind)
		}
	}
	if got := ItemMotionGroups("definitely_not_a_kind"); got != nil {
		t.Errorf("unauthored kind must resolve to nil so the caller falls back, got %v", got)
	}
	if got := ItemDefaultMotionGroups(); len(got) == 0 {
		t.Error("the default item group set must not be empty")
	} else if !reflect.DeepEqual(got, []string{"phrase.all", "phrase.short", "image.all", "image.lightleak", "entity.caption", "entity.date", "entity.metric", "data", "bullet"}) {
		t.Errorf("default item groups = %v", got)
	}
}

// TestItemMotionGroupsReturnCopies keeps the pre-built index internal: a caller
// mutating the returned slice must not corrupt the registry for everyone else.
func TestItemMotionGroupsReturnCopies(t *testing.T) {
	groups := ItemMotionGroups("map")
	if len(groups) == 0 {
		t.Fatal("map kind must author at least one group")
	}
	groups[0] = "mutated_by_caller"
	if ItemMotionGroups("map")[0] == "mutated_by_caller" {
		t.Error("ItemMotionGroups handed out the registry index itself")
	}
	defaults := ItemDefaultMotionGroups()
	defaults[0] = "mutated_by_caller"
	if ItemDefaultMotionGroups()[0] == "mutated_by_caller" {
		t.Error("ItemDefaultMotionGroups handed out the registry index itself")
	}
}

// TestNumberMotionGroupsFollowTemplateRules pins the ordered template rules: the
// first rule whose token appears in the upper-cased template wins, and the
// fallback covers everything else.
func TestNumberMotionGroupsFollowTemplateRules(t *testing.T) {
	cases := []struct {
		template string
		want     []string
	}{
		{"date_card", []string{"entity.date"}},
		{"DATE_range", []string{"entity.date"}},
		{"metric_big", []string{"entity.metric"}},
		{"stat_block", []string{"entity.metric"}},
		{"plain_number", []string{"entity.date", "entity.metric", "phrase.all"}},
		{"", []string{"entity.date", "entity.metric", "phrase.all"}},
	}
	for _, tc := range cases {
		if got := ItemNumberMotionGroups(tc.template); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ItemNumberMotionGroups(%q) = %v, want %v", tc.template, got, tc.want)
		}
	}
}

// TestChartUpgradeIsRegistryAuthored pins the chart upgrade data and the fact
// that the chart/data kinds already resolve to the same group.
func TestChartUpgradeIsRegistryAuthored(t *testing.T) {
	chartGroups := ItemChartGroups()
	if !reflect.DeepEqual(chartGroups, []string{"data"}) {
		t.Fatalf("chart groups = %v, want [data]", chartGroups)
	}
	if got := ItemChartParams(); !reflect.DeepEqual(got, []string{"chart_bar", "chart_card"}) {
		t.Fatalf("chart params = %v, want [chart_bar chart_card]", got)
	}
	for _, kind := range []string{"chart", "data"} {
		if got := ItemMotionGroups(kind); !reflect.DeepEqual(got, chartGroups) {
			t.Errorf("kind %q must already resolve to the chart group, got %v", kind, got)
		}
	}
}

// TestVisualIntentsTableIsRegistryAuthored pins the semantic-type table to the
// registry document: the preset families and the editorial priorities are data,
// so the planner's typed vocabulary must be a pure projection of them.
func TestVisualIntentsTableIsRegistryAuthored(t *testing.T) {
	intents := VisualIntents()
	if len(intents) != 18 {
		t.Fatalf("visual intents = %d, want the 18 canonical semantic types", len(intents))
	}
	want := map[string]VisualIntentSpec{
		"PERSON":           {Kind: "ENTITY_IMAGE", Family: "person_image", Priority: 80},
		"ORGANIZATION":     {Kind: "ORGANIZATION_CARD", Family: "organization", Priority: 50},
		"LOCATION":         {Kind: "LOCATION_CARD", Family: "location", Priority: 55},
		"DATE":             {Kind: "DATE_BADGE", Family: "date", Priority: 60},
		"MONEY":            {Kind: "IMPORTANT_NUMBER", Family: "money", Priority: 90},
		"NUMBER":           {Kind: "IMPORTANT_NUMBER", Family: "number", Priority: 70},
		"PERCENTAGE":       {Kind: "IMPORTANT_NUMBER", Family: "percentage", Priority: 88},
		"IMPORTANT_PHRASE": {Kind: "IMPORTANT_TEXT", Family: "important_phrase", Priority: 100},
		"QUOTE":            {Kind: "QUOTE_CARD", Family: "quote", Priority: 75},
		"CLAIM":            {Kind: "IMPORTANT_TEXT", Family: "claim", Priority: 72},
		"STATISTIC":        {Kind: "IMPORTANT_NUMBER", Family: "statistic", Priority: 85},
		"RANKING":          {Kind: "RANKING_BADGE", Family: "ranking", Priority: 75},
		"TITLE":            {Kind: "TITLE_CARD", Family: "title", Priority: 70},
		"EVENT":            {Kind: "EVENT_BADGE", Family: "event", Priority: 50},
		"IMAGE_ENTITY":     {Kind: "ENTITY_IMAGE", Family: "image_entity", Priority: 60},
		"PRODUCT":          {Kind: "ENTITY_IMAGE", Family: "product", Priority: 65},
		"TIME":             {Kind: "IMPORTANT_NUMBER", Family: "time", Priority: 58},
		"CHAPTER":          {Kind: "CHAPTER_CARD", Family: "chapter_card", Priority: 68},
	}
	for semanticType, spec := range want {
		if got := intents[semanticType]; got != spec {
			t.Errorf("visual intent %q = %+v, want %+v", semanticType, got, spec)
		}
	}
	intents["PERSON"] = VisualIntentSpec{Kind: "mutated_by_caller"}
	if VisualIntents()["PERSON"].Kind == "mutated_by_caller" {
		t.Error("VisualIntents handed out the registry table itself")
	}
}
