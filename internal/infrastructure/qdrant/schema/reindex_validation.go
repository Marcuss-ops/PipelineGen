package schema

import (
	"fmt"
	"strings"
)

// ReindexValidation is the pre-alias validation surface for a blue-green
// rebuild (PR-HASH-SEMANTICS item 14, August 2026). A projection may only be
// promoted to ACTIVE when every counter is zero and the Qdrant point count
// matches the canonical SQLite searchable-asset count.
type ReindexValidation struct {
	// SQLiteSearchableAssets is the number of searchable assets in the
	// canonical registry (SQLite).
	SQLiteSearchableAssets int
	// QdrantPoints is the number of points in the rebuilt collection.
	QdrantPoints int
	// MissingPoints are SQLite searchable assets with no Qdrant point.
	MissingPoints int
	// OrphanPoints are Qdrant points with no canonical SQLite asset.
	OrphanPoints int
	// DuplicateCanonicalIDs are canonical asset IDs mapped to >1 point.
	DuplicateCanonicalIDs int
	// InvalidTaxonomy are points whose payload taxonomy is invalid.
	InvalidTaxonomy int
	// ContractMismatches are points produced under a different embedding
	// contract than the collection's signature.
	ContractMismatches int
	// WrongDimensions are points whose vector length differs from the
	// collection's declared dimension.
	WrongDimensions int
}

// Validate fails closed on any non-zero invariant. The zero-surface is the
// promotion gate: missing=0, orphan=0, duplicate=0, invalid taxonomy=0,
// contract mismatch=0, wrong dimensions=0, and SQLite searchable assets must
// equal Qdrant points.
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
	add(v.SQLiteSearchableAssets != v.QdrantPoints,
		fmt.Sprintf("point_count_mismatch: sqlite=%d qdrant=%d", v.SQLiteSearchableAssets, v.QdrantPoints))
	if len(problems) > 0 {
		return fmt.Errorf("reindex validation failed: %s", strings.Join(problems, ", "))
	}
	return nil
}

// DeterministicTopK reports whether every run of a single golden query
// returned the identical ordered top-k. The reproducibility gate requires a
// query executed N times against the same projection to yield bit-identical
// top-10 IDs; any reordering or drift fails closed.
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
