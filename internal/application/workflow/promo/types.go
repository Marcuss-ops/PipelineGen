// Package promo provides a multi-language voiceover generation workflow.
// Extracted from voiceover/promo.go (PR 6, June 2026).
package promo

// Request represents a promotional voiceover request.
type Request struct {
	Text          string   `json:"text" binding:"required"`
	DriveFolderID string   `json:"drive_folder_id,omitempty"`
	DryRun        bool     `json:"dry_run,omitempty"`
	Languages     []string `json:"languages,omitempty"`
}

// Result holds the result of a single promo voiceover generation.
type Result struct {
	OK          bool   `json:"ok"`
	Language    string `json:"language"`
	Translated  string `json:"translated,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Response aggregates all promo voiceover results.
type Response struct {
	OK      bool     `json:"ok"`
	Total   int      `json:"total"`
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Results []Result `json:"results"`
}
