package domain

import "time"

// Scene is a single AI-generated narrative unit produced by the writing
// phase. Optional fields carry downstream concerns (images, voiceover,
// clip candidates) without coupling to specific adapters.
//
// Kind covers both the structural role (intro / content / outro /
// transition) and serves as the single source of truth for what the
// legacy field scenes.NarrationRole used to convey. Conversion at the
// boundary to scenes.SceneImage is a one-to-one field rename
// (Kind -> NarrationRole) handled by Agent 1.
type Scene struct {
	Index            int       `json:"index"`
	Kind             SceneKind `json:"kind,omitempty"`
	Text             string    `json:"text"`
	ImagePrompt      string    `json:"image_prompt,omitempty"`
	VoiceoverTone    string    `json:"voiceover_tone,omitempty"`
	DurationSec      int       `json:"duration_sec,omitempty"`
	WordCount        int       `json:"word_count"`
	SuggestedClipIDs []string  `json:"suggested_clip_ids,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// SceneKind classifies the role of a Scene in the final narrative. The
// values match the legacy scenes.NarrationRole for backward compatibility
// (see AGENTS.md §3-4 — generate-from-clips endpoint compat).
type SceneKind string

const (
	SceneKindIntro      SceneKind = "intro"
	SceneKindContent    SceneKind = "content"
	SceneKindOutro      SceneKind = "outro"
	SceneKindTransition SceneKind = "transition"
)
