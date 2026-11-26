package jobs

import (
	"context"

	assetdetail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// Consumer-owned ports for the worker asset-transfer service.
//
// The service used to depend on three concrete SQLite repositories
// (*assetindex.Service, *imagesrepo.ImagesRepository and
// *sqliteassets.VoiceoversRepository), so the capability could not be exercised
// without the SQLite adapters and every persistence change reached the domain.
// It now owns the narrow ports below; the adapters live in the composition
// layer (internal/app/modules/registry/asset_transfer_adapters.go), which is the
// only layer allowed to know both sides.
//
// Contract shared by every lookup: (nil, nil) means "this source does not hold
// the asset"; a non-nil error means the lookup itself failed and MUST be
// surfaced, never treated as absence. Adapters translate their engine's
// not-found error (sql.ErrNoRows) to (nil, nil) so the capability never imports
// database/sql.

// WorkerOutputStore owns the worker-output projection lifecycle.
//
// The mutations are intents, not record writes, so the adapter performs the
// read-modify-write: the capability cannot clear a column it does not model.
type WorkerOutputStore interface {
	GetByID(ctx context.Context, assetID string) (*WorkerOutputRecord, error)
	MarkUploading(ctx context.Context, assetID string) error
	MarkUploaded(ctx context.Context, assetID, localPath string) error
	MarkReady(ctx context.Context, assetID string) error
}

// WorkerOutputRecord is the slice of the worker-output projection this
// capability reads. It deliberately carries no identity: the caller already
// holds the assetID it looked up, and storing it beside the locations would be
// a second source of truth for the same fact.
type WorkerOutputRecord struct {
	LocalPath    string
	DriveLink    string
	DownloadLink string
}

// ImageAssetLookup resolves an image asset's canonical location.
type ImageAssetLookup interface {
	GetByID(ctx context.Context, assetID string) (*assetdetail.ImageAsset, error)
}

// VoiceoverLookup resolves a voiceover's stored artifact.
type VoiceoverLookup interface {
	GetByID(ctx context.Context, assetID string) (*VoiceoverRecord, error)
}

// VoiceoverRecord is the slice of the voiceover projection this capability
// reads.
type VoiceoverRecord struct {
	Filename     string
	LocalPath    string
	DriveLink    string
	DownloadLink string
}
