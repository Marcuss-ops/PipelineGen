package drive

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	driveapi "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// Destinations groups the canonical Drive folder IDs for media assets.
type Destinations struct {
	MediaRoot, SoundEffectsRoot, ImagesFolderID string
}

// Bundle owns the composed Google Drive ports and delivery surfaces.
type Bundle struct {
	Admin         driveapi.Admin
	Reader        driveapi.Reader
	DriveDests    *Destinations
	DestResolver  asset.Resolver
	StyleRegistry *styles.StyleRegistry
	Publisher     delivery.Publisher
	Lifecycle     driveapi.FileLifecycle
	DriveUploader *driveapi.Uploader
}
