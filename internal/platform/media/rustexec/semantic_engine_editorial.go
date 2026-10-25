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
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type passageBatchEmbedder interface {
	EmbedPassagesBatch(context.Context, []string) ([]coreasset.EmbeddingResult, error)
}

const (
	phraseImpactEmbeddingBatchSize = 32
)

// phraseStopWords resolves the phrase stop-word set the Rust editorial core
// must apply for a request's language, from the repository lexicon's
// per-language profile (config/lexicons/<lang>/, unioned with the
// cross-linguistic fallback for the languages the repository does not
// enumerate).
//
// The worker holds no word lists of its own — keyphrase extraction there is one
// language-independent algorithm whose only language-specific input is this
// set — so this function is the single place where a language becomes
// linguistic data. An uninstalled registry (low-level callers that never
// bootstrapped linguistics) degrades to no filtering rather than to a silently
// English-only extraction, and the result is sorted so the request payload is
// deterministic.
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

var semanticEditorialChapterOptions = map[string]any{
	"profile_version": "segmentation.v1", "min_sentences": 5, "max_sentences": 36,
	"min_words": 90, "context_sentences": 3, "semantic_weight": 1.0,
	"lexical_weight": 1.0, "scene_weight": 0.2, "evidence_weight": 1.0,
	"complexity_penalty": 1.0, "bullet_count": 3,
}

// SetPassageEmbedder supplies the canonical E5 passage embedder the unified
// semantic/editorial batch uses for its passage vectors.
func (a *SemanticEngineAdapter) SetPassageEmbedder(embedder passageBatchEmbedder) {
	if a != nil {
		a.embedder = embedder
	}
}

// AnalyzeSemanticEditorial obtains semantic and editorial outputs from one
// analyze_script call. Sentence splitting is a preparation request to this
// same worker; the semantic extraction and Phrase Impact run share the final
// manifest request and the same source transcript.
func (a *SemanticEngineAdapter) AnalyzeSemanticEditorial(
	ctx context.Context,
	transcript string,
	language string,
	scenes []scriptpkg.SceneAnalysisInput,
) (map[string]*scriptgen.SemanticCandidateBundle, scriptpkg.PhraseImpactResult, error) {
	language = strings.TrimSpace(language)
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return nil, scriptpkg.PhraseImpactResult{}, fmt.Errorf("semantic-engine: editorial transcript is required")
	}
	if len(scenes) == 0 {
		return nil, scriptpkg.PhraseImpactResult{}, fmt.Errorf("semantic-engine: editorial scenes are required")
	}
	editorial, err := a.prepareEditorialRequest(ctx, transcript, language, scenes)
	if err != nil {
		return nil, scriptpkg.PhraseImpactResult{}, err
	}
	semanticScenes := make([]scriptgen.SemanticSceneInput, 0, len(scenes))
	for _, scene := range scenes {
		semanticScenes = append(semanticScenes, scriptgen.SemanticSceneInput{SceneID: scene.SceneID, Text: scene.Text})
	}
	bundles, result, err := a.semanticBundles(ctx, language, semanticScenes, editorial)
	if err != nil {
		return nil, scriptpkg.PhraseImpactResult{}, err
	}
	if result == nil {
		return nil, scriptpkg.PhraseImpactResult{}, fmt.Errorf("semantic-engine: editorial result missing")
	}
	return bundles, *result, nil
}

func (a *SemanticEngineAdapter) prepareEditorialRequest(
	ctx context.Context,
	transcript string,
	language string,
	scenes []scriptpkg.SceneAnalysisInput,
) (*semanticEngineEditorial, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine: executor is not configured")
	}
	request := map[string]any{
		"transcript": transcript, "language": language, "embeddings": [][]float32{},
		"lexical_only": true, "stopwords": phraseStopWords(language),
		"options": map[string]any{
			"summary_length": "medium", "bullet_count": 5, "min_heavy": 3, "max_heavy": 15,
		},
	}
	if a.embedder != nil {
		started := time.Now()
		payload, err := json.Marshal(map[string]string{
			"operation": "split_sentences", "transcript": transcript, "language": language,
		})
		if err != nil {
			return nil, fmt.Errorf("semantic-engine: encode editorial sentence split: %w", err)
		}
		stdout, _, err := a.executor.Run(ctx, append(payload, '\n'))
		if err != nil {
			return nil, fmt.Errorf("semantic-engine: split editorial sentences: %w", err)
		}
		var split semanticEngineSplitResponse
		if err := json.Unmarshal(bytes.TrimSpace(stdout), &split); err != nil {
			return nil, fmt.Errorf("semantic-engine: decode editorial sentence split: %w", err)
		}
		if !split.OK || len(split.Sentences) == 0 {
			return nil, fmt.Errorf("semantic-engine: sentence split failed: %s", strings.TrimSpace(split.Error))
		}
		passages := make([]string, len(split.Sentences))
		for index, sentence := range split.Sentences {
			if sentence.StartByte < 0 || sentence.EndByte <= sentence.StartByte || sentence.EndByte > len(transcript) ||
				transcript[sentence.StartByte:sentence.EndByte] != sentence.Text {
				return nil, fmt.Errorf("semantic-engine: sentence %d has invalid UTF-8 byte offsets", index)
			}
			passages[index] = sentence.Text
		}
		vectors := make([][]float32, 0, len(passages))
		for start := 0; start < len(passages); start += phraseImpactEmbeddingBatchSize {
			end := min(start+phraseImpactEmbeddingBatchSize, len(passages))
			batch, err := a.embedder.EmbedPassagesBatch(ctx, passages[start:end])
			if err != nil {
				return nil, fmt.Errorf("semantic-engine: E5 embeddings for sentences %d..%d: %w", start, end, err)
			}
			if len(batch) != end-start {
				return nil, fmt.Errorf("semantic-engine: E5 returned %d embeddings for sentence batch %d..%d", len(batch), start, end)
			}
			for _, result := range batch {
				vectors = append(vectors, result.Vector)
			}
		}
		request["embeddings"], request["lexical_only"] = vectors, false
		request["embedding_ms"] = float64(time.Since(started).Microseconds()) / 1000
	}
	return &semanticEngineEditorial{
		Request: request, Scenes: scenes, ChapterOptions: semanticEditorialChapterOptions,
	}, nil
}

var _ scriptgen.SceneSemanticEditorialBatchAnalyzer = (*SemanticEngineAdapter)(nil)

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
