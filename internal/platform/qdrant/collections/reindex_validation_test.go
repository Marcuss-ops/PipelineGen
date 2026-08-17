package collections

import "testing"

func TestReindexValidation_AllZeroPasses(t *testing.T) {
	if err := (ReindexValidation{SQLiteSearchableAssets: 10, QdrantPoints: 10}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestReindexValidation_EachInvariantFailsClosed(t *testing.T) {
	base := func() ReindexValidation { return ReindexValidation{SQLiteSearchableAssets: 10, QdrantPoints: 10} }
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
		t.Run(tc.name, func(t *testing.T) {
			v := base()
			tc.mutate(&v)
			if err := v.Validate(); err == nil {
				t.Fatal("expected fail-closed error")
			}
		})
	}
}

func TestDeterministicTopK(t *testing.T) {
	if !DeterministicTopK([][]string{{"A", "B"}, {"A", "B"}, {"A", "B"}}) {
		t.Fatal("identical runs must pass")
	}
	if DeterministicTopK([][]string{{"A", "B"}, {"B", "A"}}) {
		t.Fatal("reordered runs must fail")
	}
	if DeterministicTopK([][]string{{"A", "B"}, {"A", "C"}}) {
		t.Fatal("drifting runs must fail")
	}
	if !DeterministicTopK(nil) {
		t.Fatal("empty runs are vacuously deterministic")
	}
}
