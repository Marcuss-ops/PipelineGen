// retry_broker_wiring_test.go — pins the bridge between the YouTube
// extraction classification and the job broker's retry gate.
//
// The worker decides retry-vs-dead-letter with
// `retry.IsTransient(dispatchErr) && DecideRetry(j)`; retry.IsTransient
// is a PURE TYPED PROBE (no substring fallback since FASE 6 Cut 6.1.D).
// These tests exist because a plain `errors.New` sentinel made the
// "retryable" verdict invisible to that probe: a transient all-failed or
// partial run was dead-lettered on the first attempt, ignoring the
// registry DefaultMaxRetries budget.
package jobs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// TestExtractionSentinels_ReachBrokerTypedProbe pins the sentinel-level
// contract: the retryable classification (bare and through the handler's
// failure envelope) satisfies retry.IsTransient, the terminal one does not.
func TestExtractionSentinels_ReachBrokerTypedProbe(t *testing.T) {
	require.True(t, retry.IsTransient(ErrExtractionRetryable),
		"all-failed-retryable verdict must reach the worker's retry gate")
	require.False(t, retry.IsTransient(ErrExtractionTerminal),
		"terminal verdict must stay non-retryable")

	resp := &youtubetypes.ExtractResponse{
		Error: "one or more segments failed",
		Stats: &youtubetypes.ExtractStats{Requested: 1, Failed: 1},
	}
	require.True(t, retry.IsTransient(classifiedFailureError(ErrExtractionRetryable, "retryable", resp, "")),
		"the failure_details envelope must not hide the typed retryable verdict")
	require.False(t, retry.IsTransient(classifiedFailureError(ErrExtractionTerminal, "terminal", resp, "")),
		"the failure_details envelope must not turn a terminal verdict retryable")
}

// TestClassifyExtractionResult_PartialSuccessIsRetryable pins that a
// mixed run (some processed, some failed) classifies as retryable, so the
// broker re-drives the failed segments.
func TestClassifyExtractionResult_PartialSuccessIsRetryable(t *testing.T) {
	resp := &youtubetypes.ExtractResponse{
		Stats: &youtubetypes.ExtractStats{Requested: 5, Processed: 3, Failed: 2},
		Items: []youtubetypes.ExtractItem{{Status: "failed", Error: "network timeout"}},
	}

	err := ClassifyExtractionResult(resp)
	var partial *PartialSuccessError
	require.ErrorAs(t, err, &partial)
	require.ErrorIs(t, err, ErrExtractionRetryable)
	require.True(t, retry.IsTransient(err),
		"partial success must be visible to the broker as retryable")
}

// TestHandleJob_PartialSuccess_ReturnsRetryableError pins the Sept 2026
// behaviour change: partial success returns the aggregate result map AND a
// retryable error (previously (result, nil), which flipped the job to
// SUCCEEDED and left the failed segments un-retried forever).
func TestHandleJob_PartialSuccess_ReturnsRetryableError(t *testing.T) {
	h := NewJobHandler(partialSuccessExtractor{}, zap.NewNop())
	payload, err := json.Marshal(map[string]any{
		"url": "https://www.youtube.com/watch?v=abc12345678",
		"segments": []map[string]any{
			{"start": "00:00", "end": "00:05", "name": "probe-0"},
		},
	})
	require.NoError(t, err)

	res, handleErr := h.HandleJob(context.Background(),
		&job.Job{ID: "job-partial", Payload: payload}, &appjobs.JobTools{})

	require.Error(t, handleErr)
	require.ErrorIs(t, handleErr, ErrExtractionRetryable)
	require.True(t, retry.IsTransient(handleErr),
		"the handler must surface partial success as a retryable dispatch error")
	require.NotNil(t, res, "partial success must still return the aggregate result map")
	require.Equal(t, true, res["partial_success"])
	require.Equal(t, "retryable", res["failure_class"])
	require.Equal(t, 3, res["processed"])
	require.Equal(t, 2, res["failed"])

	details := failureDetailsFromError(t, handleErr)
	require.Equal(t, "retryable", details["failure_class"])
}

// partialSuccessExtractor simulates a run where 3 of 5 segments committed
// and 2 failed transiently.
type partialSuccessExtractor struct{}

func (partialSuccessExtractor) Extract(_ context.Context, req *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	return &youtubetypes.ExtractResponse{
		OK:        false,
		SourceURL: req.URL,
		VideoID:   "abc12345678",
		Error:     "one or more segments failed",
		Stats:     &youtubetypes.ExtractStats{Requested: 5, Processed: 3, Failed: 2},
		Items: []youtubetypes.ExtractItem{
			{Name: "clip-0", Status: "processed"},
			{Name: "clip-1", Status: "processed"},
			{Name: "clip-2", Status: "processed"},
			{Name: "clip-3", Status: "failed", Error: "ytdlp download failed: network timeout"},
			{Name: "clip-4", Status: "failed", Error: "HTTP 503 Service Unavailable"},
		},
	}, nil
}
