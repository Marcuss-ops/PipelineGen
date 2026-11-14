package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
	sqliteinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/entitycatalog"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

type liveJobImageSearcher struct {
	delegate imagesapp.ImageSearcher
	calls    atomic.Int32
	rawHits  atomic.Int32
	maxItems int
}

type liveJobIdentityScorer struct {
	client *ollamaclient.Client
	http   *http.Client
}

func (s *liveJobIdentityScorer) ScoreImageCandidates(ctx context.Context, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
	for i := range out {
		candidate := &out[i]
		identity, err := entitycatalog.CanonicalizePersonName(candidate.Entity)
		if err != nil {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticRejected
			candidate.QualityReason = "live probe rejected invalid person identity"
			continue
		}
		data, mime, err := DownloadVidRushImageForCandidate(ctx, s.http, *candidate, DefaultVidRushImagePolicy())
		if err != nil {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticRejected
			candidate.QualityReason = "image could not be downloaded for visual verification: " + err.Error()
			continue
		}
		verified, err := VerifyVidRushImageBytes(*candidate, data, mime, DefaultVidRushImagePolicy())
		if err != nil {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticRejected
			candidate.QualityReason = "technical image verification rejected candidate: " + err.Error()
			continue
		}
		candidate.Width, candidate.Height = verified.Width, verified.Height
		var verdict struct {
			IdentityMatch bool    `json:"identity_match"`
			SinglePerson  bool    `json:"single_person_headshot"`
			Collage       bool    `json:"split_panel_or_collage"`
			Confidence    float64 `json:"confidence"`
		}
		prompt := fmt.Sprintf(`Inspect the attached image pixels for identity and layout. Requested person: %q. Retrieval query: %q. Ignore URLs, titles, and search metadata. Set identity_match only when the visible person is recognizable as the requested person. Require one dominant person and no collage. If uncertain, return false. Return exactly JSON: {"identity_match":false,"single_person_headshot":false,"split_panel_or_collage":false,"confidence":0.0}`, identity.CanonicalName, query)
		response, err := s.client.Chat(ctx, []types.Message{
			{Role: "system", Content: "Conservatively verify public-person identity from image pixels. Return only JSON."},
			{Role: "user", Content: prompt, Images: []string{base64.StdEncoding.EncodeToString(data)}},
		}, map[string]any{"model": s.client.Model(), "temperature": 0, "num_predict": 128, "num_ctx": 4096}, json.RawMessage(`"json"`))
		if err != nil {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticRejected
			candidate.QualityReason = "visual verification failed: " + err.Error()
			continue
		}
		if err := json.Unmarshal([]byte(response), &verdict); err != nil {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticRejected
			candidate.QualityReason = "visual verification failed: invalid JSON verdict"
			continue
		}
		if verdict.IdentityMatch && verdict.SinglePerson && !verdict.Collage && verdict.Confidence >= 0.85 {
			candidate.SemanticStatus = entitycatalog.CandidateSemanticAccepted
			candidate.SemanticScore = verdict.Confidence
			candidate.QualityReason = "Ollama visual identity verification confirmed the requested person and one dominant subject"
			continue
		}
		candidate.SemanticStatus = entitycatalog.CandidateSemanticRejected
		candidate.SemanticScore = verdict.Confidence
		candidate.QualityReason = "Ollama visual identity verification did not confirm the requested person and layout"
	}
	return out, nil
}

func newLiveJobImageSearcher(t *testing.T) *liveJobImageSearcher {
	t.Helper()
	cfg := &config.Config{}
	cfg.External.SearxngURL = strings.TrimSpace(os.Getenv("SEARXNG_URL"))
	if cfg.External.SearxngURL == "" {
		cfg.External.SearxngURL = "http://127.0.0.1:8080"
	}
	service := imagesapp.NewService(imagesapp.ImagesDeps{
		Core:     imagesapp.ImagesCoreDeps{Cfg: cfg, Log: zap.NewNop()},
		External: imagesapp.ImagesExternalDeps{RemoteFetch: &http.Client{Timeout: 20 * time.Second}},
	})
	resolver, err := imagesapp.NewImageSearchResolver(
		imagesapp.WithRetrievalBackend(service.RetrievalRegistry()),
		imagesapp.WithImageListRepository(liveJobEmptyImageRepo{}),
	)
	if err != nil {
		t.Fatalf("live image resolver: %v", err)
	}
	delegate, err := resolver.Resolve(imagesapp.TerritoryRetrieved)
	if err != nil {
		t.Fatalf("live retrieved searcher: %v", err)
	}
	maxItems := 20
	if configured := strings.TrimSpace(os.Getenv("PERSON_CATALOG_VERIFY_LIMIT")); configured != "" {
		parsed, parseErr := strconv.Atoi(configured)
		if parseErr != nil || parsed < 1 {
			t.Fatalf("PERSON_CATALOG_VERIFY_LIMIT must be a positive integer, got %q", configured)
		}
		maxItems = parsed
	}
	return &liveJobImageSearcher{delegate: delegate, maxItems: maxItems}
}

func (s *liveJobImageSearcher) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	hits, err := s.delegate.Search(ctx, imagesapp.ImageFilter{SubjectID: req.Query, Limit: req.Limit})
	if err != nil {
		return nil, err
	}
	s.rawHits.Store(int32(len(hits)))
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(hits))
	for i, hit := range hits {
		url := strings.TrimSpace(hit.PreviewURL)
		if url == "" {
			continue
		}
		out = append(out, scriptpkg.SegmentAssetCandidate{
			AssetID: fmt.Sprintf("live-job-%d", i+1), Provider: scriptpkg.VidRushProviderInternetImages,
			Query: req.Query, Entity: req.Entity, SourceURL: url, PreviewURL: url,
			SourcePageURL: hit.SourcePageURL, Width: hit.Width, Height: hit.Height,
			Score: hit.Score, RightsStatus: "unknown_allowed",
		})
	}
	if s.maxItems > 0 && len(out) > s.maxItems {
		out = out[:s.maxItems]
	}
	return out, nil
}

type liveJobEmptyImageRepo struct{}

func (liveJobEmptyImageRepo) ListImages(context.Context, imagesapp.ImageFilter) ([]imagesapp.ImageSearchResult, error) {
	return nil, nil
}

func liveJobPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Topic: "live-isolated-image-job", Language: "en",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			Extraction:     mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{Enabled: true, EntityTypes: []string{"PERSON"}}},
		},
	}
}

func liveJobInput(segmentID, name string) ProcessInput {
	return ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: segmentID, SegmentID: segmentID,
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: name, Text: name, Type: "PERSON"}}},
		}}},
		VidRushSegments: []scriptpkg.VidRushSegmentResult{{SegmentID: segmentID, SceneID: segmentID, TextHash: segmentID}},
	}
}

// TestLiveImageRetrievalJobPersistsAndReusesAssets exercises the production
// scene discovery, guarded download/verification, and durable entity-catalog
// replay against real image search results. Its SQLite DB and asset directory
// are private to the test; no shared job, Drive folder, media row, or index is
// touched. The finalizer is an isolated local equivalent of the canonical
// persistence port, so this gate covers job behavior without external writes.
//
//	Run with PERSON_CATALOG_LIVE=1 PERSON_CATALOG_QUERY="John Cena" go test ./internal/capabilities/scripts/adapters \
//	  -run '^TestLiveImageRetrievalJobPersistsAndReusesAssets$' -v -count=1 -timeout 4m
func TestLiveImageRetrievalJobPersistsAndReusesAssets(t *testing.T) {
	if os.Getenv("PERSON_CATALOG_LIVE") != "1" {
		t.Skip("set PERSON_CATALOG_LIVE=1 to run the isolated live retrieval job")
	}
	entityName := strings.TrimSpace(os.Getenv("PERSON_CATALOG_QUERY"))
	if entityName == "" {
		t.Fatal("set PERSON_CATALOG_QUERY to one person name, for example 'John Cena'")
	}
	identity, err := entitycatalog.CanonicalizePersonName(entityName)
	if err != nil {
		t.Fatalf("invalid PERSON_CATALOG_QUERY %q: %v", entityName, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	resetEntityImageCatalogCaches()
	root := t.TempDir()
	if artifactDir := strings.TrimSpace(os.Getenv("PERSON_CATALOG_ARTIFACT_DIR")); artifactDir != "" {
		root = artifactDir
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatalf("create PERSON_CATALOG_ARTIFACT_DIR: %v", err)
		}
	}
	dbPath := filepath.Join(root, "job-catalog.db")
	db := openPersistentEntityImageCatalog(t, dbPath)
	repo := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db)
	searcher := newLiveJobImageSearcher(t)
	plan := liveJobPlan()
	plan.ImagesPerScene = 1
	segmentID := "entity-" + strings.ToLower(strings.ReplaceAll(identity.CanonicalName, " ", "-"))
	input := liveJobInput(segmentID+"-cold", identity.CanonicalName)
	ollamaURL := strings.TrimSpace(os.Getenv("OLLAMA_ADDR"))
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	ollamaModel := strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
	if ollamaModel == "" {
		ollamaModel = "gemma4:e4b"
	}
	ollamaTimeout := 180
	if configured := strings.TrimSpace(os.Getenv("OLLAMA_TIMEOUT")); configured != "" {
		if parsed, parseErr := strconv.Atoi(configured); parseErr == nil && parsed > 0 {
			ollamaTimeout = parsed
		}
	}
	scorer := &liveJobIdentityScorer{
		client: ollamaclient.NewClient(ollamaURL, ollamaModel, ollamaTimeout),
		http:   &http.Client{Timeout: 30 * time.Second},
	}

	// Exercise the semantic image-resolution stage used by a generation job.
	searchStage := NewMediaResolverImageStageWithCatalog(searcher, nil, repo).WithSemanticScorer(scorer)
	discoveryResult, err := searchStage.Process(ctx, plan, input)
	if err != nil {
		t.Fatalf("cold job search: %v", err)
	}
	if len(discoveryResult.VidRushSegments) != 1 {
		t.Fatalf("cold job discovery segments=%d, want 1", len(discoveryResult.VidRushSegments))
	}
	discovered := discoveryResult.VidRushSegments[0]
	if len(discovered.Assets.Candidates) == 0 {
		t.Fatalf("cold job search returned no visually verified candidates for %q", identity.CanonicalName)
	}
	t.Logf("identity: query=%q canonical_id=%s model=%s provider_calls=%d provider_hits=%d visually_checked=%d verified_candidates=%d", entityName, identity.CanonicalEntityID, ollamaModel, searcher.calls.Load(), searcher.rawHits.Load(), min(searcher.maxItems, int(searcher.rawHits.Load())), len(discovered.Assets.Candidates))
	for i, candidate := range discovered.Assets.Candidates {
		t.Logf("verified[%d]: keyword=%q entity=%q status=%s score=%.2f reason=%q source=%s", i+1, candidate.Query, candidate.Entity, candidate.SemanticStatus, candidate.SemanticScore, candidate.QualityReason, candidate.SourceURL)
	}
	// Search persists a larger reuse pool; this isolated probe requests one
	// image and materializes at most one alternate to keep writes bounded.
	if len(discovered.Assets.Candidates) > 2 {
		discovered.Assets.Candidates = discovered.Assets.Candidates[:2]
	}
	plan.MediaPlan.Extraction.EntityImages.Enabled = false
	t.Logf("cold job: discovered=%d provider_searches=%d", len(discovered.Assets.Candidates), searcher.calls.Load())

	provider := &livePersistingImageProvider{searcher: searcher, client: &http.Client{Timeout: 25 * time.Second}}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	finalizer := &liveLocalAssetFinalizer{root: filepath.Join(root, "assets")}
	materializer := NewVidRushMaterializationProcessorWithCatalog(registry, finalizer, nil, repo)
	materialized, err := materializer.Process(ctx, plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{discovered}})
	if err != nil {
		t.Fatalf("cold job materialization: %v", err)
	}
	plan.MediaPlan.Extraction.EntityImages.Enabled = true
	if len(materialized.VidRushSegments) != 1 {
		t.Fatalf("cold materialized segments=%d, want 1", len(materialized.VidRushSegments))
	}
	assets := materialized.VidRushSegments[0].Assets.Candidates
	if finalizer.writes.Load() == 0 {
		t.Fatalf("cold job persisted no verified files (candidates=%d)", len(assets))
	}
	for _, asset := range assets {
		if asset.PersistenceStatus != scriptpkg.VidRushStatusPersisted || asset.ContentSHA256 == "" || asset.DriveLink == "" {
			continue
		}
		if _, err := os.Stat(asset.LocalPath); err != nil {
			t.Fatalf("persisted asset missing at %q: %v", asset.LocalPath, err)
		}
	}
	rows, err := repo.ListCandidates(ctx, identity.CanonicalEntityID, 20)
	if err != nil {
		t.Fatalf("read persisted catalog: %v", err)
	}
	materializedRows := 0
	uniqueURLs := map[string]bool{}
	for _, row := range rows {
		if uniqueURLs[row.SourceURL] {
			t.Fatalf("duplicate source URL persisted: %s", row.SourceURL)
		}
		uniqueURLs[row.SourceURL] = true
		m, lookupErr := repo.GetMaterialization(ctx, row.ID)
		if lookupErr == nil && m != nil && m.Status == entitycatalog.MaterializationStatusMaterialized && m.ContentSHA256 != "" {
			materializedRows++
		}
	}
	if materializedRows == 0 {
		t.Fatal("no downloaded asset materialization was committed to the isolated catalog")
	}
	callsAfterCold := searcher.calls.Load()
	if callsAfterCold == 0 {
		t.Fatal("cold job did not call a live image provider")
	}
	writesAfterCold := finalizer.writes.Load()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a new worker/job process: reopen the durable catalog and clear
	// process-local pools. The normal replay should meet the scene's requested
	// image count from the durable catalog without another provider request.
	resetEntityImageCatalogCaches()
	db = openPersistentEntityImageCatalog(t, dbPath)
	defer db.Close()
	repo = sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db)
	replayInput := liveJobInput(segmentID+"-replay", identity.CanonicalName)
	replayInput.VidRushSegments[0].Text = identity.CanonicalName + " and related events"
	replayPlan := liveJobPlan()
	replayPlan.ImagesPerScene = 1
	replayPlan.Topic = "unrelated topic for catalog replay"
	replayDiscovery, err := NewMediaResolverImageStageWithCatalog(searcher, nil, repo).WithSemanticScorer(scorer).Process(ctx, replayPlan, replayInput)
	if err != nil {
		t.Fatalf("warm job search/reuse: %v", err)
	}
	replaySegment := replayDiscovery.VidRushSegments[0]
	if got := searcher.calls.Load(); got != callsAfterCold {
		t.Fatalf("warm replay provider calls=%d, cold ended at %d; want no new search", got, callsAfterCold)
	}
	// This replay asks only for assets already materialized by the cold run.
	// Filter out the still-fresh discovery alternatives so the test asserts
	// reuse without turning catalog enrichment into another background fill.
	readyCandidates := make([]scriptpkg.SegmentAssetCandidate, 0, len(replaySegment.Assets.SecondaryImages))
	for _, candidate := range replaySegment.Assets.SecondaryImages {
		if readyVidRushCandidate(candidate) {
			readyCandidates = append(readyCandidates, candidate)
		}
	}
	if len(readyCandidates) == 0 {
		t.Fatalf("catalog replay did not hydrate materialized candidates: %+v", replaySegment.Assets)
	}
	replaySegment.Assets.Candidates = readyCandidates
	replayPlan.MediaPlan.Extraction.EntityImages.Enabled = false
	replayMaterializer := NewVidRushMaterializationProcessorWithCatalog(registry, finalizer, nil, repo)
	replayed, err := replayMaterializer.Process(ctx, replayPlan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{replaySegment}})
	if err != nil {
		t.Fatalf("warm job materialization: %v", err)
	}
	if got := finalizer.writes.Load(); got != writesAfterCold {
		t.Fatalf("warm replay wrote %d files; cold ended at %d, want no new writes", got, writesAfterCold)
	}
	ready := 0
	for _, asset := range replayed.VidRushSegments[0].Assets.SecondaryImages {
		if asset.PersistenceStatus == scriptpkg.VidRushStatusPersisted && asset.ContentSHA256 != "" && asset.DriveLink != "" {
			ready++
		}
	}
	if ready == 0 {
		t.Fatalf("warm replay returned no persisted images: %+v", replayed.VidRushSegments[0].Assets)
	}
	t.Logf("PASS cold: searches=%d assets_saved=%d unique_candidates=%d materialized_catalog_rows=%d; warm: searches=0 new_saves=0 reused_ready=%d",
		callsAfterCold, writesAfterCold, len(rows), materializedRows, ready)
}

type livePersistingImageProvider struct {
	searcher *liveJobImageSearcher
	client   *http.Client
}

func (*livePersistingImageProvider) Name() string { return scriptpkg.VidRushProviderInternetImages }
func (p *livePersistingImageProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	return p.searcher.SearchImages(ctx, InternetImageSearchRequest{SegmentID: req.SegmentID, Query: req.Query, Entity: req.Entity, QueryClass: req.QueryClass, Limit: req.Limit})
}
func (p *livePersistingImageProvider) Acquire(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	data, mime, err := DownloadVidRushImageForCandidate(ctx, p.client, candidate, DefaultVidRushImagePolicy())
	if err != nil {
		return scriptports.LocalArtifact{}, err
	}
	file, err := os.CreateTemp("", "velox-live-job-image-*")
	if err != nil {
		return scriptports.LocalArtifact{}, err
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return scriptports.LocalArtifact{}, err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return scriptports.LocalArtifact{}, err
	}
	hash := sha256.Sum256(data)
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.LocalPath, candidate.MIMEType, candidate.ContentSHA256 = file.Name(), mime, hex.EncodeToString(hash[:])
	return scriptports.LocalArtifact{Candidate: candidate, LocalPath: file.Name(), MIMEType: mime, SizeBytes: int64(len(data)), ContentSHA256: candidate.ContentSHA256}, nil
}
func (p *livePersistingImageProvider) Verify(_ context.Context, artifact scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	verified, err := VerifyVidRushImageFile(artifact.Candidate, artifact.LocalPath, DefaultVidRushImagePolicy())
	if err != nil {
		return scriptports.VerifiedArtifact{}, err
	}
	candidate := verified.Candidate
	candidate.RightsStatus = "unknown_allowed"
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	return scriptports.VerifiedArtifact{Candidate: candidate, LocalPath: artifact.LocalPath, MIMEType: verified.MIMEType, SizeBytes: verified.SizeBytes, ContentSHA256: verified.ContentSHA256, Width: verified.Width, Height: verified.Height, RightsStatus: candidate.RightsStatus, VerificationNote: "live download decoded and policy-checked"}, nil
}

type liveLocalAssetFinalizer struct {
	root   string
	writes atomic.Int32
}

func (f *liveLocalAssetFinalizer) Finalize(_ context.Context, artifact scriptports.VerifiedArtifact) (scriptpkg.SegmentAssetCandidate, error) {
	if err := os.MkdirAll(f.root, 0o700); err != nil {
		return scriptpkg.SegmentAssetCandidate{}, err
	}
	name := fmt.Sprintf("%x.jpg", sha256.Sum256([]byte(artifact.ContentSHA256)))
	dst := filepath.Join(f.root, name)
	src, err := os.Open(artifact.LocalPath)
	if err != nil {
		return scriptpkg.SegmentAssetCandidate{}, err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = src.Close()
		if os.IsExist(err) {
			if _, statErr := os.Stat(dst); statErr != nil {
				return scriptpkg.SegmentAssetCandidate{}, fmt.Errorf("inspect content-addressed asset: %w", statErr)
			}
		} else {
			return scriptpkg.SegmentAssetCandidate{}, err
		}
	} else {
		_, copyErr := io.Copy(out, src)
		closeErr := out.Close()
		if copyErr != nil {
			_ = os.Remove(dst)
			_ = src.Close()
			return scriptpkg.SegmentAssetCandidate{}, copyErr
		}
		if closeErr != nil {
			_ = os.Remove(dst)
			_ = src.Close()
			return scriptpkg.SegmentAssetCandidate{}, closeErr
		}
		f.writes.Add(1)
	}
	_ = src.Close()
	candidate := artifact.Candidate
	candidate.AssetID = "live-" + strings.TrimSuffix(name, ".jpg")
	candidate.LocalPath, candidate.ContentSHA256 = dst, artifact.ContentSHA256
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
	candidate.IndexStatus = "pending"
	candidate.DriveLink = "file://" + dst
	return candidate, nil
}

var _ scriptports.VidRushAssetProvider = (*livePersistingImageProvider)(nil)
var _ scriptports.VidRushArtifactFinalizer = (*liveLocalAssetFinalizer)(nil)
var _ io.Reader = (*os.File)(nil)
