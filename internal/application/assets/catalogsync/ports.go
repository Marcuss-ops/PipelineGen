package catalogsync

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// CatalogRepository is the application port for catalog reads and folder
// reconciliation. Concrete SQLite repositories are wired by the composition
// root and must not leak into this package.
type CatalogRepository interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	ListFolders(ctx context.Context, source string) ([]*asset.ClipFolder, error)
	DeleteFolder(ctx context.Context, id string) error
}

// AssetIndexer is a read-side capability used by the synchronizer's job
// wiring. It is not proof that the Qdrant projection is current; the
// dispatcher always emits the canonical idempotent index intent.
type AssetIndexer interface {
	GetIndexState(ctx context.Context, id string) (asset.IndexState, error)
}

// ProjectionDispatcher owns the atomic media-asset write and projection
// event enqueue. The implementation is supplied by infrastructure at the
// composition root; catalogsync only knows this narrow application port.
type ProjectionDispatcher interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error
}

// SourceReader is the read-only source catalog port used by the recursive
// synchronizer. Its DTOs are application-owned so Drive SDK/infrastructure
// metadata types cannot cross the application boundary.
type SourceReader interface {
	GetFileMeta(ctx context.Context, fileID string) (*RemoteFileMeta, error)
	ListFiles(ctx context.Context, parentID string) ([]RemoteFile, error)
}

// RemoteFileMeta is the source metadata needed for a catalog root.
type RemoteFileMeta struct {
	ID          string
	Name        string
	MimeType    string
	WebViewLink string
}

// RemoteFile is the normalized source listing item consumed by catalogsync.
type RemoteFile struct {
	ID             string
	Name           string
	MimeType       string
	Size           int64
	MD5Checksum    string
	WebViewLink    string
	WebContentLink string
	Parents        []string
}
