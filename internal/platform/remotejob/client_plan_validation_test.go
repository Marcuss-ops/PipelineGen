package remotejob

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

const r36PlanRejectionBody = `{"error":"m2m_quota_failure","message":"quota requires a valid CompiledRenderPlanV2: video_tracks[0].segments[94]: source_range_out_of_bounds (expected source_in_us + source_duration_us <= 15000000, observed 14000000 + 1013000); video_tracks[0].segments[125]: source_range_out_of_bounds (expected source_in_us + source_duration_us <= 26625000, observed 20000000 + 6645000); video_tracks[0].segments[195]: source_range_out_of_bounds (expected source_in_us + source_duration_us <= 25209000, observed 24000000 + 1213000)","ok":false}`

// TestPrepareMapsQuotaEnvelopeToTypedPlanError pins the operator-facing fix:
// the exact r36 response is not a quota incident, it is an invalid compiled
// plan, and it is terminal (no retry can make the same bytes valid).
func TestPrepareMapsQuotaEnvelopeToTypedPlanError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(r36PlanRejectionBody))
	}))
	defer server.Close()

	client := New(server.URL, "test-token")
	_, err := client.Prepare(context.Background(), map[string]any{"job_type": "video.render.v2"})
	if err == nil {
		t.Fatal("PREPARE with an invalid plan must fail")
	}
	if !errors.Is(err, ErrInvalidCompiledRenderPlan) {
		t.Fatalf("error %v is not ErrInvalidCompiledRenderPlan", err)
	}
	var planErr *PlanValidationError
	if !errors.As(err, &planErr) {
		t.Fatalf("error %v does not expose PlanValidationError", err)
	}
	if planErr.Code != "m2m_quota_failure" {
		t.Fatalf("code = %q, want the Master's raw code preserved", planErr.Code)
	}
	wantSegments := []string{"segments[125]", "segments[195]", "segments[94]"}
	if len(planErr.Segments) != len(wantSegments) {
		t.Fatalf("segments = %#v, want %#v", planErr.Segments, wantSegments)
	}
	for i, want := range wantSegments {
		if planErr.Segments[i] != want {
			t.Fatalf("segments = %#v, want %#v", planErr.Segments, wantSegments)
		}
	}
	if retry.IsTransient(err) {
		t.Fatal("a deterministic plan defect must not be retried as a transient infrastructure error")
	}
	if !strings.Contains(err.Error(), "invalid compiled render plan") {
		t.Fatalf("error %q does not name the real cause", err)
	}
}

// TestPrepareKeepsGenuineQuotaFailureDistinct: the mapper must not relabel every
// m2m_quota_failure as a plan bug. Only the compiled-plan/source-range shape is
// reclassified; a real quota envelope keeps its raw HTTP error.
func TestPrepareKeepsGenuineQuotaFailureDistinct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"m2m_quota_failure","message":"monthly quota exceeded","ok":false}`))
	}))
	defer server.Close()

	client := New(server.URL, "test-token")
	_, err := client.Prepare(context.Background(), map[string]any{"job_type": "video.render.v2"})
	if err == nil {
		t.Fatal("PREPARE must fail")
	}
	if errors.Is(err, ErrInvalidCompiledRenderPlan) {
		t.Fatalf("a genuine quota failure was relabelled as a plan defect: %v", err)
	}
	if !strings.Contains(err.Error(), "monthly quota exceeded") {
		t.Fatalf("error %q lost the Master's quota message", err)
	}
}
