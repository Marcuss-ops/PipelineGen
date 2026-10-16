package model

import (
	"reflect"
	"testing"

	capanimreg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
)

// TestMotionGroupsForItemIsRegistryBacked keeps the admission boundary honest:
// the groups an item may sample from are the registry's `item_motion_groups`
// data, so a registry remap is picked up here without a code change and a kind
// that loses its mapping falls back instead of silently admitting nothing.
func TestMotionGroupsForItemIsRegistryBacked(t *testing.T) {
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
		"map":              {"image.map_centered"},
	}
	for kind, want := range cases {
		if got := motionGroupsForItem(kind, "", nil); !reflect.DeepEqual(got, want) {
			t.Errorf("motionGroupsForItem(%q) = %v, want %v", kind, got, want)
		}
	}
	for kind := range cases {
		if got, want := motionGroupsForItem(kind, "", nil), capanimreg.ItemMotionGroups(kind); !reflect.DeepEqual(got, want) {
			t.Errorf("motionGroupsForItem(%q) = %v but the registry authors %v", kind, got, want)
		}
	}
	if got := motionGroupsForItem("not_a_real_kind", "", nil); !reflect.DeepEqual(got, capanimreg.ItemDefaultMotionGroups()) {
		t.Errorf("unauthored kind must fall back to the registry default, got %v", got)
	}
}

// TestMotionGroupsForItemKeepsNumberTemplateRules pins the number rules the
// validation boundary used to hardcode: template substring decides the group
// triple, first matching rule wins.
func TestMotionGroupsForItemKeepsNumberTemplateRules(t *testing.T) {
	cases := []struct {
		template string
		want     []string
	}{
		{"date_card", []string{"entity.date"}},
		{"metric_card", []string{"entity.metric"}},
		{"stat_card", []string{"entity.metric"}},
		{"plain_card", []string{"entity.date", "entity.metric", "phrase.all"}},
	}
	for _, tc := range cases {
		if got := motionGroupsForItem("number", tc.template, nil); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("motionGroupsForItem(number, %q) = %v, want %v", tc.template, got, tc.want)
		}
	}
}

// TestMotionGroupsForItemChartUpgrade pins the chart upgrade: the registry's
// chart param flags upgrade any shape-like item, a non-bool value must NOT
// upgrade (the params map is untyped), and chart/data kinds are already chart
// group items.
func TestMotionGroupsForItemChartUpgrade(t *testing.T) {
	chartGroups := capanimreg.ItemChartGroups()
	for _, params := range []map[string]any{{"chart_bar": true}, {"chart_card": true}, {"chart_bar": true, "other": 1}} {
		if got := motionGroupsForItem("shape", "", params); !reflect.DeepEqual(got, chartGroups) {
			t.Errorf("shape with %v = %v, want the chart group %v", params, got, chartGroups)
		}
	}
	if got := motionGroupsForItem("shape", "", map[string]any{"chart_bar": "true"}); !reflect.DeepEqual(got, capanimreg.ItemMotionGroups("shape")) {
		t.Errorf("a non-bool chart flag must not upgrade the item, got %v", got)
	}
	if got := motionGroupsForItem("shape", "", map[string]any{"chart_bar": false}); !reflect.DeepEqual(got, capanimreg.ItemMotionGroups("shape")) {
		t.Errorf("a false chart flag must not upgrade the item, got %v", got)
	}
	for _, kind := range []string{"chart", "data"} {
		if got := motionGroupsForItem(kind, "", nil); !reflect.DeepEqual(got, chartGroups) {
			t.Errorf("motionGroupsForItem(%q) = %v, want the chart group %v", kind, got, chartGroups)
		}
	}
}
