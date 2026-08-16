package schema

import "testing"

func TestCanonicalGoldenQueries(t *testing.T) {
	queries := CanonicalGoldenQueries()
	if len(queries) != 5 {
		t.Fatalf("canonical golden queries = %d, want 5", len(queries))
	}
	for _, q := range queries {
		if q.Text == "" {
			t.Fatalf("golden query with empty text: %+v", q)
		}
	}
	// The canonical order is stable.
	if queries[0].Text != "Jackie Chan interview" || queries[4].Text != "boxing interview" {
		t.Fatalf("unexpected canonical query order: %q ... %q", queries[0].Text, queries[4].Text)
	}
}

func TestCertifyGoldenDeterminism_Pass(t *testing.T) {
	// 5 queries, 10 runs each, identical ordered top-10 IDs per query.
	top10 := []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J"}
	results := make([][][]string, 5)
	for qi := range results {
		results[qi] = make([][]string, GoldenQueryRunCount)
		for ri := range results[qi] {
			results[qi][ri] = top10
		}
	}
	if err := CertifyGoldenDeterminism(results); err != nil {
		t.Fatalf("CertifyGoldenDeterminism = %v, want nil", err)
	}
}

func TestCertifyGoldenDeterminism_FailsOnDrift(t *testing.T) {
	results := [][][]string{
		{
			{"A", "B", "C"},
			{"A", "C", "B"}, // order drift on run 1
		},
	}
	if err := CertifyGoldenDeterminism(results); err == nil {
		t.Fatal("CertifyGoldenDeterminism must fail on ordered-ID drift")
	}
}

func TestCertifyGoldenDeterminism_RequiresTwoRuns(t *testing.T) {
	results := [][][]string{
		{{"A", "B", "C"}}, // single run
	}
	if err := CertifyGoldenDeterminism(results); err == nil {
		t.Fatal("CertifyGoldenDeterminism must require at least 2 runs")
	}
}

func TestCertifyGoldenDeterminism_EmptyFails(t *testing.T) {
	if err := CertifyGoldenDeterminism(nil); err == nil {
		t.Fatal("CertifyGoldenDeterminism(nil) must fail")
	}
}
