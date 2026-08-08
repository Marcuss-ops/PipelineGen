package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestScriptGenerationBranchRecorder_RecordsNormalizedLabels(t *testing.T) {
	recorder := NewScriptGenerationBranchRecorder()
	before := gatherScriptBranchCounter(t, "a", "IT")
	recorder.RecordScriptGenerationBranch("A", "it-IT")
	after := gatherScriptBranchCounter(t, "a", "IT")
	if got := after - before; got != 1 {
		t.Fatalf("counter delta = %v, want 1", got)
	}
}

func TestScriptGenerationBranchRecorder_EmptyBranchIsIgnored(t *testing.T) {
	recorder := NewScriptGenerationBranchRecorder()
	before := gatherScriptBranchCounter(t, "", "IT")
	recorder.RecordScriptGenerationBranch("", "it-IT")
	after := gatherScriptBranchCounter(t, "", "IT")
	if got := after - before; got != 0 {
		t.Fatalf("empty branch delta = %v, want 0", got)
	}
}

func gatherScriptBranchCounter(t *testing.T, branch, country string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "script_generation_branch_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["branch"] == branch && labels["country"] == country {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}
