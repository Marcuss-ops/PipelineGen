package models

// AssetNode represents a node in the asset tree hierarchy for API responses.
type AssetNode struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	AssetID     string `json:"asset_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ParentID    string `json:"parent_id"`
	RootID      string `json:"root_id"`
	Path        string `json:"path"`
	Depth       int    `json:"depth"`
	IsFolder    bool   `json:"is_folder"`
	DriveFileID string `json:"drive_file_id"`
	DriveLink   string `json:"drive_link"`
	Metadata    string `json:"metadata"`
}
