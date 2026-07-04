package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ClipBindingsProcessor assigns clips from ClipEvidence to scenes.
// Each clip maps to exactly one scene, in the canonical order from
// plan.ClipEvidence.AcceptedClipIDs (Issue #2, June 2026: renamed
// from ClipIDs; preserving the resolver's order). Extra scenes
// beyond the clip count receive no clip binding — this surfaces
// LLM output mismatches instead of silently cycling clips.
type ClipBindingsProcessor struct {
	log *zap.Logger
}

func NewClipBindingsProcessor(log *zap.Logger) *ClipBindingsProcessor {
	return &ClipBindingsProcessor{log: log}
}

func (p *ClipBindingsProcessor) Name() ProcessorName { return ProcessorClipBindings }

// Policy classifies clip_bindings as ProcessorBestEffort: a nil or
// empty ClipEvidence is a no-op (Process returns early with empty
// result) rather than a hard fail. Matches the in-body comment that
// the processor "is a no-op when plan.ClipEvidence is nil/empty".
// Pair with `clip_bindings` in defaultPolicyByName so the
// LookupPolicy override path stays consistent.
func (p *ClipBindingsProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *ClipBindingsProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	// Issue #2 (June 2026): ClipIDs renamed to AcceptedClipIDs.
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return &PostProcessResult{}, nil
	}

	scenes := input.SpecScene.Scenes
	heuristicEngaged := false

	// FASE 3 (June 2026) — prose-fallback heuristic.
	//
	// When the resolution cycle landed on prose without any
	// SpecScene.scenes (small local models such as gemma2:2b /
	// gemma4:e4b commonly emit a single intro paragraph and ignore
	// the structured-output schema), the caller still passed
	// clip_ids. Without scenes, the binder has nothing to attach
	// clips to, so it returns empty and the job surfaces a
	// "clip_bindings: empty output" warning.
	//
	// Synthesise N scenes from input.Text so the binder can attach
	// clips 1:1. Heuristic only — preserves no-op semantics when
	// scenes pre-exist or text is empty. The downstream
	// persistence/document processors receive their own copy of the
	// ProcessInput envelope by value, so they keep seeing the
	// original (empty) SpecScene.Scenes; clip_bindings' local
	// synthesis is enough to silence the empty-output warning.
	if len(scenes) == 0 {
		cleanedText := cleanProseFallbackText(input.Text)
		if cleanedText == "" {
			return &PostProcessResult{}, nil
		}
		// Issue #2 (June 2026): ClipIDs renamed → AcceptedClipIDs.
		n := len(plan.ClipEvidence.AcceptedClipIDs)
		if plan.NumClips > 0 && plan.NumClips < n {
			n = plan.NumClips
		}
		synthesized := buildScenesFromProse(cleanedText, n)
		if len(synthesized) == 0 {
			return &PostProcessResult{}, nil
		}
		scenes = synthesized
		heuristicEngaged = true
		if p.log != nil {
			p.log.Info("clip_bindings: prose-fallback heuristic engaged",
				zap.Int("synthesized", len(synthesized)),
				zap.Int("clips", len(plan.ClipEvidence.AcceptedClipIDs)))
		}
	}

	if len(scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	// P0 #2 (June 2026): use the canonical ordered list from
	// plan.ClipEvidence.AcceptedClipIDs instead of iterating the
	// DriveLinks map + sort.Strings. The resolver's order is
	// preserved; clips bind to scenes 1:1 in arrival order.
	// Issue #2 (June 2026): ClipIDs renamed → AcceptedClipIDs.
	clipIDs := plan.ClipEvidence.AcceptedClipIDs

	// Respect NumClips limit.
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}

	// One clip per scene — no modulo cycling. Extra scenes beyond
	// the clip count get no binding. This surfaces LLM output
	// mismatches (more scenes than clips) instead of silently
	// reusing clips.
	bindCount := len(clipIDs)
	if bindCount > len(scenes) {
		bindCount = len(scenes)
	}

	for i := 0; i < bindCount; i++ {
		clipID := clipIDs[i]
		driveLink := plan.ClipEvidence.DriveLinks[clipID]

		scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{
			ClipID:    clipID,
			DriveLink: driveLink,
		}
	}

	// P0 #2: extra scenes beyond the clip count get no binding.
	// Explicitly nil out any LLM-assigned stale binding so the
	// mismatch is visible.
	for i := bindCount; i < len(scenes); i++ {
		scenes[i].Bindings.Clip = nil
	}

	if p.log != nil {
		// Issue #2 (June 2026): ClipIDs renamed to AcceptedClipIDs.
		p.log.Info("clip_bindings: assigned clips to scenes",
			zap.Int("scenes", len(scenes)),
			zap.Int("clips_bound", bindCount),
			zap.Int("clips_available", len(plan.ClipEvidence.AcceptedClipIDs)),
			zap.Int("scenes_unbound", len(scenes)-bindCount),
			zap.Strings("clip_ids", clipIDs[:bindCount]))
	}

	// FASE 3 (June 2026): when the prose-fallback heuristic
	// synthesised scenes, surface them on PostProcessResult. Without
	// this the registry's IsEmpty() check would still flag the
	// binder as "returned empty output" — even though it did the
	// synthesis + 1:1 binding below. SynthesizedScenes counts as
	// observable work in PostProcessResult.IsEmpty() so the empty
	// warning does not fire.
	//
	// P1 #10 (June 2026): set Changed=true even when scenes
	// pre-existed (heuristic NOT engaged) — the binder mutated
	// input.SpecScene.Scenes by binding clips and clearing unbound
	// scenes. Without Changed the registry's IsEmpty() check would
	// still flag "returned empty output" for the non-heuristic
	// code path (the normal case where scenes pre-exist and clips
	// are attached 1:1).
	result := &PostProcessResult{Changed: true}
	if heuristicEngaged {
		result.SynthesizedScenes = scenes
		result.Warnings = []string{
			fmt.Sprintf("clip_bindings: prose-fallback synthesised %d scenes; bound %d/%d clips",
				len(scenes), bindCount, len(clipIDs)),
		}
	}
	return result, nil
}

// buildScenesFromProse is the prose-fallback helper for
// ClipBindingsProcessor. It heuristically partitions the supplied
// prose into N scenes using sentence-aware balanced distribution
// (sentences are grouped into contiguous chunks sized by word
// count, with a word-level fallback only when the input has no
// recognizable sentence breaks).
//
// The helper is unexported and lives next to its only caller; tests
// in processor_clip_bindings_test.go cover its invariants directly.
//
// Kind assignment (matches the canonical scene-kind taxonomy in
// internal/domain/script/model_output.go):
//   - n < 3  → every scene is SceneClip (no intro/outro bleed — the
//     "every requested clip is a real narrative beat" intent wins
//     over the "frame with intro/outro" heuristic).
//   - n == 0 → returns nil (caller preserves no-op).
//   - n >= 3 → scene[0]=SceneIntro, scene[n-1]=SceneOutro, the
//     middle scenes are SceneClip.
//
// Chunks that land empty after the word distribution (rare, only
// when len(words) < n) get the placeholder "Scene {i+1}" so the
// resulting SpecScene.Validate() does not fail on the "text is
// required" rule (model_output.go:265).
func buildScenesFromProse(text string, n int) []scriptpkg.SpecScene {
	if n <= 0 {
		return nil
	}
	trimmed := cleanProseFallbackText(text)
	if trimmed == "" {
		return nil
	}
	words := strings.Fields(trimmed)
	if len(words) == 0 {
		return nil
	}

	sentences := splitProseSentences(trimmed)
	segments := sentences
	if len(segments) == 0 {
		segments = []string{trimmed}
	}
	perChunk := (len(words) + n - 1) / n // ceil division
	scenes := make([]scriptpkg.SpecScene, n)
	chunks := make([]string, 0, n)
	current := make([]string, 0, len(segments))
	currentWords := 0
	for idx, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		segmentWords := len(strings.Fields(segment))
		if len(chunks) < n-1 && currentWords > 0 && currentWords+segmentWords > perChunk {
			chunks = append(chunks, strings.Join(current, " "))
			current = current[:0]
			currentWords = 0
		}
		current = append(current, segment)
		currentWords += segmentWords
		if idx == len(segments)-1 && len(current) > 0 {
			chunks = append(chunks, strings.Join(current, " "))
		}
	}
	if len(chunks) == 0 {
		chunks = []string{trimmed}
	}
	for len(chunks) < n {
		chunks = append(chunks, "")
	}
	for i := 0; i < n; i++ {
		chunk := strings.TrimSpace(chunks[i])
		if chunk == "" && i < len(words) {
			start := i * perChunk
			end := start + perChunk
			if end > len(words) {
				end = len(words)
			}
			if start < len(words) {
				chunk = strings.Join(words[start:end], " ")
			}
		}
		if chunk == "" {
			chunk = fmt.Sprintf("Scene %d", i+1)
		}
		scenes[i] = scriptpkg.SpecScene{
			ID:    fmt.Sprintf("scene-%d", i),
			Index: i,
			Text:  chunk,
			Kind:  kindForPosition(i, n),
		}
	}
	return scenes
}

// cleanProseFallbackText strips an obvious JSON wrapper from prose
// fallback input when the model emitted structured-output noise and
// the compatibility path preserved only the raw text.
//
// It is intentionally conservative: if the content does not look like
// a JSON envelope, it returns the input unchanged.
func cleanProseFallbackText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if unquoted, ok := tryUnquoteJSONString(trimmed); ok {
		trimmed = strings.TrimSpace(unquoted)
	}
	if trimmed == "" {
		return ""
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return trimmed
	}
	if end := findMatchingJSONDelim(trimmed); end > 0 {
		head := strings.TrimSpace(trimmed[:end+1])
		tail := strings.TrimSpace(trimmed[end+1:])
		if tail != "" && isJSONEnvelopeNoise(head) {
			return tail
		}
	}
	return trimmed
}

func tryUnquoteJSONString(text string) (string, bool) {
	var unquoted string
	if err := json.Unmarshal([]byte(text), &unquoted); err != nil {
		return "", false
	}
	return unquoted, true
}

func isJSONEnvelopeNoise(text string) bool {
	return strings.Contains(text, `"schema_version"`) ||
		strings.Contains(text, `"specscene"`) ||
		strings.Contains(text, `"text"`)
}

func findMatchingJSONDelim(s string) int {
	open := s[0]
	close := byte('}')
	if open == '[' {
		close = ']'
	}
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitProseSentences returns sentence-like segments for balanced
// chunking. It uses a minimal punctuation heuristic to avoid cutting
// scenes in the middle of obvious sentence boundaries.
func splitProseSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	flush := func() {
		segment := strings.TrimSpace(current.String())
		if segment != "" {
			sentences = append(sentences, segment)
		}
		current.Reset()
	}

	runes := []rune(strings.TrimSpace(text))
	for i, r := range runes {
		current.WriteRune(r)
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		if next == 0 || next == ' ' || next == '\n' || next == '\t' {
			flush()
		}
	}
	flush()
	return sentences
}

// kindForPosition encodes the position-to-kind policy used by the
// prose-fallback path in buildScenesFromProse. See model_output.go
// for the canonical SceneKind constants (SceneIntro / SceneOutro /
// SceneClip).
func kindForPosition(i, n int) scriptpkg.SceneKind {
	if n < 3 {
		return scriptpkg.SceneClip
	}
	if i == 0 {
		return scriptpkg.SceneIntro
	}
	if i == n-1 {
		return scriptpkg.SceneOutro
	}
	return scriptpkg.SceneClip
}
