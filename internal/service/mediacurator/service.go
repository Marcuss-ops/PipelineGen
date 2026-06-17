// Package mediacurator provides an intelligent service that searches ALL
// available media (YouTube clips, Artlist, stock, images) from a natural
// language topic, automatically selects the best matches via Qdrant semantic
// search, and generates a complete compilation script with intro and outro.
//
// This is NOT a thin endpoint wrapper — it is a proactive discovery service
// that can be called by any part of the system when someone says
// "make a compilation about X" without knowing exact clip IDs.
package mediacurator

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/scriptcore"
)

// ── Style Presets (caricati da config/style_presets.yaml) ────────────────

// stylePresetConfig è la struttura del file YAML dei preset.
type stylePresetConfig struct {
	Presets []stylePresetEntry `yaml:"presets"`
}

// stylePresetEntry è un singolo preset.
type stylePresetEntry struct {
	Name         string `yaml:"name"`
	Instructions string `yaml:"instructions"`
}

// loadStylePresets carica i preset da config/style_presets.yaml.
func loadStylePresets(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read style presets: %w", err)
	}

	var cfg stylePresetConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse style presets: %w", err)
	}

	presets := make(map[string]string, len(cfg.Presets))
	for _, p := range cfg.Presets {
		presets[p.Name] = strings.TrimSpace(p.Instructions)
	}
	return presets, nil
}

// ── Types ───────────────────────────────────────────────────────────────

// CurateRequest specifies a natural-language query for media curation.
type CurateRequest struct {
	// Query is the natural language topic (e.g. "funny actors parenting stories").
	Query string

	// Title is an optional explicit title. Auto-generated from search results when empty.
	Title string

	// Language for the generated script (default "en").
	Language string

	// Tone for the generated script (default "comedy").
	Tone string

	// Model is the LLM model to use (default: system default).
	Model string

	// MaxClips limits the number of clips to include (default 10, max 30).
	MaxClips int

	// TargetWords for the generated script (default 2000).
	TargetWords int

	// MaxCharsPerScene caps each paragraph (default 600, 0 = no limit).
	MaxCharsPerScene int

	// MinScore filters clips below this semantic similarity threshold (default 0.5).
	MinScore float64

	// StyleInstructions è un prompt opzionale per controllare stile, tono e struttura dello script.
	StyleInstructions string

	// Type definisce la STRUTTURA dello script:
	//   "" o "documentary" (default): narrazione fluida con intro/outro
	//   "compilation": presenta ogni clip prima con intro comica, poi la clip, poi transizione
	//   "story": arco narrativo a tre atti
	//   "interview": struttura a domanda/risposta
	Type string

	// Style è il nome di un preset salvato in config/style_presets.yaml.
	// Valori disponibili: "comedy", "documentary", "emotional", "educational", "narrative", "compilation".
	// Se specificato insieme a StyleInstructions, i due vengono concatenati.
	Style string

	// Source optionally filters by source system: "youtube", "artlist", "stock".
	// Empty means all sources.
	Source string

	// MediaType optionally filters by media type: "video", "image", "audio".
	// Empty means all types.
	MediaType string

	// SelectableClips controlla quante clip candidates cercare (il "pool" di clip
	// da cui Gemma può scegliere). Quando > 0, la ricerca cerca questo numero di clip
	// e le passa al narrative planner. MaxClips controlla invece quante clip
	// vengono effettivamente usate nello script finale.
	// Default: 0 = usa la logica legacy (MaxClips * 3).
	SelectableClips int

	// ForceRefresh bypasses the memory gate cache.
	ForceRefresh bool
}

// CurateResult is the output of a successful curation.
type CurateResult struct {
	Title             string                    `json:"title"`
	Script            string                    `json:"script"`
	WordCount         int                       `json:"word_count"`
	ClipScenes        []scriptcore.ClipScene    `json:"clip_scenes,omitempty"`
	AcceptedClipIDs   []string                  `json:"accepted_clip_ids,omitempty"`
	NarrativePlan     *scriptcore.NarrativePlan `json:"narrative_plan,omitempty"`
	SourceText        string                    `json:"source_text,omitempty"`
	SourceFingerprint string                    `json:"source_fingerprint,omitempty"`
	SearchResults     []SearchResultInfo        `json:"search_results,omitempty"`
	CacheStatus       string                    `json:"cache_status,omitempty"`
	DocLink           string                    `json:"doc_link,omitempty"`
	Timings           CurateTimings             `json:"timings,omitempty"`
}

// SearchResultInfo describes a clip found during Qdrant search.
type SearchResultInfo struct {
	ClipID    string  `json:"clip_id"`
	Name      string  `json:"name"`
	Score     float64 `json:"score"`
	Source    string  `json:"source,omitempty"`
	DriveLink string  `json:"drive_link,omitempty"`
}

// CurateTimings records how long each phase took.
type CurateTimings struct {
	SearchMs      int64 `json:"search_ms"`
	BuildCtxMs    int64 `json:"build_context_ms"`
	WriteScriptMs int64 `json:"write_script_ms"`
	TotalMs       int64 `json:"total_ms"`
}

// resolveStyleInstructions returns the effective style instructions by
// resolving the StylePreset name and merging with StyleInstructions.
func (r *CurateRequest) resolveStyleInstructions(presets map[string]string) string {
	var parts []string

	// Resolve preset by name
	if r.Style != "" && presets != nil {
		if text, ok := presets[r.Style]; ok {
			parts = append(parts, text)
		}
	}

	// Append explicit style instructions if provided
	if r.StyleInstructions != "" {
		parts = append(parts, r.StyleInstructions)
	}

	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// defaults applies sensible defaults to the request.
func (r *CurateRequest) defaults() {
	r.Query = strings.TrimSpace(r.Query)
	if r.Language == "" {
		r.Language = "en"
	}
	if r.Tone == "" {
		r.Tone = "comedy"
	}
	if r.MaxClips <= 0 {
		r.MaxClips = 10
	}
	if r.MaxClips > 30 {
		r.MaxClips = 30
	}
	if r.SelectableClips > 0 && r.SelectableClips < r.MaxClips {
		r.SelectableClips = r.MaxClips
	}
	if r.SelectableClips > 60 {
		r.SelectableClips = 60
	}
	if r.TargetWords <= 0 {
		r.TargetWords = 2000
	}
	if r.MinScore <= 0 {
		r.MinScore = 0.35
	}
}

// ── Service ─────────────────────────────────────────────────────────────

// Service curates media assets by searching across ALL available sources
// via Qdrant semantic search, selecting the best matches, and producing
// a complete compilation script with intro/outro.
//
// It is self-sufficient: creates its own embedding client and searches
// directly via the vectorstore, without requiring the realtime service.
type Service struct {
	vectorSvc   *vectorstore.Service
	embedder    realtime.EmbeddingClient
	clipsRepo   *clips.Repository
	clipBuilder *scriptcore.ClipSourceBuilder
	engine      *scriptcore.Engine
	log         *zap.Logger
	presets     map[string]string
}

// NewService creates a MediaCurator service.
func NewService(
	vectorSvc *vectorstore.Service,
	embedderURL string,
	clipsRepo *clips.Repository,
	clipBuilder *scriptcore.ClipSourceBuilder,
	engine *scriptcore.Engine,
	log *zap.Logger,
) *Service {
	var embedder realtime.EmbeddingClient
	if embedderURL != "" {
		embedder = realtime.NewPythonEmbeddingAdapter(embedderURL)
	}

	presets, err := loadStylePresets("config/style_presets.yaml")
	if err != nil {
		log.Warn("style presets not loaded, StylePreset field will be ignored", zap.Error(err))
		presets = nil
	} else {
		log.Info("style presets loaded", zap.Int("count", len(presets)))
	}

	return &Service{
		vectorSvc:   vectorSvc,
		embedder:    embedder,
		clipsRepo:   clipsRepo,
		clipBuilder: clipBuilder,
		engine:      engine,
		log:         log,
		presets:     presets,
	}
}
