// Package stock — stager.go (Stock Cutover Commit 1, July 2026).
//
// SourceStager is the neutral abstraction over a source fetcher.
// It replaces the legacy yt-dlp-baked `Service.StageSource(url string)`
// with a capability-typed port that any registered stager (youtube,
// http, artlist, drive, existing_catalog) can satisfy.
//
// Commit 1 ships ONLY the port interface; concrete implementations
// land in Commit 7 (when the legacy Service.StageSource method is
// retired in favour of registered stagers). Keeping the interface
// isolated here lets the orchestrator's compile dependencies
// stabilise first — the orchestrator already accepts (SourceStager)
// as a constructor argument so production wiring in Commit 7 is
// type-stable.
package stockpipeline

import "context"

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
