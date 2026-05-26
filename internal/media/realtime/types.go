package realtime

// MatchRequest is the payload for real-time asset matching.
type MatchRequest struct {
	Query                  string   `json:"query" binding:"required"`
	Mode                   string   `json:"mode"`                    // "text" or "visual"
	Limit                  int      `json:"limit"`                   // default 3
	MinScore               float64  `json:"min_score"`               // default 0.85
	AllowBackgroundGen     bool     `json:"allow_background_generation"`
	Source                 string   `json:"source,omitempty"`        // optional filter
	Category               string   `json:"category,omitempty"`      // optional filter
	MediaType              string   `json:"media_type,omitempty"`    // optional filter
}

// MatchResponse is the response for real-time matching.
type MatchResponse struct {
	OK                 bool              `json:"ok"`
	Status             string            `json:"status"`          // "instant_match", "fallback_used", "fallback_generating", "no_match"
	LatencyMs          int64             `json:"latency_ms"`
	Asset              *MatchAsset       `json:"asset,omitempty"`
	FallbackAsset      *MatchAsset       `json:"fallback_asset,omitempty"`
	GenerationJobID    string            `json:"generation_job_id,omitempty"`
	GenerationError    string            `json:"generation_error,omitempty"`
}

// MatchAsset represents a matched asset in the response.
type MatchAsset struct {
	ID        string  `json:"id"`
	Score     float64 `json:"score"`
	Source    string  `json:"source,omitempty"`
	Name      string  `json:"name,omitempty"`
	LocalPath string  `json:"local_path,omitempty"`
	DriveLink string  `json:"drive_link,omitempty"`
	Category  string  `json:"category,omitempty"`
	MediaType string  `json:"media_type,omitempty"`
}
