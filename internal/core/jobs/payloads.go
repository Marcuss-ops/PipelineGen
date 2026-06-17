package jobs

import "encoding/json"

type StockRunPayload struct {
	SearchQueries []string `json:"search_queries"`
	DirectURLs    []string `json:"direct_urls"`
	TotalMinutes  int      `json:"total_minutes"`
	ChunkDuration int      `json:"chunk_duration,omitempty"`
	ClipDuration  int      `json:"clip_duration,omitempty"`
	NoAudio       bool     `json:"no_audio,omitempty"`
	NoEffects     bool     `json:"no_effects,omitempty"`
	NoTransitions bool     `json:"no_transitions,omitempty"`
	MaxVideos     int      `json:"max_videos,omitempty"`
	Subfolder     string   `json:"subfolder"`
	FolderName    string   `json:"folder_name"`
	FolderID      string   `json:"folder_id,omitempty"`
	Metadata      *struct {
		Title       string            `json:"title,omitempty"`
		Description string            `json:"description,omitempty"`
		Tags        []string          `json:"tags,omitempty"`
		Category    string            `json:"category,omitempty"`
		Author      string            `json:"author,omitempty"`
		Extra       map[string]string `json:"extra,omitempty"`
	} `json:"metadata,omitempty"`
}

// ToMap serializes the payload to map[string]any for job system compatibility.
func (p *StockRunPayload) ToMap() map[string]any {
	data, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

// MediaGeneratePayload is the payload for generating a missing media asset.
type MediaGeneratePayload struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Query       string `json:"query"`
	Source      string `json:"source"`
	Mode        string `json:"mode"` // "text", "visual"
	Priority    int    `json:"priority"`
}
