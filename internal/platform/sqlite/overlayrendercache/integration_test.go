package overlayrendercache_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/overlayrendercache"
	migration "github.com/Marcuss-ops/PipelineGen/migrations/sqlite"
)

const integrationArtifactSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// countingQueueClient records submissions and returns one certified artifact.
// Submit count is the acceptance signal: a cache hit must not reach the queue.
type countingQueueClient struct {
	mu       sync.Mutex
	submits  int
	artifact scriptgen.RenderArtifact
}

func (c *countingQueueClient) Submit(context.Context, scriptgen.RenderQueueJob) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.submits++
	return nil
}

func (c *countingQueueClient) Get(_ context.Context, id string) (scriptgen.RenderQueueJob, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	artifact := c.artifact
	return scriptgen.RenderQueueJob{ID: id, State: scriptgen.RenderQueueStateCompleted, Artifact: &artifact}, nil
}

func (c *countingQueueClient) submitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.submits
}

// integrationPlan is the minimum single-item plan that exercises the real
// per-item enqueue path (the child plan carries a single RenderKey).
func integrationPlan(text string) capoverlay.OverlayPlan {
	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        "integration-plan",
		VideoID:       "integration-video",
		Width:         1920,
		Height:        1080,
		FPSNum:        30,
		FPSDen:        1,
		DurationMS:    3500,
		Items: []capoverlay.OverlayItem{{
			ID: "phrase-1", TemplateID: "IMPORTANT_PHRASE", Kind: "text_phrase",
			StartMs: 0, EndMs: 3500, StartUS: 0, DurationUS: 3_500_000,
			Text: text,
		}},
	}
	plan.Items[0].RenderKey = capoverlay.ComputeRenderKey(plan, plan.Items[0])
	return plan
}

func newIntegrationCache(t *testing.T) (*overlayrendercache.Cache, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:overlay-render-cache-integration?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(migration.OverlayRenderCacheDDL); err != nil {
		t.Fatalf("apply canonical overlay render cache DDL: %v", err)
	}
	cache, err := overlayrendercache.New(db)
	if err != nil {
		t.Fatal(err)
	}
	return cache, db
}

func newIntegrationEnqueuer(t *testing.T, client scriptgen.RenderQueueClient, cache scriptgen.OverlayRenderCache) *scriptgen.QueueRenderEnqueuer {
	t.Helper()
	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetSeparateItemRenders(true)
	enqueuer.SetOverlayRenderCache(cache, "integration-epoch")
	return enqueuer
}

// TestConsecutiveRunsReuseTheRealSQLiteCache is the end-to-end acceptance test
// for the reuse plane: the SQLite adapter is the production one (same embedded
// migration DDL), the enqueuer is the production one, and a SECOND enqueuer
// instance stands in for the next run/process. The second run must return the
// certified artifact WITHOUT calling Submit.
func TestConsecutiveRunsReuseTheRealSQLiteCache(t *testing.T) {
	t.Setenv(scriptgen.OverlayRenderCacheKillSwitch, "1")
	cache, db := newIntegrationCache(t)
	client := &countingQueueClient{artifact: scriptgen.RenderArtifact{
		ID: "render-job", URL: "https://store.example/objects/" + integrationArtifactSHA,
		SHA256: integrationArtifactSHA, SizeBytes: 4096, RenderMS: 900, EncodeMS: 200,
	}}
	ctx := context.Background()

	first, err := newIntegrationEnqueuer(t, client, cache).EnqueueChrononPlan(ctx, integrationPlan("CIAO MONDO"))
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if client.submitCount() != 1 {
		t.Fatalf("first run submits = %d, want 1", client.submitCount())
	}
	if first.Artifact == nil || first.Artifact.SHA256 != integrationArtifactSHA {
		t.Fatalf("first run artifact = %+v", first.Artifact)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM overlay_render_cache_entries`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("cache rows after first run = %d, want 1", rows)
	}

	// A NEW enqueuer instance: the next run must consult the durable index,
	// not in-memory state.
	second, err := newIntegrationEnqueuer(t, client, cache).EnqueueChrononPlan(ctx, integrationPlan("CIAO MONDO"))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if client.submitCount() != 1 {
		t.Fatalf("second run submits = %d, want 1 (cache hit must not Submit)", client.submitCount())
	}
	if second.Artifact == nil || second.Artifact.SHA256 != integrationArtifactSHA {
		t.Fatalf("second run artifact = %+v, want the cached certified locator", second.Artifact)
	}
	if second.Artifact.RenderMS != 0 || second.Artifact.EncodeMS != 0 {
		t.Fatalf("second run replayed the producer's phase timings: %+v", second.Artifact)
	}
	if rows := countRows(t, db); rows != 1 {
		t.Fatalf("cache rows after hit = %d, want 1", rows)
	}

	// Content is the key: different text is a different render.
	third, err := newIntegrationEnqueuer(t, client, cache).EnqueueChrononPlan(ctx, integrationPlan("TESTO DIVERSO"))
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if client.submitCount() != 2 {
		t.Fatalf("third run submits = %d, want 2 (different content must render)", client.submitCount())
	}
	if third.Artifact == nil || third.Artifact.SHA256 != integrationArtifactSHA {
		t.Fatalf("third run artifact = %+v", third.Artifact)
	}
	if rows := countRows(t, db); rows != 2 {
		t.Fatalf("cache rows after content change = %d, want 2", rows)
	}
}

func countRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM overlay_render_cache_entries`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestKillSwitchBypassesTheRealSQLiteCache proves the operator escape hatch at
// the integration level: the index exists, but a disabled cache still submits.
func TestKillSwitchBypassesTheRealSQLiteCache(t *testing.T) {
	cache, db := newIntegrationCache(t)
	client := &countingQueueClient{artifact: scriptgen.RenderArtifact{
		URL:    "https://store.example/objects/" + integrationArtifactSHA,
		SHA256: integrationArtifactSHA, SizeBytes: 4096,
	}}
	ctx := context.Background()
	plan := integrationPlan("CIAO MONDO")

	t.Setenv(scriptgen.OverlayRenderCacheKillSwitch, "1")
	if _, err := newIntegrationEnqueuer(t, client, cache).EnqueueChrononPlan(ctx, plan); err != nil {
		t.Fatalf("enabled run: %v", err)
	}
	t.Setenv(scriptgen.OverlayRenderCacheKillSwitch, "0")
	if _, err := newIntegrationEnqueuer(t, client, cache).EnqueueChrononPlan(ctx, plan); err != nil {
		t.Fatalf("disabled run: %v", err)
	}
	if client.submitCount() != 2 {
		t.Fatalf("disabled cache submits = %d, want 2", client.submitCount())
	}
	if rows := countRows(t, db); rows != 1 {
		t.Fatalf("cache rows = %d, want 1 (hit was not used)", rows)
	}
}
