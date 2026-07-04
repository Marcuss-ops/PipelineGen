package mediasearch

import (
	"context"
	"strings"
	"time"
)

// SemanticReadyChecker is the narrow port for the semantic_search_real
// readiness check. Production wiring injects the composition root's
// readiness probe (which composes: embedder presence, semantic
// backend in registry, Qdrant reachability, SQLite hydration path,
// workspace enforcement). Tests pass a stub. PER godlike/06 SSOT
// the api layer never imports `database/sql` / net/http probe
// implementations; readiness state is PRE-COMPUTED by the
// composition root and forwarded via this typed port.
type SemanticReadyChecker interface {
	// Ready returns nil when every canonical semantic-search sub-system is
	// wired correctly; otherwise it returns a typed multi-error listing
	// the failing sub-systems. Per godlike/07 fail-closed the orchestrator
	// returns ALL failures (not the first one) so operators see the full
	// picture in dashboards.
	Ready(ctx context.Context) error
}

// IndexVersionSource is the narrow port for the live search-side
// index version. Production wiring injects a query-time lookup against
// the canonical IndexManifest (composition root wires the read
// adapter). Empty string when the index version is unknown — the
// handler renders `index_version: ""` (no fake availability per
// godlike/07).
type IndexVersionSource interface {
	IndexVersion(ctx context.Context) string
}

// staticIndexVersion is a static-source adapter the test / default
// composition wires use when no live IndexManifest is plumbed.
type staticIndexVersion struct{ v string }

func (s staticIndexVersion) IndexVersion(_ context.Context) string { return s.v }

// StaticIndexVersion produces an IndexVersionSource that always
// returns the supplied string. Use ONLY for tests / dry-runs; the
// canonical composition wires a live adapter.
func StaticIndexVersion(v string) IndexVersionSource { return staticIndexVersion{v: v} }

// buildReadinessReport populates the ReadinessReport DTO from the
// SemanticReadyChecker result. The detailed failure breakdown is
// surfaced as `Failures` (space-joined sanitized labels) so
// dashboards show WHICH sub-systems failed without leaking internal
// details.
//
// godlike/07 fail-closed: when err is non-nil but
// decomposeReadinessFailures returns an EMPTY map (the production
// checker failed to implement the typed Subsystems() contract), no
// per-subsystem boolean can be safely reported as TRUE. Every
// sub-check defaults to false; the Failures field carries the
// "typed readiness probe not wired" sentinel so operators see
// exactly which hard-dependency is missing. This contrast was
// caught by code-review on Commit 2 BACKFILL/CUTOVER: an empty
// subErrs map plus err != nil used to render every per-subsystem
// boolean TRUE (because `subErrs["embedder"] == ""` always evaluated
// true when the map was empty), producing an internally-inconsistent
// report (top-level Ready=false, per-subsystem all GREEN).
func buildReadinessReport(err error, indexVer string) ReadinessReport {
	if err == nil {
		return ReadinessReport{
			Ready:                true,
			Embedder:             true,
			SemanticBackend:      true,
			QdrantReachable:      true,
			SQLiteHydrationReady: true,
			WorkspaceEnforced:    true,
			Timestamp:            nowRFC3339(),
			IndexVersion:         indexVer,
		}
	}
	subErrs := decomposeReadinessFailures(err)
	// godlike/07 fail-closed: typed-probe-absent branch. When the
	// underlying error is non-nil but no typed decomposition is
	// available (subErrs is empty), every per-subsystem boolean
	// MUST default to false. The single Failures token names the
	// absent typed probe so operators see the failure mode.
	typedProbeAbsent := len(subErrs) == 0
	failuresField := joinFailures(subErrs)
	if typedProbeAbsent {
		failuresField = "typed readiness probe not wired (Subsystems() contract missing)"
	}
	return ReadinessReport{
		Ready:                false,
		Embedder:             !typedProbeAbsent && subErrs["embedder"] == "",
		SemanticBackend:      !typedProbeAbsent && subErrs["semantic_backend"] == "",
		QdrantReachable:      !typedProbeAbsent && subErrs["qdrant"] == "" && subErrs["qdrant_reachable"] == "",
		SQLiteHydrationReady: !typedProbeAbsent && subErrs["sqlite_hydration"] == "" && subErrs["sqlite"] == "",
		WorkspaceEnforced:    !typedProbeAbsent && subErrs["workspace"] == "",
		Timestamp:            nowRFC3339(),
		IndexVersion:         indexVer,
		Failures:             failuresField,
	}
}

// decomposeReadinessFailures splits a multi-error message into
// per-subsystem tokens. Production implementations use
// errors.As(target, &rErr) with a typed ReadinessError struct; the
// fallback below intentionally fails-CLOSED (godlike/07) — when the
// typed probe is absent AND err is non-nil, NO sub-check is filled
// (and buildReadinessReport renders the corresponding boolean as
// "false" — i.e. not-ready). This prevents silently-green readiness
// reports when the production checker forgets to implement the
// typed Subsystems() contract.
func decomposeReadinessFailures(err error) map[string]string {
	out := make(map[string]string, 5)
	if err == nil {
		return out
	}
	// Per-subsystem typed probes — typed-error multi-error carrier.
	if rErr, ok := err.(interface{ Subsystems() map[string]string }); ok {
		return rErr.Subsystems()
	}
	// godlike/07 fail-closed: do NOT string-scan. Empty map means
	// "cannot decompose" — buildReadinessReport marks every
	// sub-check as not-ready. A missing typed implementation is
	// a programming error at the composition root, not a
	// routine message scan.
	return out
}

// joinFailures joins the per-subsystem failure summaries into a
// single space-separated string for the report's Failures field.
// godlike/07 fail-closed: the join never throws; empty input → "".
func joinFailures(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		if v != "" {
			parts = append(parts, k+"="+sanitizeMessage(v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

// nowRFC3339 returns the current UTC time formatted as RFC3339. Used
// by the readiness endpoint to stamp the report. Centralizing the
// call here makes a future migration to monotonic-time stamps a
// single-file change. Direct stdlib time usage (no pkg/timeutil
// dependency) keeps the handler free of cross-package coupling for
// one line of formatting.
func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
