package overlays

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// TestSeededIndex_ParityVector pins the exact raw-byte mapping so the Rust
// planner (crates/overlay-compiler) can assert the same vector. Both sides must
// read the raw digest and apply the same rejection rule.
func TestSeededIndex_ParityVector(t *testing.T) {
	seed := `{"job_fingerprint":"job-fp-123","scene_id":"scene_03","semantic_id":"sem_scene03_person_01","preset_family":"person_image"}`
	sum := digest.SHA256Sum([]byte(seed))
	require.Equal(t, "a4ddd8af5037979d83950214d9354333f2a7fed831d18b2d64b6aaf24f4e0d11", fmt.Sprintf("%x", sum))
	for _, tc := range []struct {
		slot int
		n    int
		want int
	}{
		{0, 2, 1}, {1, 2, 1},
		{0, 3, 1}, {1, 3, 0},
		{0, 4, 1}, {1, 4, 3},
		{0, 7, 6}, {1, 7, 6},
		{0, 41, 5}, {1, 41, 20},
	} {
		require.Equal(t, tc.want, SeededIndex(sum, tc.slot, tc.n), "slot=%d n=%d", tc.slot, tc.n)
	}
}

// TestSeededIndex_UniformOverFourSlots reproduces the audit's counter-example:
// the old ASCII-hex sampler produced 25/31.25/25/18.75 for a four-slot pool.
// Ten thousand synthetic seeds must be uniform (each slot within 2 points of
// 25%) or the bias is back.
func TestSeededIndex_UniformOverFourSlots(t *testing.T) {
	const samples = 10_000
	const slots = 4
	counts := make([]int, slots)
	for i := 0; i < samples; i++ {
		sum := digest.SHA256Sum([]byte(fmt.Sprintf("animrand-distribution-%06d", i)))
		counts[SeededIndex(sum, 0, slots)]++
	}
	entropy := 0.0
	for slot, count := range counts {
		share := float64(count) / samples
		require.Greater(t, count, 0, "slot %d unreachable", slot)
		require.Less(t, math.Abs(share-0.25), 0.02, "slot %d share %.4f is outside +/-2 points", slot, share)
		entropy -= share * math.Log2(share)
	}
	// A uniform 4-slot distribution carries exactly 2 bits of entropy.
	require.Greater(t, entropy, 1.99)
}
