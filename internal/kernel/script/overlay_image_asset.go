package script

// OverlayImageAsset binds a preselected, locally available still to one scene
// for the overlay planner. It is intentionally separate from video stock
// bindings and provider media selection.
type OverlayImageAsset struct {
	SceneID   string `json:"scene_id"`
	AssetID   string `json:"asset_id"`
	LocalPath string `json:"local_path"`
	SHA256    string `json:"sha256"`
	MIMEType  string `json:"mime_type,omitempty"`
}
