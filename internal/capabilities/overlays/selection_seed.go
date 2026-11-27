// Package overlays — selection_seed.go: single owner for deterministic seeding.
//
// Every sampler (preset sampler, map composition, group draw) goes through
// SeededIndex: one SHA-256, one raw-byte mapping, one no-modulo-bias rule.
// Plans sealed before SelectionAlgorithmV2 carry concrete motion ids on the
// wire, so a replay never re-samples; the change only affects newly compiled
// plans, which is what a selection algorithm version is for.
package overlays

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// SeededGroupIndex è l'unico hash seed→indice per i draw seeded di gruppo.
// Seed = SHA256(seed\x00target\x00groupID) mapped with SeededIndex. n<=0 → 0.
func SeededGroupIndex(seed, target, groupID string, n int) int {
	if n <= 1 {
		return 0
	}
	sum := digest.SHA256Sum([]byte(seed + "\x00" + target + "\x00" + groupID))
	return SeededIndex(sum, 0, n)
}
