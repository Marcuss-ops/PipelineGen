// Package scripts — types.go declares the canonical types shared by
// the script-pipeline orchestration layer (Engine, BatchService,
// ScenesService, Pipeline, MediaCurator, DocumentsService, etc.).
//
// PG-033 (June 2026): all service struct fields are now CONCRETE TYPES.
// The previous shape used `interface{}` slots with comments naming the
// expected concrete holder (e.g. `*ollama.Generator`) and runtime
// `ok := x.(*Type)` assertions. PG-033 collapses those to direct typed
// access — the compiler now enforces the contract at build time, and
// redundant runtime assertions are deleted from the method bodies.
//
// Cross-package imports added to this file (ollama, gemmamemory,
// voiceover, images, config, assets, scriptpkg) are ALREADY consumed
// by sibling files in the same package — verified zero cycle risk
// (images and voiceover do not import back into scripts).
//
// Two struct fields deliberately remain `interface{}` because they
// accept multiple backend implementations at runtime:
//
//   - MediaCurator.vectorStore
//   - ClipSourceBuilder.vectorStore
//   - ClipSourceBuilder.reranker
//
// These are matched against locally-defined narrow ports (e.g.
// clipSearcher) at use site; concrete static binding would force a
// single adapter and remove swap-at-composition flexibility.
package scripts

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
)

// ── Function types ──────────────────────────────────────────────────────

// FolderResolver resolves a folder ID from an input name and default root.
type FolderResolver func(ctx context.Context, input, defaultRootID string) (string, error)

// ── Curate types ────────────────────────────────────────────────────────

// narrowClipSearcher is the runtime-port MediaCurator uses to talk to
// the (dynamic) vector store. Kept local because we accept multiple
// backends (qdrant.NewSearchAdapter, ...) and the shape is too narrow
// to live in a public port interface.
type narrowClipSearcher interface {
	SearchClips(ctx context.Context, query, source, mediaType string, limit int, minScore float64) ([]RealtimeMatchAsset, error)
}

// MediaCurator orchestrates semantic clip search + script generation.
// PG-033: vectorStore remains `interface{}` (multi-backend); clipsRepo
// is now concrete (*assets.ClipsRepository).
type MediaCurator struct {
	vectorStore interface{}
	serverURL   string
	clipsRepo   *assets.ClipsRepository
	clipBuilder *ClipSourceBuilder
	engine      *Engine
	log         *zap.Logger
}

// CurateRequest carries the inputs for a curation job.
type CurateRequest struct {
	Query             string
	Title             string
	Language          string
	Tone              string
	Model             string
	MaxClips          int
	SelectableClips   int
	TargetWords       int
	MaxCharsPerScene  int
	MinScore          float64
	Source            string
	MediaType         string
	Type              string
	Style             string
	StyleInstructions string
	ForceRefresh      bool
}

// CurateResult holds the output of a curation run.
type CurateResult struct {
	Title             string
	ClipScenes        []ClipScene
	Script            string
	WordCount         int
	CacheStatus       string
	AcceptedClipIDs   []string
	NarrativePlan     *NarrativePlan
	SourceText        string
	SourceFingerprint string
	SearchResults     []SearchResultInfo
	Timings           CurateTimings
}

// CurateTimings holds timing metrics for curation phases.
type CurateTimings struct {
	SearchMs      int64
	BuildCtxMs    int64
	WriteScriptMs int64
	TotalMs       int64
}

// SearchResultInfo holds a single search result.
type SearchResultInfo struct {
	ClipID    string
	Name      string
	Score     float64
	Source    string
	DriveLink string
}

// ClipScene represents a single scene with an associated clip.
type ClipScene struct {
	SceneIndex int
	Text       string
	ClipID     string
	DriveLink  string
	Kind       string
}

// NarrativePlan holds the narrative structure plan.
type NarrativePlan struct {
	Title        string             `json:"title"`
	Sections     []NarrativeSection `json:"sections"`
	TotalWords   int                `json:"total_words"`
	Style        string             `json:"style"`
	Relationship string             `json:"relationship"`
}

// NarrativeSection is one section of a narrative plan.
type NarrativeSection struct {
	Role       string `json:"role"`
	Purpose    string `json:"purpose"`
	WordBudget int    `json:"word_budget"`
	KeyPoints  string `json:"key_points"`
}

// WriteScriptResult holds the result of a script write operation.
type WriteScriptResult struct {
	Script      string
	WordCount   int
	Model       string
	Prompt      string
	CacheStatus string
	CacheHit    bool
	WasCached   bool
	EstDuration int
	ScriptID    int64
}

// JobPayloadCurate holds the payload for a curation job.
type JobPayloadCurate struct {
	Query             string   `json:"query"`
	Title             string   `json:"title"`
	Languages         []string `json:"languages,omitempty"`
	Language          string   `json:"language"`
	Tone              string   `json:"tone"`
	Model             string   `json:"model"`
	MaxClips          int      `json:"max_clips"`
	SelectableClips   int      `json:"selectable_clips"`
	TargetWords       int      `json:"target_words"`
	MaxCharsPerScene  int      `json:"max_chars_per_scene"`
	MinScore          float64  `json:"min_score"`
	Source            string   `json:"source"`
	MediaType         string   `json:"media_type"`
	Type              string   `json:"type"`
	Style             string   `json:"style"`
	StyleInstructions string   `json:"style_instructions"`
	ForceRefresh      bool     `json:"force_refresh"`
	GenerateVoiceover bool     `json:"generate_voiceover"`
	VoiceoverFolderID string   `json:"voiceover_folder_id"`
	VoiceoverGroup    string   `json:"voiceover_group"`
}

// JobPayloadCatalogScript holds the payload for catalog-first script generation.
type JobPayloadCatalogScript struct {
	Topic              string   `json:"topic"`
	ClipIDs            []string `json:"clip_ids"`
	Title              string   `json:"title"`
	OutputName         string   `json:"output_name"`
	MaxClips           int      `json:"max_clips"`
	MinCoverage        float64  `json:"min_coverage"`
	Languages          []string `json:"languages,omitempty"`
	Language           string   `json:"language"`
	Tone               string   `json:"tone"`
	Model              string   `json:"model"`
	TargetWords        int      `json:"target_words"`
	Duration           int      `json:"duration"`
	TranscriptPolicy   string   `json:"transcript_policy"`
	OrderingStrategy   string   `json:"ordering_strategy"`
	CreateDoc          bool     `json:"create_doc"`
	SaveToDB           bool     `json:"save_to_db"`
	GenerateTimeline   bool     `json:"generate_timeline"`
	ForceRefresh       bool     `json:"force_refresh"`
	MinQualityScore    *float64 `json:"min_quality_score,omitempty"`
	MinTranscriptWords *int     `json:"min_transcript_words,omitempty"`
}

// Curate is implemented in media_curator.go (real implementation).

// ── Batch types ─────────────────────────────────────────────────────────

// GenerateBatchRequest is the input for a batch generation request.
type GenerateBatchRequest struct {
	Async               bool                 `json:"async"`
	DocTitle            string               `json:"doc_title"`
	DriveFolderID       string               `json:"drive_folder_id"`
	Language            string               `json:"language"`
	Tone                string               `json:"tone"`
	Duration            int                  `json:"duration"`
	Model               string               `json:"model"`
	PromptVersion       string               `json:"prompt_version"`
	EditorPromptVersion string               `json:"editor_prompt_version"`
	QAPromptVersion     string               `json:"qa_prompt_version"`
	ChannelID           string               `json:"channel_id"`
	RequestTimeout      int                  `json:"request_timeout"`
	SaveToDB            bool                 `json:"save_to_db"`
	NoChapters          bool                 `json:"no_chapters"`
	Items               []GenerateBatchItem  `json:"items"`
	BatchTopics         []GenerateBatchTopic `json:"batch_topics"`
}

// GenerateBatchItem is one item in a batch request.
type GenerateBatchItem struct {
	Topic      string `json:"topic"`
	SourceText string `json:"source_text"`
}

// GenerateBatchTopic is one topic in a batch request.
type GenerateBatchTopic struct {
	Topic      string `json:"topic"`
	SourceText string `json:"source_text"`
}

// BatchGenerateResponse is the response from a batch generation.
type BatchGenerateResponse struct {
	Scripts  []BatchScriptResult `json:"scripts"`
	DocTitle string              `json:"doc_title"`
	DocID    string              `json:"doc_id"`
	DocLink  string              `json:"doc_link"`
}

// ToMap converts the response to a map for job results.
func (r BatchGenerateResponse) ToMap() map[string]any {
	return map[string]any{
		"ok":           true,
		"doc_title":    r.DocTitle,
		"doc_id":       r.DocID,
		"doc_link":     r.DocLink,
		"script_count": len(r.Scripts),
	}
}

// BatchScriptResult is one script result in a batch response.
type BatchScriptResult struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	WordCount int    `json:"word_count"`
	Language  string `json:"language"`
}

// BatchService orchestrates multi-section script generation with
// Google Doc output. PG-033: all service fields are concrete types
// (no interface{} slots).
type BatchService struct {
	cfg         *config.Config
	log         *zap.Logger
	gen         *ollama.Generator
	engine      *Engine
	docClient   drive.DocClient
	voSvc       *voiceover.Service
	scriptsRepo ScriptRepository
}

// Execute runs batch generation (real implementation in batch_service.go).
// ExecuteBatchGeneration runs batch generation (real implementation in batch_service.go).

// createBatchDoc creates a Google Doc from batch parts.
func (b *BatchService) createBatchDoc(ctx context.Context, title string, parts []GeneratedPart, noChapters bool, language, folderID string) (string, string) {
	if b == nil || b.docClient == nil {
		return "", ""
	}
	sectionTitles := make([]string, 0, len(parts))
	sectionContents := make([]string, 0, len(parts))
	for _, part := range parts {
		sectionTitles = append(sectionTitles, part.topic)
		sectionContents = append(sectionContents, part.content)
	}
	content := BuildSectionDocHTML(title, sectionTitles, sectionContents, noChapters, language)
	doc, err := b.docClient.CreateDoc(ctx, title, content, folderID)
	if err != nil || doc == nil || doc.URL == "" || doc.ID == "" {
		return "", ""
	}
	return doc.URL, doc.ID
}

// saveBatchScript persists a batch script.
func (b *BatchService) saveBatchScript(ctx context.Context, req *GenerateBatchRequest, rec *batchDBRecord, sources []ScriptResearchSource) int64 {
	if b == nil || req == nil || !req.SaveToDB || b.scriptsRepo == nil || rec == nil {
		return 0
	}
	repo := b.scriptsRepo

	title := strings.TrimSpace(req.DocTitle)
	if title == "" {
		title = strings.TrimSpace(rec.docTitle)
	}
	if title == "" {
		title = "Untitled script"
	}
	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = "en"
	}

	fullDoc := rec.mergedScript
	finalWordCount := countWords(fullDoc)
	if finalWordCount == 0 {
		for _, section := range rec.sections {
			finalWordCount += countWords(section.Content)
		}
	}
	if finalWordCount == 0 {
		finalWordCount = 1
	}

	version, err := repo.NextVersionForTopic(ctx, title, language, "batch")
	if err != nil || version <= 0 {
		version = 1
	}

	scriptRec := &ScriptRecord{
		Title:          title,
		Topic:          title,
		Language:       language,
		Tone:           strings.TrimSpace(req.Tone),
		Model:          strings.TrimSpace(req.Model),
		ModelUsed:      strings.TrimSpace(req.Model),
		Mode:           "batch",
		Status:         "completed",
		TargetWords:    rec.targetWords,
		FinalWordCount: finalWordCount,
		OutputText:     fullDoc,
		NarrativeText:  fullDoc,
		FullDocument:   fullDoc,
		Version:        version,
	}

	scriptID, err := repo.SaveScript(ctx, scriptRec, rec.sections, nil)
	if err != nil || scriptID <= 0 {
		return 0
	}

	if len(rec.outlineSections) > 0 {
		outlineSections := make([]ScriptOutlineSectionRecord, len(rec.outlineSections))
		for i, section := range rec.outlineSections {
			section.ScriptID = scriptID
			outlineSections[i] = section
		}
		if err := repo.SaveOutlineSections(ctx, scriptID, outlineSections); err != nil {
			return 0
		}
	}

	if len(sources) > 0 {
		persistedSources := make([]ScriptResearchSource, len(sources))
		for i, source := range sources {
			source.ScriptID = scriptID
			persistedSources[i] = source
		}
		if err := repo.SaveResearchSources(ctx, scriptID, persistedSources); err != nil {
			return 0
		}
	}

	for _, logEntry := range rec.generationLogs {
		logEntry.ScriptID = scriptID
		if err := repo.SaveGenerationLog(ctx, logEntry); err != nil {
			return 0
		}
	}

	return scriptID
}

// ── Engine and pipeline types ───────────────────────────────────────────

// WriteScriptRequest carries the inputs for WriteScript.
// PG-033: Plan is now *ScriptGenerationPlan (concrete) and ClipPack
// was removed — engine.WriteScript never read it; callers retained
// `pack` in local scope and used `summarizeClipPack(pack)` directly.
type WriteScriptRequest struct {
	Plan        *scriptpkg.ScriptGenerationPlan
	Topic       string
	Title       string
	Language    string
	Tone        string
	Model       string
	Mode        string
	SourceText  string
	MinWords    int
	Prompt      string
	UseMemory   bool
	SaveToDB    bool
	SaveTimeout int
}

// Engine is the canonical script generation engine backed by
// ollama.Generator, gemmamemory.Service, and ScriptRepository.
// PG-033: all fields are concrete types.
type Engine struct {
	ollamaGen *ollama.Generator
	memorySvc *gemmamemory.Service
	repo      ScriptRepository
	log       *zap.Logger
}

// WriteScript is implemented in engine.go (real implementation).

// ClipSourceBuilder builds clip context from explicit clip IDs
// using the clips repository and optional vector store + reranker.
// PG-033: clipsRepo is concrete *assets.ClipsRepository; the
// `ollamaClient` field was unused and REMOVED. vectorStore and
// reranker remain `interface{}` (multi-backend composition-time
// setters; see SetVectorStore / SetReranker).
type ClipSourceBuilder struct {
	clipsRepo   *assets.ClipsRepository
	vectorStore interface{}
	reranker    interface{}
	log         *zap.Logger
}

// SetVectorStore and SetReranker are implemented in clip_source_builder.go.

// ClipGenerationOptions carries options for clip generation.
type ClipGenerationOptions struct {
	Language           string
	Tone               string
	Title              string
	Model              string
	TargetWords        int
	SourceText         string
	TranscriptPolicy   string
	OrderingStrategy   string
	StyleInstructions  string
	MinQualityScore    float64
	MinTranscriptWords int
}

// BuildClipContext is implemented in clip_source_builder.go (real implementation).
// ComputeFingerprint is implemented in clip_source_builder.go (real implementation).
// NewFingerprintContext is implemented in clip_source_builder.go (real implementation).

// PipelineResult holds the output of Pipeline.Run.
type PipelineResult struct {
	EntitiesJSON  string
	Insights      ScriptInsights
	VideoMetadata []VideoMetadata
	DocLink       string
	DocID         string
	Scenes        []SceneImage
	Voiceovers    []SceneVoiceover
}

// SceneImage represents a scene with an image.
type SceneImage struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
	URL   string `json:"url"`
}

// SceneVoiceover represents a scene with a voiceover.
type SceneVoiceover struct {
	SceneIndex int    `json:"scene_index"`
	Status     string `json:"status"`
	Link       string `json:"link"`
	LocalPath  string `json:"local_path"`
}

// Pipeline executes the post-generation phases (entity extraction,
// scene images, voiceovers, doc creation). All fields are concrete typed.
type Pipeline struct {
	log           *zap.Logger
	tag           string
	scenesSvc     *ScenesService
	docsSvc       *DocumentsService
	postGen       PostGenFunc
	resolveFolder FolderResolver
}

// Run is implemented in pipeline_impl.go (real implementation).

// ── Documents types ─────────────────────────────────────────────────────

// DocumentsService creates Google Docs from script content.
// PG-033: docClient is concrete drive.DocClient (no interface{} slot).
type DocumentsService struct {
	docClient       drive.DocClient
	log             *zap.Logger
	defaultFolderID string
}

// NewDocumentsService creates a new DocumentsService with concrete
// typed arguments. Returns nil docClient → CreateDoc short-circuits.
func NewDocumentsService(docClient drive.DocClient, log *zap.Logger, driveFolderID string) *DocumentsService {
	return &DocumentsService{
		docClient:       docClient,
		log:             log,
		defaultFolderID: driveFolderID,
	}
}

// CreateDoc creates a Google Doc.
func (d *DocumentsService) CreateDoc(ctx context.Context, title, content string, resolveFolder FolderResolver, driveFolderID string) (docLink, docID string) {
	if d == nil {
		return "", ""
	}
	if d.docClient == nil {
		return "", ""
	}
	folderID := strings.TrimSpace(driveFolderID)
	if resolveFolder != nil && folderID != "" {
		if resolved, err := resolveFolder(ctx, folderID, d.defaultFolderID); err == nil && strings.TrimSpace(resolved) != "" {
			folderID = resolved
		}
	}
	doc, err := d.docClient.CreateDoc(ctx, title, content, folderID)
	if err != nil || doc == nil || strings.TrimSpace(doc.URL) == "" || strings.TrimSpace(doc.ID) == "" {
		return "", ""
	}
	return doc.URL, doc.ID
}

// ── VideoMetadata ───────────────────────────────────────────────────────

// VideoMetadata holds YouTube metadata for a single language.
type VideoMetadata struct {
	Language    string   `json:"language"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// ── ScenesService ───────────────────────────────────────────────────────

// ScenesService handles scene image/voiceover generation during
// the post-generation pipeline phase.
// PG-033: every service field is concrete typed.
type ScenesService struct {
	imgSvc        *images.Service
	voSvc         *voiceover.Service
	log           *zap.Logger
	cfg           *config.Config
	resolveFolder FolderResolver
	groupsRes     *voiceover.GroupsResolver
	albumCapacity int
}

// NewScenesService is implemented in scenes_service.go (real implementation).

// ── Default prompt version constants ────────────────────────────────────

const (
	DefaultBookPromptVersion       = "v1"
	DefaultBookEditorPromptVersion = "v1"
	DefaultBookQAPromptVersion     = "v1"
	DefaultTextPromptVersion       = "v1"
	DefaultTextEditorPromptVersion = "v1"
	DefaultTextQAPromptVersion     = "v1"
)

// ── ClipServices ────────────────────────────────────────────────────────

// ClipServices bundles all service dependencies for clip-related functions.
//
// PG-033 (June 2026): removed dead shim interfaces (`AssociationService`,
// `ImageSearchService`, `JobEnqueueService`, `VoiceoverService`) and the
// duplicate `Harvest` field. The remaining slots are typed:
//
//   - HarvestSvc    : HarvestService      (signature tightened to concrete types)
//   - JobsSvc       : JobEnqueuer         (canonical typed port from ports.go;
//                                        *job.Service / *appjobs.Service, façade)
//   - ImgSvc        : ImageGenService     (signature tightened to match *images.Service
//                                        so composition root can bind directly)
//
// All `interface{}` slots inside the ClipServices surface are gone.
type ClipServices struct {
	ClipSearch    ClipSearchService
	DriveCheck    DriveCheckService
	Translation   TextTranslationService
	RealtimeSvc   RealtimeSearchService
	HarvestSvc    HarvestService
	Logger        *zap.Logger
	Translator    TranslatorService
	MetadataModel string
	AssocSvc      AssocSearchService
	DriveSvc      DriveCheckService
	JobsSvc       JobEnqueuer
	ArtlistFolder string
	ImgSvc        ImageGenService
}

// ClipSearchService narrows clip search operations.
type ClipSearchService interface {
	EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error)
}

// DriveCheckService narrows drive check operations.
type DriveCheckService interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// TextTranslationService narrows text translation operations.
type TextTranslationService interface {
	Translate(ctx context.Context, text, targetLang string) (string, error)
}

// HarvestService narrows harvest operations.
// PG-033: signature tightened to the canonical typed shape matching
// `internal/api/script/handler_flow.go:76`. The previous `interface{}`
// sliding-door was structurally incompatible with the (forthcoming)
// clipresolver replacement; the concrete signature is forward-compatible
// regardless of who implements it.
type HarvestService interface {
	EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error)
}

// RealtimeSearchService narrows realtime search operations.
//
// NOTE: the `RealtimeMatchAsset` element type is defined canonically in
// `internal/application/scripts/flow_helpers.go:31`. The earlier draft
// duplicated the definition here and tripped a Go redeclaration error;
// the canonical location is kept and this stub stays as a pure
// interface contract shim.
type RealtimeSearchService interface {
	SearchClips(ctx context.Context, query, source, mediaType string, limit int, minScore float64) ([]RealtimeMatchAsset, error)
}

// TranslatorService narrows translator operations with model support.
type TranslatorService interface {
	TranslateTextWithModel(ctx context.Context, text, lang, model string) (string, error)
}

// AssocSearchService narrows association search operations with typed request/response.
type AssocSearchService interface {
	BuildCandidates(ctx context.Context, req AssociationCandidatesRequest) (*AssociationCandidatesResponse, error)
}

// ImageGenService narrows image search + generation operations.
// PG-033: SearchAndDownload signature tightened to match *images.Service
// (production: subjectSlug, displayName, query, lang, tags []string),
// so *images.Service satisfies the interface directly without a
// structural wrapper. GenerateSmartImage is unchanged (already typed).
type ImageGenService interface {
	SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error)
	GenerateSmartImage(ctx context.Context, name, description, style string, prompts, tags []string, width, height int, extra string, flag bool) (*asset.ImageAsset, error)
}

// ── ScriptInsights ──────────────────────────────────────────────────────

// ScriptInsights holds entity and media suggestions extracted from a script.
type ScriptInsights struct {
	ImportantWords         []string
	ImportantPhrases       []string
	SpecialNames           []string
	ArtlistPhrases         []string
	ArtlistClipSuggestions interface{}
	EntityImages           interface{}
	RecommendedDriveFolder interface{}
	PhraseClipSuggestions  interface{}
	IntroClips             interface{}
}

// ── Helper functions ────────────────────────────────────────────────────

// BuildScenesWithMarkers is implemented in scenes_service.go (real implementation).

// SupportedScriptLanguages returns the list of supported script languages.
func SupportedScriptLanguages(translateLanguages []string, sourceLang string) []string {
	langs := []string{}
	if sourceLang != "" {
		langs = append(langs, sourceLang)
	}
	for _, l := range translateLanguages {
		found := false
		for _, existing := range langs {
			if existing == l {
				found = true
				break
			}
		}
		if !found {
			langs = append(langs, l)
		}
	}
	if len(langs) == 0 {
		langs = []string{"en", "it"}
	}
	return langs
}

// NormalizeLanguages trims, deduplicates, and preserves order for a language list.
func NormalizeLanguages(languages []string) []string {
	out := make([]string, 0, len(languages))
	seen := make(map[string]struct{}, len(languages))
	for _, lang := range languages {
		lang = strings.TrimSpace(lang)
		if lang == "" {
			continue
		}
		if _, ok := seen[lang]; ok {
			continue
		}
		seen[lang] = struct{}{}
		out = append(out, lang)
	}
	return out
}

// GeneratedPart holds a single generated part in a batch.
type GeneratedPart struct {
	topic   string
	content string
}

// ValidateGenerateBatchRequest validates a batch request (stub).
func ValidateGenerateBatchRequest(req *GenerateBatchRequest, folderID string, supportedLanguages []string) []string {
	return nil
}

// ── FromClipsResult ─────────────────────────────────────────────────────

// FromClipsResult holds the result of an enqueue-from-clips operation.
type FromClipsResult struct {
	OK        bool   `json:"ok"`
	JobID     string `json:"job_id"`
	JobStatus string `json:"job_status"`
}

// ── batchDBRecord ───────────────────────────────────────────────────────

// batchDBRecord holds the data for batch script persistence.
type batchDBRecord struct {
	docTitle        string
	mergedScript    string
	sections        []ScriptSectionRecord
	outlineSections []ScriptOutlineSectionRecord
	generationLogs  []ScriptGenerationLog
	targetWords     int
	noChapters      bool
}
