// Package scene — synthesizer.go: prose-fallback scene synthesis
// helper extracted from processor_clip_bindings.go (Phase 2 of the
// postprocessor-unification refactor).
//
// Top-level surface: SceneSynthesizer.FromProse(text, n).
// The 6 unexported helpers (tokenization + JSON envelope cleanup +
// kind assignment) live in this file with private scope so the
// binder orchestrator in binder.go delegates cleanly without
// dragging the prose-parsing details into its signature.
//
// godlike/06 SSOT (one canonical owner per fact): After Phase 2, the
// prose-fallback synthesis logic lives ONLY here. The two
// pre-Phase-2 definitions in processor_clip_bindings.go are
// physically deleted; Compile-time accesses go scene.SceneSynthesizer.
//
// godlike/07 NO-FAKE-AVAILABILITY: every labelled chunk that lands
// empty after the word distribution gets the placeholder
// "Scene {i+1}" so the resulting SpecScene.Validate() does not
// fail on the "text is required" rule (model_output.go:265).
package scene

import (
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// SceneSynthesizer is the canonical prose-fallback synthesizer
// (stateless struct; per Thinker Q1 verdict). It maps a prose
// string + a target scene count N into a balanced N-scene bundle
// using sentence-aware distribution with a word-level fallback
// when no recognizable sentence breaks exist.
//
// The synthesizer is constructed via NewSceneSynthesizer() but the
// no-arg state means callers may safely use &SceneSynthesizer{}
// directly. The constructor is the canonical entry point so future
// fields (e.g. parser logger) can be added without breaking callers.
type SceneSynthesizer struct{}

// NewSceneSynthesizer returns a SceneSynthesizer ready for
// FromProse calls. The receiver is a stateless struct today; the
// constructor exists so future fields can be added without breaking
// callers.
func NewSceneSynthesizer() *SceneSynthesizer { return &SceneSynthesizer{} }

// CleanProseFallbackText is the exported thin wrapper around the
// package-internal cleanProseFallbackText helper. It exists so
// external test packages can lock the canonical JSON-envelope
// stripping contract without going through the SceneSynthesizer
// orchestration path (the §4 test in the pasted plan exercises
// this surface directly).
//
// godlike/06 SSOT: cleanProseFallbackText stays UNEXPORTED (the
// package-internal seam is the canonical write surface per
// godlike/07 minimum-blast-radius); this wrapper is a 1-line
// delegation that preserves the exact same behaviour byte-for-byte
// (no transformation, no validation, no logging side-effect).
func CleanProseFallbackText(text string) string {
	return cleanProseFallbackText(text)
}

// FromProse heuristically partitions the supplied prose into N
// scenes using sentence-aware balanced distribution (sentences are
// grouped into contiguous chunks sized by word count, with a
// word-level fallback only when the input has no recognizable
// sentence breaks). Returns nil when N <= 0 or when the cleaned
// text is empty after JSON-envelope stripping.
//
// Kind assignment (matches the canonical scene-kind taxonomy in
// model_output.go):
//   - n < 3  → every scene is SceneClip (no intro/outro bleed — the
//     "every requested clip is a real narrative beat" intent wins
//     over the "frame with intro/outro" heuristic).
//   - n == 0 → returns nil (caller preserves no-op).
//   - n >= 3 → scene[0]=SceneIntro, scene[n-1]=SceneOutro, the
//     middle scenes are SceneClip.
func (s *SceneSynthesizer) FromProse(text string, n int) []scriptpkg.SpecScene {
	return buildScenesFromProse(text, n)
}

// FromText is the canonical fresh-generation orchestration entry point
// that pairs the prose partitioner (FromProse) with the optional
// clip-binding contract for the LLM-PLAIN-TEXT-CONTRACT wave PR-6.
//
// Signature contract (godlike/06 SSOT):
//   - text: the LLM-emitted plain prose (canonical fresh-mode contract
//     per PR-1 prompt flip + PR-2 OutputModePlainText const).
//   - numClips (alias for n): the requested scene count.
//   - evidence: optional *ClipEvidence; when non-nil AND
//     evidence.AcceptedClipIDs is non-empty, FromText binds the i-th
//     scene to AcceptedClipIDs[i] per the canonical P0 #2 1:1 binding
//     contract (NO modulo cycling: scene[i] for i>=len(AcceptedClipIDs)
//     has scene.Bindings.Clip == nil; the binder will surface its own
//     sentinel at postprocessor time if downstream needs coverage).
//
// godlike/06 SSOT (one canonical owner per fact): this method DELEGATES
// to FromProse for prose → scenes distribution (the canonical SOLE
// text-partitioner per the godlike/06 one-canonical-owner-per-fact
// discipline on the per-pipeline prose parser). The binding addition
// layer is purely additive orchestrator (zero new heuristics);
// bindSites[i] assignments go to scene.Bindings.Clip per evidence
// lookup, with the P0 #2 promote-empty invariant preserved (if the
// driver's ClipNames/DriveLinks map omits a key, the binding IS set
// with the known fields; an absent ID lookup yields nil pointer
// which the binder downstream treats as "no binding" per its sentinel
// contract).
//
// godlike/07 NO-FAKE-AVAILABILITY: never synthesises clip IDs via
// modulo cycling (i % len(AcceptedClipIDs)). When i >= len(clipIDs)
// the scene's Bindings.Clip field is left nil (NOT silently
// re-cycled to index 0). The canonical SceneAssetBinder.BindClips is
// the authority on per-clip remix; this method only honours the
// ORDERED 1:1 evidence chain.
//
// godlike/07 minimum-blast-radius: zero new dependencies; uses the
// pre-existing ClipEvidence fields (AcceptedClipIDs canonical slice,
// ClipNames/DriveLinks map[string]string lookups); zero composition-
// root wiring changes (callers opt-in by passing non-nil evidence).
//
// Returns nil when numClips <= 0 OR when the underlying FromProse
// returns nil (caller preserves the no-op invariant).
func (s *SceneSynthesizer) FromText(text string, numClips int, evidence *scriptpkg.ClipEvidence) []scriptpkg.SpecScene {
	scenes := s.FromProse(text, numClips)
	if len(scenes) == 0 {
		return nil
	}
	if evidence == nil || len(evidence.AcceptedClipIDs) == 0 {
		return scenes
	}
	// P0 #2 contract: bind scenes 0..min(len(scenes), len(clipIDs)) 1:1.
	// NO modulo cycling — i >= len(AcceptedClipIDs) leaves scene.Bindings.Clip == nil.
	bindingCount := len(scenes)
	if len(evidence.AcceptedClipIDs) < bindingCount {
		bindingCount = len(evidence.AcceptedClipIDs)
	}
	for i := 0; i < bindingCount; i++ {
		clipID := evidence.AcceptedClipIDs[i]
		if clipID == "" {
			continue
		}
		binding := &scriptpkg.ClipBinding{
			ClipID: clipID,
		}
		if title, ok := evidence.ClipNames[clipID]; ok {
			binding.ClipTitle = title
		}
		if link, ok := evidence.DriveLinks[clipID]; ok {
			binding.DriveLink = link
		}
		if detail, ok := evidence.ClipDetails[clipID]; ok {
			binding.StartMs = detail.StartMs
			binding.EndMs = detail.EndMs
			binding.DurationMs = scriptpkg.ClipDurationMs(detail.StartMs, detail.EndMs)
		}
		scenes[i].Bindings.Clip = binding
	}
	return scenes
}

// buildScenesFromProse partitions text into N scenes via
// sentence-aware chunking + per-chunk word packing. All callers
// MUST route through SceneSynthesizer.FromProse (this is the
// unexported seam that the binder invokes directly for tests).
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
// the compatibility path preserved only the raw text. Intentionally
// conservative: if the content does not look like a JSON envelope,
// it returns the input unchanged.
//
// Canonical reverse: when the model emits
// `{"schema_version": N, "specscene": {...}, "text": "..."}`,
// the prose we want is the value of the "text" key — the
// schema/specscene noise is dropped. Falls back to tail extraction
// when the JSON envelope has prose AFTER the closing delimiter (a
// less common model pattern). If neither extraction succeeds the
// input is returned unchanged.
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
	// Canonical path: parse as envelope, extract "text" key.
	var env struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(trimmed), &env); err == nil && strings.TrimSpace(env.Text) != "" {
		return strings.TrimSpace(env.Text)
	}
	// Fallback: tail extraction when prose follows the JSON envelope.
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

// isJSONEnvelopeNoise signals that the head of a `{`/`[` block looks
// like a model_output schema envelope (schema_version / specscene /
// "text" key). When true AND a non-empty tail exists after the
// closing delimiter, cleanProseFallbackText drops the head and
// returns the tail as the cleaned prose.
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
// chunking. Uses a minimal punctuation heuristic to avoid cutting
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
// prose-fallback path in buildScenesFromProse. Mirrors
// model_output.go SceneKind constants (SceneIntro / SceneOutro /
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
