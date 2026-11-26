package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	jobsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	assetdetail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	sqliteassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
)

// Adapters from the SQLite read/write projections to the consumer-owned ports
// declared by internal/capabilities/jobs (asset_transfer_ports.go).
//
// They live in the registry module — the composition layer that already owns
// the asset-index adapters — because it is the only layer allowed to know both
// the capability port and the concrete repository. The capability used to
// import the repositories directly, which made an adapter swap (or a test
// without SQLite) impossible.

// NewWorkerOutputStore adapts the asset-index projection to the worker-output
// port.
func NewWorkerOutputStore(svc *assetindex.Service) jobsapi.WorkerOutputStore {
	return &workerOutputStoreAdapter{svc: svc}
}

// workerOutputStoreAdapter owns the worker-output projection lifecycle.
//
// The mutations read the current row, apply the intent and write it back, so
// columns the capability does not model (operation_key, group_name, subfolder,
// file_hash, content_hash, metadata_json) survive. The capability used to
// assemble a fresh AssetRecord itself and could silently clear them.
type workerOutputStoreAdapter struct {
	svc *assetindex.Service
}

var _ jobsapi.WorkerOutputStore = (*workerOutputStoreAdapter)(nil)

func (a *workerOutputStoreAdapter) GetByID(ctx context.Context, assetID string) (*jobsapi.WorkerOutputRecord, error) {
	if a == nil || a.svc == nil {
		return nil, nil
	}
	rec, err := a.svc.GetByID(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return &jobsapi.WorkerOutputRecord{
		LocalPath:    rec.LocalPath,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
	}, nil
}

func (a *workerOutputStoreAdapter) MarkUploading(ctx context.Context, assetID string) error {
	return a.mutate(ctx, assetID, func(rec *assetindex.AssetRecord) {
		rec.Status = "uploading"
	})
}

func (a *workerOutputStoreAdapter) MarkUploaded(ctx context.Context, assetID, localPath string) error {
	return a.mutate(ctx, assetID, func(rec *assetindex.AssetRecord) {
		rec.LocalPath = localPath
		rec.Status = "uploaded"
	})
}

func (a *workerOutputStoreAdapter) MarkReady(ctx context.Context, assetID string) error {
	return a.mutate(ctx, assetID, func(rec *assetindex.AssetRecord) {
		rec.Status = "ready"
	})
}

func (a *workerOutputStoreAdapter) mutate(ctx context.Context, assetID string, apply func(*assetindex.AssetRecord)) error {
	if a == nil || a.svc == nil {
		return fmt.Errorf("worker-output store: asset index service not configured")
	}
	rec, err := a.svc.GetByID(ctx, assetID)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &assetindex.AssetRecord{AssetID: assetID}
	}
	// The defaults are part of the worker-output contract; they are applied by
	// the adapter so every mutation shares one definition.
	if rec.AssetType == "" {
		rec.AssetType = "worker-output"
	}
	if rec.Source == "" {
		rec.Source = "worker"
	}
	if rec.SourceID == "" {
		rec.SourceID = assetID
	}
	apply(rec)
	return a.svc.Upsert(ctx, rec)
}

// NewImageAssetLookup adapts the images repository to the image lookup port.
func NewImageAssetLookup(repo *imagesrepo.ImagesRepository) jobsapi.ImageAssetLookup {
	return &imageAssetLookupAdapter{repo: repo}
}

// imageAssetLookupAdapter satisfies jobs.ImageAssetLookup.
type imageAssetLookupAdapter struct {
	repo *imagesrepo.ImagesRepository
}

var _ jobsapi.ImageAssetLookup = (*imageAssetLookupAdapter)(nil)

func (a *imageAssetLookupAdapter) GetByID(ctx context.Context, assetID string) (*assetdetail.ImageAsset, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	img, err := a.repo.GetByID(ctx, assetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return img, nil
}

// NewVoiceoverLookup adapts the voiceovers projection to the lookup port.
func NewVoiceoverLookup(repo *sqliteassets.VoiceoversRepository) jobsapi.VoiceoverLookup {
	return &voiceoverLookupAdapter{repo: repo}
}

// voiceoverLookupAdapter satisfies jobs.VoiceoverLookup. It exposes only the
// artifact fields the capability reads, so the deprecated location columns stay
// inside the projection.
type voiceoverLookupAdapter struct {
	repo *sqliteassets.VoiceoversRepository
}

var _ jobsapi.VoiceoverLookup = (*voiceoverLookupAdapter)(nil)

func (a *voiceoverLookupAdapter) GetByID(ctx context.Context, assetID string) (*jobsapi.VoiceoverRecord, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	rec, err := a.repo.GetByID(ctx, assetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return &jobsapi.VoiceoverRecord{
		Filename:     rec.Filename,
		LocalPath:    rec.LocalPath,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
	}, nil
}
