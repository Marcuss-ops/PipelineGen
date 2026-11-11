// Package app — staging service bundle construction (FASE 3 Spina Dorsale,
// Push 3.1b, July 2026).
//
// Extracted from composition.go per PG-028 (the established
// `internal/app/build_bundles_<capability>.go` pattern). This file
// owns the SOLE canonical construction site for the artifact_stages
// Repository + staging.StoreService pair. No caller (worker pool,
// finalizer, admin tool, test stub) should construct a second
// instance or read the staging workspace dir independently — every
// consumer reaches the typed ports via ComposeRoot.Staging.
//
// godlike/06 SSOT: this is the SINGLE canonical wiring of the
// FASE 3 application-layer staging step. The composition root
// (composition.go::NewComposition) calls BuildStagingBundle in
// dependency order — it depends ONLY on dbs.Main.DB (for the
// artifact_stages SQLite handle) + cfg.Storage.StagingPath() (for
// the canonical workspace dir) + log — so it can be placed at the
// end of NewComposition, after BuildTextTrackBundle, without
// disturbing the existing 12-bundle aggregation.
//
// godlike/07 fail-closed: a misconfigured workspace dir (empty
// after the StorageConfig default-resolves to
// `/var/lib/pipelinegen/staging`) trips a wrapped error from
// staging.NewStoreService; the composition root surfaces the
// error and NewComposition aborts (no silent fallback to "." or
// /tmp). A nil *sql.DB trips a wrapped error from
// artifactstages.NewRepository; the composition root surfaces
// the error and NewComposition aborts (no zero-value DB).
package wiring

import (
	"fmt"

	"go.uber.org/zap"

	stagingsvc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	artifactstages "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifact_stages"
)

import (
	"context"
	"errors"
	appacq "github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	infacq "github.com/Marcuss-ops/PipelineGen/internal/platform/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"os"
	"path/filepath"
)

// BuildStagingBundle constructs the canonical FASE 3 Spina Dorsale
// staging bundle: the artifact_stages SQLite Repository (the
// single-writer of the artifact_stages table) + the
// application-layer staging.StoreService (the typed port the
// publisher worker pool will drain into). Returns the StagingBundle
// for assignment to ComposeRoot.Staging.
//
// Dependency surface (intentionally minimal — only 3 deps):
//   - dbs.Main.DB: the *sql.DB handle the artifact_stages Repository
//     uses for all 8 methods (Insert, GetByID, ListByJob,
//     ListByState, MarkPublished, MarkSucceeded, MarkFailedPermanent,
//     IncrementAttemptCount). dbs.Main is the same handle every
//     other repository in the composition root uses (jobs, assets,
//     voiceover, scripts, etc.) — co-location on the same handle
//     preserves the canonical "one DB per deployment" SSOT.
//   - cfg: the resolved PipelineGen config; BuildStagingBundle reads
//     cfg.Storage.StagingPath() to discover the workspace root. The
//     resolution chain (StorageConfig.StagingDir → env var
//     PIPELINEGEN_STAGING_WORKSPACE → default /var/lib/pipelinegen/staging)
//     is owned by the config layer; this constructor is a consumer
//     only.
//   - log: *zap.Logger for the boot-time wiring log line.
//
// Returns error on (a) nil dbs.Main.DB (composition-time
// misconfiguration) or (b) staging.NewStoreService rejection
// (empty workspace dir — should never trigger after
// StorageConfig.StagingPath()'s default-resolution but
// fail-closed at the boundary anyway).
func BuildStagingBundle(dbs *Databases, cfg *config.Config, log *zap.Logger) (*StagingBundle, error) {
	// Step 1: construct the artifact_stages SQLite Repository.
	// artifactstages.NewRepository is the canonical concrete
	// (internal/platform/sqlite/artifact_stages).
	// A nil *sql.DB surfaces as a wrapped error from the
	// SQLite driver on the first call; we pre-validate here to
	// fail loud at boot, not mid-job.
	if dbs == nil || dbs.DualPool == nil || dbs.DualPool.Writer == nil {
		return nil, fmt.Errorf("build staging: dbs.DualPool.Writer is nil (composition root failed to construct the DualPool before BuildStagingBundle)")
	}
	repo := artifactstages.NewRepository(dbs.DualPool.Writer)
	// Conformance with artifact.ArtifactStageRepository is pinned at the
	// StagingBundle.Repository field type + the canonical anchor
	// at internal/platform/sqlite/artifact_stages/repository.go:51
	// (`var _ artifact.Repository = (*Repository)(nil)`). The
	// compiler checks the port conformance at the
	// &StagingBundle{...} literal in this function's return.

	// Step 2: resolve the staging workspace dir via the canonical
	// StorageConfig method. StagingPath() returns
	// `/var/lib/pipelinegen/staging` if StagingDir is empty
	// (the default tag is applied at env-var load time; this
	// fallback covers tests / admin CLIs that construct the
	// struct without going through the loader).
	workspace := cfg.Storage.StagingPath()

	// Step 3: construct the application-layer StoreService. The
	// concrete is staging.NewStoreService; the field on
	// StagingBundle is typed as the Store port so test stubs
	// (fakeStore) can satisfy it via Go's implicit-interface
	// rules. The NewStoreService constructor fail-closes on
	// nil repo (impossible — repo is non-nil above) or empty
	// workspace dir (impossible — StagingPath() always returns
	// a non-empty default).
	store, err := stagingsvc.NewStoreService(repo, workspace)
	if err != nil {
		return nil, fmt.Errorf("build staging: NewStoreService (workspace=%q): %w", workspace, err)
	}
	// Conformance with staging.Store is pinned at the
	// StagingBundle.Store field type + the canonical anchor at
	// internal/capabilities/staging/service.go:53
	// (`var _ Store = (*StoreService)(nil)`). The compiler
	// checks the port conformance at the &StagingBundle{...}
	// literal in this function's return.

	log.Info("staging bundle wired (FASE 3 Spina Dorsale Push 3.1b)",
		zap.String("workspace", workspace),
		zap.String("repository_table", "artifact_stages"),
	)

	return &StagingBundle{
		Store:      store,
		Repository: repo,
		Workspace:  workspace,
	}, nil
}

// ErrStockPipelineStagerInit identifies a composition-time failure while
// constructing the canonical acquisition stager.
var ErrStockPipelineStagerInit = errors.New("internal/app: acquisition stager initialization failed")

// WireAcquisitionStager constructs the canonical filesystem-backed stager
// used by the stock pipeline. Unwired fetching remains fail-closed: callers
// receive ErrAcquisitionPrepareFailed rather than a successful no-op.
func WireAcquisitionStager(cfg *config.Config, log *zap.Logger, fetch infacq.FetchFn) (appacq.SourceStager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: cfg is nil", ErrStockPipelineStagerInit)
	}
	if log == nil {
		log = zap.NewNop()
	}
	if fetch == nil {
		fetch = func(_ context.Context, _ appacq.PrepareRequest, _ string, _ func(infacq.FetchResult)) error {
			return appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, "acquisition fetch is not wired")
		}
	}

	stager, err := infacq.NewFilesystemStager(infacq.Options{
		StagingRoot: filepath.Join(cfg.Storage.TempPath(), "stock_pipeline_staging"),
		Fetch:       fetch,
		Log:         log,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStockPipelineStagerInit, err)
	}
	return stager, nil
}

// buildYouTubeSourceStager wires the sections-only acquisition SourceStager
// used by the YouTube fanout: the fetcher downloads ONE contiguous block with
// a single yt-dlp --download-sections call (and a single cookie-authenticated
// invocation) instead of the whole source. Fail-soft by design: a staging
// error only logs — the fanout keeps the per-segment yt-dlp path (backwards
// compatible).
func buildYouTubeSourceStager(cfg *config.Config, log *zap.Logger) appacq.SourceStager {
	var youtubeSourceStager appacq.SourceStager
	ytdlpDL := downloader.NewYTDLP(cfg)
	fetch := func(ctx context.Context, req appacq.PrepareRequest, dstPath string, _ func(infacq.FetchResult)) error {
		dlReq := &downloader.DownloadRequest{
			URL:        req.Source.URL,
			OutputPath: dstPath + ".%(ext)s",
			Timeout:    req.Timeout,
			UseCookies: true,
		}
		if req.Source.MergeFormat != "" {
			dlReq.MergeFormat = req.Source.MergeFormat
		} else {
			dlReq.MergeFormat = "mp4"
		}
		if req.Source.DownloadSection != "" {
			dlReq.DownloadSections = []string{req.Source.DownloadSection}
			dlReq.ForceKeyframes = req.Source.ForceKeyframes
		}
		if err := ytdlpDL.Download(ctx, dlReq); err != nil {
			return err
		}
		tmpl := dstPath + ".%(ext)s"
		resolved, rErr := downloader.ResolveDownloadedSegmentPath(tmpl)
		if rErr != nil {
			return rErr
		}
		if resolved != dstPath {
			if err := os.Rename(resolved, dstPath); err != nil {
				return err
			}
		}
		return nil
	}
	if s, sErr := WireAcquisitionStager(cfg, log, fetch); sErr != nil {
		log.Warn("youtube section stager unavailable; fanout will use per-segment yt-dlp", zap.Error(sErr))
	} else {
		youtubeSourceStager = s
		log.Info("youtube section SourceStager wired", zap.String("staging_root", filepath.Join(cfg.Storage.TempPath(), "stock_pipeline_staging")))
	}
	return youtubeSourceStager
}
