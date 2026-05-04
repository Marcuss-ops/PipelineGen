package models

// ArtlistRunPayload rappresenta il payload per il job media.artlist
type ArtlistRunPayload struct {
	Term         string `json:"term"`
	Limit        int    `json:"limit"`
	RootFolderID string `json:"root_folder_id,omitempty"`
	Strategy     string `json:"strategy,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
}

// YoutubeClipExtractPayload rappresenta il payload per media.extract
type YoutubeClipExtractPayload struct {
	URL         string `json:"url"`
	FolderID    string `json:"folder_id,omitempty"`
	Download    bool   `json:"download,omitempty"`
	UploadDrive bool   `json:"upload_drive,omitempty"`
	StartTime   string `json:"start_time,omitempty"`
	EndTime     string `json:"end_time,omitempty"`
}

// VoiceoverPayload rappresenta il payload per voiceover
type VoiceoverPayload struct {
	Text     string `json:"text"`
	Language string `json:"language,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// VoiceoverBatchPayload rappresenta il payload per voiceover.batch
type VoiceoverBatchPayload struct {
	Items []VoiceoverPayload `json:"items"`
}

// ScriptGenPayload rappresenta il payload per script_generation
type ScriptGenPayload struct {
	Topic       string `json:"topic"`
	Text        string `json:"text,omitempty"`
	Language    string `json:"language,omitempty"`
	Template    string `json:"template,omitempty"`
	Voiceover   bool   `json:"voiceover,omitempty"`
	DriveFolder string `json:"drive_folder_id,omitempty"`
}

// RenderVideoPayload rappresenta il payload per render.video
type RenderVideoPayload struct {
	ScriptID   string   `json:"script_id,omitempty"`
	OutputPath string   `json:"output_path,omitempty"`
	Resolution string   `json:"resolution,omitempty"`
	FrameRate  int      `json:"frame_rate,omitempty"`
	Assets     []string `json:"assets,omitempty"`
}

// YouTubeUploadPayload rappresenta il payload per youtube.upload
type YouTubeUploadPayload struct {
	VideoPath   string `json:"video_path"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Tags        string `json:"tags,omitempty"`
	CategoryID  string `json:"category_id,omitempty"`
	Privacy     string `json:"privacy_status,omitempty"`
}

// CatalogSyncPayload rappresenta il payload per catalog.sync
type CatalogSyncPayload struct {
	Source string `json:"source,omitempty"`
	Full   bool   `json:"full,omitempty"`
}
