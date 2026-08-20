package adapters

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/entitycatalog"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type integrationEntityImageCatalog struct {
	mu         sync.Mutex
	entities   map[string]entitycatalog.Entity
	candidates map[int64]entitycatalog.Candidate
	nextID     int64
	materials  map[int64]entitycatalog.Materialization
}

func newIntegrationEntityImageCatalog() *integrationEntityImageCatalog {
	return &integrationEntityImageCatalog{
		entities: map[string]entitycatalog.Entity{}, candidates: map[int64]entitycatalog.Candidate{}, materials: map[int64]entitycatalog.Materialization{},
	}
}

func (c *integrationEntityImageCatalog) UpsertEntity(_ context.Context, entity entitycatalog.Entity) error {
	identity, err := entitycatalog.CanonicalizePersonIdentity(entity.CanonicalName, entity.CanonicalEntityID)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entity.CanonicalEntityID, entity.EntityType, entity.CanonicalName = identity.CanonicalEntityID, entitycatalog.EntityTypePerson, identity.CanonicalName
	c.entities[identity.CanonicalEntityID] = entity
	return nil
}

func (c *integrationEntityImageCatalog) GetEntity(_ context.Context, id string) (*entitycatalog.Entity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entity, ok := c.entities[entitycatalog.NormalizePersonEntityID(id)]
	if !ok {
		return nil, entitycatalog.ErrEntityNotFound
	}
	return &entity, nil
}

func (c *integrationEntityImageCatalog) SetRefreshState(_ context.Context, id, status string, refreshedAt time.Time, lastError string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entity, ok := c.entities[id]
	if !ok {
		return entitycatalog.ErrEntityNotFound
	}
	entity.RefreshStatus, entity.LastRefreshAt, entity.LastError = status, refreshedAt, lastError
	c.entities[id] = entity
	return nil
}

func (c *integrationEntityImageCatalog) UpsertCandidate(_ context.Context, candidate entitycatalog.Candidate) (int64, error) {
	if err := entitycatalog.ValidateCandidate(candidate); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	candidate.CanonicalEntityID = entitycatalog.NormalizePersonEntityID(candidate.CanonicalEntityID)
	for id, existing := range c.candidates {
		if existing.CanonicalEntityID == candidate.CanonicalEntityID && existing.Provider == candidate.Provider && existing.SourceURL == candidate.SourceURL {
			candidate.ID = id
			if existing.Status == entitycatalog.CandidateStatusRetired {
				candidate.Status = entitycatalog.CandidateStatusRetired
			}
			c.candidates[id] = candidate
			return id, nil
		}
	}
	c.nextID++
	candidate.ID = c.nextID
	c.candidates[c.nextID] = candidate
	return candidate.ID, nil
}

func (c *integrationEntityImageCatalog) SetCandidateStatus(_ context.Context, candidateID int64, status string) error {
	if err := entitycatalog.ValidateCandidateStatus(status); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	candidate, ok := c.candidates[candidateID]
	if !ok {
		return entitycatalog.ErrCandidateNotFound
	}
	candidate.Status = status
	c.candidates[candidateID] = candidate
	return nil
}

func (c *integrationEntityImageCatalog) ListCandidates(_ context.Context, id string, limit int) ([]entitycatalog.Candidate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id = entitycatalog.NormalizePersonEntityID(id)
	if _, ok := c.entities[id]; !ok {
		return nil, entitycatalog.ErrEntityNotFound
	}
	out := make([]entitycatalog.Candidate, 0)
	for _, candidate := range c.candidates {
		if candidate.CanonicalEntityID == id {
			out = append(out, candidate)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (c *integrationEntityImageCatalog) UpsertMaterialization(_ context.Context, materialization entitycatalog.Materialization) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.materials[materialization.CandidateID] = materialization
	return nil
}

func (c *integrationEntityImageCatalog) GetMaterialization(_ context.Context, candidateID int64) (*entitycatalog.Materialization, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	materialization, ok := c.materials[candidateID]
	if !ok {
		return nil, nil
	}
	return &materialization, nil
}

var _ entitycatalog.Repository = (*integrationEntityImageCatalog)(nil)

type catalogIntegrationSearcher struct {
	calls atomic.Int32
}

func (s *catalogIntegrationSearcher) SearchImages(_ context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls.Add(1)
	return []scriptpkg.SegmentAssetCandidate{
		{AssetID: "provider-1", Provider: scriptpkg.VidRushProviderInternetImages, Query: req.Query, Entity: req.Entity, SourceURL: "https://images.example/" + req.Query + "/1.jpg"},
		{AssetID: "provider-2", Provider: scriptpkg.VidRushProviderInternetImages, Query: req.Query, Entity: req.Entity, SourceURL: "https://images.example/" + req.Query + "/2.jpg"},
	}, nil
}

func resetEntityImageCatalogCaches() {
	entityImageCache = sync.Map{}
	entityImageLocks = sync.Map{}
	vidrushImageCache = sync.Map{}
}

func catalogPersonPlan() *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		Topic: "people",
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			Extraction:     mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{Enabled: true, EntityTypes: []string{"PERSON"}}},
		},
	}
}

func catalogPersonInput(segmentID, name string) ProcessInput {
	return ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
			ID: segmentID, SegmentID: segmentID,
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{CanonicalName: name, Text: name, Type: "PERSON"}}},
		}}},
		VidRushSegments: []scriptpkg.VidRushSegmentResult{{SegmentID: segmentID, SceneID: segmentID, TextHash: segmentID}},
	}
}

func seedCatalogPerson(t *testing.T, repo *integrationEntityImageCatalog, name string, urls ...string) {
	t.Helper()
	identity, err := entitycatalog.CanonicalizePersonName(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertEntity(context.Background(), entitycatalog.Entity{CanonicalEntityID: identity.CanonicalEntityID, EntityType: entitycatalog.EntityTypePerson, CanonicalName: identity.CanonicalName}); err != nil {
		t.Fatal(err)
	}
	for i, url := range urls {
		if _, err := repo.UpsertCandidate(context.Background(), entitycatalog.Candidate{CanonicalEntityID: identity.CanonicalEntityID, Provider: "duckduckgo", Rank: i + 1, SourceURL: url, Status: entitycatalog.CandidateStatusActive, SemanticStatus: entitycatalog.CandidateSemanticAccepted}); err != nil {
			t.Fatal(err)
		}
	}
}

func seedCatalogStatuses(t *testing.T, repo *integrationEntityImageCatalog, name string, statuses []string) {
	t.Helper()
	identity, err := entitycatalog.CanonicalizePersonName(name)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(statuses) {
		t.Fatalf("seeded rows = %d, statuses = %d", len(rows), len(statuses))
	}
	for i, status := range statuses {
		if err := repo.SetCandidateStatus(context.Background(), rows[i].ID, status); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInternetImagesProcessorUsesFreshAndStalePoolWithoutRefresh(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	urls := make([]string, 10)
	for i := range urls {
		urls[i] = "https://images.example/mj/pool-" + string(rune('a'+i)) + ".jpg"
	}
	seedCatalogPerson(t, repo, "Michael Jordan", urls...)
	statuses := []string{
		entitycatalog.CandidateStatusFresh, entitycatalog.CandidateStatusFresh,
		entitycatalog.CandidateStatusFresh, entitycatalog.CandidateStatusFresh,
		entitycatalog.CandidateStatusStale, entitycatalog.CandidateStatusStale,
		entitycatalog.CandidateStatusStale, entitycatalog.CandidateStatusStale,
		entitycatalog.CandidateStatusBroken, entitycatalog.CandidateStatusBroken,
	}
	seedCatalogStatuses(t, repo, "Michael Jordan", statuses)
	searcher := &catalogIntegrationSearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)

	result, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("pool-sufficient", "Michael Jordan"))
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 0 {
		t.Fatalf("provider calls = %d, want 0 with eight usable fresh/stale URLs", got)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 8 {
		t.Fatalf("usable fallback pool = %d, want 8; broken URLs must be excluded", got)
	}
}

func TestInternetImagesProcessorRefreshesInsufficientPoolAndKeepsFallback(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	urls := make([]string, 10)
	for i := range urls {
		urls[i] = "https://images.example/mj/refresh-" + string(rune('a'+i)) + ".jpg"
	}
	seedCatalogPerson(t, repo, "Michael Jordan", urls...)
	statuses := []string{
		entitycatalog.CandidateStatusBroken, entitycatalog.CandidateStatusBroken,
		entitycatalog.CandidateStatusBroken, entitycatalog.CandidateStatusBroken,
		entitycatalog.CandidateStatusBroken, entitycatalog.CandidateStatusBroken,
		entitycatalog.CandidateStatusBroken,
		entitycatalog.CandidateStatusStale, entitycatalog.CandidateStatusStale, entitycatalog.CandidateStatusStale,
	}
	seedCatalogStatuses(t, repo, "Michael Jordan", statuses)
	searcher := &catalogIntegrationSearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)

	result, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("pool-insufficient", "Michael Jordan"))
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 for insufficient pool", got)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 5 {
		t.Fatalf("merged candidates = %d, want 3 stale fallback + 2 refreshed results", got)
	}
	identity, _ := entitycatalog.CanonicalizePersonName("Michael Jordan")
	fresh := 0
	for _, candidate := range repo.candidates {
		if candidate.CanonicalEntityID == identity.CanonicalEntityID && candidate.Status == entitycatalog.CandidateStatusFresh {
			fresh++
		}
	}
	if fresh != 2 {
		t.Fatalf("fresh catalog candidates after refresh = %d, want 2", fresh)
	}
}

func TestInternetImagesProcessorCatalogHitSkipsProvider(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan", "https://images.example/mj/1.jpg", "https://images.example/mj/2.jpg")
	searcher := &catalogIntegrationSearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)

	result, err := processor.Process(context.Background(), catalogPersonPlan(), catalogPersonInput("mj-1", "MICHAEL   JORDAN"))
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 0 {
		t.Fatalf("provider calls = %d, want 0 on catalog hit", got)
	}
	if got := len(result.VidRushSegments[0].Assets.SecondaryImages); got != 2 {
		t.Fatalf("catalog candidates = %d, want 2", got)
	}
}

func TestInternetImagesProcessorCatalogMissPopulatesAndReusesCanonicalEntity(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &catalogIntegrationSearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)
	plan := catalogPersonPlan()

	if _, err := processor.Process(context.Background(), plan, catalogPersonInput("mj-1", "Michael Jordan")); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), plan, catalogPersonInput("mj-2", " michael   jordan ")); err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 after catalog population", got)
	}
	identity, _ := entitycatalog.CanonicalizePersonName("Michael Jordan")
	candidates, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("catalog candidates = %d, err=%v; want 2", len(candidates), err)
	}
}

func TestInternetImagesProcessorKeepsMichaelBJordanDistinct(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &catalogIntegrationSearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)
	plan := catalogPersonPlan()
	if _, err := processor.Process(context.Background(), plan, catalogPersonInput("mj", "Michael Jordan")); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Process(context.Background(), plan, catalogPersonInput("mbj", "Michael B. Jordan")); err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 distinct PERSON identities", got)
	}
	jordan, _ := entitycatalog.CanonicalizePersonName("Michael Jordan")
	bJordan, _ := entitycatalog.CanonicalizePersonName("Michael B. Jordan")
	jCandidates, _ := repo.ListCandidates(context.Background(), jordan.CanonicalEntityID, 10)
	bCandidates, _ := repo.ListCandidates(context.Background(), bJordan.CanonicalEntityID, 10)
	if len(jCandidates) != 2 || len(bCandidates) != 2 || jordan.CanonicalEntityID == bJordan.CanonicalEntityID {
		t.Fatalf("identity pools: Jordan=%d BJordan=%d ids=%q/%q", len(jCandidates), len(bCandidates), jordan.CanonicalEntityID, bJordan.CanonicalEntityID)
	}
}

func TestInternetImagesProcessorCatalogLockSharesFirstPopulation(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	searcher := &catalogIntegrationSearcher{}
	processor := NewInternetImagesProcessorWithCatalog(searcher, nil, repo)
	plan := catalogPersonPlan()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = processor.Process(context.Background(), plan, catalogPersonInput("concurrent-"+string(rune('a'+i)), "Michael Jordan"))
		}(i)
	}
	wg.Wait()
	if got := searcher.calls.Load(); got != 1 {
		t.Fatalf("concurrent provider calls = %d, want 1", got)
	}
}

func TestVidRushProviderFanoutCatalogHitSkipsProvider(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan", "https://images.example/mj/fanout.jpg")
	searcher := &catalogIntegrationSearcher{}
	fanout := NewVidRushProviderFanoutWithCatalog(nil, searcher, nil, repo)
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled}}}
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "fanout", TextHash: "fanout-hash", Insights: scriptpkg.SegmentInsights{
		Entities: []scriptpkg.ExtractedEntity{{Value: "Michael Jordan", Type: "PERSON"}}, ImageQueries: []string{"Michael Jordan"},
	}}
	result, err := fanout.ResolveProviders(context.Background(), plan, segment)
	if err != nil {
		t.Fatal(err)
	}
	if got := searcher.calls.Load(); got != 0 {
		t.Fatalf("fanout provider calls = %d, want 0 on catalog hit", got)
	}
	if len(result.Assets.SecondaryImages) != 1 {
		t.Fatalf("fanout catalog candidates = %d, want 1", len(result.Assets.SecondaryImages))
	}
}
