// Package semantic — ollama_analyzer.go: slim orchestrator for the
// concrete adapter that satisfies monitor.VideoAnalyzer.
//
// 3-file layout per PR-SPLIT-OLLAMA-ANALYZER (LONG-FILES-DECOMPOSITION-V2-2026-07-06,
// P3 BASSA band, July 2026):
//
//	ollama_analyzer.go           (this file, slim orchestrator)
//	  - Deps struct
//	  - OllamaAnalyzer struct
//	  - NewOllamaAnalyzer ctor
//	  - Classify method
//	  - AnalyzeFull stub
//	  - compile-time assertion
//
//	ollama_analyzer_score.go     (Score domain)
//	  - Score method
//	  - jsonRegexFind helper
//
//	ollama_analyzer_segments.go  (Segments domain)
//	  - FindSegments method
//	  - isLowValueMonitorSegmentName helper
//
// Step 9 commit 2 (June 2026, Channel Monitor Blocco 6 architectural
// rewrite): the OllamaAnalyzer is the canonical owner of the Ollama
// SimpleGenerate calls + JSON parsing + score normalization + segment
// duration validation. Per AGENTS.md Pattern 0 (port abstraction), the
// monitor package NEVER imports the OllamaClient directly — those
// concerns are owned here (the application-layer sibling package of
// monitor/).
//
// Surface area: implements all 3 methods of the monitor.VideoAnalyzer port
// (Score + Classify + FindSegments), distributed across the 3 sibling
// files; the AnalyzeFull stub and compile-time assertion live here in
// the slim orchestrator per godlike/06 SSOT.
package semantic

import (
	"context"
	"fmt"

	transcripts "github.com/Marcuss-ops/PipelineGen/internal/application/transcripts"
	transcript "github.com/Marcuss-ops/PipelineGen/internal/kernel/transcript"

	"go.uber.org/zap"

	monitor "github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/classifier"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
)

// Deps is the ctor payload for NewOllamaAnalyzer. The OllamaClient
// satisfies classifier.LLMClient structurally (SimpleGenerate signature
// matches); no separate interface declaration needed.
type Deps struct {
	OllamaClient *client.Client
	Subtitles    transcripts.SubtitleSource
	Log          *zap.Logger
	// Model is the Ollama model name; default "gemma4:e2b" (matches the
	// pre-Step-9 hard-default). Production callers should pass
	// cfg.External.OllamaModel.
	Model string
	// DataDir is the storage data dir for classifier.Classify's category
	// scan; default "" (caller responsible for production wiring).
	DataDir string
	// DefaultCategory is the LLM fallback when classification fails;
	// default "general".
	DefaultCategory string
}

// OllamaAnalyzer implements monitor.VideoAnalyzer. Holds the Ollama
// client + the YTDLPSubtitleAdapter for the FindSegments VTT re-fetch +
// the config knobs that drive prompt construction.
type OllamaAnalyzer struct {
	ollamaClient *client.Client
	subtitles    transcripts.SubtitleSource
	log          *zap.Logger
	model        string
	dataDir      string
	defaultCat   string
}

// NewOllamaAnalyzer constructs the analyzer with the canonical
// pre-Step-9 defaults baked in (gemma4:e2b model, "general" fallback).
func NewOllamaAnalyzer(d Deps) *OllamaAnalyzer {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	if d.Model == "" {
		d.Model = "gemma4:e2b"
	}
	if d.DefaultCategory == "" {
		d.DefaultCategory = "general"
	}
	return &OllamaAnalyzer{
		ollamaClient: d.OllamaClient,
		subtitles:    d.Subtitles,
		log:          d.Log,
		model:        d.Model,
		dataDir:      d.DataDir,
		defaultCat:   d.DefaultCategory,
	}
}

// Classify satisfies monitor.VideoAnalyzer.
//
// Delegates to classifier.Classify(...) which scans the configured
// data dir for existing category subdirectories, renders the
// classification prompt from the prompts package, and sanitizes the
// LLM response. Falls back to the configured DefaultCategory on
// Ollama error or empty sanitized category.
//
// Notice: the *OllamaAnalyzer is NOT passed to classifier.Classify;
// classifier.Classify takes the LLMClient interface as a positional
// arg so the dependency circle is broken naturally.
//
// Kept in the slim orchestrator (rather than its own file) because it's
// a 30-LoC wrapper that delegates to classifier.Classify; splitting it
// out would require a 4th file just for one method per the user-spec
// 3-file layout for PR-SPLIT-OLLAMA-ANALYZER.
func (a *OllamaAnalyzer) Classify(ctx context.Context, title string, fallback string) (string, error) {
	if a.ollamaClient == nil {
		return "", fmt.Errorf("OllamaAnalyzer.Classify: ollama client not wired")
	}
	if fallback == "" {
		fallback = a.defaultCat
	}
	category := classifier.Classify(ctx, a.log, a.ollamaClient, title, classifier.Options{
		DataDir:          a.dataDir,
		Model:            a.model,
		FallbackCategory: fallback,
		// DefaultCategories matches the pre-Step-9 monitor classifier.sh /
		// classifier.go literal. Operators wanting different seeds add
		// a per-channel field in Blocco 7.
		DefaultCategories: []string{
			"boxe", "comedy", "crime", "discovery", "explanatory",
			"hiphop", "interviews", "music", "nba", "politics", "rap", "wwe",
		},
	})
	return category, nil
}

// AnalyzeFull (Commit G, June 2026) — JSON one-shot stub on the concrete
// OllamaAnalyzer. Returns ErrAnalyzeFullNotImplemented so the orchestrator
// (analyzeVideo) can detect a non-upgraded analyzer and fall back to the
// legacy Score / Classify / FindSegments 3-call flow. The real JSON
// prompt + windowed sampling + Ollama semaphore gating land in Commit H
// per the implementation ticket tracked in CHANGELOG.md "Commit G follow".
func (a *OllamaAnalyzer) AnalyzeFull(_ context.Context, _ transcript.Document, _ monitor.AnalyzeOptions) (monitor.Analysis, error) {
	return monitor.Analysis{}, monitor.ErrAnalyzeFullNotImplemented
}

// Compile-time assertion: OllamaAnalyzer must satisfy monitor.VideoAnalyzer.
// Per AGENTS.md Pattern 0 (port abstraction layer) — every adapter that
// satisfies a port declares this assertion at the bottom of its file.
var _ monitor.VideoAnalyzer = (*OllamaAnalyzer)(nil)
