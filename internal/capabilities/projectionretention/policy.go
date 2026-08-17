// Package projectionretention defines the canonical retention policy for
// Qdrant projections. It owns the pure drop/keep decision (no network, no
// database) so the policy has a single source of truth instead of living
// inside an ad-hoc cleanup script. Concrete adapters
// (internal/platform/qdrant/collections) perform the I/O and delegate the
// decision here.
//
// Semantics (mirrors the historical CleanupWithConfig behaviour):
//
//   - RetentionDays <= 0 disables the sweep entirely.
//   - KeepLastN is the total known-good collection count to retain
//     (active alias target + N-1 rollback targets); hard floor of 2.
//   - A registry-ACTIVE collection is never dropped even when the alias
//     momentarily disagrees (defense-in-depth).
//   - Stale partials (FAILED / BUILDING / VALIDATING) are never protected
//     by the keep-last-N tail: a failed build must not crowd out a
//     known-good rollback target.
//   - Rollback candidates are scoped to the active target's schema
//     generation: a collection from a different embedding generation (e.g. an older
//     collection when the active target is e5) is dropped, never kept as a
//     rollback — its vectors are incompatible with the active embedder.
//   - The protected rollback target is always pinned (never dropped).
//   - In status-aware mode (Statuses non-nil) a prefix-matching collection
//     with no durable registry status is UNKNOWN and is left untouched
//     (never dropped) — fail-closed.
package projectionretention

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// ProjectionRetentionPolicy is the canonical retention policy for Qdrant
// projections. QDRANT_PROJECTION_RETENTION maps onto KeepLastN.
type ProjectionRetentionPolicy struct {
	// KeepLastN is the total number of known-good collections to retain
	// (active alias target + N-1 rollback targets of the SAME schema
	// generation). Floor of 2. Cross-schema collections are never counted
	// in this tail.
	KeepLastN int

	// RetentionDays is the policy switch: <=0 disables the sweep.
	RetentionDays int

	// RetiredPrefixes lists additional physical-name prefixes from retired
	// schema generations (e.g. the superseded multilingual-e5 schema:
	// "media_assets_v3_e5_768_siglip_768"). The current schema's
	// CanonicalName() prefix is always matched implicitly.
	RetiredPrefixes []string
}

// Plan is the pure drop/keep decision produced by Decide.
type Plan struct {
	// Drop lists the eligible collections to delete.
	Drop []string
	// Keep lists everything retained: the active alias target, any
	// registry-ACTIVE collection, and the kept rollback tail.
	Keep []string
	// Protected is the explicitly-protected subset of Keep (registry-ACTIVE
	// collections plus the kept rollback tail).
	Protected []string
}

// Input is the pure decision input for a single sweep.
type Input struct {
	// Collections is the full list of physical collection names.
	Collections []string
	// ActiveTarget is the runtime alias target ("" when unwritten).
	ActiveTarget string
	// CurrentPrefix is the current schema's canonical physical-name prefix.
	CurrentPrefix string
	// Statuses maps collection name -> durable projection lifecycle status.
	// A nil map disables status-aware keeping (legacy name-sort behaviour).
	// A non-nil map enables it: a prefix-matching collection absent from
	// the map is UNKNOWN and is never dropped (fail-closed).
	Statuses map[string]mediaregistry.ProjectionStatus
	// ProtectedRollback is an explicit rollback target to pin (never
	// dropped), independent of the keep-last-N tail.
	ProtectedRollback string
}

// Decide computes the drop/keep plan without touching Qdrant or SQLite.
func (p ProjectionRetentionPolicy) Decide(in Input) (Plan, error) {
	var plan Plan
	if p.RetentionDays <= 0 {
		return plan, nil
	}
	keepLastN := p.KeepLastN
	if keepLastN < 2 {
		keepLastN = 2
	}
	prefixes, err := RetentionPrefixes(in.CurrentPrefix, p.RetiredPrefixes)
	if err != nil {
		return plan, err
	}
	activePrefix := activeSchemaPrefix(in.ActiveTarget, in.CurrentPrefix, prefixes)

	eligible := make([]string, 0, len(in.Collections))
	for _, name := range in.Collections {
		if !MatchesAnyPrefix(name, prefixes) {
			continue
		}
		if name == in.ActiveTarget {
			plan.Keep = append(plan.Keep, name)
			continue
		}
		status, known := in.Statuses[name]
		if in.Statuses != nil && !known {
			// Unknown collection in status-aware mode: fail closed by
			// leaving it untouched. Dropping a prefix-matching collection
			// with no durable registry status could destroy an
			// operator-managed or crash-orphaned collection.
			continue
		}
		if status == mediaregistry.ProjectionActive {
			plan.Keep = append(plan.Keep, name)
			plan.Protected = append(plan.Protected, name)
			continue
		}
		eligible = append(eligible, name)
	}

	// Stale partials are never protected by the keep-last-N tail, and a
	// collection from a different schema generation is never a valid
	// rollback for the active target (drop it instead of pinning an
	// incompatible rollback).
	tail := make([]string, 0, len(eligible))
	for _, name := range eligible {
		if IsStalePartial(in.Statuses[name]) {
			continue
		}
		if !strings.HasPrefix(name, activePrefix) {
			continue
		}
		tail = append(tail, name)
	}

	keepSet := make(map[string]bool, len(tail)+1)
	if in.ProtectedRollback != "" {
		keepSet[in.ProtectedRollback] = true
	}
	// Sort descending (newest-first) and keep keepLastN-1 known-good
	// rollbacks beyond the active target.
	sort.Sort(sort.Reverse(sort.StringSlice(tail)))
	keepLeft := keepLastN - 1
	for _, name := range tail {
		if keepLeft <= 0 {
			break
		}
		if keepSet[name] {
			continue
		}
		keepSet[name] = true
		keepLeft--
		plan.Keep = append(plan.Keep, name)
		plan.Protected = append(plan.Protected, name)
	}

	for _, name := range eligible {
		if keepSet[name] {
			continue
		}
		plan.Drop = append(plan.Drop, name)
	}
	return plan, nil
}

// RetentionPrefixes returns the current schema prefix plus any retired
// prefixes, deduplicated and validated. A retired prefix that equals — or
// is a prefix of — the current canonical name is rejected fail-closed:
// such a prefix would match the live collection fleet (e.g. a bare
// "media_assets" or "media_assets_v3").
func RetentionPrefixes(current string, retired []string) ([]string, error) {
	prefixes := make([]string, 0, 1+len(retired))
	seen := map[string]bool{current: true}
	prefixes = append(prefixes, current)
	for _, p := range retired {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == current || strings.HasPrefix(current, p) {
			return nil, fmt.Errorf("retention: retired prefix %q overlaps the current schema prefix %q and would match live collections", p, current)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		prefixes = append(prefixes, p)
	}
	return prefixes, nil
}

// MatchesAnyPrefix reports whether name has any of the given prefixes.
func MatchesAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// IsStalePartial reports whether a projection status represents a build
// that never became a known-good target. Such collections must never be
// protected by the keep-last-N tail.
func IsStalePartial(status mediaregistry.ProjectionStatus) bool {
	switch status {
	case mediaregistry.ProjectionFailed, mediaregistry.ProjectionBuilding, mediaregistry.ProjectionValidating:
		return true
	default:
		return false
	}
}

// activeSchemaPrefix resolves the schema-generation prefix of the active
// alias target so rollback candidates are scoped to the same generation.
// A collection from a different schema generation (e.g. an older collection
// when the active target is e5) is never a valid rollback: its document
// vectors live in a different vector space, so keeping it as a rollback
// would re-introduce the query/documents drift on rollback. When the active
// target is empty or matches no known prefix, the current schema prefix is
// used (the SSOT generation).
func activeSchemaPrefix(activeTarget, currentPrefix string, prefixes []string) string {
	best := ""
	for _, p := range prefixes {
		if strings.HasPrefix(activeTarget, p) && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		return currentPrefix
	}
	return best
}
