package collections

import (
	"fmt"
	"strings"
)

// ReindexValidation is the pre-alias validation surface for a blue-green
// rebuild. Promotion is allowed only when every invariant is zero and the
// rebuilt point count equals the canonical SQLite searchable-asset count.
type ReindexValidation struct {
	SQLiteSearchableAssets int
	QdrantPoints           int
	MissingPoints          int
	OrphanPoints           int
	DuplicateCanonicalIDs  int
	InvalidTaxonomy        int
	ContractMismatches     int
	WrongDimensions        int
}

func (v ReindexValidation) Validate() error {
	var problems []string
	add := func(cond bool, msg string) {
		if cond {
			problems = append(problems, msg)
		}
	}
	add(v.MissingPoints != 0, fmt.Sprintf("missing_points=%d", v.MissingPoints))
	add(v.OrphanPoints != 0, fmt.Sprintf("orphan_points=%d", v.OrphanPoints))
	add(v.DuplicateCanonicalIDs != 0, fmt.Sprintf("duplicate_canonical_ids=%d", v.DuplicateCanonicalIDs))
	add(v.InvalidTaxonomy != 0, fmt.Sprintf("invalid_taxonomy=%d", v.InvalidTaxonomy))
	add(v.ContractMismatches != 0, fmt.Sprintf("contract_mismatches=%d", v.ContractMismatches))
	add(v.WrongDimensions != 0, fmt.Sprintf("wrong_dimensions=%d", v.WrongDimensions))
	add(v.SQLiteSearchableAssets != v.QdrantPoints, fmt.Sprintf("point_count_mismatch: sqlite=%d qdrant=%d", v.SQLiteSearchableAssets, v.QdrantPoints))
	if len(problems) > 0 {
		return fmt.Errorf("reindex validation failed: %s", strings.Join(problems, ", "))
	}
	return nil
}

// DeterministicTopK reports whether repeated runs of one golden query have
// identical ordered IDs.
func DeterministicTopK(runs [][]string) bool {
	if len(runs) < 2 {
		return true
	}
	first := strings.Join(runs[0], "\x00")
	for _, run := range runs[1:] {
		if strings.Join(run, "\x00") != first {
			return false
		}
	}
	return true
}
