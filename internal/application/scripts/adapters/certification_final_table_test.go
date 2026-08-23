package adapters_test

import (
	"context"
	"strings"
	"testing"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	localnlp "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/nlp/local"
)

// certMediaPlan is the resolved plan for the full certification chain:
// deterministic local NLP extraction plus internet-images entity-image search
// and materialization, all forced-refresh so no stale cache is replayed.
func certMediaPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Title:    "NLP Online Images Certification",
		Language: "en",
		Model:    "local",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			Extraction: mediadomain.MediaExtractionPolicy{
				Enabled:                       true,
				Device:                        "cpu",
				MaxEntitiesPerSegment:         5,
				MaxImportantPhrasesPerSegment: 1,
				MaxImportantWordsPerSegment:   5,
				EntityImages: mediadomain.EntityImagePolicy{
					Enabled: true, EntityTypes: []string{"PERSON"},
				},
			},
			ForceRefreshExtraction: true,
			ForceRefreshAssets:     true,
		},
	}
}

// certInternetImageSearcher is the deterministic stand-in for the real
// internet-image search: one discovered candidate per person query.
type certInternetImageSearcher struct {
	calls   int
	queries []string
}

func (s *certInternetImageSearcher) SearchImages(_ context.Context, req adapters.InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	s.queries = append(s.queries, req.Query)
	id := strings.ToLower(strings.ReplaceAll(req.Query, " ", "-"))
	return []scriptpkg.SegmentAssetCandidate{{
		AssetID:   "img-" + id,
		Provider:  scriptpkg.VidRushProviderInternetImages,
		Query:     req.Query,
		Entity:    req.Entity,
		SourceURL: "https://images.example/" + id + ".jpg",
		Score:     1,
	}}, nil
}

// certInternetImagesProvider is the deterministic acquire/verify boundary for
// internet-images candidates.
type certInternetImagesProvider struct{}

func (certInternetImagesProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (certInternetImagesProvider) Search(context.Context, scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return nil, nil
}
func (certInternetImagesProvider) Acquire(_ context.Context, c scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	c.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	return scriptports.LocalArtifact{Candidate: c, LocalPath: "/tmp/" + c.AssetID + ".jpg", MIMEType: "image/jpeg", SizeBytes: 10, LegacyFileMD5: "hash-" + c.AssetID}, nil
}
func (certInternetImagesProvider) Verify(_ context.Context, a scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	c := a.Candidate
	c.VerificationStatus = scriptpkg.VidRushStatusVerified
	c.RightsStatus = "verified"
	return scriptports.VerifiedArtifact{Candidate: c, LocalPath: a.LocalPath, MIMEType: a.MIMEType, SizeBytes: a.SizeBytes, LegacyFileMD5: a.LegacyFileMD5, Width: 640, Height: 480, RightsStatus: "verified"}, nil
}

// certFinalizer is the deterministic Drive persistence boundary.
type certFinalizer struct{}

func (certFinalizer) Finalize(_ context.Context, a scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	c := a.Candidate
	c.LegacyFileMD5 = a.LegacyFileMD5
	c.Width = a.Width
	c.Height = a.Height
	c.DriveLink = "https://drive.google.com/file/d/" + c.AssetID + "/view"
	c.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	c.IndexStatus = scriptpkg.VidRushStatusIndexed
	return c, nil
}

// certTableRow is one row of the final per-scene certification table.
type certTableRow struct {
	scene    string
	person   string
	place    string
	phrase   bool
	words    int
	query    string
	results  int
	verified bool
	drive    bool
	doc      bool
}

// TestCertificationFinalTableAndChecklist runs the whole chain for the ten
// controlled scenes — NLP → entity-image search → download/verify/persist/Drive
// → bind → Google Doc — and certifies the aggregate CERTIFIED=YES checklist.
// The run is video-disabled: the chain is entities→images→materialization→doc,
// with no render stage, so no RenderPlan and no video job can be produced.
func TestCertificationFinalTableAndChecklist(t *testing.T) {
	plan := certMediaPlan()
	extractor := localnlp.NewHybridExtractor()
	enricher := adapters.NewVidRushSegmentEnricher(extractor, nil)
	scenes := certScenes()

	// ── 1. NLP enrichment (per segment) ────────────────────────────────
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(scenes))
	specScenes := make([]scriptpkg.SpecScene, 0, len(scenes))
	for i, sc := range scenes {
		seg, err := enricher.Enrich(context.Background(), plan, toSpecScene(sc, i))
		if err != nil {
			t.Fatalf("Enrich(%s): %v", sc.id, err)
		}
		segments = append(segments, seg)
		specScenes = append(specScenes, toSpecScene(sc, i))
	}
	// Project the NLP person onto each scene's primary-entity annotations.
	for i := range specScenes {
		if person := firstPerson(segments[i]); person != "" {
			specScenes[i].Annotations = &scriptpkg.SceneAnnotations{
				Version: 1, Language: "en",
				PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: person, Text: person, Type: "PERSON"}},
			}
		}
	}
	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: specScenes}

	// ── 2. Entity-image search ─────────────────────────────────────────
	searcher := &certInternetImageSearcher{}
	searchOut, err := adapters.NewInternetImagesProcessor(searcher).Process(context.Background(), plan, adapters.ProcessInput{
		VidRushSegments: segments,
		SpecScene:       spec,
	})
	if err != nil {
		t.Fatalf("internet-images search: %v", err)
	}

	// ── 3. Download → verify → persist → Drive → bind ──────────────────
	registry := adapters.NewVidRushAssetProviderRegistry()
	if err := registry.Register(certInternetImagesProvider{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	finalOut, err := adapters.NewVidRushMaterializationProcessor(registry, certFinalizer{}).Process(context.Background(), plan, adapters.ProcessInput{
		VidRushSegments: searchOut.VidRushSegments,
		SpecScene:       searchOut.UpdatedSpecScene,
	})
	if err != nil {
		t.Fatalf("vidrush materialization: %v", err)
	}

	// ── 4. Google Doc ──────────────────────────────────────────────────
	docHTML, err := scriptgen.RenderDocument(
		&scriptpkg.ModelScriptOutputV1{SpecScene: finalOut.UpdatedSpecScene},
		scriptgen.DocumentRenderOptions{Title: "NLP Online Images Certification"},
	)
	if err != nil {
		t.Fatalf("render document: %v", err)
	}

	// ── 5. Per-scene table + checklist counters ────────────────────────
	expectedQueries := make(map[string]bool, len(scenes))
	for _, sc := range scenes {
		expectedQueries[sc.person] = true
	}

	rows := make([]certTableRow, 0, len(scenes))
	persons, phrases, places, wordsOK := 0, 0, 0, 0
	results, verified, drive, docs := 0, 0, 0, 0
	for i, sc := range scenes {
		seg := finalOut.VidRushSegments[i]
		img := finalOut.UpdatedSpecScene.Scenes[i].Annotations.PrimaryEntities[0].Image

		row := certTableRow{
			scene: sc.id, person: sc.person, place: sc.place,
			words: len(seg.Insights.ImportantWords), query: sc.person,
			results: len(seg.Assets.Candidates),
		}
		if len(seg.Insights.ImportantPhrases) == 1 && strings.Contains(seg.Insights.ImportantPhrases[0], sc.person) {
			row.phrase = true
			phrases++
		}
		if len(seg.Insights.ImportantWords) >= 3 {
			wordsOK++
		}
		if hasEntity(seg, sc.person, "PERSON") {
			persons++
		}
		if sc.place != "" && hasEntity(seg, sc.place, "GPE") {
			places++
		}
		if len(seg.Assets.Candidates) > 0 {
			results++
		}
		for _, c := range seg.Assets.Candidates {
			if c.AcquisitionStatus == scriptpkg.VidRushStatusAcquired &&
				c.VerificationStatus == scriptpkg.VidRushStatusVerified &&
				c.PersistenceStatus == scriptpkg.VidRushStatusPersisted {
				row.verified = true
				verified++
			}
			if c.DriveLink != "" {
				row.drive = true
				drive++
			}
		}
		if img != nil && img.Status == "resolved" && img.DriveLink != "" {
			row.doc = true
			docs++
		}
		rows = append(rows, row)
	}

	// ── 6. Assert the checklist ────────────────────────────────────────
	if len(segments) != 10 || len(finalOut.VidRushSegments) != 10 {
		t.Fatalf("scenes = %d/%d, want 10", len(segments), len(finalOut.VidRushSegments))
	}
	if persons != 10 {
		t.Fatalf("expected persons = %d/10", persons)
	}
	if places != 10 {
		t.Fatalf("expected places = %d/10", places)
	}
	if phrases != 10 {
		t.Fatalf("important phrase = %d/10", phrases)
	}
	if wordsOK != 10 {
		t.Fatalf("important words >= 3 = %d/10", wordsOK)
	}
	if searcher.calls != 10 {
		t.Fatalf("real provider search calls = %d, want 10", searcher.calls)
	}
	for _, q := range searcher.queries {
		if !expectedQueries[q] {
			t.Fatalf("search query %q is not a primary entity name", q)
		}
	}
	if results != 10 {
		t.Fatalf("online candidates >= 1 = %d/10", results)
	}
	if verified != 10 || drive != 10 {
		t.Fatalf("verified=%d drive=%d, want 10/10", verified, drive)
	}
	if docs != 10 {
		t.Fatalf("resolved entity images in doc = %d/10", docs)
	}
	if got := strings.Count(docHTML, "<h2>Scene "); got != 10 {
		t.Fatalf("doc scenes = %d, want 10", got)
	}
	if got := strings.Count(docHTML, "<img src=\""); got != 10 {
		t.Fatalf("doc inline entity images = %d, want 10", got)
	}
	if got := strings.Count(docHTML, "<strong>Entity image:</strong>"); got != 10 {
		t.Fatalf("doc entity Drive links = %d, want 10", got)
	}

	// VIDEO — the certification chain has no render stage, so no RenderPlan and
	// no video job are produced. The explicit RenderVideo=false → RenderPlan=nil
	// → zero-video-jobs contract is asserted by
	// TestRunner_AudioOnlyDocumentsReceiveFinalAudio in the scriptgeneration package.
	if strings.Contains(docHTML, "render_plan") {
		t.Fatal("doc must never carry a render_plan for a video-disabled certification")
	}

	// ── 7. Emit the certification table ────────────────────────────────
	t.Logf("SCENE | PERSON | PLACE | PHRASE | WORDS | QUERY | RESULTS | VERIFIED | DRIVE | DOC")
	for _, r := range rows {
		t.Logf("%s | %s | %s | %v | %d | %s | %d | %v | %v | %v",
			r.scene, r.person, r.place, r.phrase, r.words, r.query, r.results, r.verified, r.drive, r.doc)
	}
	t.Logf("CERTIFIED = YES (NLP %d/10 persons, search %d calls, materialization %d/10 verified, doc %d/10 inline, video disabled)", persons, searcher.calls, verified, docs)
}

func firstPerson(seg scriptpkg.VidRushSegmentResult) string {
	for _, e := range seg.Insights.Entities {
		if e.Type == "PERSON" {
			return e.Value
		}
	}
	return ""
}

func hasEntity(seg scriptpkg.VidRushSegmentResult, value, kind string) bool {
	for _, e := range seg.Insights.Entities {
		if e.Value == value && e.Type == kind {
			return true
		}
	}
	return false
}
