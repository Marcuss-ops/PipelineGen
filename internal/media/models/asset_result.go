package models

// AssetExecutionResult is a unified result type for asset processing across all modules.
// It combines fields from BatchItem, RunTagItem, ExtractItem, AssetResult, FinalizeResult.
type AssetExecutionResult struct {
	ID           string `json:"id,omitempty"`
	Source       string `json:"source,omitempty"`     // e.g. "youtube", "artlist", "voiceover"
	MediaType    string `json:"media_type,omitempty"` // e.g. "video", "audio", "image"
	Filename     string `json:"filename,omitempty"`
	LocalPath    string `json:"local_path,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DownloadLink string `json:"download_link,omitempty"`
	FileHash     string `json:"file_hash,omitempty"`
	Status       string `json:"status,omitempty"` // e.g. "processed", "skipped_existing", "failed"
	Error        string `json:"error,omitempty"`
}
