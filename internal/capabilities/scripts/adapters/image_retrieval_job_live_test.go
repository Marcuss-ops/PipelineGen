package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	sqliteinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/entitycatalog"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

type liveJobImageSearcher struct {
	delegate imagesapp.ImageSearcher
	calls    atomic.Int32
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
	return &liveJobImageSearcher{delegate: delegate}
}

func (s *liveJobImageSearcher) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	hits, err := s.delegate.Search(ctx, imagesapp.ImageFilter{SubjectID: req.Query, Limit: req.Limit})
	if err != nil {
		return nil, err
	}
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
//	Run with PERSON_CATALOG_LIVE=1 go test ./internal/capabilities/scripts/adapters \
//	  -run '^TestLiveImageRetrievalJobPersistsAndReusesAssets$' -v -count=1 -timeout 4m
func TestLiveImageRetrievalJobPersistsAndReusesAssets(t *testing.T) {
	if os.Getenv("PERSON_CATALOG_LIVE") != "1" {
		t.Skip("set PERSON_CATALOG_LIVE=1 to run the isolated live retrieval job")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	resetEntityImageCatalogCaches()
	root := t.TempDir()
	dbPath := filepath.Join(root, "job-catalog.db")
	db := openPersistentEntityImageCatalog(t, dbPath)
	repo := sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db)
	searcher := newLiveJobImageSearcher(t)
	plan := liveJobPlan()
	plan.ImagesPerScene = 2
	input := liveJobInput("job-trump-cold", "Donald Trump")

	// Exercise the semantic image-resolution stage used by a generation job.
	searchStage := NewMediaResolverImageStageWithCatalog(searcher, nil, repo)
	discoveryResult, err := searchStage.Process(ctx, plan, input)
	if err != nil {
		t.Fatalf("cold job search: %v", err)
	}
	if len(discoveryResult.VidRushSegments) != 1 {
		t.Fatalf("cold job discovery segments=%d, want 1", len(discoveryResult.VidRushSegments))
	}
	discovered := discoveryResult.VidRushSegments[0]
	if len(discovered.Assets.Candidates) == 0 {
		t.Fatal("cold job search returned no candidates")
	}
	// Search persists a larger reuse pool; this isolated job requests and
	// materializes only two candidates to keep the live write set bounded.
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
	rows, err := repo.ListCandidates(ctx, "person:donald-trump", 20)
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
	// process-local pools. Equivalent person spelling and topic must reuse it.
	resetEntityImageCatalogCaches()
	db = openPersistentEntityImageCatalog(t, dbPath)
	defer db.Close()
	repo = sqliteinfra.NewSQLiteEntityImageCatalogAdapter(db)
	replayInput := liveJobInput("job-trump-replay", "Donald Trump")
	replayInput.VidRushSegments[0].Text = "Donald Trump and the White House"
	replayPlan := liveJobPlan()
	replayPlan.Topic = "US presidents and Washington landmarks"
	replayDiscovery, err := NewMediaResolverImageStageWithCatalog(searcher, nil, repo).Process(ctx, replayPlan, replayInput)
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
