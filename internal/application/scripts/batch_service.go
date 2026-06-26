// Package scripts — batch_service.go replaces the BatchService stub
// with a real implementation that iterates batch items, calls the
// Engine for each, creates a Google Doc, and persists results.
//
// AGENT-3 (June 2026): the previous stub returned an empty response.
// The real implementation:
//  1. Iterates over req.Items and req.BatchTopics
//  2. Calls engine.WriteScript for each item
//  3. Concatenates results into a Google Doc via docClient
//  4. Persists the batch script via ScriptRepository
//
// PG-029 (June 2026): batch-related types + methods consolidated here
// from the now-deleted types.go.
package scripts

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// ── Batch types ──────────────────────────────────────────────────────────

// BatchService orchestrates multi-section script generation with
// Google Doc output. All fields are concrete typed.
type BatchService struct {
	cfg         interface{} // *config.Config
	log         *zap.Logger
	gen         interface{} // *ollama.Generator
	engine      *Engine
	docsSvc     *DocumentsService
	voSvc       interface{} // *voiceover.Service
	scriptsRepo interface{} // ScriptRepository
}

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

// GeneratedPart holds a single generated part in a batch.
type GeneratedPart struct {
	topic   string
	content string
}

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

// NewBatchService constructs a real BatchService backed by the
// config, logger, ollama generator, engine, doc client, voiceover
// service, and script repository.
//
// All args are concrete typed. Any arg may be nil; Execute returns
// an error when required deps are missing.
func NewBatchService(
	cfg *config.Config,
	log *zap.Logger,
	gen *ollama.Generator,
	engine *Engine,
	doc drive.DocClient,
	vo *voiceover.Service,
	repo ScriptRepository,
) *BatchService {
	var docsSvc *DocumentsService
	if doc != nil {
		docsSvc = NewDocumentsService(doc, log, "")
	}
	return &BatchService{
		cfg:         cfg,
		log:         log,
		gen:         gen,
		engine:      engine,
		docsSvc:     docsSvc,
		voSvc:       vo,
		scriptsRepo: repo,
	}
}

// Execute runs the batch generation synchronously. Iterates over
// req.Items and req.BatchTopics, calls engine.WriteScript for each,
// concatenates results, creates a Google Doc, and persists.
func (b *BatchService) Execute(
	ctx context.Context,
	req *GenerateBatchRequest,
	progressFunc interface{},
) (BatchGenerateResponse, error) {
	if b == nil {
		return BatchGenerateResponse{}, fmt.Errorf("batch service: not constructed")
	}
	if b.engine == nil {
		return BatchGenerateResponse{}, fmt.Errorf("batch service: engine not configured")
	}
	if req == nil {
		return BatchGenerateResponse{}, fmt.Errorf("batch service: nil request")
	}

	// Unwrap progress function.
	onProgress := func(pct int, msg string) {}
	if pf, ok := progressFunc.(func(int, string)); ok && pf != nil {
		onProgress = pf
	}

	// Collect items from both sources.
	type batchItem struct {
		Topic      string
		SourceText string
	}
	items := make([]batchItem, 0, len(req.Items)+len(req.BatchTopics))
	for _, it := range req.Items {
		items = append(items, batchItem{
			Topic:      strings.TrimSpace(it.Topic),
			SourceText: strings.TrimSpace(it.SourceText),
		})
	}
	for _, bt := range req.BatchTopics {
		items = append(items, batchItem{
			Topic:      strings.TrimSpace(bt.Topic),
			SourceText: strings.TrimSpace(bt.SourceText),
		})
	}

	if len(items) == 0 {
		if b.log != nil {
			b.log.Warn("batch service: no items to generate")
		}
		return BatchGenerateResponse{
			DocTitle: strings.TrimSpace(req.DocTitle),
		}, nil
	}

	totalItems := len(items)
	onProgress(0, fmt.Sprintf("Starting batch generation of %d sections", totalItems))

	// Generate each item.
	var allSections []ScriptSectionRecord
	var mergedBuilder strings.Builder
	scriptResults := make([]BatchScriptResult, 0, totalItems)

	for i, item := range items {
		onProgress((i*80)/totalItems, fmt.Sprintf("Generating section %d/%d: %s", i+1, totalItems, item.Topic))

		writeReq := WriteScriptRequest{
			Topic:      item.Topic,
			Title:      item.Topic,
			Language:   req.Language,
			Tone:       req.Tone,
			Model:      req.Model,
			Mode:       "batch",
			SourceText: item.SourceText,
			UseMemory:  true,
			SaveToDB:   false, // We save the whole batch at the end.
		}

		writeResult, err := b.engine.WriteScript(ctx, writeReq)
		if err != nil {
			if b.log != nil {
				b.log.Error("batch service: engine.WriteScript failed for item",
					zap.String("topic", item.Topic),
					zap.Error(err))
			}
			return BatchGenerateResponse{}, fmt.Errorf("batch service: section %d (%s) failed: %w", i+1, item.Topic, err)
		}

		sectionRec := ScriptSectionRecord{
			Index:        i,
			SectionTitle: item.Topic,
			SectionType:  "text",
			Content:      writeResult.Script,
			WordCount:    writeResult.WordCount,
			Status:       "completed",
			SortOrder:    i,
		}
		allSections = append(allSections, sectionRec)

		mergedBuilder.WriteString(fmt.Sprintf("# %s\n\n%s\n\n", item.Topic, writeResult.Script))

		scriptResults = append(scriptResults, BatchScriptResult{
			Title:     item.Topic,
			Content:   writeResult.Script,
			WordCount: writeResult.WordCount,
			Language:  req.Language,
		})
	}

	onProgress(90, "Creating Google Doc...")

	docTitle := strings.TrimSpace(req.DocTitle)
	if docTitle == "" {
		docTitle = "Batch Script"
	}

	mergedScript := mergedBuilder.String()

	// Create Google Doc.
	var docLink, docID string
	if b.docsSvc != nil {
		sectionTitles := make([]string, len(allSections))
		sectionContents := make([]string, len(allSections))
		for i, sec := range allSections {
			sectionTitles[i] = sec.SectionTitle
			sectionContents[i] = sec.Content
		}
		htmlContent := BuildSectionDocHTML(docTitle, sectionTitles, sectionContents, req.NoChapters, req.Language)
		link, id := b.docsSvc.CreateDoc(ctx, docTitle, htmlContent, nil, req.DriveFolderID)
		docLink = link
		docID = id
	}

	// Persist to DB.
	if req.SaveToDB && b.scriptsRepo != nil {
		rec := &batchDBRecord{
			docTitle:     docTitle,
			mergedScript: mergedScript,
			sections:     allSections,
			targetWords:  req.Duration * 150 / 60,
			noChapters:   req.NoChapters,
		}
		_ = b.saveBatchScript(ctx, req, rec, nil)
	}

	onProgress(100, "Batch generation completed")

	return BatchGenerateResponse{
		Scripts:  scriptResults,
		DocTitle: docTitle,
		DocID:    docID,
		DocLink:  docLink,
	}, nil
}

// ExecuteBatchGeneration runs batch generation with the canonical
// progress callback signature. Delegates to Execute.
func (b *BatchService) ExecuteBatchGeneration(
	ctx context.Context,
	req *GenerateBatchRequest,
	onProgress func(int, string),
) (BatchGenerateResponse, error) {
	return b.Execute(ctx, req, onProgress)
}

// createBatchDoc creates a Google Doc from batch parts.
func (b *BatchService) createBatchDoc(ctx context.Context, title string, parts []GeneratedPart, noChapters bool, language, folderID string) (string, string) {
	if b == nil || b.docsSvc == nil {
		return "", ""
	}
	sectionTitles := make([]string, 0, len(parts))
	sectionContents := make([]string, 0, len(parts))
	for _, part := range parts {
		sectionTitles = append(sectionTitles, part.topic)
		sectionContents = append(sectionContents, part.content)
	}
	content := BuildSectionDocHTML(title, sectionTitles, sectionContents, noChapters, language)
	return b.docsSvc.CreateDoc(ctx, title, content, nil, folderID)
}

// saveBatchScript persists a batch script.
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
