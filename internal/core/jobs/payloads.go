package jobs

type ArtlistRunPayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Term        string `json:"term"`
	Limit       int    `json:"limit"`
	Strategy    string `json:"strategy"`
	DryRun      bool   `json:"dry_run"`
}

type ScriptGeneratePayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Topic       string `json:"topic"`
	Style       string `json:"style"`
	Language    string `json:"language"`
}

type ScriptPublishPayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	ScriptID    string `json:"script_id"`
	Target      string `json:"target"`
}

type VoiceoverGeneratePayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	ScriptID    string `json:"script_id"`
	Voice       string `json:"voice"`
}

type MediaMatchPayload struct {
	WorkspaceID   string   `json:"workspace_id"`
	ProjectID     string   `json:"project_id"`
	ScriptID      string   `json:"script_id"`
	MaxPerSegment int      `json:"max_per_segment"`
	Sources       []string `json:"sources"`
}

type StockRunPayload struct {
	SearchQueries []string `json:"search_queries"`
	DirectURLs    []string `json:"direct_urls"`
	TotalMinutes  int      `json:"total_minutes"`
	Subfolder     string   `json:"subfolder"`
	FolderName    string   `json:"folder_name"`
}

type MediaImportPayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Source      string `json:"source"`
	DryRun      bool   `json:"dry_run"`
}
