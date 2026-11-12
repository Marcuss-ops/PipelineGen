package scriptgeneration

// overlay_render_cache_retry_reuse_test.go pins the retry contract of the
// content-addressed overlay cache: a second attempt of the SAME plan (the
// Kaufmann r144 retry shape) must consume the certified artifact the first
// attempt produced and must not submit another render. Without this, every
// retry pays for an identical GPU encode again — the exact waste the cache
// exists to prevent.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// retryReuseCache is an in-memory OverlayRenderCache. It returns a miss until
// an artifact is stored, then serves that exact artifact, so a hit can only
// come from the first attempt's certified render.
type retryReuseCache struct {
	artifact RenderArtifact
	stored   int
	lookups  int
	hits     int
}

func (c *retryReuseCache) Lookup(context.Context, OverlayRenderCacheKey) (*RenderArtifact, bool, error) {
	c.lookups++
	if c.stored == 0 {
		return nil, false, nil
	}
	c.hits++
	artifact := c.artifact
	return &artifact, true, nil
}

func (c *retryReuseCache) Store(_ context.Context, _ OverlayRenderCacheKey, artifact RenderArtifact) error {
	c.artifact = artifact
	c.stored++
	return nil
}

func (c *retryReuseCache) Invalidate(context.Context, OverlayRenderCacheKey, string) error {
	return nil
}

// retryReuseQueueClient completes every submitted job with the certified
// artifact and counts the submissions, which is the observable proof that no
// second encode was requested.
type retryReuseQueueClient struct {
	submits  int
	artifact RenderArtifact
}

func (c *retryReuseQueueClient) Submit(context.Context, RenderQueueJob) error {
	c.submits++
	return nil
}

func (c *retryReuseQueueClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	artifact := c.artifact
	return RenderQueueJob{ID: id, State: RenderQueueStateCompleted, Artifact: &artifact}, nil
}

func TestRetryWithUnchangedPlanReusesCertifiedArtifactWithoutReencoding(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	artifact := RenderArtifact{
		SHA256:     strings.Repeat("a", 64),
		SizeBytes:  4096,
		URL:        "https://objects.example/objects/" + strings.Repeat("a", 64),
		DurationUS: 2_000_000,
		Width:      1920, Height: 1080, FPSNum: 30, FPSDen: 1,
	}
	client := &retryReuseQueueClient{artifact: artifact}
	cache := &retryReuseCache{}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatalf("NewQueueRenderEnqueuer: %v", err)
	}
	enqueuer.renderCache = cache
	enqueuer.renderCacheEpoch = "retry-reuse-epoch"
	enqueuer.SetPollInterval(time.Millisecond)

	plan := manifestTestPlan()
	// The queue wire carries millisecond timing; the manifest fixture names
	// only the microsecond windows the batch composer uses.
	for i := range plan.Items {
		plan.Items[i].StartMs = plan.Items[i].StartUS / 1000
		plan.Items[i].EndMs = (plan.Items[i].StartUS + plan.Items[i].DurationUS + 999) / 1000
	}
	first, err := enqueuer.enqueueChrononPlan(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if client.submits != 1 {
		t.Fatalf("first attempt submissions = %d, want 1", client.submits)
	}
	if cache.stored != 1 || first.Artifact == nil || first.Artifact.SHA256 != artifact.SHA256 {
		t.Fatalf("first attempt did not certify the artifact: stored=%d artifact=%+v", cache.stored, first.Artifact)
	}

	// The retry replays the exact same plan from a FRESH enqueuer (a new
	// attempt/process) sharing only the durable index. The cache key is
	// content-addressed (item content + plan context + epoch), so it must
	// resolve to the artifact the first attempt stored, and the client must
	// not see a second Submit.
	retryEnqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatalf("NewQueueRenderEnqueuer (retry): %v", err)
	}
	retryEnqueuer.renderCache = cache
	retryEnqueuer.renderCacheEpoch = "retry-reuse-epoch"
	retryEnqueuer.SetPollInterval(time.Millisecond)
	retry, err := retryEnqueuer.enqueueChrononPlan(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("retry with unchanged plan: %v", err)
	}
	if client.submits != 1 {
		t.Fatalf("retry re-encoded the unchanged plan: submissions = %d, want 1", client.submits)
	}
	if cache.hits != 1 {
		t.Fatalf("retry cache hits = %d, want 1 (the certified artifact must be reused)", cache.hits)
	}
	if retry.Artifact == nil || retry.Artifact.SHA256 != artifact.SHA256 || retry.Artifact.SizeBytes != artifact.SizeBytes {
		t.Fatalf("retry artifact = %+v, want the certified identity %s", retry.Artifact, artifact.SHA256)
	}
}
