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
	AssetID   string
	AssetType string // "clip", "stock", "artlist", "image", "voiceover"
	ProjectID  string
	FolderName string
	Metadata  map[string]interface{}
}

// ResolveResult contains the resolved destination information.
type ResolveResult struct {
	LocationKind string // "drive", "local", "s3", etc.
	URI          string // Drive folder ID, local path, etc.
	Extra        map[string]interface{}
}
