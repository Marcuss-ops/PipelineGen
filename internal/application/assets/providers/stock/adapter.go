// Package stock adapts internal/media/stockpipeline.Service to the
// canonical providers.FetchProvider contract in
// internal/application/assets/providers.
//
// Wave 12 scope: this adapter is the FIRST real FetchProvider in
// the codebase. artlist and youtube implement only SearchProvider
// because their download paths live elsewhere (artlist's pipeline
// is stockpipeline + drive upload; youtube's yt-dlp path is the
// channel-monitor's responsibility). Fetch was reserved at the
// contract level so the upcoming channel-monitor YouTube fetch path
// and Stock's binary delivery had a stable target — Stock is now
// the first concrete FetchProvider and SETS THE PATTERN for any
// subsequent Fetch source (eg. artlist if a public fetch binary
// ever lands, channel-monitor if it adopts the contract).
//
// Layout note (post-Agent-3 cleanup): stock lives at
// providers/stock/adapter.go, parallel to artlist/adapter.go and
// youtube/adapter.go. The historical nesting under
// providers/adapters/<src>/ is removed.
package stock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/media/stockpipeline"
)

// Compile-time assertion: *Adapter satisfies providers.FetchProvider.
// Catches interface drift at build time. The Adapter intentionally
// does NOT implement SearchProvider — Stock is fetch-only by design
// (the search path is stockpipeline.query.go::resolveQuery, which
// is part of pipeline orchestration, NOT a source-search surface).
var _ providers.FetchProvider = (*Adapter)(nil)

// ErrSourceNotWired is returned by Fetch when the Adapter has no
// underlying runner wired (nil interface value).
var ErrSourceNotWired = errors.New("stock adapter: runner not wired")

// stockRunner is the minimal internal interface the adapter depends on.
// Defining it private to this package lets the unit tests inject a
// stub without constructing a full *stockpipeline.Service (which
// carries a heavy Drive + Jobs + AssetIndex chain).
//
// *stockpipeline.Service satisfies stockRunner via its public Run method.
//
// NewAdapter accepts a stockRunner (NOT a concrete *stockpipeline.Service)
// so unit tests can hand in *fakeRunner without a constructor signature
// mismatch. Production wiring in internal/app/registry.go passes
// *stockpipeline.Service which auto-satisfies the interface.
type stockRunner interface {
	Run(ctx context.Context, input *stockpipeline.RunInput) (*stockpipeline.PipelineResult, error)
}

// stockDefaults holds the pipeline-tuning numbers used when the
// caller supplies only a SourceRef (URL). Extracted into named
// constants with rationale so future readers understand why each
// value was chosen — these are NOT pipeline-policy magic numbers
// pulled from thin air.
//
// Rationale for each default:
//
//   - TotalMinutes: 5  — minimum sensible target so the pipeline
//     produces ≥ 1 chunk even for short clips.
//   - ChunkDuration: 25s  — matches stockpipeline.DefaultPipelineConfig
//     and keeps the chunking boundary stable across registrations.
//   - ClipDuration:  5s  — matches DefaultPipelineConfig.ClipDuration.
//
// All three should be promoted to FetchRequest fields once a second
// FetchProvider (channel-monitor YouTube) sets the precedent —
// keeping them adapter-internal for now (YAGNI) avoids API churn.
const (
	stockDefaultTotalMinutes  = 5
	stockDefaultChunkDuration = 25
	stockDefaultClipDuration  = 5
)

// Adapter wraps a stockRunner (production: *stockpipeline.Service)
// and exposes it as a providers.FetchProvider. The Adapter does NOT
// invent new fetch semantics: it routes a single known URL through
// stockpipeline's DirectURLs branch and returns the staged chunk's
// LocalPath. Heavy lifting (FFmpeg composition, Drive upload,
// clip extraction) stays inside stockpipeline.Service — this
// adapter exposes only the "give me a staged copy of URL X" boundary.
type Adapter struct {
	runner stockRunner
}

// NewAdapter returns an Adapter wrapping the given stock pipeline
// runner. Accepts the private stockRunner interface so unit tests
// inject a stub; production callers pass *stockpipeline.Service
// which auto-satisfies the interface.
//
// A nil runner is tolerated (the Adapter compiles and is
// composable) but Fetch returns ErrSourceNotWired at runtime — see
// the Fetch doc for why a struct guard is preferable to a panic.
func NewAdapter(runner stockRunner) *Adapter {
	return &Adapter{runner: runner}
}

// Name implements providers.Provider. Stable across calls.
// "stock" is the canonical identifier; Registry.Register rejects
// empty names.
func (a *Adapter) Name() string { return "stock" }

// Capabilities implements providers.Provider.
//
// CapabilityFetch IS declared: this adapter IS a FetchProvider, so
// Registry.ByCapability(CapabilityFetch) returns it. This is the
// first adapter in the codebase to advertise CapabilityFetch, so
// the registry's fetch capability slot goes from empty → {stock}.
//
// CapabilitySearch is intentionally NOT declared: Stock's URL
// resolution lives in stockpipeline.query.go (resolveQuery) —
// it is part of pipeline orchestration, NOT a SearchProvider
// surface. Treating Stock as a search source would conflate the
// pipeline-runner and source-searcher roles.
//
// CapabilityVideo IS declared: Stock returns video content; a
// caller filtering ByCapability(CapabilityVideo) sees Stock.
func (a *Adapter) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilityFetch,
		providers.CapabilityVideo,
	}
}

// Fetch implements providers.FetchProvider.
//
// What Fetch does:
//
//   - Validates runner is wired (nil guard).
//   - Validates SourceRef (a URL) is non-empty.
//   - Calls runner.Run with a RunInput pinned to the DirectURLs path:
//     no SearchQueries, no effect overlay, stockDefault* durations.
//     The resulting PipelineResult.Chunks[0].LocalPath is the staged
//     payload on disk.
//   - Returns a FetchedAsset with LocalPath = chunk[0].LocalPath,
//     FetchedAt = now (UTC), and Bytes from a stat() of LocalPath.
//
// What Fetch deliberately does NOT do:
//
//   - decide Drive destination (the caller resolves via
//     core/asset.Resolver and passes req.DestinationID;
//     adapters do not own destination policy);
//   - emit Qdrant upserts or asset lifecycle transitions
//     (Provider MUST NOT do these per provider.go preamble);
//   - touch the search path (Search is not a Capability of Stock).
//
// Asset: intentionally nil. Canonical asset generation is the
// downstream ingest use case responsibility — this adapter
// returns raw staging information only.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchedAsset, error) {
	if a.runner == nil {
		return nil, ErrSourceNotWired
	}
	if req.SourceRef == "" {
		return nil, fmt.Errorf("stock fetch: missing SourceRef (URL)")
	}

	// DirectURLs path only. SearchQueries left empty so resolveQuery
	// (yt-dlp search) is never invoked.
	input := &stockpipeline.RunInput{
		DirectURLs:    []string{req.SourceRef},
		TotalMinutes:  stockDefaultTotalMinutes,
		ChunkDuration: stockDefaultChunkDuration,
		ClipDuration:  stockDefaultClipDuration,
		// NoAudio/NoEffects/NoTransitions intentionally false so the
		// output is the unaltered staged payload. Future waves can
		// promote these to FetchRequest fields if a non-effect overlay
		// path is needed.
	}

	result, err := a.runner.Run(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("stock fetch: pipeline run failed: %w", err)
	}
	if result == nil || len(result.Chunks) == 0 {
		return nil, fmt.Errorf("stock fetch: no chunks yielded for %q", req.SourceRef)
	}
	localPath := result.Chunks[0].LocalPath
	if localPath == "" {
		return nil, fmt.Errorf("stock fetch: chunk[0].LocalPath empty for %q", req.SourceRef)
	}

	return &providers.FetchedAsset{
		Asset:     nil, // canonical asset generation is downstream
		LocalPath: localPath,
		FetchedAt: time.Now().UTC(),
		Bytes:     fileBytes(localPath),
	}, nil
}

// fileBytes returns the size of the staged payload or 0 when the
// stat fails. Best-effort: callers receive FetchedAsset.LocalPath
// unconditionally and can re-stat themselves if precise bytes
// matter — the helper exists only so log aggregations (Drive
// upload accounting, telemetry) see a non-zero value when the
// stage succeeded. Stat errors are NOT surfaced as Fetch errors:
// a missing intermediate file (cleaned up between hand-off and
// upload) is a transient race that the upload step will catch.
func fileBytes(p string) int64 {
	if p == "" {
		return 0
	}
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}
