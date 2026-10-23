// Package rendercacheinfo is the process-local runtime status of the
// content-addressed overlay render cache.
//
// It exists for one operational question: "is this process reusing certified
// overlay renders, under which epoch, and if not, why not?" That answer used to
// live only in startup logs, so diagnosing a cache that silently failed to wire
// meant grepping the process log after the fact. The composition root sets the
// status once while wiring (exactly like buildinfo.SetRuntime), and the health
// surfaces publish it so one request answers the question.
//
// Honesty rules:
//   - Wired=true means a cache adapter was successfully constructed and
//     installed on the render enqueuer. It does not claim the cache is useful,
//     only that it can be consulted.
//   - Enabled=false comes from the kill switch; Wired=false + Reason explains
//     why the cache is not active (missing cache DB, adapter error, queue not
//     configured).
//   - Reason is a bounded operational string, never a stack trace and never a
//     credential.
package rendercacheinfo

import "sync"

const (
	// TableName is the cache-plane table the adapter reads and writes.
	TableName = "overlay_render_cache_entries"

	// ReasonKillSwitch is published when the operator disabled the cache.
	ReasonKillSwitch = "disabled by " + KillSwitchEnv
	// ReasonNoCacheDB is published when the cache-plane database is absent.
	ReasonNoCacheDB = "cache database is not available"
	// ReasonNotConfigured is the default before the composition root wires the
	// script render runtime (render queue disabled, or process startup).
	ReasonNotConfigured = "script render queue is not configured"
)

// KillSwitchEnv is the canonical spelling of the kill switch. The scripts
// package consumes this constant instead of declaring a second literal, so the
// wiring, the cache decision and the health document cannot drift.
const KillSwitchEnv = "PIPELINEGEN_OVERLAY_RENDER_CACHE"

// Status is the stable JSON document published under "render_cache" on
// /health and /ready. Every key is always present (no omitempty): a script can
// assert fields without branching on key absence, and "unknown" stays
// distinguishable from "absent".
type Status struct {
	Enabled bool   `json:"enabled"`
	Wired   bool   `json:"wired"`
	Epoch   string `json:"epoch"`
	Table   string `json:"table"`
	Reason  string `json:"reason"`
}

var (
	mu      sync.RWMutex
	current Status
)

// SetRuntime records the process-lifecycle status. Called once from the
// composition root right after the script render runtime is wired; safe to call
// from tests that need a deterministic value.
func SetRuntime(status Status) {
	if status.Wired && status.Table == "" {
		status.Table = TableName
	}
	mu.Lock()
	defer mu.Unlock()
	current = status
}

// Current returns a copy of the status. The zero value is an honest "not
// wired, not enabled" document for processes that never built the script
// runtime.
func Current() Status {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Reset restores the zero status. Test-only helper; production wiring sets the
// status once per process.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	current = Status{}
}
