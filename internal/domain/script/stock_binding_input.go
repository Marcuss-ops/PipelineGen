package script

// StockBindingInput is the caller-facing direct stock contract.
type StockBindingInput struct {
	Index      int     `json:"index"`
	SceneID    string  `json:"scene_id,omitempty"`
	SegmentID  string  `json:"segment_id,omitempty"`
	AssetID    string  `json:"asset_id,omitempty"`
	Name       string  `json:"name,omitempty"`
	Source     string  `json:"source,omitempty"`
	DriveLink  string  `json:"drive_link,omitempty"`
	FolderID   string  `json:"folder_id,omitempty"`
	FolderLink string  `json:"folder_link,omitempty"`
	Score      float64 `json:"score,omitempty"`
	Fallback   bool    `json:"fallback"`
	StartMs    int64   `json:"start_ms,omitempty"`
	EndMs      int64   `json:"end_ms,omitempty"`
}
