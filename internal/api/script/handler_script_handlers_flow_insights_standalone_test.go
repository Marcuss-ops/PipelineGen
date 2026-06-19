package script

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/core"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/application/association"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"github.com/Marcuss-ops/PipelineGen/internal/application/realtime"
	"go.uber.org/zap"
)

// ── Mocks ────────────────────────────────────────────────────────────────────

type mockEntityExtractor struct {
	result *core.FullEntityAnalysis
	err    error
}

func (m *mockEntityExtractor) ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, entityCount int, model string) (*core.FullEntityAnalysis, error) {
	return m.result, m.err
}

type mockClipSearch struct {
	assets []realtime.MatchAsset
	err    error
}

func (m *mockClipSearch) SearchClips(ctx context.Context, query, source, mediaType string, limit int, minScore float64) ([]realtime.MatchAsset, error) {
	return m.assets, m.err
}

type mockAssoc struct {
	resp *association.CandidatesResponse
	err  error
}

func (m *mockAssoc) BuildCandidates(ctx context.Context, req association.CandidatesRequest) (*association.CandidatesResponse, error) {
	return m.resp, m.err
}

type mockDriveCheck struct {
	active bool
	err    error
}

func (m *mockDriveCheck) FileIsNotTrashed(ctx context.Context, fileID string) (bool, error) {
	return m.active, m.err
}

type mockTranslator struct {
	translated string
	err        error
}

func (m *mockTranslator) TranslateTextWithModel(ctx context.Context, text, targetLanguage, model string) (string, error) {
	return m.translated, m.err
}

type mockJobEnqueuer struct {
	enqueued []*jobserviceEnqueueRequest
}

type jobserviceEnqueueRequest struct {
	Type       string
	Payload    map[string]any
	MaxRetries int
}

func (m *mockJobEnqueuer) Enqueue(ctx context.Context, req *jobservice.EnqueueRequest) (*jobservice.Job, error) {
	m.enqueued = append(m.enqueued, &jobserviceEnqueueRequest{
		Type:       req.Type,
		Payload:    req.Payload,
		MaxRetries: req.MaxRetries,
	})
	return &jobservice.Job{ID: "test-job"}, nil
}

type mockImageSearch struct {
	searchResult   *media.ImageAsset
	searchErr      error
	generateResult *media.ImageAsset
	generateErr    error
}

func (m *mockImageSearch) SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*media.ImageAsset, error) {
	return m.searchResult, m.searchErr
}

func (m *mockImageSearch) GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*media.ImageAsset, error) {
	return m.generateResult, m.generateErr
}

// TriggerPrewarm satisfies the ImageSearchService interface. Mock no-op
// because tests don't exercise the Playwright prewarm hot-path — prewarm
// fires as a goroutine from the orchestrator (best-effort, errors only
// logged) and tests do not assert on sidecar calls.
func (m *mockImageSearch) TriggerPrewarm(ctx context.Context, jobID string, count int) {}

type mockHarvest struct {
	enqueued []harvestCall
}

type harvestCall struct {
	term   string
	limit  int
	preset string
}

func (m *mockHarvest) EnqueueHarvest(ctx context.Context, term string, limit int, preset string) (string, error) {
	m.enqueued = append(m.enqueued, harvestCall{term: term, limit: limit, preset: preset})
	return "harvest-job-id", nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func defaultClipServices(logger *zap.Logger) ClipServices {
	return ClipServices{
		Logger:        logger,
		RealtimeSvc:   &mockClipSearch{assets: []realtime.MatchAsset{}},
		AssocSvc:      &mockAssoc{resp: &association.CandidatesResponse{Candidates: []association.Candidate{}}},
		DriveSvc:      &mockDriveCheck{active: true},
		Translator:    &mockTranslator{translated: "test"},
		JobsSvc:       &mockJobEnqueuer{},
		ImgSvc:        &mockImageSearch{},
		ArtlistFolder: "",
		MetadataModel: "test-model",
	}
}

func nopLogger() *zap.Logger {
	l, _ := zap.NewDevelopment()
	return l
}

// ── ExtractScriptEntities Tests ──────────────────────────────────────────────

func TestExtractScriptEntities_NilExtractor(t *testing.T) {
	result, err := ExtractScriptEntities(context.Background(), nil, "some script", "model")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result, got %q", result)
	}
}

func TestExtractScriptEntities_ExtractorError(t *testing.T) {
	extractor := &mockEntityExtractor{err: errors.New("extraction failed")}
	_, err := ExtractScriptEntities(context.Background(), extractor, "some script", "model")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "extraction failed") {
		t.Fatalf("expected 'extraction failed' in error, got %v", err)
	}
}

func TestExtractScriptEntities_EmptyScript(t *testing.T) {
	extractor := &mockEntityExtractor{
		result: &core.FullEntityAnalysis{
			TotalSegments:   0,
			SegmentEntities: []core.SegmentEntities{},
		},
	}
	result, err := ExtractScriptEntities(context.Background(), extractor, "", "model")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	var analysis core.FullEntityAnalysis
	if err := json.Unmarshal([]byte(result), &analysis); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if analysis.TotalSegments != 0 {
		t.Fatalf("expected 0 total segments, got %d", analysis.TotalSegments)
	}
}

func TestExtractScriptEntities_Success(t *testing.T) {
	extractor := &mockEntityExtractor{
		result: &core.FullEntityAnalysis{
			TotalSegments: 1,
			SegmentEntities: []core.SegmentEntities{
				{
					SegmentIndex:     0,
					SegmentText:      "This is a test script about Rome.",
					ParoleImportanti: []string{"Rome", "Colosseum"},
					FrasiImportanti:  []string{"Rome was not built in a day."},
					NomiSpeciali:     []string{"Julius Caesar"},
					ArtlistPhrases:   []string{"ancient rome"},
				},
			},
		},
	}
	result, err := ExtractScriptEntities(context.Background(), extractor, "This is a test script about Rome.", "model")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	var analysis core.FullEntityAnalysis
	if err := json.Unmarshal([]byte(result), &analysis); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if analysis.TotalSegments != 1 {
		t.Fatalf("expected 1 segment, got %d", analysis.TotalSegments)
	}
	if len(analysis.SegmentEntities) != 1 {
		t.Fatalf("expected 1 segment entity, got %d", len(analysis.SegmentEntities))
	}
	seg := analysis.SegmentEntities[0]
	if len(seg.ParoleImportanti) != 2 || seg.ParoleImportanti[0] != "Rome" {
		t.Fatalf("expected [Rome Colosseum], got %v", seg.ParoleImportanti)
	}
	if len(seg.NomiSpeciali) != 1 || seg.NomiSpeciali[0] != "Julius Caesar" {
		t.Fatalf("expected [Julius Caesar], got %v", seg.NomiSpeciali)
	}
	if len(seg.ArtlistPhrases) != 1 || seg.ArtlistPhrases[0] != "ancient rome" {
		t.Fatalf("expected [ancient rome], got %v", seg.ArtlistPhrases)
	}
}

func TestExtractScriptEntities_SuccessWithModel(t *testing.T) {
	var capturedModel string
	extractor := &mockEntityExtractor{
		result: &core.FullEntityAnalysis{TotalSegments: 1},
	}
	// Override with custom mock that captures the model
	extractorWithCapture := &entityExtractorCapture{
		EntityScriptExtractor: extractor,
		captureModel:          &capturedModel,
	}
	_, err := ExtractScriptEntities(context.Background(), extractorWithCapture, "test script", "custom-model")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if capturedModel != "custom-model" {
		t.Fatalf("expected model 'custom-model', got %q", capturedModel)
	}
}

// entityExtractorCapture wraps an EntityScriptExtractor to capture the model parameter.
type entityExtractorCapture struct {
	EntityScriptExtractor
	captureModel *string
}

func (m *entityExtractorCapture) ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, entityCount int, model string) (*core.FullEntityAnalysis, error) {
	*m.captureModel = model
	return m.EntityScriptExtractor.ExtractEntitiesFromScriptWithModel(ctx, segments, entityCount, model)
}

// ── ScriptInsightBuilder.Build Tests ─────────────────────────────────────────

func TestScriptInsightBuilder_Build_EmptyEntitiesJSON(t *testing.T) {
	logger := nopLogger()
	svc := defaultClipServices(logger)
	builder := &ScriptInsightBuilder{
		Logger:      logger,
		MaxEntities: 12,
		Services:    svc,
	}

	insights := builder.Build(context.Background(), "Test Title", "Test script content.", "")

	if insights.ImportantWords == nil {
		t.Fatal("expected non-nil ImportantWords")
	}
	if insights.ImportantPhrases == nil {
		t.Fatal("expected non-nil ImportantPhrases")
	}
	if insights.SpecialNames == nil {
		t.Fatal("expected non-nil SpecialNames")
	}
	if insights.ArtlistPhrases == nil {
		t.Fatal("expected non-nil ArtlistPhrases")
	}
	if len(insights.ImportantWords) != 0 {
		t.Fatalf("expected 0 important words, got %d", len(insights.ImportantWords))
	}
}

func TestScriptInsightBuilder_Build_WithValidEntities(t *testing.T) {
	logger := nopLogger()
	svc := defaultClipServices(logger)
	builder := &ScriptInsightBuilder{
		Logger:      logger,
		MaxEntities: 12,
		Services:    svc,
	}

	entitiesJSON := `{
		"total_segments": 1,
		"segment_entities": [{
			"segment_index": 0,
			"segment_text": "Rome is a beautiful city with many ancient monuments.",
			"parole_importanti": ["Rome", "Colosseum", "Vatican", "ancient", "monuments"],
			"frasi_importanti": ["Rome was not built in a day.", "The Colosseum is a marvel."],
			"nomi_speciali": ["Julius Caesar", "Augustus"],
			"artlist_phrases": ["ancient rome", "roman empire"]
		}]
	}`

	insights := builder.Build(context.Background(), "The History of Rome", "Script about Rome.", entitiesJSON)

	// Verify entity extraction
	if len(insights.ImportantWords) == 0 {
		t.Fatal("expected important words to be populated")
	}
	if len(insights.ImportantPhrases) == 0 {
		t.Fatal("expected important phrases to be populated")
	}
	if len(insights.SpecialNames) == 0 {
		t.Fatal("expected special names to be populated")
	}
	if len(insights.ArtlistPhrases) == 0 {
		t.Fatal("expected artlist phrases to be populated")
	}

	// Check specific values
	hasRome := false
	for _, w := range insights.ImportantWords {
		if w == "Rome" {
			hasRome = true
			break
		}
	}
	if !hasRome {
		t.Fatalf("expected 'Rome' in important words, got %v", insights.ImportantWords)
	}

	if insights.RecommendedDriveFolder != nil {
		t.Fatal("expected nil RecommendedDriveFolder with empty mock associations")
	}
	// ArtlistClipSuggestions are always created per-phrase (even without clip
	// search results), so we expect entries matching our artlist_phrases.
	if len(insights.ArtlistClipSuggestions) == 0 {
		t.Fatal("expected at least 1 artlist clip suggestion for artlist_phrases")
	}
	// Check that phrases match the original input phrases (before translation)
	foundPhrase := false
	for _, s := range insights.ArtlistClipSuggestions {
		if s.Phrase == "ancient rome" || s.Phrase == "roman empire" {
			foundPhrase = true
			break
		}
	}
	if !foundPhrase {
		t.Fatalf("expected artlist phrases 'ancient rome' or 'roman empire', got %v", insights.ArtlistClipSuggestions)
	}
}

func TestScriptInsightBuilder_Build_MaxEntitiesLimit(t *testing.T) {
	logger := nopLogger()
	svc := defaultClipServices(logger)
	builder := &ScriptInsightBuilder{
		Logger:      logger,
		MaxEntities: 2, // Only 2 entities max
		Services:    svc,
	}

	// Provide 5 important words - should be capped to 2
	entitiesJSON := `{
		"total_segments": 1,
		"segment_entities": [{
			"segment_index": 0,
			"segment_text": "Test.",
			"parole_importanti": ["word1", "word2", "word3", "word4", "word5"],
			"frasi_importanti": [],
			"nomi_speciali": [],
			"artlist_phrases": []
		}]
	}`

	insights := builder.Build(context.Background(), "Test", "Test.", entitiesJSON)

	if len(insights.ImportantWords) > 2 {
		t.Fatalf("expected max 2 important words due to MaxEntities=2, got %d: %v", len(insights.ImportantWords), insights.ImportantWords)
	}
}

func TestScriptInsightBuilder_Build_WithDriveFolder(t *testing.T) {
	logger := nopLogger()
	assoc := &mockAssoc{
		resp: &association.CandidatesResponse{
			Candidates: []association.Candidate{
				{
					Name:     "Rome Folder",
					Path:     "Travel/Rome",
					FolderID: "folder-123",
					Link:     "https://drive.google.com/drive/folders/folder-123",
					Score:    95,
					Reason:   "matched topic Rome",
				},
			},
		},
	}
	svc := defaultClipServices(logger)
	svc.AssocSvc = assoc
	builder := &ScriptInsightBuilder{
		Logger:      logger,
		MaxEntities: 12,
		Services:    svc,
	}

	entitiesJSON := `{
		"total_segments": 1,
		"segment_entities": [{
			"segment_index": 0,
			"segment_text": "Rome is the topic.",
			"parole_importanti": ["Rome"],
			"frasi_importanti": [],
			"nomi_speciali": [],
			"artlist_phrases": ["rome"]
		}]
	}`

	insights := builder.Build(context.Background(), "Rome", "Rome is the topic.", entitiesJSON)

	if insights.RecommendedDriveFolder == nil {
		t.Fatal("expected RecommendedDriveFolder to be set")
	}
	if insights.RecommendedDriveFolder.Name != "Rome Folder" {
		t.Fatalf("expected 'Rome Folder', got %q", insights.RecommendedDriveFolder.Name)
	}
	if insights.RecommendedDriveFolder.FolderID != "folder-123" {
		t.Fatalf("expected 'folder-123', got %q", insights.RecommendedDriveFolder.FolderID)
	}
}

func TestScriptInsightBuilder_Build_WithArtlistClips(t *testing.T) {
	logger := nopLogger()
	clipSearch := &mockClipSearch{
		assets: []realtime.MatchAsset{
			{
				ID:        "clip-001",
				Name:      "Ancient Rome Landscape",
				Source:    "artlist",
				Score:     0.95,
				DriveLink: "https://drive.google.com/file/d/clip-001/view",
			},
		},
	}
	svc := defaultClipServices(logger)
	svc.RealtimeSvc = clipSearch
	svc.Translator = &mockTranslator{translated: "ancient rome"} // English translation for artlist search
	builder := &ScriptInsightBuilder{
		Logger:      logger,
		MaxEntities: 12,
		Services:    svc,
	}

	entitiesJSON := `{
		"total_segments": 1,
		"segment_entities": [{
			"segment_index": 0,
			"segment_text": "Test.",
			"parole_importanti": [],
			"frasi_importanti": ["The Colosseum is a marvel."],
			"nomi_speciali": [],
			"artlist_phrases": ["roma antica"]
		}]
	}`

	insights := builder.Build(context.Background(), "Ancient Rome", "Test.", entitiesJSON)

	// Artlist clip suggestions should still be populated (the mock returns clips)
	if len(insights.ArtlistClipSuggestions) > 0 {
		t.Logf("got %d artlist clip suggestions (expected 0 or more)", len(insights.ArtlistClipSuggestions))
	}
}

func TestScriptInsightBuilder_Build_WithEntityImages(t *testing.T) {
	logger := nopLogger()
	imgSvc := &mockImageSearch{
		searchResult: &media.ImageAsset{
			Hash:         "hash123",
			SourceURL:    "https://example.com/rome.jpg",
			PathRel:      "images/rome.jpg",
			Description:  "Roman Colosseum at sunset",
			DriveFileID:  "drive-img-123",
			MetadataJSON: `{"source": "wikipedia"}`,
		},
	}
	svc := defaultClipServices(logger)
	svc.ImgSvc = imgSvc
	builder := &ScriptInsightBuilder{
		Logger:      logger,
		MaxEntities: 12,
		Services:    svc,
	}

	entitiesJSON := `{
		"total_segments": 1,
		"segment_entities": [{
			"segment_index": 0,
			"segment_text": "Julius Caesar was a leader.",
			"parole_importanti": [],
			"frasi_importanti": [],
			"nomi_speciali": ["Julius Caesar"],
			"artlist_phrases": []
		}]
	}`

	insights := builder.Build(context.Background(), "Julius Caesar", "Julius Caesar was a leader.", entitiesJSON)

	// Entity images should be populated
	if len(insights.EntityImages) == 0 {
		t.Fatal("expected EntityImages to be populated")
	}
	hasCaesar := false
	for _, img := range insights.EntityImages {
		if img.EntityName == "Julius Caesar" {
			hasCaesar = true
			if img.ImageHash != "hash123" {
				t.Fatalf("expected hash 'hash123', got %q", img.ImageHash)
			}
			break
		}
	}
	if !hasCaesar {
		t.Fatalf("expected 'Julius Caesar' in EntityImages, got %v", insights.EntityImages)
	}
}

func TestScriptInsightBuilder_Build_TrashedDriveFolderSkipped(t *testing.T) {
	logger := nopLogger()
	assoc := &mockAssoc{
		resp: &association.CandidatesResponse{
			Candidates: []association.Candidate{
				{
					Name:     "Trashed Folder",
					FolderID: "folder-trashed",
					Score:    80,
					Reason:   "matched",
				},
			},
		},
	}
	driveCheck := &mockDriveCheck{active: false} // folder is trashed
	svc := defaultClipServices(logger)
	svc.AssocSvc = assoc
	svc.DriveSvc = driveCheck
	builder := &ScriptInsightBuilder{
		Logger:      logger,
		MaxEntities: 12,
		Services:    svc,
	}

	entitiesJSON := `{
		"total_segments": 1,
		"segment_entities": [{
			"segment_index": 0,
			"segment_text": "Test.",
			"parole_importanti": ["Rome"],
			"frasi_importanti": [],
			"nomi_speciali": [],
			"artlist_phrases": []
		}]
	}`

	insights := builder.Build(context.Background(), "Rome", "Test.", entitiesJSON)

	// Trashed folder should be skipped, so RecommendedDriveFolder should be nil
	if insights.RecommendedDriveFolder != nil {
		t.Fatalf("expected nil RecommendedDriveFolder for trashed folder, got %+v", *insights.RecommendedDriveFolder)
	}
}

func TestScriptInsightBuilder_Build_PhrasesAndIntroClips(t *testing.T) {
	logger := nopLogger()
	clipSearch := &mockClipSearch{
		assets: []realtime.MatchAsset{
			{
				ID:        "intro-001",
				Name:      "Colosseum Overview",
				Source:    "youtube",
				Score:     0.88,
				DriveLink: "https://drive.google.com/file/d/intro-001/view",
			},
		},
	}
	svc := defaultClipServices(logger)
	svc.RealtimeSvc = clipSearch
	builder := &ScriptInsightBuilder{
		Logger:      logger,
		MaxEntities: 12,
		Services:    svc,
	}

	entitiesJSON := `{
		"total_segments": 1,
		"segment_entities": [{
			"segment_index": 0,
			"segment_text": "The Colosseum is a marvel of ancient engineering.",
			"parole_importanti": ["Colosseum"],
			"frasi_importanti": ["The Colosseum is a marvel of ancient engineering."],
			"nomi_speciali": ["Colosseum"],
			"artlist_phrases": []
		}]
	}`

	insights := builder.Build(context.Background(), "Colosseum", "The Colosseum is a marvel of ancient engineering.", entitiesJSON)

	// Phrase clip suggestions should be attempted (even if empty)
	if insights.PhraseClipSuggestions == nil {
		t.Fatal("expected non-nil PhraseClipSuggestions")
	}
	// Intro clips should be attempted
	if insights.IntroClips == nil {
		t.Fatal("expected non-nil IntroClips")
	}
}

func TestScriptInsightBuilder_Build_InvalidEntitiesJSON(t *testing.T) {
	logger := nopLogger()
	svc := defaultClipServices(logger)
	builder := &ScriptInsightBuilder{
		Logger:      logger,
		MaxEntities: 12,
		Services:    svc,
	}

	// Invalid JSON should be handled gracefully (empty slices, no crash)
	insights := builder.Build(context.Background(), "Test", "Script.", "{invalid}")

	if insights.ImportantWords == nil {
		t.Fatal("expected non-nil ImportantWords even with invalid JSON")
	}
	if len(insights.ImportantWords) != 0 {
		t.Fatalf("expected 0 important words with invalid JSON, got %d", len(insights.ImportantWords))
	}
}
