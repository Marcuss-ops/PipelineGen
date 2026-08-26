// Package indexing — payload_mapper_validation.go: vector validation helpers.
//
// Extracted from payload_mapper.go (July 2026, PR-PAYLOAD-MAPPER-SPLIT).
// Owns: getVectorForChannel, channelPolicy, classifyChannel,
// validateDenseVector, isNaNOrInf.
package indexing

import (
	"fmt"
	"math"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// resampleFloat32Vector deterministically resizes a dense vector to the
// target dimension using linear interpolation. The helper is only used as a
// compatibility shim for legacy visual embeddings produced at a different
// size than the current schema.
func resampleFloat32Vector(vec []float32, targetDim int) ([]float32, error) {
	if targetDim <= 0 {
		return nil, fmt.Errorf("invalid target dimension %d", targetDim)
	}
	if len(vec) == 0 {
		return nil, fmt.Errorf("empty vector")
	}
	if len(vec) == targetDim {
		out := make([]float32, targetDim)
		copy(out, vec)
		return out, nil
	}
	if len(vec) == 1 {
		out := make([]float32, targetDim)
		for i := range out {
			out[i] = vec[0]
		}
		return out, nil
	}
	if targetDim == 1 {
		return []float32{vec[0]}, nil
	}

	out := make([]float32, targetDim)
	scale := float64(len(vec)-1) / float64(targetDim-1)
	for i := range out {
		pos := float64(i) * scale
		left := int(math.Floor(pos))
		right := left + 1
		if right >= len(vec) {
			right = len(vec) - 1
		}
		frac := pos - float64(left)
		out[i] = float32((1-frac)*float64(vec[left]) + frac*float64(vec[right]))
	}
	return out, nil
}

// getVectorForChannel returns the embedding vector for a given channel.
func (m *PayloadMapper) getVectorForChannel(asset *AssetData, channel string) []float32 {
	switch channel {
	case "text":
		return asset.TextVector
	case "transcript":
		return asset.TranscriptVector
	case "visual":
		return asset.VisualVector
	case "audio":
		return asset.AudioVector
	default:
		return nil
	}
}

func isNaNOrInf(v float32) bool {
	return math.IsNaN(float64(v)) || math.IsInf(float64(v), 0)
}

// ══════════════════════════════════════════════════════════════════════════
// Task 4 (July 2026) — canonical dense-vector validation.
// ══════════════════════════════════════════════════════════════════════════

// channelPolicy classifies each dense vector channel as required,
// optional, or fatal-if-missing. The classification is per-channel
// because the embedding pipeline differs:
//
//   - text:       REQUIRED — every searchable asset must have a text embedding.
//   - transcript: OPTIONAL — YouTube-only; dropped when absent (PR 2).
//   - visual:     OPTIONAL — image/video assets only; dropped when absent.
//   - audio:      OPTIONAL — audio assets only; dropped when absent.
//   - any other:  OPTIONAL — future channels default to optional.
type channelPolicy int

const (
	policyRequired channelPolicy = iota // nil → ErrMissingRequiredVector
	policyOptional                      // nil → silently dropped
)

// classifyChannel returns the policy for the given Qdrant vector channel.
func classifyChannel(ch string) channelPolicy {
	switch ch {
	case "text":
		return policyRequired
	case "transcript", "visual", "audio":
		return policyOptional
	default:
		return policyOptional
	}
}

// validateDenseVector performs the canonical 5-step validation of a
// dense embedding vector before it is included in the IndexDocument.
//
// Checks (in order, first failure returned):
//  1. Nil check      → policyRequired → ErrMissingRequiredVector
//  2. Zero-length    → ErrEmptyVector
//  3. Dimension      → ErrVectorDimensionMismatch
//  4. NaN            → ErrNaNOrInf
//  5. Inf            → ErrNaNOrInf
//
// Returns nil when the vector is valid OR when it is nil AND the
// channel is optional (policyOptional → silent skip).
func validateDenseVector(channel string, vec []float32, expectedDim int, assetID string) error {
	// Step 1: nil check — required vs optional.
	if vec == nil {
		if classifyChannel(channel) == policyRequired {
			return &transport.ErrMissingRequiredVector{
				Channel: channel,
				AssetID: assetID,
			}
		}
		return nil // optional channel, absent is allowed
	}

	// Step 2: zero-length vector — present but corrupted.
	if len(vec) == 0 {
		return &transport.ErrEmptyVector{
			Channel: channel,
			AssetID: assetID,
		}
	}

	// Step 3: dimension mismatch.
	if len(vec) != expectedDim {
		return &transport.ErrVectorDimensionMismatch{
			Channel:  channel,
			Expected: expectedDim,
			Actual:   len(vec),
			AssetID:  assetID,
		}
	}

	// Step 4 & 5: NaN / Inf — reuse the canonical helper.
	for _, v := range vec {
		if isNaNOrInf(v) {
			return &transport.ErrNaNOrInf{
				Channel: channel,
				AssetID: assetID,
			}
		}
	}

	return nil
}
