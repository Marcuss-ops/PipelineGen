// Package workerruntime — capabilities.go (P1-3, June 2026).
//
// ParseAndValidateCaps is the canonical entry for the
// $VELOX_WORKER_CAPABILITIES env var. Pre-P1-3 logic moved
// verbatim from cmd/worker/main.go::parseAndValidateCaps:
//
//   - Empty env         → error (Creator Blocco 1.2: refusing to start
//     with full capability set; operators must set
//     a profile or explicit capabilities)
//   - Malformed JSON    → error (fail-fast)
//   - Empty job_types   → error (fail-fast)
//   - Unknown type      → error (fail-fast)
//   - Duplicate entries → dedup in input order
//   - Final set         → sort.Strings (deterministic ordering pin)
//
// Behaviour intention: the W1 spec calls for "final set sorted
// and non-empty". Without the sort the order would mirror input
// order, which would mask regressions in logging / agent-config
// tooling that assumes a deterministic order. The sort is pinned
// to keep [c, a, b] → [a, b, c] canonical.
//
// Creator Blocco 1.2 (July 2026): the empty-env → full-registered-set
// path was removed. Operators must now explicitly set either
// $VELOX_WORKER_PROFILE (which gates via ResolveCapabilities) or
// $VELOX_WORKER_CAPABILITIES with a non-empty job_types array.
// A worker started with neither now fails closed.
package workerruntime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// ParseAndValidateCaps parses raw (the JSON body of
// $VELOX_WORKER_CAPABILITIES) and validates every entry against
// the registered set. Empty raw returns an error (Creator Blocco 1.2:
// operators must set a profile or explicit capabilities).
func ParseAndValidateCaps(raw string, registeredTypes []string) (appjobs.WorkerCapabilities, error) {
	if strings.TrimSpace(raw) == "" {
		return appjobs.WorkerCapabilities{}, fmt.Errorf(
			"VELOX_WORKER_CAPABILITIES empty and no worker profile set — refusing to start with full capability set")
	}
	var caps appjobs.WorkerCapabilities
	if err := json.Unmarshal([]byte(raw), &caps); err != nil {
		return appjobs.WorkerCapabilities{}, fmt.Errorf("malformed VELOX_WORKER_CAPABILITIES JSON: %w", err)
	}
	if len(caps.JobTypes) == 0 {
		return appjobs.WorkerCapabilities{}, fmt.Errorf("VELOX_WORKER_CAPABILITIES has empty job_types array")
	}

	registered := make(map[string]struct{}, len(registeredTypes))
	for _, t := range registeredTypes {
		registered[t] = struct{}{}
	}

	seen := make(map[string]struct{}, len(caps.JobTypes))
	var validated []string
	for _, jt := range caps.JobTypes {
		jt = strings.TrimSpace(jt)
		if jt == "" {
			continue
		}
		if _, ok := seen[jt]; ok {
			continue
		}
		seen[jt] = struct{}{}
		if _, ok := registered[jt]; !ok {
			return appjobs.WorkerCapabilities{}, fmt.Errorf("VELOX_WORKER_CAPABILITIES contains unknown job type: %s", jt)
		}
		validated = append(validated, jt)
	}
	if len(validated) == 0 {
		return appjobs.WorkerCapabilities{}, fmt.Errorf("VELOX_WORKER_CAPABILITIES resolved to empty set")
	}

	// W1 spec: final set sorted and non-empty. Without this sort
	// the resulting slice would mirror input order, which would
	// mask regression in logging/agent-config tooling that
	// assumes deterministic order. Pin it here so a test that
	// hands in ["c","a","b"] snaps to canonical ascending order.
	sort.Strings(validated)
	caps.JobTypes = validated
	return caps, nil
}
