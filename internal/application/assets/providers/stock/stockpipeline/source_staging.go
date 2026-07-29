// Package stockpipeline — source_staging.go (Stock P0 split, July 2026).
//
// This file owns the source-staging methods previously co-located in
// service.go: StageSource and stageSection. Both methods route through
// the canonical acquisition.SourceStager port (Stock Cutover §12-4).
//
// godlike/06 SSOT: one canonical owner for "how does stock fetch its
// source bytes?" — the acquisition.SourceStager port + these two methods.
package stockpipeline

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// StageSource downloads a video from a URL and returns the staged file.
// It delegates to the canonical acquisition.SourceStager port which
// owns persistent stage registry + .meta.json sidecars + TTL eviction.
//
// §12-4 (July 2026): the legacy yt-dlp-baked local implementation is
// RETIRED. The Service no longer holds a `*downloader.YTDLPDownloader`
// field directly; instead it asks `Service.sourceStager.Prepare(ctx, req)`
// for the canonical PrepareContext + LocalPath. The TempPath + MkdirTemp
// dance is gone — stagingRoot lives in the FilesystemStager so multiple
// runs share persistent state across calls (idempotency invariant).
//
// Blocco 2a (July 2026, preserved for the FetchProvider contract): the
// returned *StagedSource is the legacy dual-shape carrier; the adapter
// flattens the PrepareContext.LocalPath + PrepareContext.SizeBytes
// into the StagedSource struct so callers (Adapter.Fetch etc.) don't
// need to switch shapes mid-call. The cleanup function is a thin
// wrapper around sourceStager.Release(ctx, PrepareContext.CleanupToken).
func (s *Service) StageSource(ctx context.Context, url string) (*StagedSource, error) {
	// P6 (July 2026): nil-guard for test-fixture path where
	// the acquisition.SourceStager is not wired at composition time.
	// ErrAcquisitionNotWired surfaces a typed sentinel callers can
	// errors.Is against.
	if s.sourceStager == nil {
		return nil, fmt.Errorf("stage source %q: %w", url, acquisition.ErrAcquisitionNotWired)
	}
	prepared, err := s.sourceStager.Prepare(ctx, acquisition.PrepareRequest{
		Source: acquisition.SourceRef{
			URL:           url,
			PolicyVersion: "v1",
		},
		IdempotencyKey: "stock.stage." + acquisition.DeriveIdempotencyKey(acquisition.SourceRef{
			URL:           url,
			PolicyVersion: "v1",
		}),
		CallerRef: "stock.StageSource",
	})
	if err != nil {
		return nil, fmt.Errorf("stage source: prepare via acquisition.SourceStager: %w", err)
	}
	fi, statErr := s.statLocal(prepared.LocalPath)
	if statErr != nil {
		return nil, fmt.Errorf("stage source: stat staged file %q: %w", prepared.LocalPath, statErr)
	}
	if fi.Size() == 0 {
		return nil, fmt.Errorf("stage source: staged file is empty: %s", prepared.LocalPath)
	}
	s.log.Info("stage source: video downloaded via acquisition port",
		zap.String("url", url),
		zap.String("local_path", prepared.LocalPath),
		zap.String("stage_id", prepared.ID),
		zap.String("cleanup_token", prepared.CleanupToken),
		zap.Int64("bytes", fi.Size()),
		zap.Time("expires_at", prepared.ExpiresAt),
	)
	return &StagedSource{
		LocalPath: prepared.LocalPath,
		Bytes:     fi.Size(),
	}, nil
}

// stageSection downloads a single time-slice of a video via the
// canonical acquisition.SourceStager port (Stock Cutover §12-4).
//
// §12-4 (July 2026): the section path no longer threads a raw
// downloader.YTDLPDownloader.Download call. Instead the section time
// range flows through the same acquisition.SourceRef envelope as the
// full-asset path; the yt-dlp invocation logic that handles yt-dlp's
// `--download-sections` lives INSIDE the production concrete
// (`*acquisition.YTDLPSourceStager`, §12-4.2 forward-pointer). Today
// the FilesystemStager concrete writes the file via its Fetch
// closure — which stock callers wire to the yt-dlp subprocess.
//
// The legacy `s.ytdlp.Download` direct call is RETIRED.
func (s *Service) stageSection(ctx context.Context, ref appassets.SourceRef) (*appassets.StagedAsset, error) {
	// P6 (July 2026): nil-guard for test-fixture path.
	if s.sourceStager == nil {
		return nil, fmt.Errorf("stage section %q: %w", ref.URL, acquisition.ErrAcquisitionNotWired)
	}
	prepared, err := s.sourceStager.Prepare(ctx, acquisition.PrepareRequest{
		Source: acquisition.SourceRef{
			URL:             ref.URL,
			DownloadSection: ref.DownloadSection,
			ForceKeyframes:  ref.ForceKeyframes,
			MergeFormat:     ref.MergeFormat,
			PolicyVersion:   "v1",
		},
		IdempotencyKey: "stock.section." + acquisition.DeriveIdempotencyKey(acquisition.SourceRef{
			URL:             ref.URL,
			DownloadSection: ref.DownloadSection,
			PolicyVersion:   "v1",
		}),
		CallerRef: "stock.stageSection",
	})
	if err != nil {
		return nil, fmt.Errorf("stage section: prepare via acquisition.SourceStager (%q section=%q): %w", ref.URL, ref.DownloadSection, err)
	}
	fi, statErr := s.statLocal(prepared.LocalPath)
	if statErr != nil {
		return nil, fmt.Errorf("stage section: stat %q: %w", prepared.LocalPath, statErr)
	}
	if fi.Size() == 0 {
		return nil, fmt.Errorf("stage section: staged file is empty: %s", prepared.LocalPath)
	}
	s.log.Info("stage section: video section downloaded via acquisition port",
		zap.String("url", ref.URL),
		zap.String("section", ref.DownloadSection),
		zap.String("local_path", prepared.LocalPath),
		zap.String("stage_id", prepared.ID),
		zap.String("cleanup_token", prepared.CleanupToken),
		zap.Int64("bytes", fi.Size()),
		zap.Time("expires_at", prepared.ExpiresAt),
	)
	return &appassets.StagedAsset{
		LocalPath: prepared.LocalPath,
		Bytes:     fi.Size(),
	}, nil
}

// statLocal delegates to s.localFS.Stat when the port is wired;
// returns an error when the port is nil (PR-REFACTOR-P0-IO-BINDER).
func (s *Service) statLocal(path string) (os.FileInfo, error) {
	if s.localFS == nil {
		return nil, fmt.Errorf("stat %q: LocalFSPort not wired (composition root must inject filesystem.NewLocal())", path)
	}
	return s.localFS.Stat(path)
}
