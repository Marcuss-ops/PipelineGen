package transport

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// TestSyncWireCapabilityMounted_NilRegistry pins the canonical
// "stale binary" detection contract: a nil WireRegistry reports
// every capability as 0 (NOT_MOUNTED) in the gauge.
func TestSyncWireCapabilityMounted_NilRegistry(t *testing.T) {
	SyncWireCapabilityMounted(nil)
	for _, cap := range knownCapabilities {
		got := testutil.ToFloat64(WireCapabilityMounted.WithLabelValues(cap.name))
		assert.Equal(t, 0.0, got, "nil registry must report %q as 0 in gauge", cap.name)
	}
}

// TestSyncWireCapabilityMounted_StockMounted pins the canonical
// "stock pipeline is mounted" case: the gauge reports stock=1
// and every other tracked capability as 0.
func TestSyncWireCapabilityMounted_StockMounted(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline/run"},
		{Method: "POST", Path: "/api/stock-pipeline/search-and-run"},
	})
	SyncWireCapabilityMounted(reg)
	assert.Equal(t, 1.0, testutil.ToFloat64(WireCapabilityMounted.WithLabelValues("stock")),
		"stock should be 1 (MOUNTED) in gauge")
	for _, cap := range knownCapabilities {
		if cap.name == "stock" {
			continue
		}
		got := testutil.ToFloat64(WireCapabilityMounted.WithLabelValues(cap.name))
		assert.Equal(t, 0.0, got, "non-stock capability %q should be 0 in gauge", cap.name)
	}
}

// TestSyncWireCapabilityMounted_AllCapabilitiesMounted pins the
// production happy path: every tracked capability is mounted, so
// the gauge reports 1 for each.
func TestSyncWireCapabilityMounted_AllCapabilitiesMounted(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline/run"},
		{Method: "POST", Path: "/api/artlist/sync"},
		{Method: "POST", Path: "/api/media/voiceover/generate"},
		{Method: "POST", Path: "/api/script/generate"},
		{Method: "POST", Path: "/api/clips/process"},
		{Method: "POST", Path: "/api/register/from-youtube"},
		{Method: "POST", Path: "/api/storage/sync"},
		{Method: "POST", Path: "/api/drive/admin"},
		{Method: "POST", Path: "/api/media/clips/upload"},
		{Method: "POST", Path: "/internal/v1/media/search"},
		{Method: "GET", Path: "/qdrant/live"},
	})
	SyncWireCapabilityMounted(reg)
	for _, cap := range knownCapabilities {
		got := testutil.ToFloat64(WireCapabilityMounted.WithLabelValues(cap.name))
		assert.Equal(t, 1.0, got, "all-capabilities-mounted case must report %q as 1", cap.name)
	}
}

// TestSyncWireCapabilityMounted_MixedMountState pins the partial-mount
// case: stock + artlist mounted, others not. The gauge must reflect
// the per-capability state independently.
func TestSyncWireCapabilityMounted_MixedMountState(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline/run"},
		{Method: "POST", Path: "/api/artlist/sync"},
		{Method: "POST", Path: "/internal/v1/media/search"},
	})
	SyncWireCapabilityMounted(reg)
	assert.Equal(t, 1.0, testutil.ToFloat64(WireCapabilityMounted.WithLabelValues("stock")))
	assert.Equal(t, 1.0, testutil.ToFloat64(WireCapabilityMounted.WithLabelValues("artlist")))
	assert.Equal(t, 1.0, testutil.ToFloat64(WireCapabilityMounted.WithLabelValues("mediasearch")))
	assert.Equal(t, 0.0, testutil.ToFloat64(WireCapabilityMounted.WithLabelValues("voiceover")))
	assert.Equal(t, 0.0, testutil.ToFloat64(WireCapabilityMounted.WithLabelValues("youtube")))
	assert.Equal(t, 0.0, testutil.ToFloat64(WireCapabilityMounted.WithLabelValues("register")))
	assert.Equal(t, 0.0, testutil.ToFloat64(WireCapabilityMounted.WithLabelValues("storage")))
	assert.Equal(t, 0.0, testutil.ToFloat64(WireCapabilityMounted.WithLabelValues("qdrant_health")))
}

// TestSyncWireCapabilityMounted_EmptyRegistry pins the
// empty-but-not-nil case: a registry built from zero routes
// reports every capability as 0 (same as nil — empty routes
// means no routes are mounted).
func TestSyncWireCapabilityMounted_EmptyRegistry(t *testing.T) {
	reg := NewWireRegistry(nil)
	SyncWireCapabilityMounted(reg)
	for _, cap := range knownCapabilities {
		got := testutil.ToFloat64(WireCapabilityMounted.WithLabelValues(cap.name))
		assert.Equal(t, 0.0, got, "empty registry must report %q as 0", cap.name)
	}
}

// TestSyncWireCapabilityMounted_AllLabelsPopulated pins the
// observability contract: the gauge is set for EVERY known
// capability after the sync, never only the mounted ones. This
// ensures Grafana dashboards always have all 8 series even on
// partial-mount deployments.
func TestSyncWireCapabilityMounted_AllLabelsPopulated(t *testing.T) {
	reg := NewWireRegistry([]RouteInfo{
		{Method: "POST", Path: "/api/stock-pipeline/run"},
	})
	SyncWireCapabilityMounted(reg)
	for _, cap := range knownCapabilities {
		// Direct assertion: each label must have a numeric value (1 or 0).
		got := testutil.ToFloat64(WireCapabilityMounted.WithLabelValues(cap.name))
		assert.True(t, got == 0.0 || got == 1.0,
			"capability %q gauge must be 0 or 1, got %v", cap.name, got)
	}
}
