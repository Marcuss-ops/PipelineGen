// errors_retry_visibility_test.go — pins that *ExtractionError exposes
// its typed Retryable classification through the structural retryability
// contract consumed by the job broker.
//
// Why this test exists (observed wiring gap, Sept 2026): the worker's
// retry gate is `retry.IsTransient(dispatchErr)` — a PURE TYPED PROBE
// with no substring fallback since FASE 6 Cut 6.1.D. The typed
// ExtractionError carried `Retryable` only as a STRUCT FIELD, which the
// probe cannot observe, so a transient extraction failure (yt-dlp 429,
// timeout, Drive rate-limit) was dead-lettered on the FIRST attempt
// instead of consuming the registry retry budget
// (youtube_clip.extract: DefaultMaxRetries=2).
package usecase

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

func TestExtractionError_IsRetryable_ReachesBrokerTypedProbe(t *testing.T) {
	transient := NewExtractionError(FailureCodeDriveUploadFailed, true, "drive rejected the upload", nil)
	terminal := NewExtractionError(FailureCodeDurationOutOfRange, false, "duration 61s out of range [4, 60]", nil)

	require.True(t, retry.IsTransient(transient),
		"a transient *ExtractionError must satisfy pkg/retry.RetryableError (worker schedules a retry)")
	require.True(t, retry.IsTransient(fmt.Errorf("extraction failed: %w", transient)),
		"the typed classification must survive the job handler's error envelope")
	require.False(t, retry.IsTransient(terminal),
		"a terminal *ExtractionError must NOT be retried")
	require.False(t, retry.IsTransient((*ExtractionError)(nil)),
		"a nil *ExtractionError in the chain must classify as non-retryable")
}
