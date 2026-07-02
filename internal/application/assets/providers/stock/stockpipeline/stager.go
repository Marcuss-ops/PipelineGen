// Package stock — stager.go (Stock Cutover Commits 1 + 2, July 2026).
//
// SourceStager is the neutral abstraction over a source fetcher.
// It replaces the legacy yt-dlp-baked `Service.StageSource(url string)`
// with a capability-typed port that any registered stager (youtube,
// http, artlist, drive, existing_catalog) can satisfy.
//
// Commit 1 ships the port interface; Commit 2 adds a NoopSourceStager
// to satisfy the Orchestrator's compile-time nil-guard while the
// real implementations land in Commit 7 (when the legacy
// Service.StageSource method is retired in favour of registered
// stagers).
//
// Keeping the port isolated here lets the orchestrator's compile
// dependencies stabilise first — the orchestrator already accepts
// (SourceStager) as a constructor argument so production wiring in
// Commit 7 is type-stable.
package stockpipeline

import (
	"context"
	"errors"
)

// SourceStager is the typed port for source-fetch capability.
//
// Implementations:
//   - youtube (yt-dlp-backed; replaces Service.StageSource URL-only path in Commit 7)
//   - http    (direct download for already-URL'd sources)
//   - artlist (Artlist-asset retrieval via the Artlist search/path layer)
//   - drive   (existing Drive file fetched from current Drive path)
//   - existing_catalog (lookup of an already-ingested stock asset)
//
// All implementations must return a StagedSource whose LocalPath
// points to a temporary file the caller owns and must Cleanup()
// when done — the contract mirrors Service.StageSource.Stage.
type SourceStager interface {
	// Stage downloads a single source and returns a StagedSource
	// pointing at a temp file the orchestrator (or its delegate)
	// owns. The cleanup function is invoked by the orchestrator
	// once the downstream pipeline has consumed the file.
	Stage(ctx context.Context, req StageRequest) (*StagedSource, error)
}

// ── NoopSourceStager (Stock Cutover Commit 2, July 2026) ──────────────
//
// NoopSourceStager is the Commit 2 placeholder SourceStager. It
// satisfies the SourceStager interface so the Orchestrator's
// compile-time nil-guard (`o.planner == nil || o.steps == nil ||
// o.stager == nil`) is satisfied, but its Stage call returns
// ErrStagerNotImplemented because the legacy Service.Run body has
// been retired in Commit 2 — no real SourceStager impl exists yet.
//
// Commit 7 replaces this with the real implementations
// (youtube/http/artlist/drive/existing_catalog). The replacement
// keeps the same SourceStager port so the Orchestrator's compile
// dependencies are stable across the Commit 2 → Commit 7 transition.
//
// Why not nil-stager? The Orchestrator's Run signature rejects
// nil-stager early (ErrOrchestratorNilDeps); a typed noop lets
// Service.RunOrchestrator construct a Service.Run → Orchestrator
// delegate without conditional guards.
type NoopSourceStager struct{}

// NewNoopSourceStager constructs a NoopSourceStager. Returns the
// canonical interface value so callers can swap in real
// implementations behind the same SourceStager port without
// touching Service.RunOrchestrator.
func NewNoopSourceStager() SourceStager {
	return &NoopSourceStager{}
}

// ErrStagerNotImplemented is returned by NoopSourceStager.Stage
// because no real source-stager implementation lands before
// Commit 7. The error wraps with `%w` + a "Commit 7 ships real
// impl" annotation so operators running Commit 2 in production
// see a clear message rather than a silent zero-value.
var ErrStagerNotImplemented = errors.New("stockpipeline: SourceStager not implemented (Stock Cutover Commit 7 ships the real impl; NoopSourceStager is the Commit 2 placeholder)")

// Compile-time assertion: *NoopSourceStager satisfies SourceStager.
// Catches signature drift at build (not run time) and pins the
// noop path as a first-class stager in the lineage.
var _ SourceStager = (*NoopSourceStager)(nil)

// Stage returns ErrStagerNotImplemented. The Orchestrator's Run
// in Commit 2 does NOT actually invoke stager.Stage (the
// `stage_sources` step is Begin/Complete only — see
// orchestrator.go::Run), so this error never surfaces in
// Commit 2's end-to-end path. Commit 7 wires the real Stagers.
func (n *NoopSourceStager) Stage(ctx context.Context, req StageRequest) (*StagedSource, error) {
	return nil, ErrStagerNotImplemented
}

// StageRequest captures the minimum contract any SourceStager needs.
//
// URL is the canonical source identifier (a YouTube URL, an HTTP
// file URL, an Artlist asset URL, a Drive file ID, etc. depending
// on the implementation). Source is a hint about the source kind —
// concrete stagers may short-circuit on it (e.g. a "drive:" prefix
// skips URL parsing).
//
// SourceVersion is the content-hash produced by the upstream
// discovery stage (so the stager can short-circuit if the cached
// file is still valid).
//
// PolicyVersion is the planner policy version the orchestrator
// was operating under — stagers may persist it as the staged
// file's owning policy for the manifest.
//
// TimeoutSeconds bounds the stager-side download (a zero value
// falls back to the stager's own default; this is the per-call
// orchestrator override).
type StageRequest struct {
	URL            string
	Source         string
	SourceVersion  string
	PolicyVersion  string
	TimeoutSeconds int
}
