package script

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// SceneKind tags each scene with its primary visual treatment.
type SceneKind string

const (
	// SceneNarration — voiceover or on-screen narration with no
	// associated visual asset.
	SceneNarration SceneKind = "narration"

	// SceneIntro — introductory narration for the video.
	SceneIntro SceneKind = "intro"

	// SceneOutro — concluding narration / call-to-action for the video.
	SceneOutro SceneKind = "outro"

	// SceneClip — the scene is anchored to a selected YouTube clip.
	SceneClip SceneKind = "clip"

	// SceneImage — the scene is illustrated by an AI-generated image.
	SceneImage SceneKind = "image"

	// SceneMixed — the scene combines multiple visual elements
	// (e.g. clip overlaid with generated imagery).
	SceneMixed SceneKind = "mixed"
)

// Valid reports whether k is a known scene kind.
func (k SceneKind) Valid() bool {
	switch k {
	case SceneNarration, SceneIntro, SceneOutro, SceneClip, SceneImage, SceneMixed:
		return true
	}
	return false
}

// SpecSceneOutput is the structured scene breakdown embedded within
// ModelScriptOutputV1. Version tracks the specscene schema version
// (currently 1).
//
// JSON shape:
//
//	{
//	  "version": 1,
//	  "scenes": [
//	    { "id": "scene-1", "index": 0, "text": "...", "kind": "clip", "bindings": {...} }
//	  ]
//	}
type SpecSceneOutput struct {
	// Version is the specscene schema version. Currently 1.
	Version int `json:"version"`

	// Scenes is the ordered list of scenes. May be empty for pure
	// prose generation where no scene breakdown is expected.
	Scenes []SpecScene `json:"scenes"`

	// VisualAssignments contains independent intro/post-segment timeline
	// selections alongside the per-scene bindings.
	VisualAssignments []media.VisualAssignment `json:"visual_assignments,omitempty"`
}

// Validate checks structural invariants on the specscene block.
//
// Rules:
//   - Version must be 1.
//   - Scene Indexes must be sequential (0..len-1).
//   - Scene IDs must be non-empty and unique.
//   - Every scene must pass its own Validate.
func (s *SpecSceneOutput) Validate() error {
	var details []string

	if s.Version != 1 {
		details = append(details,
			fmt.Sprintf("unsupported specscene version %d (expected 1)", s.Version))
	}

	seenIDs := make(map[string]int) // id → first index
	for i := range s.Scenes {
		scene := &s.Scenes[i]

		// Scene-level validation.
		if err := scene.Validate(); err != nil {
			if me, ok := err.(*ModelOutputError); ok {
				for _, d := range me.Details {
					details = append(details,
						fmt.Sprintf("scenes[%d]: %s", i, d))
				}
			} else {
				details = append(details,
					fmt.Sprintf("scenes[%d]: %s", i, err.Error()))
			}
		}

		// Index must match position.
		if scene.Index != i {
			details = append(details,
				fmt.Sprintf("scenes[%d]: index mismatch (expected %d, got %d)",
					i, i, scene.Index))
		}

		// Duplicate ID check.
		if scene.ID != "" {
			if prev, ok := seenIDs[scene.ID]; ok {
				details = append(details,
					fmt.Sprintf("scenes[%d]: duplicate scene id %q (first seen at index %d)",
						i, scene.ID, prev))
			} else {
				seenIDs[scene.ID] = i
			}
		}
	}

	if len(details) > 0 {
		return &ModelOutputError{Details: details}
	}
	return nil
}

// SpecScene is a single scene within the structured breakdown.
// Every scene has a stable ID, sequential Index, narrative Text,
// a Kind tag, and optional Bindings to resolved assets.
//
// JSON shape:
//
//	{
//	  "id": "scene-1",
//	  "index": 0,
//	  "text": "Scene narration text...",
//	  "title": "Opening",
//	  "kind": "clip",
//	  "bindings": { "clip": {...} }
//	}
type SpecScene struct {
	// ID is a stable identifier within the result. Must be non-empty.
	ID        string `json:"id"`
	SegmentID string `json:"segment_id,omitempty"`

	// Index is the zero-based position in the scene array.
	Index int `json:"index"`

	// Text is the narrative text for this scene. Must be non-empty.
	Text string `json:"text"`

	// Title is an optional human-readable scene title.
	Title string `json:"title,omitempty"`

	// Metadata carries technical details that must not be spoken in
	// the voiceover text. It is optional and omitted when empty.
	Metadata *SceneMetadata `json:"metadata,omitempty"`

	// Kind tags the scene's primary visual treatment.
	Kind SceneKind `json:"kind"`

	// VisualPlan carries the canonical visual plan produced by the
	// visual planning processor. It is nil when visual planning is
	// disabled or produced no layers for this scene.
	VisualPlan *VisualPlan `json:"visual_plan,omitempty"`

	// Bindings holds the resolved asset references for this scene.
	// Always present in the JSON output (as {} when no assets are
	// bound). Individual binding fields (clip, image, voiceover)
	// use omitempty and are absent when nil.
	Bindings SceneBindings `json:"bindings"`
}

// Validate checks structural invariants on a single scene.
//
// Rules:
//   - ID must be non-empty.
//   - Text must be non-empty.
//   - Kind must be a valid SceneKind.
func (s *SpecScene) Validate() error {
	var details []string

	if strings.TrimSpace(s.ID) == "" {
		details = append(details, "id is required")
	}
	if strings.TrimSpace(s.Text) == "" {
		details = append(details, "text is required")
	}
	if !s.Kind.Valid() {
		details = append(details,
			fmt.Sprintf("unknown scene kind %q", s.Kind))
	}

	if len(details) > 0 {
		return &ModelOutputError{Details: details}
	}
	return nil
}
