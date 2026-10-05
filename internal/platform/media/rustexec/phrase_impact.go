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

const phraseImpactOutputLimit = 2 << 20

type phraseImpactResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	Result struct {
		Summary      string `json:"summary"`
		BulletPoints []struct {
			Text string `json:"text"`
		} `json:"bullet_points"`
		HeavySentences []scriptpkg.ImportantSentence `json:"heavy_sentences"`
	} `json:"result"`
	Sentences []string `json:"sentences"`
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

// Analyze uses the canonical E5 passage vectors when available; without E5 it
// explicitly uses the bounded lexical scoring mode within Rust.
func (a *PhraseImpactAnalyzer) Analyze(ctx context.Context, transcript, language string) (scriptpkg.PhraseImpactResult, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return scriptpkg.PhraseImpactResult{}, nil
	}
	if a == nil || a.runner == nil || strings.TrimSpace(a.binary) == "" {
		return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact Rust worker is not configured")
	}
	request := map[string]any{
		"transcript": transcript, "language": strings.TrimSpace(language),
		"embeddings": [][]float32{}, "lexical_only": true,
		"options": map[string]any{"summary_length": "medium", "bullet_count": 5, "min_heavy": 3, "max_heavy": 15},
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
		vectors, err := a.embedder.EmbedPassagesBatch(ctx, split.Sentences)
		if err != nil {
			return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact E5 embeddings: %w", err)
		}
		if len(vectors) != len(split.Sentences) {
			return scriptpkg.PhraseImpactResult{}, fmt.Errorf("phrase-impact E5 returned %d embeddings for %d sentences", len(vectors), len(split.Sentences))
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
	result := scriptpkg.PhraseImpactResult{Summary: response.Result.Summary}
	for _, bullet := range response.Result.BulletPoints {
		result.BulletPoints = append(result.BulletPoints, bullet.Text)
	}
	result.HeavySentences = response.Result.HeavySentences
	return result, nil
}
