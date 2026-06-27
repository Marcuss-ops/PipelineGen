package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/manifest"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	gdrive "google.golang.org/api/drive/v3"
)

// ArtlistBundle is the capability bundle for the Artlist module.
//
// PR4d-chunk2 (June 2026): wraps the 25 cross-bundle reads of WireArtlist
// into 11 typed fields. Built in WireRegistry immediately before calling
// WireArtlist. VectorStore is a separate direct arg (the 11thWireArtlist param)
// since it has a single use inside wireClipResolver.
//
// fix-build-base (June 2026): adds the 11th field ManifestService to
// thread the canonical asset manifest service (constructed in
// BuildProcessBundle) into the Artlist SemanticEnricher per PR 7
// cutover. Mirrors AssetsBundle.ManifestService — single source of
// truth for per-asset manifest upsert across clips + artlist.
//
// Field budget: 11 fields (per AGENTS.md / arch constraint).
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
	// ManifestService is the canonical asset manifest service instance
	// shared with the Artlist SemanticEnricher (PR 7 cutover, June 2026).
	// Built once in BuildProcessBundle and threaded into the bundle via
	// ComposeRoot.Process.ManifestService. The same instance is used by
	// AssetsBundle.ManifestService so the manifest write path is
	// single-source-of-truth across clips + artlist.
	ManifestService manifest.Service
}
