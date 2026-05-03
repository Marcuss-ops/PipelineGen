package artlist

// RunTagRequest represents the full Artlist tag pipeline request.
type RunTagRequest struct {
	Term         string `json:"term"`
	Limit        int    `json:"limit"`
	RootFolderID string `json:"root_folder_id,omitempty"`
	Strategy     string `json:"strategy,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
	// Deprecated: kept for backward compatibility with older clients.
	ForceReupload bool `json:"force_reupload,omitempty"`
}

// ToMap converts RunTagRequest to a map for job payload.
func (r *RunTagRequest) ToMap() map[string]any {
	return map[string]any{
		"term":           r.Term,
		"limit":         r.Limit,
		"root_folder_id": r.RootFolderID,
		"strategy":      r.Strategy,
		"dry_run":       r.DryRun,
	}
}

// RunDedupKey creates a deduplication key for artlist jobs.
func RunDedupKey(term, rootFolderID, strategy string, dryRun bool) string {
	return runDedupKey(term, rootFolderID, strategy, dryRun)
}

// RunTagItem represents the result for a single clip in the full pipeline.
type RunTagItem struct {
	ClipID       string `json:"clip_id"`
	Name         string `json:"name"`
	Filename     string `json:"filename"`
	Status       string `json:"status"`
	DownloadURL  string `json:"download_url,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DownloadLink string `json:"download_link,omitempty"`
	LocalPath    string `json:"local_path,omitempty"`
	FileHash     string `json:"file_hash,omitempty"`
	Error        string `json:"error,omitempty"`
}

// RunTagResponse represents the result of the full tag pipeline.
type RunTagResponse struct {
	OK              bool         `json:"ok"`
	RunID           string       `json:"run_id,omitempty"`
	Status          string       `json:"status,omitempty"`
	Term            string       `json:"term"`
	Strategy        string       `json:"strategy,omitempty"`
	DryRun          bool         `json:"dry_run,omitempty"`
	RootFolderID    string       `json:"root_folder_id,omitempty"`
	TagFolderID     string       `json:"tag_folder_id,omitempty"`
	Requested       int          `json:"requested"`
	Found           int          `json:"found"`
	Processed       int          `json:"processed"`
	Skipped         int          `json:"skipped"`
	Failed          int          `json:"failed"`
	WouldProcess    int          `json:"would_process,omitempty"`
	WouldSkip       int          `json:"would_skip,omitempty"`
	EstimatedSize   int          `json:"estimated_size,omitempty"`
	LastProcessedAt *string      `json:"last_processed_at,omitempty"`
	StartedAt       *string      `json:"started_at,omitempty"`
	EndedAt         *string      `json:"ended_at,omitempty"`
	Items           []RunTagItem `json:"items,omitempty"`
	Error           string       `json:"error,omitempty"`
}
