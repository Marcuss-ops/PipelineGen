package rustexec

import "testing"

func TestProjectSemanticChartsAcceptsDeltaComparison(t *testing.T) {
	source := "Prima 10% poi 12%"
	out, err := projectSemanticCharts(source, []semanticEngineProposal{{
		ProposalType: "delta_comparison",
		ChartItems: []semanticEngineChartItem{
			{Text: "10%", Value: 10, StartByte: 6, EndByte: 9},
			{Text: "12%", Value: 12, StartByte: 14, EndByte: 17},
		},
	}})
	if err != nil {
		t.Fatalf("projectSemanticCharts: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("charts = %d, want 2", len(out))
	}
	if out[0].Text != "10%" || out[0].Value != 10 || out[1].Text != "12%" || out[1].Value != 12 {
		t.Fatalf("unexpected chart candidates: %+v", out)
	}
}

func TestProjectSemanticChartsDedupesAValueSharedWithTheChartProposal(t *testing.T) {
	source := "Prima 10% poi 12%"
	items := []semanticEngineChartItem{
		{Text: "10%", Value: 10, StartByte: 6, EndByte: 9},
		{Text: "12%", Value: 12, StartByte: 14, EndByte: 17},
	}
	out, err := projectSemanticCharts(source, []semanticEngineProposal{
		{ProposalType: "chart", ChartItems: items},
		{ProposalType: "delta_comparison", ChartItems: items},
	})
	if err != nil {
		t.Fatalf("projectSemanticCharts: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("charts = %d, want 2 unique values: %+v", len(out), out)
	}
}

func TestProjectSemanticChartsStillSkipsNonChartProposals(t *testing.T) {
	source := "Prima 10% poi 12%"
	out, err := projectSemanticCharts(source, []semanticEngineProposal{{
		ProposalType: "timeline",
		ChartItems: []semanticEngineChartItem{
			{Text: "10%", Value: 10, StartByte: 6, EndByte: 9},
		},
	}})
	if err != nil {
		t.Fatalf("projectSemanticCharts: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("charts = %d, want 0", len(out))
	}
}
