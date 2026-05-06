package artlist

// RunTagRequest represents the full Artlist tag pipeline request.
type RunTagRequest struct {
	Term         string `json:"term"`
	Limit        int    `json:"limit"`
	RootFolderID string `json:"root_folder_id,omitempty"`
	Strategy     string `json:"strategy,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
}

// ToMap converts RunTagRequest to a map for job payload.
func (r *RunTagRequest) ToMap() map[string]any {
	return map[string]any{
		"term":           r.Term,
		"limit":          r.Limit,
		"root_folder_id": r.RootFolderID,
		"strategy":       r.Strategy,
		"dry_run":        r.DryRun,
	}
}

// RunSmartRequest represents a simplified Artlist pipeline request with presets.
type RunSmartRequest struct {
	Term   string `json:"term" binding:"required"`
	Limit  int    `json:"limit"`
	Preset string `json:"preset"`
}

// PresetConfig defines the configuration for a preset.
type PresetConfig struct {
	ClipDuration int
	Width        int
	Height       int
	FPS          int
	Strategy     string
}

// Presets defines available presets.
var Presets = map[string]PresetConfig{
	"youtube_1080p_7s": {
		ClipDuration: 7,
		Width:        1920,
		Height:       1080,
		FPS:          30,
		Strategy:     "verify",
	},
	"youtube_720p_7s": {
		ClipDuration: 7,
		Width:        1280,
		Height:       720,
		FPS:          30,
		Strategy:     "verify",
	},
	"stock_7s_drive": {
		ClipDuration: 7,
		Width:        1920,
		Height:       1080,
		FPS:          30,
		Strategy:     "verify",
	},
}

// ToRunTagRequest converts RunSmartRequest to RunTagRequest using preset.
func (r *RunSmartRequest) ToRunTagRequest() *RunTagRequest {
	req := &RunTagRequest{
		Term:     r.Term,
		Limit:    r.Limit,
		Strategy: "verify",
	}

	// Apply preset if specified
	if r.Preset != "" {
		if preset, ok := Presets[r.Preset]; ok {
			req.Strategy = preset.Strategy
			// TODO: pass preset config to service for processing
		}
	}

	return req
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
	LastProcessedAt *string     `json:"last_processed_at,omitempty"`
	StartedAt       *string     `json:"started_at,omitempty"`
	EndedAt         *string     `json:"ended_at,omitempty"`
	Items           []RunTagItem `json:"items,omitempty"`
	Error           string       `json:"error,omitempty"`
}

// EvaluateRunOutcome evaluates whether a RunTagResponse represents a failed run.
// This centralizes the policy logic that was previously scattered in job handlers and endpoints.
// Returns (isFailed bool, errorMsg string).
func EvaluateRunOutcome(resp *RunTagResponse) (bool, string) {
	if resp == nil {
		return true, "nil response"
	}
	if !resp.OK {
		errMsg := resp.Error
		if errMsg == "" {
			errMsg = "run was not successful"
		}
		return true, errMsg
	}
	// Policy: if all items failed (Failed > 0, Processed == 0, Skipped == 0), mark as failed
	if resp.Failed > 0 && resp.Processed == 0 && resp.Skipped == 0 {
		return true, "all artlist items failed"
	}
	return false, ""
}
