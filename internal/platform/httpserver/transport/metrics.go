package transport

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// WireCapabilityMounted is the Prometheus gauge that mirrors the
// WireRegistry mount state. Each tracked capability has one sample:
//
//	wire_capability_mounted{capability="stock"}       1.0   (MOUNTED)
//	wire_capability_mounted{capability="voiceover"}   0.0   (NOT_MOUNTED)
//
// godlike/06 SSOT (one canonical owner per fact): the gauge values
// are derived from the WireRegistry (the canonical source of truth
// for "is this capability mounted?"). The gauge is the wire-format
// mirror — operators read either /ready JSON or /metrics and get
// the same answer.
//
// godlike/07 NO-FAKE-AVAILABILITY: gauge is set to 0 (NOT_MOUNTED)
// for every capability until routes.go::Setup() calls
// syncWireCapabilityMounted. A stale binary that never reaches
// Setup() surfaces every capability as 0 in Grafana — the canonical
// "binary is broken" signal.
//
// promauto registers the gauge against the default Prometheus
// registry at package init. The /metrics handler (registered in
// routes.go) emits the gauge to scrapers.
var WireCapabilityMounted = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "wire_capability_mounted",
		Help: "Capability mount state (1 = MOUNTED, 0 = NOT_MOUNTED). " +
			"Mirrors the /ready wire field for Grafana alerting.",
	},
	[]string{"capability"},
)

// SyncWireCapabilityMounted walks the wire map and updates the
// WireCapabilityMounted gauge for each tracked capability. Called
// once at the end of routes.go::Setup() after the WireRegistry is
// built from the fully-routed engine.
//
// The gauge is set for every known capability (1.0 or 0.0) so
// scraped metrics always include all 8 labels — operators can
// dashboard on `wire_capability_mounted` directly without needing
// to first ensure the binary has seen the capability at least
// once.
func SyncWireCapabilityMounted(reg *WireRegistry) {
	if reg == nil {
		// Nil registry: every capability is NOT_MOUNTED. This is
		// the canonical "stale binary" detection surface.
		for _, cap := range knownCapabilities {
			WireCapabilityMounted.WithLabelValues(cap.name).Set(0)
		}
		return
	}
	for _, cap := range knownCapabilities {
		if reg.IsMounted(cap.name) {
			WireCapabilityMounted.WithLabelValues(cap.name).Set(1)
		} else {
			WireCapabilityMounted.WithLabelValues(cap.name).Set(0)
		}
	}
}
