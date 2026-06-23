package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	gdrive "google.golang.org/api/drive/v3"
)

// ArtlistBundle is the capability bundle for the Artlist module.
//
// PR4d-chunk2 (June 2026): wraps the 25 cross-bundle reads of WireArtlist
// into 10 typed fields. Built in WireRegistry immediately before calling
// WireArtlist. VectorStore is a separate direct arg (the 11thWireArtlist param)
// since it has a single use inside wireClipResolver.
//
// Field budget: 10 fields (per AGENTS.md / arch constraint).
type ArtlistBundle struct {
	DB                 *storage.SQLiteDB
	Assets             *asset.Service
	ClipsRepo          *assets.ClipsRepository
	DriveClient        *gdrive.Service
	DriveUploader      *driveup.Uploader
	AssetIndexService  *assetindex.Service
	ClipIndexerService *clipindexer.Service
	MediaProcessor     asset.Processor
	Jobs               *JobsBundle // Service + Facade
	CatalogSyncService *catalogsync.Service
}
