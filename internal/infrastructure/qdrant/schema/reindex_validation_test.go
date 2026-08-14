package schema

import "testing"

func TestReindexValidation_AllZeroPasses(t *testing.T) {
	v := ReindexValidation{
		SQLiteSearchableAssets: 10,
		QdrantPoints:           10,
	}
	if err := v.Validate(); err != nil {
		t.Fatalf("all-zero validation must pass: %v", err)
	}
}

func TestReindexValidation_EachInvariantFailsClosed(t *testing.T) {
	base := func() ReindexValidation {
		return ReindexValidation{SQLiteSearchableAssets: 10, QdrantPoints: 10}
	}

	cases := []struct {
		name   string
		mutate func(*ReindexValidation)
	}{
		{"missing points", func(v *ReindexValidation) { v.MissingPoints = 1 }},
		{"orphan points", func(v *ReindexValidation) { v.OrphanPoints = 1 }},
		{"duplicate canonical ids", func(v *ReindexValidation) { v.DuplicateCanonicalIDs = 1 }},
		{"invalid taxonomy", func(v *ReindexValidation) { v.InvalidTaxonomy = 1 }},
		{"contract mismatch", func(v *ReindexValidation) { v.ContractMismatches = 1 }},
		{"wrong dimensions", func(v *ReindexValidation) { v.WrongDimensions = 1 }},
		{"point count mismatch", func(v *ReindexValidation) { v.QdrantPoints = 9 }},
	}
	for _, tc := range cases {
		v := base()
		tc.mutate(&v)
		if err := v.Validate(); err == nil {
			t.Errorf("%s: expected fail-closed error, got nil", tc.name)
		}
	}
}

func TestDeterministicTopK(t *testing.T) {
	identical := [][]string{
		{"A", "B", "C", "D", "E"},
		{"A", "B", "C", "D", "E"},
		{"A", "B", "C", "D", "E"},
	}
	if !DeterministicTopK(identical) {
		t.Fatal("identical runs must be deterministic")
	}

	reordered := [][]string{
		{"A", "B", "C", "D", "E"},
		{"A", "B", "C", "E", "D"},
	}
	if DeterministicTopK(reordered) {
		t.Fatal("reordered runs must fail determinism")
	}

	drifting := [][]string{
		{"A", "B", "C", "D", "E"},
		{"A", "B", "C", "D", "F"},
	}
	if DeterministicTopK(drifting) {
		t.Fatal("drifting runs must fail determinism")
	}

	// A single run (or empty) is vacuously deterministic.
	if !DeterministicTopK([][]string{{"A"}}) {
		t.Fatal("single run must be deterministic")
	}
	if !DeterministicTopK(nil) {
		t.Fatal("no runs must be deterministic")
	}
}
