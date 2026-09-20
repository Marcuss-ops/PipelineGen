// failure_verdict_projection_test.go — pins that a failed segment's TYPED
// verdict (FailureCode + Retryable) is projected onto the serialized
// item, so the job-side classifier never has to re-derive it from Error
// text (dto.ExtractItem).
//
// Sept 2026: the job classifier used to scan Error strings with a
// transient-marker taxonomy because the per-segment verdict stopped at the
// typed error envelope. `fail()` and `failedFanOutResult` are the ONLY two
// places that write Item.Error, so they are also the only two producers
// that must write the typed fields — a failure path that sets Error alone
// would silently fall back to heuristics downstream.
package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// TestFail_ProjectsTypedVerdictOntoItem covers the transient case: the
// broker-visible verdict must survive serialization.
func TestFail_ProjectsTypedVerdictOntoItem(t *testing.T) {
	uc := NewProcessYouTubeSegmentFromSubBundles(validProcessSegmentDeps())
	out := youtubetypes.ProcessSegmentResult{
		Item: youtubetypes.ExtractItem{Name: "clip-0", Status: "failed"},
	}
	typed := NewExtractionError(FailureCodeDriveUploadFailed, true, "drive rejected the upload (503)", nil)

	returned := uc.fail(&out, typed)

	require.Same(t, typed, returned, "fail must return the same typed error it recorded")
	require.Equal(t, "failed", out.Item.Status)
	require.Equal(t, string(FailureCodeDriveUploadFailed), out.Item.FailureCode)
	require.NotNil(t, out.Item.Retryable)
	require.True(t, *out.Item.Retryable, "a transient verdict must be projected as retryable=true")
	require.Contains(t, out.Item.Error, "drive_upload_failed")
}

// TestProcessSegment_FailedItemCarriesTypedTerminalVerdict exercises the
// real pipeline path (Step 1 duration gate, no I/O) and asserts the item
// carries the typed terminal verdict end to end.
func TestProcessSegment_FailedItemCarriesTypedTerminalVerdict(t *testing.T) {
	uc := NewProcessYouTubeSegmentFromSubBundles(validProcessSegmentDeps())

	out, err := uc.Execute(context.Background(), youtubetypes.ProcessSegmentCommand{
		VideoID: "abc",
		Segment: youtubetypes.Segment{Start: "0:00", End: "2:00:00", Name: "TooLong"},
		Index:   0,
		OutDir:  t.TempDir(),
	})

	require.Error(t, err, "the SegmentPolicy gate is fail-closed")
	require.Equal(t, "failed", out.Item.Status)
	require.Equal(t, string(FailureCodeDurationOutOfRange), out.Item.FailureCode,
		"the item must carry the typed failure code, not just the rendered message")
	require.NotNil(t, out.Item.Retryable)
	require.False(t, *out.Item.Retryable, "an out-of-range duration is terminal")
}

// TestFailedFanOutResult_ProjectsTypedVerdict covers the panicking/erroring
// goroutine path, which bypasses the step pipeline.
func TestFailedFanOutResult_ProjectsTypedVerdict(t *testing.T) {
	typed := NewExtractionError(FailureCodeDriveUploadFailed, true, "drive 503", nil)

	res := failedFanOutResult(
		youtubetypes.ProcessSegmentResult{},
		youtubetypes.Segment{Start: "0:00", End: "0:05", Name: "clip-0"},
		0, "drive-folder", "Group/clip-0", typed,
	)

	require.Equal(t, "failed", res.Item.Status)
	require.Equal(t, string(FailureCodeDriveUploadFailed), res.Item.FailureCode)
	require.NotNil(t, res.Item.Retryable)
	require.True(t, *res.Item.Retryable)
	require.Contains(t, res.Item.Error, "drive_upload_failed")
}

// TestFailedFanOutResult_UntypedErrorLeavesVerdictUnset pins the honest
// degradation: a non-typed error (e.g. a goroutine panic wrapped by the
// caller) has NO typed verdict, so the fields stay unset and the
// classifier falls back to its legacy taxonomy instead of guessing.
func TestFailedFanOutResult_UntypedErrorLeavesVerdictUnset(t *testing.T) {
	res := failedFanOutResult(
		youtubetypes.ProcessSegmentResult{},
		youtubetypes.Segment{Start: "0:00", End: "0:05", Name: "clip-0"},
		0, "drive-folder", "Group/clip-0", errors.New("segment 0 panic: boom"),
	)

	require.Empty(t, res.Item.FailureCode)
	require.Nil(t, res.Item.Retryable)
	require.Contains(t, res.Item.Error, "panic: boom")
}
