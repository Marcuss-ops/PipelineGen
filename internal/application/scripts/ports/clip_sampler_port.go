// Package scripts — clip_sampler_port.go declares the canonical
// ClipSampler port shared by the search, catalog, and curate
// resolvers (\u00a73.1 scripts/usecase.SourceResolvers).
//
// godlike/06 SSOT (single source of truth): the deduplication
// + selection + coverage loop is owned by exactly one
// implementation living in usecase/clip_sampler_impl.go. The
// three resolvers normalize their raw candidates into
// []ClipSamplerCandidate, call Select(req, candidates), and
// consume the resulting ClipSamplerResult. There is no
// resolver-local copy of the loop; the user's "vietati tre
// sampler separati" constraint is enforced structurally.
//
// godlike/07 NO-FAKE-AVAILABILITY: the registry wiring in
// composition root fails closed at runtime (see
// internal/application/scripts/usecase/clip_sampler_registry.go).
// A nil registry with caller != "" panics with a typed message;
// callers must wire NewClipSamplerRegistry() at composition time.
//
// The signature Select(req, candidates) \u2192 ClipSamplerResult is the
// "plan \u2192 ResolvedClipPlan" envelope the user requested. The
// `plan` shape is a minimal request envelope (per-caller input
// parameters); the `ResolvedClipPlan` shape (result) wraps the
// deduplicated clip-id list + search-result items into a single
// canonical slice. Both shapes are intentionally narrow to keep
// the port reusable for any caller that produces candidates.
package ports

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ClipSamplerCandidate is one normalized input item. Resolvers
// populate these from their per-source raw types (search hits,
// catalog results, hint clip IDs) before calling Select. Source
// is the canonical "semantic" / "catalog" / "clip_search" tag
// that propagates to scriptpkg.SearchResultItem.Source verbatim.
type ClipSamplerCandidate struct {
	// ClipID is the canonical media_assets.id reference. Empty
	// IDs are skipped (defensive; SemanticSearchPort and
	// LocalCatalogPort should not emit them but dedups are the
	// single place to enforce).
	ClipID string

	// Name is the human-readable label (log/debug only; same
	// scope as ports.ClipSearchHit.Name).
	Name string

	// Score is the cosine-similarity score for semantic/catalog;
	// 0.0 for hint-only IDs (curate path pre-SEM-resolver).
	Score float64

	// Source is the source-family tag for this candidate
	// ("semantic", "catalog", "clip_search", or whatever the
	// caller's per-source convention is). It propagates to
	// scriptpkg.SearchResultItem.Source verbatim.
	Source string
}

// ClipSamplerRequest is the per-call envelope (plan-shaped input
// the user requested). It is intentionally minimal: callers pass
// only the parameters that ACTUALLY affect selection decisions.
// Per-source context (SourceSpec fields beyond limit/coverage/score)
// stays with the caller; the sampler never reads them.
type ClipSamplerRequest struct {
	// Query is the operator-facing search text. Carried for
	// audit/metrics; selection logic does not branch on it.
	Query string

	// Limit is the maximum number of candidates to return. The
	// sampler returns at most Limit clip IDs. Limit <= 0 is
	// fail-closed (returns an error).
	Limit int

	// MinCoverage is the fractional minimum of (selected / limit)
	// below which the sampler returns a coverage-gate error.
	// Zero = no coverage gate (curate uses this; search/catalog
	// pass 0.0..1.0).
	MinCoverage float64

	// MinScore is the per-candidate floor. Candidates with
	// Score < MinScore are dropped before dedup. 0 = no floor
	// (search/catalog don't prefilter by score; curate does so
	// before calling the sampler). Move-only preserved.
	MinScore float64

	// SourceType is the canonical scriptpkg.SourceType that
	// triggered this Select. Carried for telemetry and error
	// envelope verbosity; selection logic does not branch on it.
	SourceType scriptpkg.SourceType

	// CallingSource identifies the resolver ("search", "catalog",
	// "curate") for audit logging. The single-impl registry
	// resolves every caller to the same impl; this tag propagates
	// to log output only.
	CallingSource string
}

// ClipSamplerResult is the canonical selection output (the
// "ResolvedClipPlan" the user requested). It wraps the
// deduplicated clip-id list + matched items into a single struct
// the resolver can feed into the shared clipContextBuilder.
//
// The fields are byte-for-byte equivalent to what each resolver
// was constructing inline before the move-only refactor:
//   - ClipIDs[i]      == resolver_clipIDs[i]    (deduped, limit-trimmed)
//   - SearchItems[i]  == resolver_searchItems[i] (with Source field set)
type ClipSamplerResult struct {
	// ClipIDs is the deduplicated, limit-trimmed list of
	// canonical media_assets.id references. Order is the order
	// the candidates were emitted by the caller's per-source
	// enumeration (semantic: descending score; catalog: native
	// catalog order; curate: search hits then hint IDs in
	// declaration order).
	ClipIDs []string

	// SearchItems mirrors ClipIDs 1:1 but with full metadata
	// (Name + Score + Source) for downstream consumers that
	// need the rows (ClipSourceBuilder, audit trail).
	SearchItems []scriptpkg.SearchResultItem
}

// ClipSampler is the canonical port. There is exactly ONE
// implementation in this codebase (usecase.defaultClipSampler,
// exposed via ClipSamplerRegistry.SamplerFor). The interface
// enforces godlike/06 SSOT: callers cannot substitute their
// own logic; the dedup+select+cover decision lives in one
// place.
type ClipSampler interface {
	// Select applies the canonical dedup + limit + coverage
	// policy. Pure; deterministic given identical inputs + limit.
	// On coverage-gate failure: returns the partial result AND
	// a non-nil error so the caller can decide whether to
	// surface it (the original search/catalog code returned a
	// pure error envelope; the lifted impl keeps that contract
	// and additionally surfaces the partial result for callers
	// that prefer fail-fast-with-audit over error-only).
	Select(req ClipSamplerRequest, candidates []ClipSamplerCandidate) (ClipSamplerResult, error)
}
