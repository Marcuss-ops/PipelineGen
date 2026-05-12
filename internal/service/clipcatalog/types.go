package clipcatalog

import "time"

// ClipMetadata represents extended metadata for a clip
type ClipMetadata struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	SearchText   string    `json:"search_text"`
	Tags         []string  `json:"tags,omitempty"`
	Embedding    []float64 `json:"embedding,omitempty"`
	Category     string    `json:"category,omitempty"`
	SceneType   string    `json:"scene_type,omitempty"`
	UsableFor   []string  `json:"usable_for,omitempty"`
	AvoidFor     []string  `json:"avoid_for,omitempty"`
	QualityScore float64   `json:"quality_score"`
	ReuseCount   int       `json:"reuse_count"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	LastIndexedAt *time.Time `json:"last_indexed_at,omitempty"`
}

// ClipCandidate represents a clip candidate for recommendation
type ClipCandidate struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	SearchText   string   `json:"search_text"`
	Category     string   `json:"category"`
	SceneType   string   `json:"scene_type"`
	Tags         []string `json:"tags"`
	DriveLink    string   `json:"drive_link,omitempty"`
	LocalPath    string   `json:"local_path,omitempty"`
	QualityScore float64  `json:"quality_score"`
	ReuseCount   int      `json:"reuse_count"`
	UsableFor   []string `json:"usable_for,omitempty"`
	AvoidFor     []string `json:"avoid_for,omitempty"`
}

// IndexRequest represents a request to index a clip
type IndexRequest struct {
	ClipID      string   `json:"clip_id"`
	SearchText  string   `json:"search_text,omitempty"`
	Category    string   `json:"category,omitempty"`
	SceneType   string   `json:"scene_type,omitempty"`
	UsableFor   []string `json:"usable_for,omitempty"`
	AvoidFor    []string `json:"avoid_for,omitempty"`
}


