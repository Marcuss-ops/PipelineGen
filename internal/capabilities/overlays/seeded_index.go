// Package overlays — seeded_index.go: the ONE seeded-index primitive.
//
// The original samplers read the first eight ASCII characters of the SHA-256
// hex digest and parsed them as a big-endian integer. Hex characters are not
// uniformly distributed (only 10 of the 16 symbols are digits), so the modulo
// of that value was biased for most pool sizes: with four candidates the map
// landed on one slot 31.25% of the time instead of 25%. The audit measured
// exactly that skew (ANIMRAND-02).
//
// SeededIndex reads the RAW digest bytes and applies rejection sampling, so the
// mapping is uniform for every pool size. The algorithm is versioned: sealed
// plans carry concrete motion ids and are never re-sampled, so a replay stays
// byte-identical; a plan compiled with this version records
// SelectionAlgorithmV2 in its provenance.
package overlays

import (
	"encoding/binary"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// SelectionAlgorithmV2 is the provenance string for the unbiased sampler.
// It is stamped into selection-origin telemetry so a plan can be attributed to
// the algorithm that produced it.
const SelectionAlgorithmV2 = "sha256-binary-bytes-rejection-v2"

// SeededIndex maps one 8-byte big-endian window of a raw SHA-256 digest onto
// [0, n) without modulo bias.
//
// slot selects the 32-byte window (`(slot*8) % 32`); when the candidate window
// falls in the rejection region the next window is tried, wrapping around, so
// the result is a pure function of (sum, slot, n). n <= 1 returns 0 because
// there is nothing to choose.
func SeededIndex(sum [digest.SHA256Size]byte, slot, n int) int {
	if n <= 1 {
		return 0
	}
	size := uint64(n)
	// bound is the largest multiple of size that fits in [0, 2^64).
	bound := ^uint64(0) - (^uint64(0) % size)
	for attempt := 0; attempt < digest.SHA256Size/8; attempt++ {
		offset := (((slot+attempt)*8)%digest.SHA256Size + digest.SHA256Size) % digest.SHA256Size
		value := binary.BigEndian.Uint64(sum[offset : offset+8])
		if value < bound {
			return int(value % size)
		}
	}
	// Every window was rejected: probability (32/2^64)^4, i.e. impossible in
	// practice. Fall back to the first window so the function stays total.
	return int(binary.BigEndian.Uint64(sum[0:8]) % size)
}

// SeededIndexFromText is the convenience form for samplers whose seed is a
// string: SHA-256 the bytes first, then apply SeededIndex. Callers that
// already hold a digest use SeededIndex directly.
func SeededIndexFromText(seed string, slot, n int) int {
	return SeededIndex(digest.SHA256Sum([]byte(seed)), slot, n)
}
