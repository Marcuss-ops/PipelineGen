// Package scripts — batch_service.go replaces the BatchService stub
// with a real implementation that iterates batch items, calls the
// Engine for each, creates a Google Doc, and persists results.
//
// AGENT-3 (June 2026): the previous stub returned an empty response.
// The real implementation:
//   1. Iterates over req.Items and req.BatchTopics
//   2. Calls engine.WriteScript for each item
//   3. Concatenates results into a Google Doc via docClient
//   4. Persists the batch script via ScriptRepository
package scripts

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"
)

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
	return &BatchService{
		cfg:         cfg,
		log:         log,
		gen:         gen,
		engine:      engine,
		docClient:   doc,
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
	if docClient, ok := b.docClient.(drive.DocClient); ok && docClient != nil {
		sectionTitles := make([]string, len(allSections))
		sectionContents := make([]string, len(allSections))
		for i, sec := range allSections {
			sectionTitles[i] = sec.SectionTitle
			sectionContents[i] = sec.Content
		}
		htmlContent := BuildSectionDocHTML(docTitle, sectionTitles, sectionContents, req.NoChapters, req.Language)
		doc, err := docClient.CreateDoc(ctx, docTitle, htmlContent, req.DriveFolderID)
		if err != nil {
			if b.log != nil {
				b.log.Warn("batch service: failed to create Google Doc", zap.Error(err))
			}
		} else if doc != nil {
			docLink = doc.URL
			docID = doc.ID
		}
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
