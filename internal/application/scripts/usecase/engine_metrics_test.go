package usecase

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestExtractCountryForTelemetry_BCP47_Composite(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"it-IT", "it-IT", "IT"},
		{"en-US", "en-US", "US"},
		{"pt-BR", "pt-BR", "BR"},
		{"es-ES", "es-ES", "ES"},
		{"fr-FR", "fr-FR", "FR"},
		{"de-DE", "de-DE", "DE"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractCountryForTelemetry(tc.in); got != tc.want {
				t.Fatalf("ExtractCountryForTelemetry(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractCountryForTelemetry_Fallback(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"language only it", "it", "IT"},
		{"language only zh upper", "ZH", "ZH"},
		{"language only zh lower", "zh", "ZH"},
		{"empty sentinel", "", "XX"},
		{"whitespace sentinel", "   ", "XX"},
		{"bcp47 trim whitespace", "  en-US  ", "US"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractCountryForTelemetry(tc.in); got != tc.want {
				t.Fatalf("ExtractCountryForTelemetry(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRecordScriptGenerationBranch_CounterIncrements_BranchA(t *testing.T) {
	before := readCounter(t, "script_generation_branch_total", "a", "IT")

	RecordScriptGenerationBranch("a", "it-IT")

	after := readCounter(t, "script_generation_branch_total", "a", "IT")

	delta := after - before
	if delta != 1 {
		t.Fatalf("Branch A increment delta = %v, want 1 (before=%v after=%v)", delta, before, after)
	}
}

func TestRecordScriptGenerationBranch_CounterIncrements_BranchB(t *testing.T) {
	before := readCounter(t, "script_generation_branch_total", "b", "BR")

	RecordScriptGenerationBranch("b", "pt-BR")

	after := readCounter(t, "script_generation_branch_total", "b", "BR")

	delta := after - before
	if delta != 1 {
		t.Fatalf("Branch B increment delta = %v, want 1 (before=%v after=%v)", delta, before, after)
	}
}

func TestRecordScriptGenerationBranch_EmptyBranch_SilentDrop(t *testing.T) {
	// Defensive: empty branch is silently dropped (no increment, no panic).
	RecordScriptGenerationBranch("", "it-IT")
	// If we reach here without panic, the test passes.
}

func TestRecordScriptGenerationBranch_TwoCallsCumulative(t *testing.T) {
	before := readCounter(t, "script_generation_branch_total", "a", "IT")
	RecordScriptGenerationBranch("a", "it-IT")
	RecordScriptGenerationBranch("a", "it-IT")
	after := readCounter(t, "script_generation_branch_total", "a", "IT")
	delta := after - before
	if delta != 2 {
		t.Fatalf("Two Branch A calls cumulative delta = %v, want 2", delta)
	}
}

// readCounter returns the current value of the CounterVec sample
// (name, labelValues...). Uses prometheus.DefaultGatherer to evaluate
// the counter at the (name, *labels) tuple — canonical Prometheus test
// pattern, no extra dependency.
func readCounter(t *testing.T, name string, labelValues ...string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := m.GetLabel()
			if len(labels) != len(labelValues) {
				continue
			}
			match := true
			for i, lv := range labelValues {
				if labels[i].GetValue() != lv {
					match = false
					break
				}
			}
			if match {
				c := m.GetCounter()
				if c == nil {
					t.Fatalf("metric %s has non-counter shape", name)
				}
				return c.GetValue()
			}
		}
	}
	return 0 // metric not yet recorded — first call return 0 (Prometheus semantics)
}
