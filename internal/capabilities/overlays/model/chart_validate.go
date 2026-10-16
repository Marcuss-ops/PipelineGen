package model

import (
	"fmt"
	"strings"
)

// validateChartItem is the model-side guard extracted from the chart planner:
// it lives with the types it validates so the plan's fail-closed invariant
// stays in one package. Only a SHAPE item may attempt a chart payload, and
// charts must be lowered to rect/text primitives by the planner.
// validateChartItem prevents unsupported shape.chart and pseudo-chart payloads
// from passing plan validation and reaching the queue. Supported charts must
// already have been lowered to ordinary rect/text items by BuildChartItems.
func validateChartItem(item OverlayItem) error {
	if strings.TrimSpace(item.TemplateID) != "SHAPE" {
		return nil
	}
	shape, _ := item.Params["shape"].(string)
	if shape == "chart" {
		return fmt.Errorf("overlay plan: item %q uses unsupported shape.chart; lower charts to rect/text primitives", item.ID)
	}
	if _, hasChart := item.Params["chart"]; hasChart {
		return fmt.Errorf("overlay plan: item %q carries unsupported chart payload; lower charts to rect/text primitives", item.ID)
	}
	return nil
}
