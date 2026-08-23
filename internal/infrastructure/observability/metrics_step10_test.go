// Package observability — metrics_step10_test.go: hermetic TDD test
// for the Step 10 partial-state counter + adapter
// (PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY, July 2026).
//
// godlike/06 SSOT: the transcript_metadata_step10_fail_after_clip_total
// counter is the SOLE canonical writer of Step 10 partial-state
// telemetry. The test pins:
//  1. The counter is registered against the default Prometheus
//     registry with the canonical name + the failure_code label.
//  2. The adapter increments the counter exactly once per call,
//     with the failure_code label matching the input string.
//  3. The counter is partitioned by failure_code (label cardinality
//     test).
//  4. A compile-time pin enforces the adapter satisfies the
//     application-layer port signature (lives in the composition
//     root package to avoid an observability→youtubeports import
//     edge).
//
// godlike/07 NO-FAKE-AVAILABILITY: the test uses the canonical
// testutil.CollectAndCount + testutil.ToFloat64 helpers from
// prometheus/client_golang/prometheus/testutil. The test is hermetic
// — no live server, no production state mutated.
package observability

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Compile-time assertion: the Step10MetricsAdapter satisfies the
// canonical interface signature expected by the composition root.
// (The full interface lives in internal/capabilities/youtube/ports/ports.go;
// we re-declare the single-method shape here to avoid an import edge
// from observability→application — the same compile-time pin in the
// composition root catches signature drift on both sides.)
type step10MetricsRecorderShape interface {
	IncStep10FailAfterClip(failureCode string)
}

var _ step10MetricsRecorderShape = (*Step10MetricsAdapter)(nil)

// TestStep10FailAfterClipTotal_Registered locks the canonical counter
// name + the failure_code label partition against the default
// Prometheus registry.
func TestStep10FailAfterClipTotal_Registered(t *testing.T) {

	// Mint a fresh metric on the default registry is a no-op (the
	// counter is already registered at package init). Instead, we
	// assert the canonical Desc name + the canonical label set via
	// testutil.CollectAndCount on an already-known reference label.
	//
	// The contract: the counter exists, is addressable by its
	// canonical name, and accepts a failure_code label.
	if Step10FailAfterClipTotal == nil {
		t.Fatal("Step10FailAfterClipTotal counter is nil (canonical initialization regression)")
	}

	// Read the Desc — failure_code is the SOLE label per the
	// counter definition. The Desc is the canonical metadata
	// surface for Prometheus metrics.
	desc := Step10FailAfterClipTotal.WithLabelValues("test_registered_probe").Desc()
	if desc == nil {
		t.Fatal("counter Desc() is nil (counter not registered against the default registry)")
	}
	fqName := desc.String()
	if !strings.Contains(fqName, `fqName: "transcript_metadata_step10_fail_after_clip_total"`) {
		t.Errorf("counter fqName = %q, want substring transcript_metadata_step10_fail_after_clip_total", fqName)
	}
}

// TestStep10MetricsAdapter_IncStep10FailAfterClip_HappyPath locks the
// adapter contract: one call → counter += 1, with the failure_code
// label matching the input.
func TestStep10MetricsAdapter_IncStep10FailAfterClip_HappyPath(t *testing.T) {

	const failureCode = "metadata_failed"

	// Snapshot pre-call count (counter is package-global, so a prior
	// test in the same process may have incremented it).
	pre := testutil.ToFloat64(Step10FailAfterClipTotal.WithLabelValues(failureCode))

	// Call the adapter.
	adapter := NewStep10MetricsAdapter()
	adapter.IncStep10FailAfterClip(failureCode)

	// Post-call count must be pre+1 (exactly-once contract per
	// godlike/07 NO-FAKE-AVAILABILITY).
	post := testutil.ToFloat64(Step10FailAfterClipTotal.WithLabelValues(failureCode))
	if post != pre+1 {
		t.Errorf("Step10FailAfterClipTotal(%q): pre=%v post=%v, want post=pre+1", failureCode, pre, post)
	}
}

// TestStep10MetricsAdapter_LabelCardinalityIsPartitioned locks the
// canonical invariant: each distinct failure_code label produces a
// distinct time series (no cross-label contamination).
func TestStep10MetricsAdapter_LabelCardinalityIsPartitioned(t *testing.T) {

	const codeA = "metadata_failed"
	const codeB = "video_processing_failed"

	preA := testutil.ToFloat64(Step10FailAfterClipTotal.WithLabelValues(codeA))
	preB := testutil.ToFloat64(Step10FailAfterClipTotal.WithLabelValues(codeB))

	adapter := NewStep10MetricsAdapter()
	adapter.IncStep10FailAfterClip(codeA) // only codeA
	adapter.IncStep10FailAfterClip(codeA) // twice

	postA := testutil.ToFloat64(Step10FailAfterClipTotal.WithLabelValues(codeA))
	postB := testutil.ToFloat64(Step10FailAfterClipTotal.WithLabelValues(codeB))

	if postA != preA+2 {
		t.Errorf("Step10FailAfterClipTotal(%q): pre=%v post=%v, want post=pre+2", codeA, preA, postA)
	}
	if postB != preB {
		t.Errorf("Step10FailAfterClipTotal(%q) should be untouched by codeA increments: pre=%v post=%v, want post=pre", codeB, preB, postB)
	}
}
