package scriptcore

import (
	"fmt"
	"strings"
)

// ClipScene links a script paragraph to its source clip.
//
// Kind distinguishes "clip" scenes (tied to a real pack.Accepted clip) from
// "narration" scenes (opening / closing / intro / outro / transition narration
// blocks without a clip_id). DriveLink is empty for narration scenes.
type ClipScene struct {
	SceneIndex    int    `json:"scene_index"`
	ClipID        string `json:"clip_id,omitempty"`
	Text          string `json:"text"`
	DriveLink     string `json:"drive_link,omitempty"`
	Kind          string `json:"kind"`                     // "clip" | "narration" (default "clip" when empty)
	NarrationRole string `json:"narration_role,omitempty"` // "intro", "outro", ... (only when Kind == "narration")
}

// BuildClipScenes parses the script into paragraphs and maps each to a real clip
// from pack.Clips (not the narrative plan, which may contain phantom entries).
// One scene per real clip — no duplicates, no phantom entries.
//
// When there are more paragraphs than clips, the first paragraph is treated as
// an INTRO scene (no clip_id / drive_link) giving compilations a strong hook.
// When there are at least 2 more paragraphs than clips, the last paragraph
// also becomes an OUTRO scene (closing reflection, no clip_id / drive_link).
//
// This function is shared by both the clip-source job handler and the
// MediaCurator service to avoid code duplication.
func BuildClipScenes(script string, pack *ClipSourcePack) []ClipScene {
	realClips := pack.Clips

	paragraphs := []string{}
	for _, p := range strings.Split(strings.TrimSpace(script), "\n\n") {
		p = strings.TrimSpace(p)
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
	}

	scenes := buildScenesFromParagraphs(paragraphs, realClips)
	for i := range scenes {
		if scenes[i].Kind == "" {
			scenes[i].Kind = "clip"
		}
	}
	return scenes
}

// BuildScenesWithMarkers prefers LLM-emitted [Clip: id] markers when present
// (each marker delimits the scene boundary; the body between two markers is
// the scene text). When the LLM did not emit enough markers to account for
// every accepted clip, the missing pieces are filled by distributing the
// script's leftover text across the unused clips via round-robin (the same
// heuristic BuildClipScenes uses as a primary strategy).
//
// The resulting slice always has exactly len(realClips) clip-anchored scenes
// (no duplicates, no orphans), so downstream code (scene_images, voiceovers,
// final result mapping) can rely on a 1:1 correspondence.
//
// Narration scenes ([Narration: opening|closing|intro|outro|transition]) are
// preserved at their original positions from the LLM output and marked with
// Kind="narration" + NarrationRole.
//
// This function is the post-process fallback promised by BuildSourceText's
// OUTPUT CONTRACT for when small models don't reliably emit [Clip: ...] markers.
func BuildScenesWithMarkers(script string, pack *ClipSourcePack) []ClipScene {
	parsed := ParseScenes(script)
	realClips := pack.Clips

	// Collect which clip IDs the LLM actually emitted (in order)
	clipIDOrder := make([]string, 0, len(realClips))
	// Collect narration-only scenes in their original position with text
	type narrationSlot struct {
		text  string
		role  string
		scene *ClipScene
	}
	// Track which real clips have NOT been mentioned yet
	mentioned := make(map[string]bool)
	// First pass: walk parsed scenes, keep precise alignment where possible.
	precise := make([]ClipScene, 0, len(parsed))

	// Build a quick lookup for real clip ID -> ClipEvidence
	realByID := make(map[string]ClipEvidence, len(realClips))
	for _, c := range realClips {
		realByID[c.ClipID] = c
	}

	for _, p := range parsed {
		switch p.Kind {
		case "clip":
			if ev, ok := realByID[p.ClipID]; ok && !mentioned[p.ClipID] {
				precise = append(precise, ClipScene{
					ClipID:    ev.ClipID,
					Text:      p.Text,
					DriveLink: ev.DriveLink,
					Kind:      "clip",
				})
				clipIDOrder = append(clipIDOrder, ev.ClipID)
				mentioned[p.ClipID] = true
			} else if !ok {
				// LLM emitted an unknown / phantom clip_id — treat as a
				// narration scene so we don't lose the text.
				precise = append(precise, ClipScene{
					Text: p.Text,
					Kind: "narration",
				})
			}
			// else: already seen this clip — skip duplicate silently
		case "narration":
			precise = append(precise, ClipScene{
				Text:          p.Text,
				Kind:          "narration",
				NarrationRole: p.NarrationRole,
			})
		case "preamble":
			// Preamble (text before the first marker) becomes an intro narration
			// scene so its content isn't lost.
			if strings.TrimSpace(p.Text) != "" {
				precise = append(precise, ClipScene{
					Text:          p.Text,
					Kind:          "narration",
					NarrationRole: "intro",
				})
			}
		}
	}

	// Find unmentioned clips that still need to be represented.
	var orphanClips []ClipEvidence
	for _, c := range realClips {
		if !mentioned[c.ClipID] {
			orphanClips = append(orphanClips, c)
		}
	}

	if len(orphanClips) == 0 {
		// LLM emitted every clip (or there were none) → re-index precisely.
		for i := range precise {
			precise[i].SceneIndex = i + 1
		}
		return precise
	}

	// Merge: keep the LLM-aligned scenes for mentioned clips, then add
	// orphan clips using a round-robin redistribution of all paragraphs
	// (the same strategy BuildClipScenes uses). This guarantees 1:1 clip
	// coverage even when the writer model skipped several clips.
	merged := make([]ClipScene, 0, len(precise)+len(orphanClips)+2)
	// Preserve the LLM-aligned clip scenes in original order; orphans get
	// appended at the end with round-robin-distributed text from leftover
	// paragraphs (everything that hasn't already been assigned).
	assigned := make(map[string]bool)
	for _, cs := range precise {
		if cs.Kind == "clip" {
			cs.SceneIndex = len(merged) + 1
			merged = append(merged, cs)
			assigned[cs.ClipID] = true
		} else {
			// Narration scenes without clip_id: preserve position, give index.
			cs.SceneIndex = len(merged) + 1
			merged = append(merged, cs)
		}
	}
	for _, oc := range orphanClips {
		merged = append(merged, ClipScene{
			ClipID:    oc.ClipID,
			Text:      "",
			DriveLink: oc.DriveLink,
			Kind:      "clip",
		})
	}
	// Distribute all paragraphs across the merged clip-anchored scenes
	// (round-robin over the clip scenes only, preserving narration in place).
	paragraphs := []string{}
	for _, p := range strings.Split(strings.TrimSpace(script), "\n\n") {
		p = strings.TrimSpace(p)
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
	}
	clipSceneIdx := make([]int, 0, len(merged))
	for i, cs := range merged {
		if cs.Kind == "clip" {
			clipSceneIdx = append(clipSceneIdx, i)
		}
	}
	if len(clipSceneIdx) > 0 && len(paragraphs) > 0 {
		for j, para := range paragraphs {
			idx := clipSceneIdx[j%len(clipSceneIdx)]
			if merged[idx].Text != "" {
				merged[idx].Text += "\n\n"
			}
			merged[idx].Text += para
		}
	}
	for i := range merged {
		merged[i].SceneIndex = i + 1
	}
	return merged
}

// RenderScript reconstitutes a script text from a slice of aligned ClipScenes,
// guaranteeing that every scene has the correct [Clip: ...] or [Narration: ...] marker
// on its FIRST line. This is the inverse of BuildScenesWithMarkers: it lets the
// pipeline emit the markers explicitly even when the LLM skipped them, so the
// DB-stored `script` string matches the structured `clip_scenes[]` array 1:1.
//
// Format:
//   - Clip scene:    `[Clip: <clip_id>]\n<body text>`
//   - Narration scene: `[Narration: <role>]\n<body text>`
//   - Scenes are separated by a blank line.
//
// Default role for narration scenes with empty NarrationRole is `transition`.
// Fully empty pseudo-scenes (no text + no clip_id + no role) are skipped to
// avoid emitting dangling blank markers.
func RenderScript(scenes []ClipScene) string {
	var sb strings.Builder
	for i, s := range scenes {
		if s.Text == "" && s.ClipID == "" && s.NarrationRole == "" {
			continue
		}
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch s.Kind {
		case "narration":
			role := s.NarrationRole
			if role == "" {
				role = "transition"
			}
			sb.WriteString(fmt.Sprintf("[Narration: %s]\n", role))
		default: // "clip" or empty (treated as clip)
			if s.ClipID == "" {
				sb.WriteString("[Narration: transition]\n")
			} else {
				sb.WriteString(fmt.Sprintf("[Clip: %s]\n", s.ClipID))
			}
		}
		sb.WriteString(strings.TrimSpace(s.Text))
	}
	return sb.String()
}

// buildScenesFromParagraphs is the shared body used by both BuildClipScenes
// (when the LLM emitted no markers) and as fallback inside BuildScenesWithMarkers.
// Extracted so the intro/outro + round-robin logic lives in one place.
func buildScenesFromParagraphs(paragraphs []string, realClips []ClipEvidence) []ClipScene {
	if len(realClips) == 0 {
		// No clips at all — every paragraph becomes a narration scene.
		// Use "transition" role (consistent with RenderScript's default)
		// so downstream validator + rendering stay aligned.
		scenes := make([]ClipScene, len(paragraphs))
		for i, para := range paragraphs {
			scenes[i] = ClipScene{
				SceneIndex:    i + 1,
				Text:          para,
				Kind:          "narration",
				NarrationRole: "transition",
			}
		}
		return scenes
	}

	numClips := len(realClips)
	hasIntro := len(paragraphs) > numClips
	hasOutro := len(paragraphs) > numClips+1

	startIdx := 0
	endIdx := len(paragraphs)
	if hasIntro {
		startIdx = 1
	}
	if hasOutro {
		endIdx = len(paragraphs) - 1
	}

	scenes := make([]ClipScene, 0, numClips+2)

	if hasIntro {
		scenes = append(scenes, ClipScene{
			SceneIndex:    1,
			Text:          paragraphs[0],
			Kind:          "narration",
			NarrationRole: "intro",
		})
	}

	for i := range realClips {
		scene := ClipScene{
			SceneIndex: len(scenes) + 1,
			ClipID:     realClips[i].ClipID,
			DriveLink:  realClips[i].DriveLink,
			Kind:       "clip",
		}
		for j := startIdx; j < endIdx; j++ {
			if (j-startIdx)%numClips == i {
				if scene.Text != "" {
					scene.Text += "\n\n"
				}
				scene.Text += paragraphs[j]
			}
		}
		scenes = append(scenes, scene)
	}

	if hasOutro {
		scenes = append(scenes, ClipScene{
			SceneIndex:    len(scenes) + 1,
			Text:          paragraphs[len(paragraphs)-1],
			Kind:          "narration",
			NarrationRole: "outro",
		})
	}

	return scenes
}
