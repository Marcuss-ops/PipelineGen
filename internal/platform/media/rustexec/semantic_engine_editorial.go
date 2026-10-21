package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

var semanticEditorialChapterOptions = map[string]any{
	"profile_version": "segmentation.v1", "min_sentences": 5, "max_sentences": 36,
	"min_words": 90, "context_sentences": 3, "semantic_weight": 1.0,
	"lexical_weight": 1.0, "scene_weight": 0.2, "evidence_weight": 1.0,
	"complexity_penalty": 1.0, "bullet_count": 3,
}

// SetPassageEmbedder supplies the same canonical E5 passage embedder used by
// the legacy Phrase Impact adapter during the production cutover.
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
