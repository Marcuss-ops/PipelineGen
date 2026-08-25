package artlist

import (
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// RenditionSubdir maps rendition kinds to their on-disk subdirectory names.
const (
	SubdirMaster     = "master"
	SubdirMezzanine  = "mezzanine"
	SubdirProxy      = "proxy"
	SubdirThumbnail  = "thumbnail"
	SubdirStoryboard = "storyboard"
)

// StorageLayout builds filesystem paths for provider-scoped asset storage.
// The layout is: <DataDir>/media/<provider>/<external_asset_id>/<rendition>/<filename>
type StorageLayout struct {
	DataDir         string
	Provider        string
	ExternalAssetID string
}

// NewStorageLayout creates a layout for a given provider and external asset ID.
// The external asset ID is sanitized for safe filesystem use.
func NewStorageLayout(dataDir, provider, externalAssetID string) *StorageLayout {
	return &StorageLayout{
		DataDir:         dataDir,
		Provider:        provider,
		ExternalAssetID: sanitizeExternalAssetID(externalAssetID),
	}
}

// BaseDir returns the asset's base directory: <DataDir>/media/<provider>/<external_asset_id>.
func (l *StorageLayout) BaseDir() string {
	return filepath.Join(l.DataDir, "media", l.Provider, l.ExternalAssetID)
}

// RenditionDir returns the directory for a specific rendition kind.
func (l *StorageLayout) RenditionDir(kind asset.RenditionKind) string {
	return filepath.Join(l.BaseDir(), renditionSubdir(kind))
}

// MasterPath returns the path for the original master file.
func (l *StorageLayout) MasterPath(filename string) string {
	return filepath.Join(l.RenditionDir(asset.RenditionKindMaster), filename)
}

// MezzaninePath returns the path for the edited mezzanine file.
func (l *StorageLayout) MezzaninePath(filename string) string {
	return filepath.Join(l.RenditionDir(asset.RenditionKindMezzanine), filename)
}

// ProxyPath returns the path for the proxy file.
func (l *StorageLayout) ProxyPath(filename string) string {
	return filepath.Join(l.RenditionDir(asset.RenditionKindProxy), filename)
}

// ThumbnailPath returns the path for the thumbnail file.
func (l *StorageLayout) ThumbnailPath(filename string) string {
	return filepath.Join(l.RenditionDir(asset.RenditionKindThumbnail), filename)
}

// StoryboardPath returns the path for the storyboard file.
func (l *StorageLayout) StoryboardPath(filename string) string {
	return filepath.Join(l.RenditionDir(asset.RenditionKindStoryboard), filename)
}

// PathFor returns the directory for an arbitrary rendition kind.
func (l *StorageLayout) PathFor(kind asset.RenditionKind, filename string) string {
	return filepath.Join(l.RenditionDir(kind), filename)
}

// sanitizeExternalAssetID makes an value safe to use as a filesystem path segment.
func sanitizeExternalAssetID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "unknown"
	}
	id = textutil.SafeName(id)
	if id == "" {
		return "unknown"
	}
	return id
}

// renditionSubdir maps a RenditionKind to its on-disk subdirectory name.
func renditionSubdir(kind asset.RenditionKind) string {
	switch kind {
	case asset.RenditionKindMaster:
		return SubdirMaster
	case asset.RenditionKindMezzanine:
		return SubdirMezzanine
	case asset.RenditionKindProxy:
		return SubdirProxy
	case asset.RenditionKindThumbnail:
		return SubdirThumbnail
	case asset.RenditionKindStoryboard:
		return SubdirStoryboard
	default:
		return strings.ToLower(string(kind))
	}
}

// DeriveExternalAssetID returns a filesystem-safe identifier from a clip ID
// and source URL. If clipID is empty, it falls back to a truncated MD5 of
// the source URL.
func DeriveExternalAssetID(clipID, sourceURL string) string {
	if strings.TrimSpace(clipID) != "" {
		return sanitizeExternalAssetID(clipID)
	}
	if strings.TrimSpace(sourceURL) != "" {
		return hashutil.LegacyMD5String(sourceURL)[:12]
	}
	return "unknown"
}

// SafeFilename returns a sanitized filename, preserving the extension.
func SafeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "asset"
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	base = textutil.SafeName(base)
	if base == "" {
		base = "asset"
	}
	return base + ext
}
