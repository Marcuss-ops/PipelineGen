package wiring

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	infraartifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	"go.uber.org/zap"
)

// ClipRenderRuntime is the production clip-render graph. RenderingGen is the
// only executor; it owns semantic lowering, queue execution, and Chronon.
type ClipRenderRuntime struct {
	RenderingGenExecutor cliprender.RenderExecutor
	ContinuationStore    cliprender.ContinuationStore
}

// assetMaterializationRoot is the ONE content-addressed materialization root
// every asset consumer shares. The materializer caches under
// <root>/assets/<sha256>/source.<ext>, so a single root means a source
// downloaded once is reused by every capability instead of being downloaded a
// second time into a second tree.
//
// This replaced two parallel roots (temp/cliprender and temp/localization) that
// each held a private copy of the same bytes: the clip.render flow and the
// localization flow cached the same asset twice, and the two trees were free to
// drift. Only the ASSET CACHE moves here — each consumer keeps its own work
// directory (worker scratch, localized outputs, Drive staging), which holds
// genuinely different content.
func assetMaterializationRoot(cfg *config.Config) string {
	return filepath.Join(cfg.Storage.TempPath(), "materialized")
}

// assetMaterializationResolverRoot is the prepared-asset resolver's view of
// assetMaterializationRoot: the resolver addresses files as
// <root>/<sha256>/source.<ext>, i.e. the materializer's "assets" subdirectory.
func assetMaterializationResolverRoot(cfg *config.Config) string {
	return filepath.Join(assetMaterializationRoot(cfg), "assets")
}

func BuildClipRenderRuntime(cfg *config.Config, root *ComposeRoot, log *zap.Logger) (*ClipRenderRuntime, error) {
	if root == nil {
		return nil, fmt.Errorf("clip render runtime: composition root is nil")
	}
	if root.ClipRenderRuntime != nil {
		return root.ClipRenderRuntime, nil
	}
	if cfg == nil {
		return nil, fmt.Errorf("clip render runtime: config is required")
	}
	if root.MediaExec == (mediaexec.ExecutionConfig{}) {
		return nil, fmt.Errorf("clip render runtime: resolved media execution config is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	queueURL := strings.TrimSpace(cfg.External.RenderingGenQueueURL)
	if queueURL == "" {
		return nil, fmt.Errorf("clip render runtime: RENDERINGGEN_QUEUE_URL is required; clip rendering fails closed")
	}
	executor, err := renderinggen.NewClipRenderExecutor(renderinggen.New(queueURL))
	if err != nil {
		return nil, fmt.Errorf("clip render runtime: build RenderingGen executor: %w", err)
	}
	// Completion is event-driven (the queue client long-polls
	// GET /jobs/{id}/wait), so this only tunes the polling FALLBACK used when
	// the queue server lacks that route. The configured value (default 0 →
	// the built-in 250 ms) is applied before the runtime is cached in the
	// composition root.
	if cfg.External.RenderingGenPollIntervalMS > 0 {
		executor.SetPollInterval(time.Duration(cfg.External.RenderingGenPollIntervalMS) * time.Millisecond)
	}
	casRoot := filepath.Join(cfg.Storage.AbsDataDir(), "cas")
	stager, err := infraartifacts.NewLocalStore(infraartifacts.Config{Workspace: filepath.Join(casRoot, ".staging")})
	if err != nil {
		return nil, fmt.Errorf("clip render runtime: build continuation stager: %w", err)
	}
	casStore, err := cas.NewStore(cas.Config{Root: casRoot, Stager: stager})
	if err != nil {
		return nil, fmt.Errorf("clip render runtime: build continuation CAS: %w", err)
	}
	continuationStore, err := renderinggen.NewCASContinuationStore(casStore)
	if err != nil {
		return nil, fmt.Errorf("clip render runtime: build continuation store: %w", err)
	}
	runtime := &ClipRenderRuntime{RenderingGenExecutor: executor, ContinuationStore: continuationStore}
	root.ClipRenderRuntime = runtime
	return runtime, nil
}

// newClipRenderMediaResolver returns the asset read surface for clip.render and
// localization. PostgreSQL is the media SSOT and is the ONLY read surface:
// there is no second catalog, so a missing media PostgreSQL is a fail-closed
// wiring error rather than a silent fallback to the legacy SQLite asset
// registry (MEDIA-SSOT P1-5: closes the read split-brain where a
// PostgreSQL-committed asset was invisible to a SQLite reader, and removes the
// last branch in which clip.render could read a different catalog than the one
// it writes).
//
// It lives in this file, not a sibling, because internal/app/wiring is a
// registered hotspot whose debt is the FILE COUNT itself: a resolver for the
// clip.render runtime graph belongs with the rest of that graph, and adding a
// 151st production file to the package for 33 LOC is exactly the growth the
// ratchet forbids.
func newClipRenderMediaResolver(root *ComposeRoot, log *zap.Logger) (cliprender.AssetResolver, error) {
	if root == nil {
		return nil, errors.New("clip.render: composition root is nil")
	}
	if root.MediaPostgres == nil {
		return nil, errors.New("clip.render: media PostgreSQL SSOT is required (no second media catalog: the legacy SQLite asset registry is no longer a valid read surface)")
	}
	return clipadapters.NewClipRenderPGAssetResolver(pgmedia.NewMediaSearcher(root.MediaPostgres), log)
}
