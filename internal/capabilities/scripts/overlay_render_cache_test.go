package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

const cacheTestArtifactSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func cacheTestPlan() capoverlay.OverlayPlan {
	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        "plan-cache",
		VideoID:       "video-cache",
		Width:         1920,
		Height:        1080,
		FPSNum:        30,
		FPSDen:        1,
		DurationMS:    3500,
		Items: []capoverlay.OverlayItem{{
			ID: "phrase-1", TemplateID: "IMPORTANT_PHRASE", Kind: "text_phrase",
			StartMs: 0, EndMs: 3500, StartUS: 0, DurationUS: 3_500_000,
			Text: "CIAO MONDO", MotionParams: map[string]any{"enter_frames": 3},
		}},
	}
	plan.Items[0].RenderKey = capoverlay.ComputeRenderKey(plan, plan.Items[0])
	return plan
}

func TestOverlayRenderCacheKeyIsStableAndContentAddressed(t *testing.T) {
	plan := cacheTestPlan()
	key, ok := overlayRenderCacheKeyForPlan(plan, "epoch-1")
	if !ok {
		t.Fatal("single-item plan must produce a cache key")
	}
	if err := key.Validate(); err != nil {
		t.Fatalf("key validate: %v", err)
	}
	if key.RenderKey != plan.Items[0].RenderKey {
		t.Fatalf("key render key = %q, want the item render key %q", key.RenderKey, plan.Items[0].RenderKey)
	}
	again, ok := overlayRenderCacheKeyForPlan(plan, "epoch-1")
	if !ok || again != key || again.Digest() != key.Digest() {
		t.Fatalf("key is not stable: %+v vs %+v", key, again)
	}
	otherEpoch, _ := overlayRenderCacheKeyForPlan(plan, "epoch-2")
	if otherEpoch.Digest() == key.Digest() {
		t.Fatal("deployment epoch must participate in the cache identity")
	}
}

func TestOverlayRenderCacheKeyTracksItemAndPlanRenderInputs(t *testing.T) {
	base := cacheTestPlan()
	key, _ := overlayRenderCacheKeyForPlan(base, "epoch-1")

	cases := map[string]func(*capoverlay.OverlayPlan){
		"item text": func(p *capoverlay.OverlayPlan) {
			p.Items[0].Text = "DIVERSO"
			p.Items[0].RenderKey = ""
		},
		"camera motion": func(p *capoverlay.OverlayPlan) { p.Items[0].CameraMotionID = "slow_push" },
		"map backend": func(p *capoverlay.OverlayPlan) {
			// The backend is stamped after RenderKey is computed, so it can only
			// be caught by the plan-context digest.
			p.Items[0].Params = map[string]any{"map_backend": "native"}
		},
		"item kind": func(p *capoverlay.OverlayPlan) { p.Items[0].Kind = "chain" },
		"background": func(p *capoverlay.OverlayPlan) {
			p.Background = &capoverlay.OverlayBackground{Kind: "video", Fit: "cover", AssetRefs: []capoverlay.OverlayAssetRef{{SHA256: strings.Repeat("b", 64)}}}
		},
		"background opacity": func(p *capoverlay.OverlayPlan) {
			opacity := 0.5
			p.Background = &capoverlay.OverlayBackground{Kind: "color", Color: []float64{0, 0, 0}, Opacity: &opacity}
		},
		"animation policy": func(p *capoverlay.OverlayPlan) {
			p.AnimationPolicies = []capoverlay.AnimationPolicy{{Target: "text", GroupID: "g", MotionID: "fade"}}
		},
		"foreground scale": func(p *capoverlay.OverlayPlan) { p.ForegroundScalePercent = 85 },
		"media contract":   func(p *capoverlay.OverlayPlan) { p.MediaContract = "overlay/mp4-h264-v1" },
		"entity seed":      func(p *capoverlay.OverlayPlan) { p.EntityVariationSeed = "seed-7" },
		"renderer version": func(p *capoverlay.OverlayPlan) { p.RendererVersion = "chronon-next" },
		"canvas":           func(p *capoverlay.OverlayPlan) { p.Width = 1080 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			mutated := cacheTestPlan()
			mutate(&mutated)
			other, ok := overlayRenderCacheKeyForPlan(mutated, "epoch-1")
			if !ok {
				t.Fatal("mutated single-item plan must still produce a key")
			}
			if other.Digest() == key.Digest() {
				t.Fatalf("%s mutation left the cache identity unchanged", name)
			}
		})
	}
}

func TestOverlayRenderCacheKeyPinsEntityStyleSamplingToPlanIdentity(t *testing.T) {
	// The worker samples entity styles from plan id + video id + item id. Two
	// plans with identical item bytes therefore render different styles, so a
	// cached artifact from plan A must never satisfy plan B.
	planA := cacheTestPlan()
	planA.Items[0].EntityStyleID = "premium_random_v1"
	planA.Items[0].RenderKey = capoverlay.ComputeRenderKey(planA, planA.Items[0])
	planB := cacheTestPlan()
	planB.Items[0].EntityStyleID = "premium_random_v1"
	planB.Items[0].RenderKey = capoverlay.ComputeRenderKey(planB, planB.Items[0])
	planB.PlanID = "plan-cache-B"

	keyA, okA := overlayRenderCacheKeyForPlan(planA, "epoch-1")
	keyB, okB := overlayRenderCacheKeyForPlan(planB, "epoch-1")
	if !okA || !okB {
		t.Fatal("entity-style plans must still be cacheable for retries")
	}
	if keyA.Digest() == keyB.Digest() {
		t.Fatal("entity-style sampling identity must differ between plans")
	}

	// Without an entity style selector the same item content is genuinely
	// reusable across plans: that is the throughput win.
	plainA := cacheTestPlan()
	plainB := cacheTestPlan()
	plainB.PlanID = "plan-cache-B"
	plainKeyA, _ := overlayRenderCacheKeyForPlan(plainA, "epoch-1")
	plainKeyB, _ := overlayRenderCacheKeyForPlan(plainB, "epoch-1")
	if plainKeyA.Digest() != plainKeyB.Digest() {
		t.Fatal("plans without entity sampling should share a content-addressed key")
	}
}

func TestOverlayRenderCacheKeyAddressesMultiItemCompositionDeterministically(t *testing.T) {
	plan := cacheTestPlan()
	second := plan.Items[0]
	second.ID = "phrase-2"
	second.Text = "SECONDO"
	second.StartMs, second.StartUS = 1000, 1_000_000
	second.DurationUS, second.EndMs = 1_000_000, 2000
	second.RenderKey = capoverlay.ComputeRenderKey(plan, second)
	plan.Items = append(plan.Items, second)
	key, ok := overlayRenderCacheKeyForPlan(plan, "epoch-1")
	if !ok {
		t.Fatal("a complete multi-item composition must be cacheable")
	}
	repeated, ok := overlayRenderCacheKeyForPlan(plan, "epoch-1")
	if !ok || repeated.Digest() != key.Digest() {
		t.Fatal("composition cache key must be deterministic")
	}
	plan.Items[0], plan.Items[1] = plan.Items[1], plan.Items[0]
	reordered, ok := overlayRenderCacheKeyForPlan(plan, "epoch-1")
	if !ok || reordered.Digest() == key.Digest() {
		t.Fatal("item order must participate in the composition cache identity")
	}
}

func TestOverlayRenderCacheKeyRejectsIncompleteIdentity(t *testing.T) {
	for name, key := range map[string]OverlayRenderCacheKey{
		"empty":          {},
		"bad render key": {RenderKey: "short", PlanContext: strings.Repeat("a", 64), Epoch: "e"},
		"bad context":    {RenderKey: strings.Repeat("a", 64), PlanContext: "short", Epoch: "e"},
		"empty epoch":    {RenderKey: strings.Repeat("a", 64), PlanContext: strings.Repeat("b", 64)},
	} {
		if err := key.Validate(); err == nil {
			t.Errorf("%s key validated, want rejection", name)
		}
	}
}

func TestCachedOverlayArtifactCertification(t *testing.T) {
	good := &RenderArtifact{URL: "https://store.example/objects/" + cacheTestArtifactSHA, SHA256: cacheTestArtifactSHA, SizeBytes: 1024}
	if !validCachedOverlayArtifact(good) {
		t.Fatal("a complete certified locator must be valid")
	}
	for name, artifact := range map[string]*RenderArtifact{
		"nil":          nil,
		"missing sha":  {URL: good.URL, SizeBytes: 1},
		"short sha":    {URL: good.URL, SHA256: "abc", SizeBytes: 1},
		"zero size":    {URL: good.URL, SHA256: cacheTestArtifactSHA},
		"non-http url": {URL: "/objects/" + cacheTestArtifactSHA, SHA256: cacheTestArtifactSHA, SizeBytes: 1},
	} {
		if validCachedOverlayArtifact(artifact) {
			t.Errorf("%s artifact passed certification", name)
		}
	}
}

func TestSanitizeCachedOverlayArtifactDropsForeignMeasurements(t *testing.T) {
	artifact := &RenderArtifact{
		ID: "foreign-job", URL: "https://store.example/objects/" + cacheTestArtifactSHA, SHA256: cacheTestArtifactSHA,
		SizeBytes: 10, RenderMS: 900, EncodeMS: 300, MaterializeMS: 80, PlanMS: 40, ProbeMS: 20, HashMS: 10,
		UploadMS: 50, DrivePublishMS: 60,
	}
	clean := sanitizeCachedOverlayArtifact(artifact)
	if clean == artifact {
		t.Fatal("sanitize must return a copy")
	}
	if clean.RenderMS != 0 || clean.EncodeMS != 0 || clean.MaterializeMS != 0 || clean.PlanMS != 0 ||
		clean.ProbeMS != 0 || clean.HashMS != 0 || clean.UploadMS != 0 || clean.DrivePublishMS != 0 {
		t.Fatalf("cached artifact still carries foreign phase timings: %+v", clean)
	}
	if clean.ID != "" {
		t.Fatalf("cached artifact still carries the producing job id %q", clean.ID)
	}
	if artifact.RenderMS != 900 {
		t.Fatal("sanitize mutated the caller's artifact")
	}
}

// fakeOverlayRenderCache is an in-memory OverlayRenderCache with call counts.
type fakeOverlayRenderCache struct {
	mu          sync.Mutex
	entries     map[string]RenderArtifact
	invalidated map[string]string
	lookups     int
	stores      int
	lookupErr   error
	storeErr    error
}

func newFakeOverlayRenderCache() *fakeOverlayRenderCache {
	return &fakeOverlayRenderCache{entries: map[string]RenderArtifact{}, invalidated: map[string]string{}}
}

func (c *fakeOverlayRenderCache) Lookup(_ context.Context, key OverlayRenderCacheKey) (*RenderArtifact, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lookups++
	if c.lookupErr != nil {
		return nil, false, c.lookupErr
	}
	artifact, ok := c.entries[key.Digest()]
	if !ok {
		return nil, false, nil
	}
	copy := artifact
	return &copy, true, nil
}

func (c *fakeOverlayRenderCache) Store(_ context.Context, key OverlayRenderCacheKey, artifact RenderArtifact) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stores++
	if c.storeErr != nil {
		return c.storeErr
	}
	c.entries[key.Digest()] = artifact
	return nil
}

func (c *fakeOverlayRenderCache) Invalidate(_ context.Context, key OverlayRenderCacheKey, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key.Digest())
	c.invalidated[key.Digest()] = reason
	return nil
}

func (c *fakeOverlayRenderCache) storedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// onlyStoredKey returns the single key the cache holds. The enqueuer keys the
// CHILD plan it actually renders (its target duration differs from the parent
// item), so tests must read the key back instead of re-deriving it from the
// parent plan.
func (c *fakeOverlayRenderCache) onlyStoredKey(t *testing.T) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) != 1 {
		t.Fatalf("cache holds %d rows, want 1", len(c.entries))
	}
	for digest := range c.entries {
		return digest
	}
	return ""
}

// countingRenderQueueClient returns one certified artifact for every submit.
type countingRenderQueueClient struct {
	submits  int
	artifact RenderArtifact
	failNext bool
}

func (c *countingRenderQueueClient) Submit(_ context.Context, _ RenderQueueJob) error {
	c.submits++
	if c.failNext {
		c.failNext = false
		return errors.New("injected transient submit failure")
	}
	return nil
}

func (c *countingRenderQueueClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	return RenderQueueJob{ID: id, State: RenderQueueStateCompleted, Artifact: &c.artifact}, nil
}

func certifiedOverlayArtifact() RenderArtifact {
	return RenderArtifact{
		ID: "render-job", URL: "https://store.example/objects/" + cacheTestArtifactSHA,
		SHA256: cacheTestArtifactSHA, SizeBytes: 2048, RenderMS: 950, EncodeMS: 240,
	}
}

func cacheTestEnqueuer(t *testing.T, cache OverlayRenderCache) (*QueueRenderEnqueuer, *countingRenderQueueClient) {
	t.Helper()
	client := &countingRenderQueueClient{artifact: certifiedOverlayArtifact()}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetSeparateItemRenders(true)
	enqueuer.SetOverlayRenderCache(cache, "test-epoch")
	return enqueuer, client
}

func TestOverlayRenderCacheMissStoresThenSecondRunHitsWithoutSubmit(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	cache := newFakeOverlayRenderCache()
	enqueuer, client := cacheTestEnqueuer(t, cache)
	plan := cacheTestPlan()
	plan.RequireDriveBeforeReturn = true
	enqueuer.SetArtifactPublisher(&captureOverlayPublication{})

	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("first render: %v", err)
	}
	if client.submits != 1 {
		t.Fatalf("first run submits = %d, want 1", client.submits)
	}
	if cache.storedCount() != 1 {
		t.Fatalf("first run stored %d cache rows, want 1", cache.storedCount())
	}

	ref, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if client.submits != 1 {
		t.Fatalf("cache hit submitted again: submits = %d, want 1", client.submits)
	}
	if ref.Artifact == nil || ref.Artifact.SHA256 != cacheTestArtifactSHA {
		t.Fatalf("cache hit artifact = %+v, want the certified locator", ref.Artifact)
	}
	if ref.Artifact.RenderMS != 0 || ref.Artifact.MaterializeMS != 0 {
		t.Fatalf("cache hit replayed foreign phase timings: %+v", ref.Artifact)
	}
	if cache.stores != 1 {
		t.Fatalf("cache hit stored again: stores = %d, want 1", cache.stores)
	}
}

func TestOverlayRenderCacheTimedLookupSeparatesIndexAndObjectProbe(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	cache := newFakeOverlayRenderCache()
	enqueuer, _ := cacheTestEnqueuer(t, cache)
	key := OverlayRenderCacheKey{RenderKey: cacheTestArtifactSHA, PlanContext: cacheTestArtifactSHA, Epoch: "test-epoch"}
	if err := key.Validate(); err != nil {
		t.Fatal(err)
	}
	cache.entries[key.Digest()] = certifiedOverlayArtifact()
	enqueuer.SetOverlayArtifactProber(delayedPresentOverlayArtifactProber{})

	artifact, hit, timing := enqueuer.lookupOverlayRenderCacheTimed(context.Background(), key)
	if !hit || artifact == nil || timing.outcome != "hit" {
		t.Fatalf("timed lookup = hit=%v artifact=%+v outcome=%q", hit, artifact, timing.outcome)
	}
	if timing.lookup <= 0 || timing.probe < time.Millisecond {
		t.Fatalf("cache timings did not separate index and object probe: lookup=%s probe=%s", timing.lookup, timing.probe)
	}
}

func TestOverlayRenderCacheReusesCertifiedFusedBatchOnRetry(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	cache := newFakeOverlayRenderCache()
	enqueuer, client := cacheTestEnqueuer(t, cache)
	enqueuer.SetOverlayWindowBatching(true)
	plan := cacheTestPlan()
	plan.Items[0].SceneID = "scene-1"
	second := plan.Items[0]
	second.ID = "phrase-2"
	second.Text = "ALTRO TESTO"
	second.StartUS = 1_000_000
	second.DurationUS = 2_000_000
	second.StartMs = 1000
	second.EndMs = 3000
	second.RenderKey = capoverlay.ComputeRenderKey(plan, second)
	plan.Items = append(plan.Items, second)

	firstRef, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("first batch render: %v", err)
	}
	if len(firstRef.Batches) != 1 || len(firstRef.Batches[0].ItemIDs) != 2 {
		t.Fatalf("first reference did not preserve both batch members: %+v", firstRef)
	}
	if client.submits != 1 || cache.storedCount() != 1 {
		t.Fatalf("first render submits=%d stored=%d, want 1/1", client.submits, cache.storedCount())
	}
	secondRef, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("retry batch render: %v", err)
	}
	if client.submits != 1 {
		t.Fatalf("retry resubmitted a certified fused batch: submits=%d, want 1", client.submits)
	}
	if len(secondRef.Batches) != 1 || len(secondRef.Batches[0].ItemIDs) != 2 {
		t.Fatalf("cached retry lost batch membership: %+v", secondRef)
	}
}

func TestOverlayRenderCacheRetrySubmitsOnlyTheBatchThatFailed(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	cache := newFakeOverlayRenderCache()
	enqueuer, client := cacheTestEnqueuer(t, cache)
	enqueuer.SetOverlayWindowBatching(true)
	plan := cacheTestPlan()
	plan.DurationMS = 50_000
	plan.Items[0].SceneID = "scene-1"
	plan.Items[0].RenderKey = capoverlay.ComputeRenderKey(plan, plan.Items[0])
	makePhrase := func(id, scene, text string, startUS, durationUS int64) capoverlay.OverlayItem {
		item := plan.Items[0]
		item.ID, item.SceneID, item.Text = id, scene, text
		item.StartUS, item.DurationUS = startUS, durationUS
		item.StartMs, item.EndMs = startUS/1000, (startUS+durationUS)/1000
		item.RenderKey = ""
		item.RenderKey = capoverlay.ComputeRenderKey(plan, item)
		return item
	}
	plan.Items = append(plan.Items,
		makePhrase("phrase-2", "scene-1", "ANCORA UNO", 1_000_000, 2_000_000),
	)
	first, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("seed first certified batch: %v", err)
	}
	if len(first.Batches) != 1 || cache.storedCount() != 1 || client.submits != 1 {
		t.Fatalf("seed batch ref=%+v cache=%d submits=%d; want one certified batch", first.Batches, cache.storedCount(), client.submits)
	}

	plan.Items = append(plan.Items,
		makePhrase("phrase-3", "scene-2", "ALTRO TRE", 40_000_000, 2_000_000),
		makePhrase("phrase-4", "scene-2", "ALTRO QUATTRO", 41_000_000, 2_000_000),
	)
	client.failNext = true
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "injected transient submit failure") {
		t.Fatalf("first attempt with one failed batch error = %v", err)
	}
	if client.submits != 2 {
		t.Fatalf("first failed attempt submitted %d jobs, want only the uncached batch", client.submits-1)
	}

	retried, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("retry after one batch failure: %v", err)
	}
	if client.submits != 3 {
		t.Fatalf("resume submitted %d jobs total, want initial seed + failed batch + its retry", client.submits)
	}
	if len(retried.Batches) != 2 || cache.storedCount() != 2 {
		t.Fatalf("retry did not reconstruct both certified batch references: batches=%d cache=%d", len(retried.Batches), cache.storedCount())
	}
	if got := plan.Items[0].MotionParams["enter_frames"]; got != 3 {
		t.Fatalf("child-specific entrance mutated the parent/shared MotionParams map: got %v, want 3", got)
	}
}

func TestOverlayRenderCacheKillSwitchForcesFreshRender(t *testing.T) {
	cache := newFakeOverlayRenderCache()
	enqueuer, client := cacheTestEnqueuer(t, cache)
	plan := cacheTestPlan()

	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("first render: %v", err)
	}
	t.Setenv(OverlayRenderCacheKillSwitch, "0")
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("second render: %v", err)
	}
	if client.submits != 2 {
		t.Fatalf("disabled cache submits = %d, want 2 (no reuse)", client.submits)
	}
	if cache.lookups != 1 {
		t.Fatalf("cache lookups = %d, want only the enabled run's miss", cache.lookups)
	}
}

func TestOverlayRenderCacheInvalidLocatorIsMissAndInvalidated(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	cache := newFakeOverlayRenderCache()
	enqueuer, client := cacheTestEnqueuer(t, cache)
	plan := cacheTestPlan()
	plan.RequireDriveBeforeReturn = true
	enqueuer.SetArtifactPublisher(&captureOverlayPublication{})
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("seed render: %v", err)
	}
	digest := cache.onlyStoredKey(t)
	cache.entries[digest] = RenderArtifact{SHA256: cacheTestArtifactSHA, SizeBytes: 2048}

	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("render with an uncertifiable locator: %v", err)
	}
	if client.submits != 2 {
		t.Fatalf("invalid locator submits = %d, want a fresh render", client.submits)
	}
	if _, invalidated := cache.invalidated[digest]; !invalidated {
		t.Fatal("an uncertifiable locator must be invalidated")
	}
}

func TestOverlayRenderCacheProberMissingObjectForcesFreshRender(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	cache := newFakeOverlayRenderCache()
	enqueuer, client := cacheTestEnqueuer(t, cache)
	enqueuer.SetOverlayArtifactProber(missingOverlayArtifactProber{})
	plan := cacheTestPlan()

	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("first render: %v", err)
	}
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("second render: %v", err)
	}
	if client.submits != 2 {
		t.Fatalf("missing object submits = %d, want a re-render", client.submits)
	}
}

type missingOverlayArtifactProber struct{}

func (missingOverlayArtifactProber) Present(context.Context, RenderArtifact) (bool, error) {
	return false, nil
}

type delayedPresentOverlayArtifactProber struct{}

func (delayedPresentOverlayArtifactProber) Present(context.Context, RenderArtifact) (bool, error) {
	time.Sleep(2 * time.Millisecond)
	return true, nil
}

func TestOverlayRenderCacheHitThatFailsPublicationIsInvalidated(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	cache := newFakeOverlayRenderCache()
	enqueuer, client := cacheTestEnqueuer(t, cache)
	plan := cacheTestPlan()
	plan.RequireDriveBeforeReturn = true
	enqueuer.SetArtifactPublisher(&captureOverlayPublication{})
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("first render: %v", err)
	}
	digest := cache.onlyStoredKey(t)
	enqueuer.SetArtifactPublisher(failingOverlayPublisher{err: errors.New("drive upload failed")})

	_, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err == nil {
		t.Fatal("a failing publication on a cache hit must surface its error")
	}
	if client.submits != 1 {
		t.Fatalf("cache hit submitted again: submits = %d, want 1", client.submits)
	}
	if reason := cache.invalidated[digest]; !strings.Contains(reason, "drive upload failed") {
		t.Fatalf("invalidation reason = %q, want the publication failure", reason)
	}
}

type failingOverlayPublisher struct{ err error }

func (p failingOverlayPublisher) PublishOverlay(_ context.Context, _ OverlayPublicationSpec, _ *RenderArtifact) error {
	return p.err
}

func TestOverlayRenderCacheLookupErrorDegradesToRender(t *testing.T) {
	t.Setenv(OverlayRenderCacheKillSwitch, "1")
	cache := newFakeOverlayRenderCache()
	cache.lookupErr = fmt.Errorf("cache db unavailable")
	enqueuer, client := cacheTestEnqueuer(t, cache)

	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), cacheTestPlan()); err != nil {
		t.Fatalf("cache outage must degrade to a render: %v", err)
	}
	if client.submits != 1 {
		t.Fatalf("submits = %d, want 1", client.submits)
	}
}
