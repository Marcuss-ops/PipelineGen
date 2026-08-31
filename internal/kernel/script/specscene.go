package script

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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

	// SceneStock — the scene is anchored to a direct stock binding.
	SceneStock SceneKind = "stock"

	// SceneImage — the scene is illustrated by an AI-generated image.
	SceneImage SceneKind = "image"

	// SceneMixed — the scene combines multiple visual elements
	// (e.g. clip overlaid with generated imagery).
	SceneMixed SceneKind = "mixed"
)

// Valid reports whether k is a known scene kind.
func (k SceneKind) Valid() bool {
	switch k {
	case SceneNarration, SceneIntro, SceneOutro, SceneClip, SceneStock, SceneImage, SceneMixed:
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

	// Render describes the requested materialization of the selected clips.
	// It is kept alongside the scene bindings so a generated script is
	// self-contained: consumers know that the referenced clips must be
	// recreated with the requested subtitles/watermark settings.
	Render VideoRenderSpec `json:"render,omitempty"`

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

	// Annotations contains deterministic, scene-local semantic annotations
	// produced from the final scene text.
	Annotations *SceneAnnotations `json:"annotations,omitempty"`

	// Metadata carries technical details that must not be spoken in
	// the voiceover text. It is optional and omitted when empty.
	Metadata *SceneMetadata `json:"metadata,omitempty"`

	// Kind tags the scene's primary visual treatment.
	Kind SceneKind `json:"kind"`

	// AudioMode and source range are explicit render intent. Combined audio
	// jobs must populate these fields; adapters never infer clip audio from
	// filenames or from the mere presence of a clip binding.
	AudioMode        string `json:"audio_mode,omitempty"`
	AudioAssetID     string `json:"audio_asset_id,omitempty"`
	AudioSourceInMS  int64  `json:"audio_source_in_ms,omitempty"`
	AudioSourceOutMS int64  `json:"audio_source_out_ms,omitempty"`

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

// SceneAnnotations is the versioned semantic surface for one scene. Offsets
// are Unicode-rune offsets into SpecScene.Text, never UTF-8 byte offsets.
type SceneAnnotations struct {
	Version           int               `json:"version"`
	Language          string            `json:"language"`
	ImportantPhrases  []AnnotationSpan  `json:"important_phrases,omitempty"`
	PrimaryEntities   []AnnotatedEntity `json:"primary_entities,omitempty"`
	SecondaryEntities []AnnotatedEntity `json:"secondary_entities,omitempty"`
	ImportantWords    []AnnotationSpan  `json:"important_words,omitempty"`
	Status            string            `json:"status,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
}

type AnnotationSpan struct {
	ID        string  `json:"id,omitempty"`
	Text      string  `json:"text"`
	Lemma     string  `json:"lemma,omitempty"`
	StartRune int     `json:"start_rune"`
	EndRune   int     `json:"end_rune"`
	Score     float64 `json:"score,omitempty"`
	Kind      string  `json:"kind,omitempty"`
}

type AnnotatedEntity struct {
	ID            string              `json:"id,omitempty"`
	Text          string              `json:"text"`
	CanonicalName string              `json:"canonical_name"`
	Type          string              `json:"type"`
	Confidence    float64             `json:"confidence,omitempty"`
	Mentions      []AnnotationSpan    `json:"mentions,omitempty"`
	Image         *EntityImageBinding `json:"image,omitempty"`
	// CanonicalEntityID is the stable canonical identity of the entity in
	// the entities-package spelling (e.g. "person:floyd-mayweather"). It is
	// stamped from the Image Search Intent resolver's decision so the media
	// index / overlay resolver join on the SAME identity the resolver
	// chose, never a re-derivation from a possibly-different surface. Empty
	// when the resolver was not wired or the entity was not part of its
	// decision (the overlay compile then derives the id deterministically).
	CanonicalEntityID string `json:"canonical_entity_id,omitempty"`
}

type EntityImageBinding struct {
	Status      string `json:"status"`
	AssetID     string `json:"asset_id,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	// PreviewURL is the direct image URL used for inline rendering in the
	// Google Doc (IDEAL PASS). It is the candidate's source image URL, never
	// a Drive view-page link. Empty when no direct image is available.
	PreviewURL string `json:"preview_url,omitempty"`
	// SHA256 is the content address of the materialized asset bytes (the
	// provider candidate's LegacyFileMD5 after verification). It is what lets the
	// binding be promoted into the content-addressed EntityMediaIndex — a
	// binding without it stays a plain reference and can never become a
	// verifiable card asset.
	SHA256  string `json:"sha256,omitempty"`
	Source  string `json:"source,omitempty"`
	License string `json:"license,omitempty"`
}

// InjectFixedSections injects literal intro/outro SpecScenes verbatim.
// It is the SpecScene-level counterpart of the Runner's Scene injection:
// intro/outro text is never sent to the LLM, never rewritten from
// source_text, and the supplied clip_id is bound with Kind=intro/outro.
// The function reindexes scenes sequentially and de-duplicates IDs.
func InjectFixedSections(plan *ResolvedGenerationPlan, spec *SpecSceneOutput) {
	if plan == nil || spec == nil {
		return
	}
	if plan.Intro == nil && plan.Outro == nil {
		return
	}
	out := make([]SpecScene, 0, len(spec.Scenes)+2)
	if plan.Intro != nil {
		ids := plan.Intro.NormalizedClipIDs()
		if len(ids) == 1 {
			cleanText := strings.TrimSpace(plan.Intro.Text)
			title := plan.Intro.Title
			out = append(out, SpecScene{
				ID:    "scene-intro",
				Index: 0,
				Text:  cleanText,
				Title: title,
				Kind:  SceneIntro,
				Bindings: SceneBindings{
					Clips: []ClipBinding{{ClipID: ids[0]}},
					Clip:  &ClipBinding{ClipID: ids[0]},
				},
			})
		}
	}
	out = append(out, spec.Scenes...)
	if plan.Outro != nil {
		ids := plan.Outro.NormalizedClipIDs()
		if len(ids) == 1 {
			cleanText := strings.TrimSpace(plan.Outro.Text)
			title := plan.Outro.Title
			out = append(out, SpecScene{
				ID:    "scene-outro",
				Index: 0,
				Text:  cleanText,
				Title: title,
				Kind:  SceneOutro,
				Bindings: SceneBindings{
					Clips: []ClipBinding{{ClipID: ids[0]}},
					Clip:  &ClipBinding{ClipID: ids[0]},
				},
			})
		}
	}
	seen := make(map[string]struct{}, len(out))
	for i := range out {
		if strings.TrimSpace(out[i].ID) == "" {
			out[i].ID = fmt.Sprintf("scene-%d", i)
		}
		base := out[i].ID
		for {
			if _, exists := seen[base]; !exists {
				break
			}
			base = fmt.Sprintf("%s-%d", out[i].ID, i)
		}
		seen[base] = struct{}{}
		out[i].ID = base
		out[i].Index = i
	}
	spec.Scenes = out
	if spec.Version == 0 {
		spec.Version = 1
	}
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
