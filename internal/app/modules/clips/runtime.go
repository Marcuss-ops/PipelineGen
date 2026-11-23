package clipswiring

import (
	"database/sql"
	"fmt"
	"os"
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

// ClipRenderRuntime is the shared production graph for clip.render and
// localization. RenderingGen owns semantic lowering, queue execution and
// Chronon; this package only composes the producer-side ports.
type ClipRenderRuntime struct {
	RenderingGenExecutor cliprender.RenderExecutor
	ContinuationStore    cliprender.ContinuationStore
}

// AssetMaterializationRoot is the common content-addressed cache root used by
// clip.render and localization. Consumer scratch directories remain separate.
func AssetMaterializationRoot(cfg *config.Config) string {
	return filepath.Join(cfg.Storage.TempPath(), "materialized")
}

// AssetMaterializationResolverRoot is the prepared-asset resolver's view of
// AssetMaterializationRoot.
func AssetMaterializationResolverRoot(cfg *config.Config) string {
	return filepath.Join(AssetMaterializationRoot(cfg), "assets")
}

// BuildClipRenderRuntime constructs the shared runtime without depending on
// ComposeRoot. The caller owns caching the returned value in its composition
// state, keeping this leaf package independent from the root package.
func BuildClipRenderRuntime(deps ClipRenderRuntimeDeps) (*ClipRenderRuntime, error) {
	cfg, mediaExec, existing := deps.Config, deps.MediaExec, deps.Existing
	if existing != nil {
		return existing, nil
	}
	if cfg == nil {
		return nil, fmt.Errorf("clip render runtime: config is required")
	}
	if mediaExec == (mediaexec.ExecutionConfig{}) {
		return nil, fmt.Errorf("clip render runtime: resolved media execution config is required")
	}
	queueURL := strings.TrimSpace(cfg.External.RenderingGenQueueURL)
	if queueURL == "" {
		return nil, fmt.Errorf("clip render runtime: RENDERINGGEN_QUEUE_URL is required; clip rendering fails closed")
	}
	executor, err := renderinggen.NewClipRenderExecutor(renderinggen.New(queueURL))
	if err != nil {
		return nil, fmt.Errorf("clip render runtime: build RenderingGen executor: %w", err)
	}
	// CR-18: chunking is a measured NO-GO (2 chunks = 31.51 s vs 18.66 s for
	// one job), so production must opt in explicitly. The historical wiring
	// enabled it unconditionally and armed the slower path for every eligible
	// clip. Keep the executor default (no chunking) unless the operator sets
	// the flag for a new V3 measurement.
	if ClipRenderChunkingEnabled() {
		video := cfg.Video.WithDefaults()
		executor.SetChunking(time.Duration(video.ChunkDuration)*time.Second, int64(video.KeyframeInterval), 2)
	}
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
	return &ClipRenderRuntime{RenderingGenExecutor: executor, ContinuationStore: continuationStore}, nil
}

// EnvClipRenderChunking is the explicit opt-in for the chunking experiment.
// Absent, empty, "0" and "false" keep the measured NO-GO policy: one job per
// plan, no chunk family, no anchor overhead.
const EnvClipRenderChunking = "PIPELINEGEN_CLIP_RENDER_CHUNKING"

// ClipRenderChunkingEnabled reports whether the operator explicitly armed the
// chunking experiment. The default is OFF because the only certified measurement
// is negative (2026-11-20: 2 chunks 31.51 s vs 18.66 s single job).
func ClipRenderChunkingEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvClipRenderChunking))) {
	case "1", "true":
		return true
	default:
		return false
	}
}

// NewClipRenderMediaResolver creates the single media read surface for
// clip.render. PostgreSQL is authoritative; there is no SQLite fallback.
func NewClipRenderMediaResolver(mediaDB *sql.DB, log *zap.Logger) (cliprender.AssetResolver, error) {
	if mediaDB == nil {
		return nil, fmt.Errorf("clip.render: media PostgreSQL SSOT is required (no second media catalog: the legacy SQLite asset registry is no longer a valid read surface)")
	}
	return clipadapters.NewClipRenderPGAssetResolver(pgmedia.NewMediaSearcher(mediaDB), log)
}
