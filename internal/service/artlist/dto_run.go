package artlist

import (
	"io/ioutil"

	"gopkg.in/yaml.v3"
)

// RunTagRequest represents the full Artlist tag pipeline request.
type RunTagRequest struct {
	Term         string `json:"term"`
	Limit        int    `json:"limit"`
	RootFolderID string `json:"root_folder_id,omitempty"`
	Strategy     string `json:"strategy,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
	ClipDuration int    `json:"clip_duration,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	FPS          int    `json:"fps,omitempty"`
}

// ToMap converts RunTagRequest to a map for job payload.
// Deprecated: Use JobCodec.PayloadFromRequest instead for consistency.
func (r *RunTagRequest) ToMap() map[string]any {
	return (&JobCodec{}).PayloadFromRequest(r)
}

// RunSmartRequest represents a simplified Artlist pipeline request with presets.
type RunSmartRequest struct {
	Term   string `json:"term" binding:"required"`
	Limit  int    `json:"limit"`
	Preset string `json:"preset"`
}

// PresetConfig defines the configuration for a preset.
type PresetConfig struct {
	ClipDuration int    `yaml:"clip_duration"`
	Width        int    `yaml:"width"`
	Height       int    `yaml:"height"`
	FPS          int    `yaml:"fps"`
	Strategy     string `yaml:"strategy"`
}

// PresetsConfig holds all preset definitions from config file.
type PresetsConfig struct {
	Presets map[string]PresetConfig `yaml:"presets"`
}

// LoadPresets loads preset configurations from YAML file.
func LoadPresets(path string) (*PresetsConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &PresetsConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ToRunTagRequest converts RunSmartRequest to RunTagRequest using preset.
func (r *RunSmartRequest) ToRunTagRequest(presets *PresetsConfig) *RunTagRequest {
	req := &RunTagRequest{
		Term:     r.Term,
		Limit:    r.Limit,
		Strategy: "verify",
	}

	// Apply preset if specified
	if r.Preset != "" && presets != nil {
		if preset, ok := presets.Presets[r.Preset]; ok {
			req.Strategy = preset.Strategy
			req.ClipDuration = preset.ClipDuration
			req.Width = preset.Width
			req.Height = preset.Height
			req.FPS = preset.FPS
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
	DriveFileID  string `json:"drive_file_id"`
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
	TagFolderLink   string       `json:"tag_folder_link,omitempty"`
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
	CompletedAt     *string     `json:"completed_at,omitempty"`
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
