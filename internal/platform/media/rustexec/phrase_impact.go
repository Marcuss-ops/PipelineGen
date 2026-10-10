package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	phraseImpactOutputLimit        = 2 << 20
	phraseImpactEmbeddingBatchSize = 32
)

// phraseStopWords resolves the phrase stop-word set the Rust worker must apply
// for a request's language, from the repository lexicon's per-language profile
// (config/lexicons/<lang>/, unioned with the cross-linguistic fallback for the
// languages the repository does not enumerate).
//
// The worker holds no word lists of its own — keyphrase extraction there is one
// language-independent algorithm whose only language-specific input is this
// set — so this function is the single place where a language becomes linguistic
// data. An uninstalled registry (low-level callers that never bootstrapped
// linguistics) degrades to no filtering rather than to a silently English-only
// extraction, and the result is sorted so the request payload is deterministic.
func phraseStopWords(language string) []string {
	registry := linguistics.DefaultLexiconOrNil()
	if registry == nil {
		return nil
	}
	words := registry.PhraseStopWords(strings.TrimSpace(language))
	out := make([]string, 0, len(words))
	for word := range words {
		out = append(out, word)
	}
	sort.Strings(out)
	return out
}

// SceneInput is the worker-facing alias of the kernel scene identity contract.
type SceneInput = scriptpkg.SceneAnalysisInput

type phraseImpactResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	Result struct {
		Summary      string `json:"summary"`
		BulletPoints []struct {
			Text string `json:"text"`
		} `json:"bullet_points"`
		HeavySentences  []scriptpkg.ImportantSentence `json:"heavy_sentences"`
		ChapterManifest scriptpkg.ChapterManifest     `json:"chapter_manifest"`
		SceneHighlights []scriptpkg.SceneHighlight    `json:"scene_highlights"`
		Timings         scriptpkg.PhraseImpactTimings `json:"timings"`
	} `json:"result"`
	Sentences []struct {
		Text      string `json:"text"`
		StartByte int    `json:"start_byte"`
		EndByte   int    `json:"end_byte"`
	} `json:"sentences"`
}

type passageBatchEmbedder interface {
	EmbedPassagesBatch(context.Context, []string) ([]coreasset.EmbeddingResult, error)
}

type PhraseImpactAnalyzer struct {
	binary   string
	runner   RustProcessRunner
	embedder passageBatchEmbedder
}

func NewPhraseImpactAnalyzer(binary string, runner RustProcessRunner, embedder passageBatchEmbedder) *PhraseImpactAnalyzer {
	if runner == nil {
		runner = newPersistentRustProcessRunner()
	}
	return &PhraseImpactAnalyzer{binary: binary, runner: runner, embedder: embedder}
}

// Analyze preserves the historical API for callers that have no scene context.
func (a *PhraseImpactAnalyzer) Analyze(ctx context.Context, transcript, language string) (scriptpkg.PhraseImpactResult, error) {
	return a.AnalyzeWithContext(ctx, transcript, language, nil, nil)
}

// AnalyzeScenes runs the single canonical analysis with verified per-scene
// identity (editorial.v1). Error states are distinct: worker unavailable /
// transport failure returns an error (caller continues without editorial);
// contract errors (duplicate identity, bad offsets, unknown schema) also
// return errors but the caller must treat them as "manifest not certified"
// rather than retrying blindly. A valid manifest with zero highlights is a
// successful empty result, not an error.
func (a *PhraseImpactAnalyzer) AnalyzeScenes(ctx context.Context, transcript, language string, scenes []SceneInput) (scriptpkg.PhraseImpactResult, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return scriptpkg.PhraseImpactResult{}, nil
	}
	if a == nil || a.runner == nil || strings.TrimSpace(a.binary) == "" {
		return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact Rust worker is not configured")
	}
	seen := map[string]struct{}{}
	for _, s := range scenes {
		if strings.TrimSpace(s.SceneID) == "" {
			return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact scene with empty id")
		}
		if _, dup := seen[s.SceneID]; dup {
			return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact duplicate scene identity")
		}
		seen[s.SceneID] = struct{}{}
		if s.StartByte != nil && s.EndByte != nil {
			st, en := *s.StartByte, *s.EndByte
			if st < 0 || en <= st || en > len(transcript) || transcript[st:en] != s.Text {
				return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact scene %q slice mismatch", s.SceneID)
			}
			// UTF-8 boundary check: slicing above would panic otherwise.
			_ = st
		}
	}
	legacyScenes := make([]string, len(scenes))
	legacyTopics := make([]string, len(scenes))
	for i, s := range scenes {
		legacyScenes[i] = s.Text
		if s.Topic != nil {
			legacyTopics[i] = *s.Topic
		}
	}
	base, err := a.AnalyzeWithContext(ctx, transcript, language, legacyScenes, legacyTopics)
	if err != nil {
		return base, err
	}
	// Second pass with structured identity for highlights only; legacy
	// products above stay the certified baseline.
	highlights, certified, err := a.analyzeSceneHighlights(ctx, transcript, language, scenes)
	if err != nil {
		// Contract-level failure: keep legacy products, mark uncertified.
		base.SceneHighlightsCertified = false
		return base, nil
	}
	base.SceneHighlights = highlights
	base.SceneHighlightsCertified = certified
	return base, nil
}

// AnalyzeWithContext uses the canonical E5 passage vectors when available;
// without E5 it explicitly uses the bounded lexical scoring mode within Rust.
// Scene and topic metadata is optional editorial context for chapter analysis.
func (a *PhraseImpactAnalyzer) AnalyzeWithContext(ctx context.Context, transcript, language string, scenes, topics []string) (scriptpkg.PhraseImpactResult, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return scriptpkg.PhraseImpactResult{}, nil
	}
	if a == nil || a.runner == nil || strings.TrimSpace(a.binary) == "" {
		return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact Rust worker is not configured")
	}
	chapterOptions := map[string]any{
		"profile_version":    "segmentation.v1",
		"min_sentences":      5,
		"max_sentences":      36,
		"min_words":          90,
		"context_sentences":  3,
		"semantic_weight":    1.0,
		"lexical_weight":     1.0,
		"scene_weight":       0.2,
		"evidence_weight":    1.0,
		"complexity_penalty": 1.0,
		"bullet_count":       3,
	}
	request := map[string]any{
		"transcript": transcript, "language": strings.TrimSpace(language),
		"embeddings": [][]float32{}, "lexical_only": true,
		"scenes": scenes, "scene_topics": topics,
		"chapter_options": chapterOptions,
		"stopwords":       phraseStopWords(language),
		"options":         map[string]any{"summary_length": "medium", "bullet_count": 5, "min_heavy": 3, "max_heavy": 15},
	}
	if a.embedder != nil {
		started := time.Now()
		splitPayload, _ := json.Marshal(map[string]string{"operation": "split_sentences", "transcript": transcript, "language": language})
		stdout, stderr, err := a.runner.Run(ctx, a.binary, append(splitPayload, '\n'), phraseImpactOutputLimit)
		if err != nil {
			return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact sentence split: %w: %s", err, strings.TrimSpace(string(stderr)))
		}
		var split phraseImpactResponse
		if err := json.Unmarshal(bytes.TrimSpace(stdout), &split); err != nil {
			return scriptpkg.PhraseImpactResult{}, fmt.Errorf("decode phrase-impact sentence split: %w", err)
		}
		if !split.OK || len(split.Sentences) == 0 {
			return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact sentence split failed: %s", split.Error)
		}
		sentences := make([]string, len(split.Sentences))
		for i, sentence := range split.Sentences {
			if sentence.StartByte < 0 || sentence.EndByte <= sentence.StartByte || sentence.EndByte > len(transcript) || !bytes.Equal([]byte(transcript[sentence.StartByte:sentence.EndByte]), []byte(sentence.Text)) {
				return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact sentence %d has invalid UTF-8 byte offsets", i)
			}
			sentences[i] = sentence.Text
		}
		vectors := make([]coreasset.EmbeddingResult, 0, len(sentences))
		for start := 0; start < len(sentences); start += phraseImpactEmbeddingBatchSize {
			end := min(start+phraseImpactEmbeddingBatchSize, len(sentences))
			batch, err := a.embedder.EmbedPassagesBatch(ctx, sentences[start:end])
			if err != nil {
				return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact E5 embeddings for sentences %d..%d: %w", start, end, err)
			}
			if len(batch) != end-start {
				return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact E5 returned %d embeddings for sentence batch %d..%d", len(batch), start, end)
			}
			vectors = append(vectors, batch...)
		}
		rows := make([][]float32, len(vectors))
		for i := range vectors {
			rows[i] = vectors[i].Vector
		}
		request["embeddings"], request["lexical_only"] = rows, false
		request["embedding_ms"] = float64(time.Since(started).Microseconds()) / 1000
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return scriptpkg.PhraseImpactResult{}, fmt.Errorf("marshal phrase-impact request: %w", err)
	}
	stdout, stderr, err := a.runner.Run(ctx, a.binary, append(payload, '\n'), phraseImpactOutputLimit)
	if err != nil {
		return scriptpkg.PhraseImpactResult{}, fmt.Errorf("run phrase-impact Rust worker: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	var response phraseImpactResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &response); err != nil {
		return scriptpkg.PhraseImpactResult{}, fmt.Errorf("decode phrase-impact Rust response: %w", err)
	}
	if !response.OK {
		return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact Rust analysis: %s", response.Error)
	}
	result := scriptpkg.PhraseImpactResult{Summary: response.Result.Summary, ChapterManifest: response.Result.ChapterManifest}
	for _, bullet := range response.Result.BulletPoints {
		result.BulletPoints = append(result.BulletPoints, bullet.Text)
	}
	result.HeavySentences = response.Result.HeavySentences
	result.SceneHighlights, result.SceneHighlightsCertified = validateSceneHighlights(transcript, response.Result.SceneHighlights)
	// The worker's own stage breakdown travels with the result so the runner
	// can publish the embedding cost; it is telemetry, not an input to any
	// editorial decision.
	result.Timings = response.Result.Timings
	return result, nil
}

// validateSceneHighlights enforces the contract: single invalid spans are
// dropped (degraded manifest), incoherent identity invalidates certification.
// Unknown title_status is a schema error -> uncertified, never silent pass.
func validateSceneHighlights(transcript string, in []scriptpkg.SceneHighlight) ([]scriptpkg.SceneHighlight, bool) {
	if len(in) == 0 {
		return nil, true
	}
	seen := map[string]struct{}{}
	out := make([]scriptpkg.SceneHighlight, 0, len(in))
	for _, h := range in {
		if strings.TrimSpace(h.SceneID) == "" {
			return nil, false
		}
		if _, dup := seen[h.SceneID]; dup {
			return nil, false
		}
		seen[h.SceneID] = struct{}{}
		if h.TitleStatus != "resolved" && h.TitleStatus != "unavailable" {
			return nil, false
		}
		if h.TitleStatus == "unavailable" && h.Title != nil {
			return nil, false
		}
		validBullets := h.Bullets[:0]
		for _, b := range h.Bullets {
			if b.SentenceStart < 0 || b.SentenceEnd <= b.SentenceStart || strings.TrimSpace(b.Text) == "" {
				continue
			}
			if !strings.Contains(transcript, b.Text) {
				continue
			}
			validBullets = append(validBullets, b)
		}
		h.Bullets = validBullets
		validHl := h.Highlights[:0]
		for _, sp := range h.Highlights {
			if sp.StartByte < 0 || sp.EndByte <= sp.StartByte || sp.EndByte > len(transcript) {
				continue
			}
			if transcript[sp.StartByte:sp.EndByte] != sp.Text {
				continue
			}
			validHl = append(validHl, sp)
		}
		h.Highlights = validHl
		out = append(out, h)
	}
	return out, true
}

func (a *PhraseImpactAnalyzer) analyzeSceneHighlights(ctx context.Context, transcript, language string, scenes []SceneInput) ([]scriptpkg.SceneHighlight, bool, error) {
	chapterOptions := map[string]any{
		"profile_version": "segmentation.v1", "min_sentences": 5, "max_sentences": 36,
		"min_words": 90, "context_sentences": 3, "semantic_weight": 1.0,
		"lexical_weight": 1.0, "scene_weight": 0.2, "evidence_weight": 1.0,
		"complexity_penalty": 1.0, "bullet_count": 3,
	}
	request := map[string]any{
		"transcript": transcript, "language": strings.TrimSpace(language),
		"embeddings": [][]float32{}, "lexical_only": true,
		"scene_inputs": scenes, "chapter_options": chapterOptions,
		"stopwords": phraseStopWords(language),
		"options":   map[string]any{"summary_length": "medium", "bullet_count": 5, "min_heavy": 3, "max_heavy": 15},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, false, err
	}
	stdout, stderr, err := a.runner.Run(ctx, a.binary, append(payload, '\n'), phraseImpactOutputLimit)
	if err != nil {
		return nil, false, fmt.Errorf("run phrase-impact scene highlights: %w: %s", err, strings.TrimSpace(string(stderr)))
	}
	var response phraseImpactResponse
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &response); err != nil {
		return nil, false, fmt.Errorf("decode phrase-impact highlights: %w", err)
	}
	if !response.OK {
		return nil, false, fmt.Errorf("phrase-impact highlights: %s", response.Error)
	}
	hl, certified := validateSceneHighlights(transcript, response.Result.SceneHighlights)
	if !certified {
		return nil, false, fmt.Errorf("incoherent scene identity")
	}
	return hl, true, nil
}
