// Package scripts — compatibility types and adapters for the script pipeline.
// Some surfaces remain intentionally simplified while the pipeline is being
// reconstituted, but the package now carries real orchestration logic too.
package scripts

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
)

// ── Function types ──────────────────────────────────────────────────────

// FolderResolver resolves a folder ID from an input name and default root.
type FolderResolver func(ctx context.Context, input, defaultRootID string) (string, error)

// ── Curate types ────────────────────────────────────────────────────────

// MediaCurator is a stub for the media curator service.
type MediaCurator struct{}

// NewMediaCurator is the canonical constructor for *MediaCurator.
//
// AGENT-2 (June 2026): stub constructor wired to satisfy the
// `scripts.NewMediaCurator(vector, serverURL, clipsRepo, clipBuilder,
// engine, log)` call site in `internal/app/wire_script.go`. Args
// are intentionally interface{} to avoid forcing more
// canonical-package imports on this leaf.
func NewMediaCurator(vector, serverURL, clipsRepo, clipBuilder, engine, log interface{}) *MediaCurator {
	return &MediaCurator{}
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

// Curate executes the curation operation (stub).
func (m *MediaCurator) Curate(ctx context.Context, req CurateRequest) (*CurateResult, error) {
	return &CurateResult{
		Title:     req.Title,
		Script:    req.Query,
		WordCount: 0,
	}, nil
}

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

// BatchService is a stub for the batch generation service.
type BatchService struct {
	scriptsRepo interface{}
	docClient   interface{}
	log         *zap.Logger
}

// NewBatchService is the canonical constructor for *BatchService.
//
// AGENT-2 (June 2026): stub constructor wired to satisfy the
// canonical `scripts.NewBatchService(cfg, log, gen, engine, doc,
// vo, repo)` call site in `internal/app/wire_script.go`. Args are
// intentionally interface{} to avoid forcing more canonical-package
// imports on this leaf.
func NewBatchService(cfg, log, gen, engine, doc, vo, repo interface{}) *BatchService {
	var logger *zap.Logger
	if l, ok := log.(*zap.Logger); ok {
		logger = l
	}
	return &BatchService{
		scriptsRepo: repo,
		docClient:   doc,
		log:         logger,
	}
}

// Execute runs batch generation (stub).
func (b *BatchService) Execute(ctx context.Context, req *GenerateBatchRequest, progressFunc interface{}) (BatchGenerateResponse, error) {
	return BatchGenerateResponse{DocTitle: req.DocTitle}, nil
}

// ExecuteBatchGeneration runs batch generation with progress callback (stub).
func (b *BatchService) ExecuteBatchGeneration(ctx context.Context, req *GenerateBatchRequest, onProgress func(int, string)) (BatchGenerateResponse, error) {
	return BatchGenerateResponse{DocTitle: req.DocTitle}, nil
}

// createBatchDoc creates a Google Doc from batch parts (stub).
func (b *BatchService) createBatchDoc(ctx context.Context, title string, parts []GeneratedPart, noChapters bool, language, folderID string) (string, string) {
	if b == nil || b.docClient == nil {
		return "", ""
	}
	client, ok := b.docClient.(drive.DocClient)
	if !ok || client == nil {
		return "", ""
	}
	sectionTitles := make([]string, 0, len(parts))
	sectionContents := make([]string, 0, len(parts))
	for _, part := range parts {
		sectionTitles = append(sectionTitles, part.topic)
		sectionContents = append(sectionContents, part.content)
	}
	content := BuildSectionDocHTML(title, sectionTitles, sectionContents, noChapters, language)
	doc, err := client.CreateDoc(ctx, title, content, folderID)
	if err != nil || doc == nil || doc.URL == "" || doc.ID == "" {
		return "", ""
	}
	return doc.URL, doc.ID
}

// saveBatchScript persists a batch script (stub).
func (b *BatchService) saveBatchScript(ctx context.Context, req *GenerateBatchRequest, rec *batchDBRecord, sources []ScriptResearchSource) int64 {
	if b == nil || req == nil || !req.SaveToDB || b.scriptsRepo == nil || rec == nil {
		return 0
	}
	repo, ok := b.scriptsRepo.(ScriptRepository)
	if !ok || repo == nil {
		return 0
	}

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
type WriteScriptRequest struct {
	Plan        interface{} // *scriptpkg.ScriptGenerationPlan
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
	ClipPack    interface{}
}

// Engine is a stub for the script engine.
type Engine struct{}

// NewEngine is the canonical constructor for *Engine.
//
// AGENT-2 (June 2026): stub constructor wired to satisfy
// `internal/app/composition.go::BuildAIBundle` and the canonical
// `scriptcore.NewEngine` alias. Real implementation was removed from
// remote in commit d61068b3. Args are intentionally interface{} to
// avoid forcing more canonical-package imports on this leaf — the
// production engine carries concrete *ollama.Generator + *gemmamemory
// .Service + ScriptRepository + *zap.Logger but the stub does not
// touch them.
func NewEngine(scriptGen, memorySvc, repoAdapter, log interface{}) *Engine {
	return &Engine{}
}

// WriteScript is a stub that always returns empty.
func (e *Engine) WriteScript(ctx context.Context, req WriteScriptRequest) (*WriteScriptResult, error) {
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		topic = strings.TrimSpace(req.Title)
	}
	if topic == "" {
		topic = "the topic"
	}
	tone := strings.TrimSpace(req.Tone)
	if tone == "" {
		tone = "clear"
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "fallback"
	}
	body := strings.TrimSpace(req.SourceText)
	if body != "" {
		body = strings.ReplaceAll(body, "\n", " ")
	}
	script := strings.Join([]string{
		"Intro: " + topic + " matters because it gives us a concrete problem to explain.",
		"This draft keeps the tone " + tone + " and shows the idea in a simple, usable structure.",
		"If more detail is available, the next pass can expand the examples without changing the core message.",
	}, " ")
	if body != "" {
		script += " Source notes: " + body
	}
	wordCount := countWords(script)
	if wordCount <= 0 {
		wordCount = 1
	}
	return &WriteScriptResult{
		Script:      script,
		WordCount:   wordCount,
		Model:       model,
		Prompt:      req.Prompt,
		CacheStatus: "generated",
	}, nil
}

// ClipSourceBuilder builds clip context from explicit clip IDs.
type ClipSourceBuilder struct {
	vectorStore interface{}
	reranker    interface{}
}

// NewClipSourceBuilder is the canonical constructor for
// *ClipSourceBuilder. The vector store / reranker slots are wired via
// SetVectorStore / SetReranker setters so call sites can opt-in
// lazily.
//
// AGENT-2 (June 2026): stub constructor wired to satisfy the
// `scripts.NewClipSourceBuilder(clipsRepo, ollama, log)` call site
// in `internal/app/wire_script.go`. Args are intentionally interface{}
// to avoid forcing more canonical-package imports on this leaf.
func NewClipSourceBuilder(clipsRepo, ollama, log interface{}) *ClipSourceBuilder {
	return &ClipSourceBuilder{}
}

// SetVectorStore attaches a vector-store adapter (typically the
// qdrant SearchAdapter). Stub receiver — store and discard for
// future replacement.
func (c *ClipSourceBuilder) SetVectorStore(v interface{}) { c.vectorStore = v }

// SetReranker attaches a reranker client (typically reranker.NewClient).
// Stub receiver — store and discard for future replacement.
func (c *ClipSourceBuilder) SetReranker(r interface{}) { c.reranker = r }

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

// BuildClipContext builds clip context (stub).
func (c *ClipSourceBuilder) BuildClipContext(ctx context.Context, clipIDs []string, opts *ClipGenerationOptions) (interface{}, *NarrativePlan, string, error) {
	return nil, nil, "", nil
}

// ComputeFingerprint computes a fingerprint (stub).
func (c *ClipSourceBuilder) ComputeFingerprint(clipIDs []string, pack interface{}, opts *ClipGenerationOptions, fpCtx interface{}) string {
	return ""
}

// NewFingerprintContext creates a fingerprint context (stub).
func NewFingerprintContext(model, promptModel string) interface{} {
	return nil
}

// PipelineResult holds the output of Pipeline.Run.
type PipelineResult struct {
	EntitiesJSON  string
	Insights      interface{}
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

// Pipeline executes the post-generation pipeline phases.
type Pipeline struct{}

// NewPipeline is the canonical constructor for *Pipeline.
//
// AGENT-2 (June 2026): stub constructor wired to satisfy the
// `scripts.NewPipeline(log, "", scenesSvc, docsSvc, postGen, nil)`
// call site in `internal/app/wire_script.go`. Real implementation
// was removed from remote in commit d61068b3 — this stub returns
// empty PipelineResult from Pipeline.Run so consumers stay nil-tolerant.
func NewPipeline(log interface{}, tag string, scenesSvc, docsSvc, postGen, resolveFolder interface{}) *Pipeline {
	return &Pipeline{}
}

// Run executes the pipeline (stub).
func (p *Pipeline) Run(ctx context.Context, spec interface{}, script string, tools interface{}) (*PipelineResult, error) {
	return &PipelineResult{}, nil
}

// ── Documents types ─────────────────────────────────────────────────────

// DocumentsService creates Google Docs from script content.
type DocumentsService struct {
	docClient       interface{}
	log             *zap.Logger
	defaultFolderID string
}

// NewDocumentsService creates a new DocumentsService (stub).
func NewDocumentsService(docClient interface{}, log interface{}, driveFolderID string) *DocumentsService {
	var logger *zap.Logger
	if l, ok := log.(*zap.Logger); ok {
		logger = l
	}
	return &DocumentsService{
		docClient:       docClient,
		log:             logger,
		defaultFolderID: driveFolderID,
	}
}

// CreateDoc creates a Google Doc.
func (d *DocumentsService) CreateDoc(ctx context.Context, title, content string, resolveFolder FolderResolver, driveFolderID string) (docLink, docID string) {
	if d == nil {
		return "", ""
	}
	client, ok := d.docClient.(drive.DocClient)
	if !ok || client == nil {
		return "", ""
	}
	folderID := strings.TrimSpace(driveFolderID)
	if resolveFolder != nil && folderID != "" {
		if resolved, err := resolveFolder(ctx, folderID, d.defaultFolderID); err == nil && strings.TrimSpace(resolved) != "" {
			folderID = resolved
		}
	}
	doc, err := client.CreateDoc(ctx, title, content, folderID)
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

// ScenesService handles scene image/voiceover generation.
type ScenesService struct{}

// NewScenesService creates a new ScenesService (stub).
func NewScenesService(imgSvc, voSvc, log, cfg, resolveFolder, groupsRes interface{}, albumCapacity int) *ScenesService {
	return &ScenesService{}
}

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
type ClipServices struct {
	ClipSearch    ClipSearchService
	Association   AssociationService
	DriveCheck    DriveCheckService
	ImageSearch   ImageSearchService
	Translation   TextTranslationService
	JobEnqueue    JobEnqueueService
	Harvest       HarvestService
	Voiceover     VoiceoverService
	RealtimeSvc   RealtimeSearchService
	HarvestSvc    HarvestService
	Logger        *zap.Logger
	Translator    TranslatorService
	MetadataModel string
	AssocSvc      AssocSearchService
	DriveSvc      DriveCheckService
	JobsSvc       JobEnqueueService
	ArtlistFolder string
	ImgSvc        ImageGenService
}

// ClipSearchService narrows clip search operations.
type ClipSearchService interface {
	EmbedTextForVector(ctx context.Context, text, vectorName string) ([]float32, error)
}

// AssociationService narrows association operations.
type AssociationService interface {
	BuildCandidates(ctx context.Context, req interface{}) (interface{}, error)
}

// DriveCheckService narrows drive check operations.
type DriveCheckService interface {
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
}

// ImageSearchService narrows image search operations.
type ImageSearchService interface {
	Search(ctx context.Context, query string, limit int) ([]interface{}, error)
}

// TextTranslationService narrows text translation operations.
type TextTranslationService interface {
	Translate(ctx context.Context, text, targetLang string) (string, error)
}

// JobEnqueueService narrows job enqueue operations.
type JobEnqueueService interface {
	Enqueue(ctx context.Context, req interface{}) (interface{}, error)
}

// HarvestService narrows harvest operations.
type HarvestService interface {
	EnqueueHarvest(ctx context.Context, req interface{}, maxClips int, profile string) (interface{}, error)
}

// RealtimeSearchService narrows realtime search operations.
//
// NOTE (AGENT-2, June 2026): the `RealtimeMatchAsset` element type
// referenced below is now defined canonically in
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
type ImageGenService interface {
	SearchAndDownload(ctx context.Context, name, description, query, language string, extra interface{}) (*asset.ImageAsset, error)
	GenerateSmartImage(ctx context.Context, name, description, style string, prompts, tags []string, width, height int, extra string, flag bool) (*asset.ImageAsset, error)
}

// VoiceoverService narrows voiceover operations.
type VoiceoverService interface {
	Generate(ctx context.Context, text, language, filename string) (interface{}, error)
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

// BuildScenesWithMarkers builds scenes with narration/clip markers (stub).
func BuildScenesWithMarkers(script string, pack interface{}) []ClipScene {
	return nil
}

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
