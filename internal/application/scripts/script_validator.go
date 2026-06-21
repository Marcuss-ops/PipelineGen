package scripts

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// ── ScriptValidationResult ──────────────────────────────────────────────
//
// ScriptValidationResult is the deterministic quality gate for scripts
// generated via the clip-to-script path. It collects structural issues
// (clip IDs, scene count, scene length, marker format) and warnings
// (statistical issues from the existing ValidateScript).
//
// Two operating modes (controlled by the caller of ValidateScriptWithPack):
//   - "soft" (default): all checks run, all issues are collected. Hard
//     failures (empty clip ID, unknown clip ID, unparseable script) are
//     logged as warnings. The script is still passed to SaveMemory.
//   - "hard" (future PR): hard failures skip SaveMemory.
//
// The soft mode is the current behavior because the LLM occasionally
// produces salvageable scripts with minor structural issues; cutting them
// off entirely would lose good output. The gate's job is to make the
// issues visible, not to be a kill switch.
type ScriptValidationResult struct {
	// Valid is true when no hard structural failure was found.
	// Hard failures: empty clip ID, unknown clip ID, unparseable script.
	Valid bool

	// Structural failures (hard): make Valid=false.
	MissingClipIDs       []string // marker present but with empty ID, e.g. "[Clip: ]"
	UnknownClipIDs       []string // clip ID not in pack.Accepted
	DuplicateClipIDs     []string // same clip ID used in two markers
	EmptyClipBlocks      []int    // 1-based scene indices with empty text
	InvalidMarkers       []string // markers that don't match the expected format
	MissingAcceptedClips []string // accepted clips never used in the script

	// Soft warnings: do not change Valid.
	OverlongScenes           []int    // 1-based scene indices exceeding MaxCharsPerScene
	NarrationScenesForbidden []int    // 1-based scene indices that are narration but AllowNarrationScenes=false
	StructuralWarnings       []string // any other soft issue with a human-readable message

	// All warnings aggregated from the statistical checks (ValidateScript).
	Warnings []string

	// SceneCount is the number of [Clip: ...] blocks parsed.
	SceneCount int

	// ExpectedSceneCount is what we expected based on the pack + Type +
	// AllowNarrationScenes. May differ from SceneCount if the LLM dropped
	// or added scenes.
	ExpectedSceneCount int
}

// ── Markers ─────────────────────────────────────────────────────────────

// validNarrationRoles are the only allowed values inside [Narration: ...].
// Keep this list small and explicit to prevent LLM drift.
var validNarrationRoles = map[string]bool{
	"opening":    true,
	"closing":    true,
	"transition": true,
	"intro":      true,
	"outro":      true,
}

// ── Parsing ─────────────────────────────────────────────────────────────

// ParsedScene represents a single scene block from the generated script,
// starting with either a [Clip: id] or [Narration: role] marker.
type ParsedScene struct {
	// 1-based scene index in the order they appear in the script
	Index int
	// Marker is the raw marker line as it appears in the script
	Marker string
	// Kind is "clip" or "narration"
	Kind string
	// ClipID is non-empty when Kind == "clip"
	ClipID string
	// NarrationRole is non-empty when Kind == "narration"
	NarrationRole string
	// Text is the body of the scene (everything between this marker
	// and the next marker, or end of script)
	Text string
	// CharCount is len(Text) for cheap length checks
	CharCount int
}

// ParseScenes splits a script into its scene blocks by [Clip: ...] and
// [Narration: ...] markers. A scene that starts with a marker ends just
// before the next marker (or at the end of the script). Preamble text
// (before the first marker) is included as a "preamble" pseudo-scene
// with Index=0, Kind="preamble".
func ParseScenes(script string) []ParsedScene {
	lines := strings.Split(script, "\n")
	// Build a list of (line_index, marker, kind, payload) tuples by
	// scanning for matching lines.
	type markerHit struct {
		lineIdx int
		marker  string
		kind    string // "clip" | "narration"
		clipID  string
		role    string
	}
	var hits []markerHit
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := textutil.ClipMarkerRe.FindStringSubmatch(trimmed); m != nil {
			hits = append(hits, markerHit{lineIdx: i, marker: trimmed, kind: "clip", clipID: m[1]})
			continue
		}
		if m := textutil.NarrationMarkerRe.FindStringSubmatch(trimmed); m != nil {
			hits = append(hits, markerHit{lineIdx: i, marker: trimmed, kind: "narration", role: m[1]})
		}
	}

	// Preamble (text before the first marker)
	if len(hits) == 0 {
		// No markers at all — single pseudo-scene
		return []ParsedScene{{Index: 1, Kind: "preamble", Text: script, CharCount: len(script)}}
	}

	scenes := make([]ParsedScene, 0, len(hits)+1)

	// Preamble
	preambleLines := lines[:hits[0].lineIdx]
	preamble := strings.TrimSpace(strings.Join(preambleLines, "\n"))
	if preamble != "" {
		scenes = append(scenes, ParsedScene{
			Index:     0, // 0 = preamble
			Kind:      "preamble",
			Text:      preamble,
			CharCount: len(preamble),
		})
	}

	for i, h := range hits {
		var bodyLines []string
		if i+1 < len(hits) {
			bodyLines = lines[h.lineIdx+1 : hits[i+1].lineIdx]
		} else {
			bodyLines = lines[h.lineIdx+1:]
		}
		body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
		scenes = append(scenes, ParsedScene{
			Index:         i + 1,
			Marker:        h.marker,
			Kind:          h.kind,
			ClipID:        h.clipID,
			NarrationRole: h.role,
			Text:          body,
			CharCount:     len(body),
		})
	}

	return scenes
}

// ── Top-level validation ────────────────────────────────────────────────

// ValidateScriptWithPack runs the full set of post-generation checks
// (statistical + structural) and returns a ScriptValidationResult.
//
// When pack is nil (regular text generation, not clip-to-script), only
// the statistical checks run and the structural fields are zeroed out.
//
// mode controls behavior:
//   - "soft" (default): all checks run, all issues are returned; the
//     caller decides whether to use the result.
//   - "hard" (future): same checks; the caller should skip SaveMemory
//     when result.Valid is false.
func ValidateScriptWithPack(sc string, plan *script.ScriptGenerationPlan, pack *ClipSourcePack, allowNarration bool, maxCharsPerScene int) *ScriptValidationResult {
	res := &ScriptValidationResult{Valid: true}

	if sc == "" {
		res.Valid = false
		res.StructuralWarnings = append(res.StructuralWarnings, "script is empty")
		return res
	}

	// Always run the statistical checks (existing ValidateScript)
	stat := ValidateScript(sc, plan)
	for _, s := range stat.Scores {
		if !s.Passed {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s", s.Check, s.Message))
		}
	}

	// No pack → only statistical checks (regular text generation flow)
	if pack == nil {
		return res
	}

	// Build the set of accepted clip IDs
	accepted := make(map[string]bool, len(pack.Clips))
	for _, c := range pack.Clips {
		accepted[c.ClipID] = true
	}

	scenes := ParseScenes(sc)
	res.SceneCount = countClipScenes(scenes)

	// Expected scene count.
	// Narration scenes (opening, closing) are OPTIONAL and never bump the
	// expected count -- the LLM may include 0, 1, or 2 of them.
	expected := len(pack.Clips)
	res.ExpectedSceneCount = expected

	// Walk scenes and collect issues
	seenClipIDs := make(map[string]int) // clip_id -> first scene index
	usedAccepted := make(map[string]bool)

	for _, s := range scenes {
		switch s.Kind {
		case "clip":
			// Empty clip ID
			if strings.TrimSpace(s.ClipID) == "" {
				res.MissingClipIDs = append(res.MissingClipIDs, s.Marker)
				res.Valid = false
				continue
			}
			// Unknown clip ID
			if !accepted[s.ClipID] {
				res.UnknownClipIDs = append(res.UnknownClipIDs, s.ClipID)
				res.Valid = false
				continue
			}
			// Duplicate
			if prev, dup := seenClipIDs[s.ClipID]; dup {
				res.DuplicateClipIDs = append(res.DuplicateClipIDs,
					fmt.Sprintf("%s (also at scene %d)", s.ClipID, prev))
				res.Valid = false
				continue
			}
			seenClipIDs[s.ClipID] = s.Index
			usedAccepted[s.ClipID] = true
			// Empty text
			if s.CharCount == 0 {
				res.EmptyClipBlocks = append(res.EmptyClipBlocks, s.Index)
				res.Valid = false
			}
			// Overlong scene
			if maxCharsPerScene > 0 && s.CharCount > maxCharsPerScene {
				res.OverlongScenes = append(res.OverlongScenes, s.Index)
			}

		case "narration":
			if !allowNarration {
				res.NarrationScenesForbidden = append(res.NarrationScenesForbidden, s.Index)
				res.Valid = false
				continue
			}
			if !validNarrationRoles[s.NarrationRole] {
				res.InvalidMarkers = append(res.InvalidMarkers,
					fmt.Sprintf("scene %d: unknown role %q", s.Index, s.NarrationRole))
				res.Valid = false
			}
			if s.CharCount == 0 {
				res.EmptyClipBlocks = append(res.EmptyClipBlocks, s.Index)
				res.Valid = false
			}
			if maxCharsPerScene > 0 && s.CharCount > maxCharsPerScene {
				res.OverlongScenes = append(res.OverlongScenes, s.Index)
			}
		}
	}

	// Missing accepted clips
	for _, c := range pack.Clips {
		if !usedAccepted[c.ClipID] {
			res.MissingAcceptedClips = append(res.MissingAcceptedClips, c.ClipID)
		}
	}

	return res
}

// countClipScenes returns the number of scenes with kind="clip".
// Narration and preamble scenes are not counted toward the expected count.
func countClipScenes(scenes []ParsedScene) int {
	n := 0
	for _, s := range scenes {
		if s.Kind == "clip" {
			n++
		}
	}
	return n
}

// ── LogWarnings ─────────────────────────────────────────────────────────

// LogWarnings writes the validation result to the given logger at
// warn-level for hard failures and info-level for soft warnings. It is
// used by engine.WriteScript after CleanScript but before SaveMemory, so
// that the cache contains an audit trail of every quality issue.
func (r *ScriptValidationResult) LogWarnings(log *zap.Logger) {
	if r == nil || log == nil {
		return
	}
	if !r.Valid {
		log.Warn("script validation: HARD failures detected (script may be salvaged but should be reviewed)",
			zap.Strings("missing_clip_ids", r.MissingClipIDs),
			zap.Strings("unknown_clip_ids", r.UnknownClipIDs),
			zap.Strings("duplicate_clip_ids", r.DuplicateClipIDs),
			zap.Ints("empty_clip_blocks", r.EmptyClipBlocks),
			zap.Strings("missing_accepted_clips", r.MissingAcceptedClips),
			zap.Ints("narration_forbidden", r.NarrationScenesForbidden),
		)
	}
	if len(r.OverlongScenes) > 0 {
		log.Warn("script validation: overlong scenes",
			zap.Ints("scene_indices", r.OverlongScenes))
	}
	if len(r.Warnings) > 0 {
		log.Info("script validation: soft warnings",
			zap.Strings("warnings", r.Warnings))
	}
	if len(r.StructuralWarnings) > 0 {
		log.Warn("script validation: structural warnings",
			zap.Strings("warnings", r.StructuralWarnings))
	}
}
