package stockpipeline

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestExtractLease_NilJob_ReturnsEmptyLease covers the guard rail:
// when a nil job is passed (composition-test fixture mistake), the
// helper MUST NOT panic and MUST return the zero-valued Lease so
// the finalizer's validateRequest produces the typed ERR_INVALID_LEASE.
func TestExtractLease_NilJob_ReturnsEmptyLease(t *testing.T) {
	lease := extractLease(nil)
	require.NotNil(t, lease.LeaseID)
	assert.Equal(t, "", lease.LeaseID)
	assert.Equal(t, "", lease.JobID)
	assert.Equal(t, "", lease.WorkerID)
	assert.Equal(t, 0, lease.Attempt)
	assert.True(t, lease.ExpiresAt.IsZero())
}

// TestExtractLease_HappyPath_AllFieldsPopulated canonical mapping
// table: each field must round-trip from the legacy *appjobs.Job
// (broker-claimed) into the canonical Lease struct.
func TestExtractLease_HappyPath_AllFieldsPopulated(t *testing.T) {
	expiry := time.Date(2030, time.January, 15, 12, 30, 0, 0, time.UTC)
	j := &appjobs.Job{
		ID:          "job-stock-123",
		WorkerID:    "worker-stock-1",
		LeaseID:     "lease-abc-def",
		LeaseExpiry: &expiry,
		RetryCount:  2,
	}

	lease := extractLease(j)
	assert.Equal(t, "lease-abc-def", lease.LeaseID, "LeaseID round-trip")
	assert.Equal(t, "job-stock-123", lease.JobID, "JobID round-trip")
	assert.Equal(t, "worker-stock-1", lease.WorkerID, "WorkerID round-trip")
	assert.Equal(t, 3, lease.Attempt, "Attempt = RetryCount + 1 (the canonical next-attempt formula)")
	assert.Equal(t, expiry, lease.ExpiresAt, "ExpiresAt round-trip verbatim")
}

// TestExtractLease_NilLeaseExpiry_DefensiveFallbackFiveMinutes
// covers the rare path where the broker hasn't populated LeaseExpiry
// (synthetic test fixtures, mid-Claim race). The helper MUST fall
// back to now+5m so validateRequest's Lease.Valid() doesn't raise
// an empty-time false-positive that would falsely mask a real
// expiry-time enforcement bug.
func TestExtractLease_NilLeaseExpiry_DefensiveFallbackFiveMinutes(t *testing.T) {
	before := time.Now().UTC()
	j := &appjobs.Job{
		ID:          "job-x",
		WorkerID:    "w-x",
		LeaseID:     "lease-x",
		LeaseExpiry: nil, // ← the defensible nil case
	}

	lease := extractLease(j)
	require.False(t, lease.ExpiresAt.IsZero(), "defensive fallback MUST populate ExpiresAt")

	// Fallback should be roughly 5min from now (allow ±2 seconds for
	// monotonic clock skew between the before snapshot and the fallback call).
	delta := lease.ExpiresAt.Sub(before)
	assert.GreaterOrEqual(t, delta, 4*time.Minute+58*time.Second, "fallback ExpiresAt should be ~5min ahead")
	assert.LessOrEqual(t, delta, 5*time.Minute+2*time.Second, "fallback ExpiresAt should be ~5min ahead")
}

// TestExtractLease_AttemptMapping_RetryCountZeroToOne pins the
// RetryCount+1 mapping (first attempt = retry_count 0 → attempt 1)
// which the finalizer's in-tx lease fence validates.
func TestExtractLease_AttemptMapping_RetryCountZeroToOne(t *testing.T) {
	j := &appjobs.Job{ID: "x", RetryCount: 0}
	lease := extractLease(j)
	assert.Equal(t, 1, lease.Attempt, "retry count 0 → attempt 1 (fresh claim)")

	j.RetryCount = 5
	lease = extractLease(j)
	assert.Equal(t, 6, lease.Attempt, "retry count 5 → attempt 6 (sixth try)")
}

// TestExtractLease_LeaseValidAfterMapping ensures the extracted
// Lease satisfies Lease.Valid() (relevant to validateRequest's
// pre-tx gate). Future expiry → valid; past expiry → invalid
// (defense-in-depth invariant).
func TestExtractLease_LeaseValidAfterMapping(t *testing.T) {
	future := time.Now().UTC().Add(30 * time.Minute)
	j := &appjobs.Job{
		ID:          "y",
		WorkerID:    "w-y",
		LeaseID:     "l-y",
		LeaseExpiry: &future,
	}
	lease := extractLease(j)
	assert.True(t, lease.Valid(), "future ExpiresAt → Lease.Valid() should be true")

	past := time.Now().UTC().Add(-30 * time.Minute)
	j.LeaseExpiry = &past
	lease = extractLease(j)
	assert.False(t, lease.Valid(), "past ExpiresAt → Lease.Valid() should be false (defense-in-depth)")
}

// TestExtractLease_ZeroLeaseExpiry_FallsBackForSynthetic verifies
// that non-nil LeaseExpiry with a zero value falls through to the
// 5-minute defensive fallback path (not silently accepting zero
// which would mark the lease as expired at tx-commit time).
func TestExtractLease_ZeroLeaseExpiry_FallsBackForSynthetic(t *testing.T) {
	zero := time.Time{}
	j := &appjobs.Job{
		ID:          "z",
		WorkerID:    "w-z",
		LeaseID:     "l-z",
		LeaseExpiry: &zero,
	}

	lease := extractLease(j)
	require.False(t, lease.ExpiresAt.IsZero(), "zero LeaseExpiry MUST trigger defensive fallback (not silently accepted)")
	assert.True(t, lease.ExpiresAt.After(time.Now().UTC()), "fallback ExpiresAt MUST be in the future")
}

// ── manifestBytes tests ───────────────────────────────────────────

// TestManifestBytes_NilManifest_ReturnsTypedError covers the
// invariant that a nil manifest cannot be marshalled (finalizer
// would crash with nil deref inside tx). Returns a typed error so
// the caller can errors.Is inspect.
func TestManifestBytes_NilManifest_ReturnsTypedError(t *testing.T) {
	raw, err := manifestBytes(nil)
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "nil manifest", "typed-error message must name the failure class")
}

// TestManifestBytes_PopulatedManifest_MarshalsAllFields verifies
// the marshalled bytes carry the canonical schema_version +
// workflow_id + job_id + 5 fixed artifact entries. The finalizer
// embeds these bytes verbatim into result_data.
func TestManifestBytes_PopulatedManifest_MarshalsAllFields(t *testing.T) {
	manifest := buildStockManifest("wf-001", "job-stock-001")
	require.NotNil(t, manifest)

	raw, err := manifestBytes(manifest)
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	// Round-trip — the marshalled bytes must decode back into a
	// matching *job.ArtifactManifest with all 5 fixed entries.
	var roundTrip job.ArtifactManifest
	require.NoError(t, json.Unmarshal(raw, &roundTrip))
	assert.Equal(t, job.SchemaVersionArtifactManifestV1, roundTrip.SchemaVersion, "SchemaVersion round-trip")
	assert.Equal(t, "wf-001", roundTrip.WorkflowID, "WorkflowID round-trip")
	assert.Equal(t, "job-stock-001", roundTrip.JobID, "JobID round-trip")
	assert.Len(t, roundTrip.Artifacts, 5, "C12 5-artifact envelope preserved")
}

// TestManifestBytes_StableAcrossReencode verifies byte-stability:
// marshalling the same manifest twice produces identical bytes, so
// the JobFinalizer.IdempotencyCache + completion fingerprint
// recognise a replay as the SAME request (godlike/07 idempotency).
func TestManifestBytes_StableAcrossReencode(t *testing.T) {
	manifest := buildStockManifest("wf", "job-id")
	first, err1 := manifestBytes(manifest)
	require.NoError(t, err1)
	second, err2 := manifestBytes(manifest)
	require.NoError(t, err2)
	assert.Equal(t, first, second, "manifest marshalling MUST be byte-stable for fingerprint idempotency")
}

// ── fakeJobFinalizer (recording stub for integration tests) ────────

// fakeJobFinalizer records CompleteWithArtifacts calls so service
// tests can assert the gate→finalizer wiring preserves every field
// of the canonical envelope. Production wiring passes the concrete
// *finalizer.Finalizer (which the broker mediates via
// agentjobs.Broker.CompleteWithArtifacts).
type fakeJobFinalizer struct {
	calls []finalization.FinalizationRequest
	// errOnNextCall makes the next call return err instead of success.
	errOnNextCall error
	// response overrides the FinalizationResult when non-nil; otherwise
	// the helper builds a default-shape result.
	response *finalization.FinalizationResult
	// lastAttempt captures the lease.Attempt from the most recent call.
	lastAttempt int
	// lastJobID captures the lease.JobID from the most recent call.
	lastJobID string
}

// CompleteWithArtifacts satisfies finalization.JobFinalizer.
func (f *fakeJobFinalizer) CompleteWithArtifacts(_ context.Context, req finalization.FinalizationRequest) (*finalization.FinalizationResult, error) {
	f.calls = append(f.calls, req)
	f.lastAttempt = req.Lease.Attempt
	f.lastJobID = req.Lease.JobID
	if f.errOnNextCall != nil {
		err := f.errOnNextCall
		f.errOnNextCall = nil
		return nil, err
	}
	if f.response != nil {
		return f.response, nil
	}
	return &finalization.FinalizationResult{
		JobID:        req.Result.JobID,
		Status:       "SUCCEEDED",
		CompletedAt:  time.Now().UTC(),
		ArtifactRefs: nil,
	}, nil
}

// TestFakeJobFinalizer_RecordsCallArguments is a smoke test on
// the recording stub itself — guarantees the stub captures
// FinalizationRequest byte-for-byte so the integration tests
// downstream can assert the gate→finalizer wiring preserves
// every field of the canonical envelope.
func TestFakeJobFinalizer_RecordsCallArguments(t *testing.T) {
	f := &fakeJobFinalizer{}
	req := finalization.FinalizationRequest{
		Lease: finalization.Lease{
			LeaseID:   "lease-fake",
			JobID:     "job-fake",
			WorkerID:  "worker-fake",
			Attempt:   1,
			ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
		},
		Result: finalization.ResultManifest{
			SchemaVersion: "v1",
			JobID:         "job-fake",
			Attempt:       1,
			Data:          json.RawMessage(`{"foo":"bar"}`),
		},
		Artifacts: nil,
	}

	resp, err := f.CompleteWithArtifacts(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "SUCCEEDED", resp.Status)
	assert.Equal(t, 1, len(f.calls), "exactly one call recorded")
	assert.Equal(t, "lease-fake", f.calls[0].Lease.LeaseID)
	assert.Equal(t, "job-fake", f.lastJobID, "lastJobID recorded for assertion")
	assert.Equal(t, 1, f.lastAttempt, "lastAttempt recorded for assertion")
}
