package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	phraseImpactOutputLimit        = 2 << 20
	phraseImpactEmbeddingBatchSize = 32
)

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
	// The worker's own stage breakdown travels with the result so the runner
	// can publish the embedding cost; it is telemetry, not an input to any
	// editorial decision.
	result.Timings = response.Result.Timings
	return result, nil
}
