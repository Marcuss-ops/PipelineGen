package cliprender

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The older measurement hand-wrote this hex; keep the helper local so the test
// does not depend on a production formatter.
func testSHA256(seed string) string {
	base := seed
	for len(base) < 64 {
		base += seed
	}
	return base[:64]
}

func TestContinuation_TimingsRoundTrip(t *testing.T) {
	accepted := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	submitted := accepted.Add(90 * time.Second)
	c := Continuation{
		Submission: Submission{
			RenderJobID:   "job-1",
			PlanSHA256:    testSHA256("a"),
			CorrelationID: "corr",
			State:         RemoteRenderSubmitted,
			Attempt:       1,
			AcceptedAt:    &accepted,
			SubmittedAt:   &submitted,
		},
		Resume: ContinuationRef{SHA256: testSHA256("b"), SizeBytes: 42},
	}
	raw, err := c.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var decoded Continuation
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	gotAccepted, gotSubmitted := continuationTimings(&decoded)
	if !gotAccepted.Equal(accepted) {
		t.Fatalf("accepted_at = %s, want %s", gotAccepted, accepted)
	}
	if !gotSubmitted.Equal(submitted) {
		t.Fatalf("submitted_at = %s, want %s", gotSubmitted, submitted)
	}
	if !strings.Contains(string(raw), `"accepted_at"`) {
		t.Fatalf("encoded continuation lost accepted_at: %s", raw)
	}
}

func TestContinuation_TimingsOmittedStayZero(t *testing.T) {
	c := Continuation{
		Submission: Submission{
			RenderJobID: "job-1",
			PlanSHA256:  testSHA256("a"),
			State:       RemoteRenderSubmitted,
			Attempt:     1,
		},
		Resume: ContinuationRef{SHA256: testSHA256("b"), SizeBytes: 1},
	}
	raw, err := c.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(string(raw), `"accepted_at"`) {
		t.Fatalf("absent accepted_at must stay absent: %s", raw)
	}
	var decoded Continuation
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	accepted, submitted := continuationTimings(&decoded)
	if !accepted.IsZero() || !submitted.IsZero() {
		t.Fatalf("timings = (%s, %s), want zero", accepted, submitted)
	}
}

func TestApplyRequestTimings_EndToEndAsync(t *testing.T) {
	m := NewRenderMetricsV2()
	accepted := time.Now().Add(-10 * time.Minute)
	submitted := accepted.Add(90 * time.Second)
	jobStart := submitted.Add(5 * time.Second)
	completed := accepted.Add(11 * time.Minute)

	applyRequestTimings(m, accepted, submitted, jobStart, completed, "pending")

	if got, want := int64(m.RequestToArtifactMS), int64(11*time.Minute/time.Millisecond); got != want {
		t.Fatalf("request_to_artifact_ms = %d, want %d", got, want)
	}
	if got, want := int64(m.SubmitWallMS), int64(90*time.Second/time.Millisecond); got != want {
		t.Fatalf("submit_wall_ms = %d, want %d", got, want)
	}
	if got, want := int64(m.SettleWallMS), int64(565*time.Second/time.Millisecond); got != want {
		t.Fatalf("settle_wall_ms = %d, want %d", got, want)
	}
	if int64(m.RequestToDeliveryMS) != NotInstrumented {
		t.Fatalf("request_to_delivery_ms = %d, want NOT_INSTRUMENTED on a pending delivery", m.RequestToDeliveryMS)
	}
	// The realtime factor is derived from the end-to-end wall, not the settle
	// wall: 60 s of media over 660 s is ~0.09×, not 60/565.
	m.Compute(60)
	if m.RealtimeFactor <= 0 || m.RealtimeFactor > 0.1 {
		t.Fatalf("realtime factor = %f, want ~0.09 from the end-to-end wall", m.RealtimeFactor)
	}
}

func TestApplyRequestTimings_TerminalDelivery(t *testing.T) {
	m := NewRenderMetricsV2()
	accepted := time.Now().Add(-2 * time.Minute)
	completed := accepted.Add(90 * time.Second)

	applyRequestTimings(m, accepted, time.Time{}, accepted, completed, "not_required")

	if got, want := int64(m.RequestToDeliveryMS), int64(m.RequestToArtifactMS); got != want {
		t.Fatalf("request_to_delivery_ms = %d, want %d", got, want)
	}
	if int64(m.SubmitWallMS) != NotInstrumented {
		t.Fatalf("submit_wall_ms = %d, want NOT_INSTRUMENTED when the submit time is absent", m.SubmitWallMS)
	}
}

func TestApplyRequestTimings_NoAcceptanceStaysUninstrumented(t *testing.T) {
	m := NewRenderMetricsV2()
	jobStart := time.Now().Add(-time.Second)
	completed := time.Now()

	applyRequestTimings(m, time.Time{}, time.Time{}, jobStart, completed, "completed")

	if int64(m.RequestToArtifactMS) != NotInstrumented {
		t.Fatalf("request_to_artifact_ms = %d, want NOT_INSTRUMENTED without accepted_at", m.RequestToArtifactMS)
	}
	if int64(m.RequestToDeliveryMS) != NotInstrumented {
		t.Fatalf("request_to_delivery_ms = %d, want NOT_INSTRUMENTED", m.RequestToDeliveryMS)
	}
	if int64(m.SettleWallMS) != int64(completed.Sub(jobStart)/time.Millisecond) {
		t.Fatalf("settle_wall_ms = %d, want the handler wall", m.SettleWallMS)
	}
}
