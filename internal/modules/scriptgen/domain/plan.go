package domain

import "time"

// Plan is the high-level outline produced by the planning phase of the
// pipeline, before text is written per section. One Plan owns many
// OutlineSections, each of which later becomes one or more Scenes.
type Plan struct {
	ID        int64            `json:"id"`
	ScriptID  int64            `json:"script_id"`
	Topic     string           `json:"topic"`
	Goal      string           `json:"goal"`
	Style     string           `json:"style"`
	Tone      string           `json:"tone"`
	Language  string           `json:"language"`
	Sections  []OutlineSection `json:"sections"`
	CreatedAt time.Time        `json:"created_at"`
}

// OutlineSection represents one planned section in the narrative.
// Index is its position in the final document (0-based).
type OutlineSection struct {
	Index         int      `json:"index"`
	Title         string   `json:"title"`
	Purpose       string   `json:"purpose"`
	TargetWords   int      `json:"target_words"`
	KeyPoints     []string `json:"key_points,omitempty"`
	EmotionalRole string   `json:"emotional_role,omitempty"`
}
