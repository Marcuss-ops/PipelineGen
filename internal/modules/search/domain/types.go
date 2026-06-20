// Package domain contains the canonical domain types for the search
// bounded context. Search is intentionally separate from scriptgen: its
// re-exported contract let scriptgen consume semantic search without
// pulling in any Qdrant/embedding client.
//
// Adapters live in internal/modules/search/adapters/{qdrant, reranker,
// embeddings} — they convert these types into the wire format of each
// respective subsystem.
package domain

import "time"

// SceneQuery is the input for semantic search matched against a Scene.
type SceneQuery struct {
	ID            string   `json:"id"`
	SceneText     string   `json:"scene_text"`
	Topics        []string `json:"topics,omitempty"`
	Language      string   `json:"language,omitempty"`
	TopK          int      `json:"top_k,omitempty"`
	MinScore      float64  `json:"min_score,omitempty"`
	UseTranscript bool     `json:"use_transcript,omitempty"`
	UseVisual     bool     `json:"use_visual,omitempty"`
	UseAudio      bool     `json:"use_audio,omitempty"`
}

// SceneCandidate is a single semantic match returned by asset search.
type SceneCandidate struct {
	AssetID      string    `json:"asset_id"`
	Title        string    `json:"title"`
	Source       string    `json:"source"` // "youtube" | "artlist" | "stock"
	Score        float64   `json:"score"`
	RerankScore  float64   `json:"rerank_score,omitempty"`
	ClipStartSec int       `json:"clip_start_sec,omitempty"`
	ClipEndSec   int       `json:"clip_end_sec,omitempty"`
	Snippet      string    `json:"snippet,omitempty"`
	DriveLink    string    `json:"drive_link,omitempty"`
	MatchedAt    time.Time `json:"matched_at"`
}

// SceneCandidates groups the matches for a single SceneQuery, ordered
// by Score (highest first).
type SceneCandidates struct {
	QueryID      string           `json:"query_id"`
	Candidates   []SceneCandidate `json:"candidates"`
	UsedReranker bool             `json:"used_reranker,omitempty"`
}
