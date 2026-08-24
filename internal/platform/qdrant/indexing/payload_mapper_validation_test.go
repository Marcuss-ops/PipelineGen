// Package indexing — payload_mapper_validation_test.go
// (PR-SPLIT-VO-PARENT-AGG-TESTS-mirror, July 2026).
//
// VALIDATION test surface (mirror of payload_mapper_validation.go
// production split). Per godlike/06 SSOT (one canonical owner per
// fact), this file is the SOLE canonical owner of the 10 Test funcs
// that exercise the dense-vector validation pipeline:
//
//   - TestValidateDenseVector_RequiredChannelNil
//     (text channel nil → ErrMissingRequiredVector)
//
//   - TestValidateDenseVector_OptionalChannelNil
//     (audio / transcript / visual / future_channel nil → silently skipped)
//
//   - TestValidateDenseVector_ZeroLengthVector
//     (non-nil, len==0 → ErrEmptyVector; distinct from nil)
//
//   - TestValidateDenseVector_DimensionMismatch
//     (len(vec) != expectedDim → ErrVectorDimensionMismatch)
//
//   - TestValidateDenseVector_NaN
//     (vec[i] = NaN → ErrNaNOrInf)
//
//   - TestValidateDenseVector_Inf
//     (vec[i] = +Inf → ErrNaNOrInf)
//
//   - TestValidateDenseVector_NegativeInf
//     (vec[i] = -Inf → ErrNaNOrInf)
//
//   - TestValidateDenseVector_ValidVector
//     (768-d, finite values → nil)
//
//   - TestValidateDenseVector_MultipleErrors_FirstWins
//     (5-step ordering: nil → zero-len → dimension → NaN → Inf;
//
//     first failure wins)
//
//   - TestClassifyChannel
//     (text=Required; audio/transcript/visual/future_channel=Optional)
//
// All test funcs target the VALIDATION production code only
// (payload_mapper_validation.go::validateDenseVector +
// ::classifyChannel + ::isNaNOrInf). They use the
// makeFloat32Slice vector factory from payload_mapper_testhelpers_test.go
// via same-package visibility. godlike/07 minimum-blast-radius:
// pure code-motion, no logic change.
package indexing

import (
	"errors"
	"math"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────
// Task 4 (July 2026) §1: required channel nil → typed error.
// ─────────────────────────────────────────────────────────────────

// TestValidateDenseVector_RequiredChannelNil pins the canonical
// first-error path of validateDenseVector: a nil vector on a
// required channel (text) MUST return *transport.ErrMissingRequiredVector.
// The error carries the channel name + asset ID for operator
// forensics.
func TestValidateDenseVector_RequiredChannelNil(t *testing.T) {
	err := validateDenseVector("text", nil, 768, "asset-1")
	require.Error(t, err, "nil text vector must return error (policyRequired)")

	var missing *transport.ErrMissingRequiredVector
	require.True(t, errors.As(err, &missing),
		"expected *transport.ErrMissingRequiredVector, got %T: %v", err, err)
	assert.Equal(t, "text", missing.Channel)
	assert.Equal(t, "asset-1", missing.AssetID)
}

// ─────────────────────────────────────────────────────────────────
// Task 4 §2: nil on optional channels → silent skip.
// ─────────────────────────────────────────────────────────────────

// TestValidateDenseVector_OptionalChannelNil pins the
// policyOptional → nil-return contract. The 3 documented optional
// channels (audio, transcript, visual) + a future channel must
// each return nil when the vector is nil — the mapper drops them
// silently rather than failing the IndexDocument.
func TestValidateDenseVector_OptionalChannelNil(t *testing.T) {
	for _, ch := range []string{"audio", "transcript", "visual", "future_channel"} {
		ch := ch
		t.Run(ch, func(t *testing.T) {
			err := validateDenseVector(ch, nil, 512, "asset-1")
			assert.NoError(t, err,
				"nil %q vector must be silently skipped (policyOptional)", ch)
		})
	}
}

// ─────────────────────────────────────────────────────────────────
// Task 4 §3: zero-length non-nil vector → ErrEmptyVector.
// Distinct from nil (missing) — present-but-corrupted.
// ─────────────────────────────────────────────────────────────────

// TestValidateDenseVector_ZeroLengthVector pins the second-step
// check: a non-nil vector with len==0 is corrupted ("present but
// empty"), distinct from nil ("missing"). Returns
// *transport.ErrEmptyVector.
func TestValidateDenseVector_ZeroLengthVector(t *testing.T) {
	emptyVec := make([]float32, 0) // non-nil, zero-length
	err := validateDenseVector("text", emptyVec, 768, "asset-2")
	require.Error(t, err, "zero-length vector must return error")

	var empty *transport.ErrEmptyVector
	require.True(t, errors.As(err, &empty),
		"expected *transport.ErrEmptyVector, got %T: %v", err, err)
	assert.Equal(t, "text", empty.Channel)
	assert.Equal(t, "asset-2", empty.AssetID)
}

// ─────────────────────────────────────────────────────────────────
// Task 4 §4: dimension mismatch → ErrVectorDimensionMismatch.
// ─────────────────────────────────────────────────────────────────

// TestValidateDenseVector_DimensionMismatch pins the third-step
// check: a 512-d vector against a 768-d schema expectation MUST
// return *transport.ErrVectorDimensionMismatch with the expected
// and actual dimensions in the error struct.
func TestValidateDenseVector_DimensionMismatch(t *testing.T) {
	vec := makeFloat32Slice(512) // 512d, but schema expects 768d
	err := validateDenseVector("text", vec, 768, "asset-3")
	require.Error(t, err, "dimension mismatch must return error")

	var dimErr *transport.ErrVectorDimensionMismatch
	require.True(t, errors.As(err, &dimErr),
		"expected *transport.ErrVectorDimensionMismatch, got %T: %v", err, err)
	assert.Equal(t, 768, dimErr.Expected)
	assert.Equal(t, 512, dimErr.Actual)
}

// ─────────────────────────────────────────────────────────────────
// Task 4 §5: NaN value → ErrNaNOrInf.
// ─────────────────────────────────────────────────────────────────

// TestValidateDenseVector_NaN pins the fourth-step check: a NaN
// value anywhere in the vector MUST return *transport.ErrNaNOrInf.
// Uses the math.NaN() sentinel.
func TestValidateDenseVector_NaN(t *testing.T) {
	vec := makeFloat32Slice(768)
	vec[100] = float32(math.NaN())
	err := validateDenseVector("text", vec, 768, "asset-4")
	require.Error(t, err, "NaN vector must return error")

	var nanErr *transport.ErrNaNOrInf
	require.True(t, errors.As(err, &nanErr),
		"expected *transport.ErrNaNOrInf, got %T: %v", err, err)
	assert.Equal(t, "text", nanErr.Channel)
	assert.Equal(t, "asset-4", nanErr.AssetID)
}

// ─────────────────────────────────────────────────────────────────
// Task 4 §6: positive Inf value → ErrNaNOrInf.
// ─────────────────────────────────────────────────────────────────

// TestValidateDenseVector_Inf pins the fifth-step check: a +Inf
// value MUST return *transport.ErrNaNOrInf (same type as NaN —
// the canonical NaN/Inf umbrella type rather than a separate
// ErrInf sentinel).
func TestValidateDenseVector_Inf(t *testing.T) {
	vec := makeFloat32Slice(768)
	vec[100] = float32(math.Inf(1))
	err := validateDenseVector("text", vec, 768, "asset-5")
	require.Error(t, err, "+Inf vector must return error")

	var nanErr *transport.ErrNaNOrInf
	require.True(t, errors.As(err, &nanErr),
		"expected *transport.ErrNaNOrInf, got %T: %v", err, err)
}

// ─────────────────────────────────────────────────────────────────
// Task 4 §6 (symmetric): negative Inf value → ErrNaNOrInf.
// ─────────────────────────────────────────────────────────────────

// TestValidateDenseVector_NegativeInf pins the symmetric
// negative-Inf path: -Inf MUST return *transport.ErrNaNOrInf
// (no distinction between +Inf and -Inf in the canonical
// umbrella type).
func TestValidateDenseVector_NegativeInf(t *testing.T) {
	vec := makeFloat32Slice(768)
	vec[100] = float32(math.Inf(-1))
	err := validateDenseVector("text", vec, 768, "asset-6")
	require.Error(t, err, "-Inf vector must return error")

	var nanErr *transport.ErrNaNOrInf
	require.True(t, errors.As(err, &nanErr),
		"expected *transport.ErrNaNOrInf, got %T: %v", err, err)
}

// ─────────────────────────────────────────────────────────────────
// Happy path: valid 768-d vector → nil error.
// ─────────────────────────────────────────────────────────────────

// TestValidateDenseVector_ValidVector pins the happy path: a
// 768-d, all-finite vector MUST return nil across every
// channel (text, audio, transcript, visual). This is the
// production-shaped payload for the canonical index pipeline.
func TestValidateDenseVector_ValidVector(t *testing.T) {
	for _, ch := range []string{"text", "audio", "transcript", "visual"} {
		ch := ch
		t.Run(ch, func(t *testing.T) {
			vec := makeFloat32Slice(768)
			err := validateDenseVector(ch, vec, 768, "asset-valid")
			assert.NoError(t, err,
				"valid 768-d %q vector must return nil", ch)
		})
	}
}

// ─────────────────────────────────────────────────────────────────
// Ordering invariant: when multiple errors are present, the
// FIRST one (per the 5-step docstring) MUST win.
// ─────────────────────────────────────────────────────────────────

// TestValidateDenseVector_MultipleErrors_FirstWins pins the
// 5-step ordering invariant documented in the validateDenseVector
// godoc comment (Step 1: nil check → Step 2: zero-length → Step 3:
// dimension → Step 4: NaN → Step 5: Inf). When a vector fails
// MULTIPLE checks, the FIRST error type returned wins.
//
// This test uses a 512-d vector with len!=expected dim AND a
// NaN value at position 100; the step 4 NaN check fires AFTER
// the step 3 dimension-mismatch check, so DimensionMismatch
// wins. (See also the existing TestValidateDenseVector_DimensionMismatch.)
func TestValidateDenseVector_MultipleErrors_FirstWins(t *testing.T) {
	vec := makeFloat32Slice(512)   // wrong dimension: step 3 fires first
	vec[100] = float32(math.NaN()) // step 4 also would-fire

	err := validateDenseVector("text", vec, 768, "asset-multi")
	require.Error(t, err)

	var dimErr *transport.ErrVectorDimensionMismatch
	require.True(t, errors.As(err, &dimErr),
		"multi-error vector MUST return FIRST error (dimension mismatch), got %T: %v",
		err, err)
}

// ─────────────────────────────────────────────────────────────────
// Channel classification policy: text vs optional channels.
// ─────────────────────────────────────────────────────────────────

// TestClassifyChannel pins the canonical channelPolicy classification
// per the classifyChannel documentation:
//
//   - "text"       → policyRequired (nil → ErrMissingRequiredVector)
//   - "transcript" → policyOptional (nil → silently skipped)
//   - "visual"     → policyOptional
//   - "audio"      → policyOptional
//   - "future_channel" / any other → policyOptional (default-future-safe)
func TestClassifyChannel(t *testing.T) {
	cases := []struct {
		ch   string
		want channelPolicy
	}{
		{"text", policyRequired},
		{"transcript", policyOptional},
		{"visual", policyOptional},
		{"audio", policyOptional},
		{"future_channel", policyOptional},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.ch, func(t *testing.T) {
			got := classifyChannel(tc.ch)
			assert.Equal(t, tc.want, got,
				"classifyChannel(%q) = %v, want %v (godlike/06 SSOT channel policy)", tc.ch, got, tc.want)
		})
	}
}
