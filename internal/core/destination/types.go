package destination

import "context"

// Resolver is the canonical interface for resolving asset destinations.
// It unifies drive destination, local destination, and other output targets.
type Resolver interface {
	// Resolve returns the destination URI and metadata for an asset.
	Resolve(ctx context.Context, req *ResolveRequest) (*ResolveResult, error)
}

// ResolveRequest contains the information needed to resolve a destination.
type ResolveRequest struct {
	Source          string // e.g. "youtube", "artlist", "voiceover"
	Group           string // Name of the group folder
	FolderID        string // explicit folder ID (overrides group)
	FolderPath      string // optional path info
	SubfolderName   string // Name of the subfolder or video ID
	CreateSubfolder bool   // whether to create subfolder if not exists
	AssetID         string
	AssetType       string // "clip", "stock", "artlist", "image", "voiceover"
	ProjectID       string
	FolderName      string
	Metadata        map[string]interface{}
}

// ResolveResult contains the resolved destination information.
type ResolveResult struct {
	LocationKind string // "drive", "local", "s3", etc.
	URI          string // Drive folder ID, local path, etc.
	FolderID     string // Drive folder ID
	FolderPath   string // Full folder path
	DriveLink    string // Drive web link
	Extra        map[string]interface{}
}
