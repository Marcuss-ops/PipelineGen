package artlist

import "velox/go-master/pkg/models"

// RecommendRequest is the request for the recommendation endpoint
type RecommendRequest struct {
	Topic       string   `json:"topic,omitempty"`
	SegmentID   string   `json:"segment_id,omitempty"`
	SegmentText string   `json:"segment_text,omitempty"`
	Queries     []string `json:"queries,omitempty"`
	Category    string   `json:"category,omitempty"`
	SceneType   string   `json:"scene_type,omitempty"`
	AvoidTerms  []string `json:"avoid_terms,omitempty"`
	UsedClipIDs []string `json:"used_clip_ids,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	MinScore    float64  `json:"min_score,omitempty"`
	Explain     bool     `json:"explain,omitempty"`
}

// RecommendResponse is the response for the recommendation endpoint
type RecommendResponse struct {
	OK           bool            `json:"ok"`
	Topic        string          `json:"topic,omitempty"`
	SegmentID    string          `json:"segment_id,omitempty"`
	Recommended  []ClipRecommend `json:"recommended"`
	Rejected     []ClipRejected  `json:"rejected,omitempty"`
	NeedsHarvest bool            `json:"needs_harvest,omitempty"`
	HarvestTerms []string        `json:"harvest_terms,omitempty"`
}

// ClipRecommend represents a recommended clip with score breakdown
type ClipRecommend struct {
	ClipID         string          `json:"clip_id"`
	Title          string          `json:"title"`
	DriveLink      string          `json:"drive_link,omitempty"`
	LocalPath      string          `json:"local_path,omitempty"`
	Score          float64         `json:"score"`
	MatchedQuery   string          `json:"matched_query,omitempty"`
	Category       string          `json:"category,omitempty"`
	SceneType      string          `json:"scene_type,omitempty"`
	MatchedTerms   []string        `json:"matched_terms,omitempty"`
	ScoreBreakdown *ScoreBreakdown `json:"score_breakdown,omitempty"`
	Reason         string          `json:"reason,omitempty"`
}

// ClipRejected represents a rejected clip with reason
type ClipRejected struct {
	ClipID       string  `json:"clip_id"`
	Title        string  `json:"title"`
	Score        float64 `json:"score"`
	RejectReason string  `json:"reject_reason"`
}

// ScoreBreakdown provides explainable scoring
type ScoreBreakdown struct {
	TextScore       float64 `json:"text_score,omitempty"`
	VectorScore     float64 `json:"vector_score,omitempty"`
	TopicBoost      float64 `json:"topic_boost,omitempty"`
	CategoryBoost   float64 `json:"category_boost,omitempty"`
	QualityScore    float64 `json:"quality_score,omitempty"`
	NegativePenalty float64 `json:"negative_penalty,omitempty"`
	ReusePenalty    float64 `json:"reuse_penalty,omitempty"`
}

// ClipWithScore is an internal type for scoring clips
type ClipWithScore struct {
	Clip         *models.MediaAsset
	Score        float64
	Breakdown    *ScoreBreakdown
	MatchedQuery string
	MatchedTerms []string
	RejectReason string
}
