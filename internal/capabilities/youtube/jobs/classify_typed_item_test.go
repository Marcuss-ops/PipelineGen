// classify_typed_item_test.go — pins the TYPED-FIRST per-item verdict.
//
// History: classifyItemError used to decide retryability by scanning the
// item's Error string against a transient-marker taxonomy (429/503/
// timeout/...), because the canonical per-segment verdict stopped at the
// typed error envelope. The pipeline now projects that verdict onto the
// item (dto.ExtractItem.FailureCode + .Retryable), so the typed value is
// authoritative and the string scan is only the legacy fallback for items
// produced before those fields existed.
package jobs

import (
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

func boolPtr(v bool) *bool { return &v }

// TestClassifyExtractionResult_TypedTerminalBeatsTransientLookingText is
// the item-level twin of Correttezza #9: a terminal verdict must NOT be
// re-classified as retryable just because the rendered message contains a
// transient-looking word.
func TestClassifyExtractionResult_TypedTerminalBeatsTransientLookingText(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(1, 0, 0, 1),
		Items: []youtubetypes.ExtractItem{
			{
				Status:      "failed",
				Error:       "duration_out_of_range: duration 61s out of range after a network timeout",
				FailureCode: "duration_out_of_range",
				Retryable:   boolPtr(false),
			},
		},
	}

	err := ClassifyExtractionResult(resp)
	require.ErrorIs(t, err, ErrExtractionTerminal,
		"the typed terminal verdict must win over the 'timeout' marker in the message")
}

// TestClassifyExtractionResult_TypedRetryableWithoutMarker pins that a
// typed retryable verdict is honoured even when the message carries no
// marker at all (previously a dead-letter).
func TestClassifyExtractionResult_TypedRetryableWithoutMarker(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: fullStats(1, 0, 0, 1),
		Items: []youtubetypes.ExtractItem{
			{
				Status:      "failed",
				Error:       "drive_upload_failed: drive rejected the upload",
				FailureCode: "drive_upload_failed",
				Retryable:   boolPtr(true),
			},
		},
	}

	err := ClassifyExtractionResult(resp)
	require.ErrorIs(t, err, ErrExtractionRetryable)
}

// TestClassifyExtractionResult_LegacyItemFallsBackToMarkerTaxonomy pins
// the migration window: an item with NO typed verdict (Retryable == nil,
// e.g. a replayed pre-Sept-2026 result row) is still classified by the
// historical marker taxonomy instead of being forced terminal.
func TestClassifyExtractionResult_LegacyItemFallsBackToMarkerTaxonomy(t *testing.T) {
	retryableResp := &youtubetypes.ExtractResponse{
		Stats: fullStats(1, 0, 0, 1),
		Items: []youtubetypes.ExtractItem{
			{Status: "failed", Error: "HTTP 503 Service Unavailable"},
		},
	}
	require.ErrorIs(t, ClassifyExtractionResult(retryableResp), ErrExtractionRetryable)

	terminalResp := &youtubetypes.ExtractResponse{
		Stats: fullStats(1, 0, 0, 1),
		Items: []youtubetypes.ExtractItem{
			{Status: "failed", Error: "Video unavailable"},
		},
	}
	require.ErrorIs(t, ClassifyExtractionResult(terminalResp), ErrExtractionTerminal)
}

// TestClassifyItemError_TypedVerdictIsAuthoritativeUnit pins the helper
// itself so a future refactor cannot silently reintroduce string-first
// classification.
func TestClassifyItemError_TypedVerdictIsAuthoritativeUnit(t *testing.T) {
	require.True(t, classifyItemError(youtubetypes.ExtractItem{
		Status: "failed", Error: "no marker here", Retryable: boolPtr(true),
	}))
	require.False(t, classifyItemError(youtubetypes.ExtractItem{
		Status: "failed", Error: "network timeout", Retryable: boolPtr(false),
	}))
	require.True(t, classifyItemError(youtubetypes.ExtractItem{
		Status: "failed", Error: "network timeout",
	}), "legacy fallback still applies when the typed verdict is absent")
}
