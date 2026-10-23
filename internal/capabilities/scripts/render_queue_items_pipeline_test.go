package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/render"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// pipeliningRenderClient models the ONE property the per-item enqueue path is
// supposed to have: more than one render may be in flight at once.
//
// Every render is held at the queue until `want` of them have been submitted.
// A submission that blocks on its own terminal state before submitting the next
// one can therefore never reach `want`, so this fixture distinguishes a
// pipelined enqueue from a serialised one instead of merely observing that both
// eventually return an artifact.
type pipeliningRenderClient struct {
	want int

	mu       sync.Mutex
	submits  int
	started  int
	inFlight int
	peak     int
	// metricPeak is the highest value the in-flight GAUGE was read at while a
	// render was still held. Sampling it here, inside Submit, is what makes the
	// observation deterministic: the gauge is incremented before Submit and
	// decremented only after Get returns, so at the Nth Submit at least N
	// renders are in flight. Polling the gauge from the test goroutine instead
	// races the batch, which can finish before the first sample.
	metricPeak float64
	release    chan struct{}
	once       sync.Once
}

func newPipeliningRenderClient(want int) *pipeliningRenderClient {
	return &pipeliningRenderClient{want: want, release: make(chan struct{})}
}

func (c *pipeliningRenderClient) Submit(_ context.Context, _ RenderQueueJob) error {
	c.mu.Lock()
	c.submits++
	c.started++
	c.inFlight++
	if c.inFlight > c.peak {
		c.peak = c.inFlight
	}
	if v := testutil.ToFloat64(observability.OverlayItemRenderInFlight); v > c.metricPeak {
		c.metricPeak = v
	}
	reached := c.started >= c.want
	c.mu.Unlock()
	if reached {
		c.once.Do(func() { close(c.release) })
	}
	return nil
}

func (c *pipeliningRenderClient) Get(ctx context.Context, id string) (RenderQueueJob, error) {
	select {
	case <-c.release:
	case <-ctx.Done():
		return RenderQueueJob{}, ctx.Err()
	}
	c.mu.Lock()
	c.inFlight--
	c.mu.Unlock()
	return RenderQueueJob{ID: id, State: RenderQueueStateCompleted}, nil
}

func (c *pipeliningRenderClient) snapshot() (submits, peak int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.submits, c.peak
}

// inFlightMetricPeak returns the highest in-flight gauge value observed while
// a render was held.
func (c *pipeliningRenderClient) inFlightMetricPeak() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.metricPeak
}

// completingRenderQueueClient accepts every submission and reports it terminal
// on the first status read, so a test can inspect the plans that were submitted
// without exercising the wait cadence.
type completingRenderQueueClient struct{}

func (c *completingRenderQueueClient) Submit(_ context.Context, _ RenderQueueJob) error { return nil }

func (c *completingRenderQueueClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	return RenderQueueJob{ID: id, State: RenderQueueStateCompleted}, nil
}

// overlayPlanWithPhrases returns the golden plan plus `extra` additional phrase
// items, so a test can drive more items than the golden plan's three renderable
// layers.
func overlayPlanWithPhrases(extra int) capoverlay.OverlayPlan {
	plan := caprender.GoldenOverlayPlanV1()
	for i := 0; i < extra; i++ {
		plan.Items = append(plan.Items, capoverlay.OverlayItem{
			ID:         fmt.Sprintf("phrase_extra_%d", i+1),
			TemplateID: "IMPORTANT_PHRASE",
			StartMs:    int64(1000 + i*1000),
			EndMs:      int64(1500 + i*1000),
			Text:       fmt.Sprintf("EXTRA PHRASE %d", i+1),
		})
	}
	return plan
}

func TestMarshalRenderingGenOverlayPlanProjectsMicrosecondTiming(t *testing.T) {
	plan := caprender.GoldenOverlayPlanV1()
	plan.Items = append(plan.Items, capoverlay.OverlayItem{
		ID: "canonical-timing", TemplateID: "IMPORTANT_PHRASE",
		StartMs: 101, EndMs: 207, StartUS: 100_500, DurationUS: 105_500,
		Text: "CERTIFIED TIMING",
	})
	raw, err := marshalRenderingGenOverlayPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, item := range wire.Items {
		var id string
		if err := json.Unmarshal(item["id"], &id); err != nil {
			t.Fatal(err)
		}
		if id != "canonical-timing" {
			continue
		}
		var startUS, durationUS int64
		if err := json.Unmarshal(item["start_us"], &startUS); err != nil {
			t.Fatalf("start_us missing or invalid: %v", err)
		}
		if err := json.Unmarshal(item["duration_us"], &durationUS); err != nil {
			t.Fatalf("duration_us missing or invalid: %v", err)
		}
		if startUS != 100_500 || durationUS != 105_500 {
			t.Fatalf("canonical timing = [%d,+%d), want [100500,+105500)", startUS, durationUS)
		}
		var durationMS int64
		if err := json.Unmarshal(item["duration_ms"], &durationMS); err != nil {
			t.Fatalf("legacy duration_ms projection missing or invalid: %v", err)
		}
		if durationMS != 106 {
			t.Fatalf("duration_ms = %d, want ceil(end_ms-start_ms) = 106", durationMS)
		}
		return
	}
	t.Fatal("wire plan lost canonical-timing item")
}

func TestMarshalRenderingGenOverlayPlanOmitsGeocodingScopeFromMapPins(t *testing.T) {
	plan := newTestMapPlan("map-scope-wire")
	plan.Items[0].Map.Pins[0].Scope = "city"
	plan.Items[0].Map.AreaGlowRadiusKM = 1.5
	raw, err := marshalRenderingGenOverlayPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Items []struct {
			Map struct {
				Pins             []map[string]json.RawMessage `json:"pins"`
				AreaGlowRadiusKM json.RawMessage              `json:"area_glow_radius_km"`
			} `json:"map"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Items) != 1 || len(wire.Items[0].Map.Pins) != 1 {
		t.Fatalf("wire map pins = %#v", wire.Items)
	}
	if _, present := wire.Items[0].Map.Pins[0]["scope"]; present {
		t.Fatal("geocoding scope guides PipelineGen map selection but is not RenderingGen overlay-plan.v1 data")
	}
	if len(wire.Items[0].Map.AreaGlowRadiusKM) != 0 {
		t.Fatal("dynamic-map-only area glow is not supported by the shared semantic renderer contract")
	}
}

// TestSeparateOverlayItemsPipelineMultipleRendersInFlight pins the measured
// bottleneck this path used to have. The sequential body submitted one child
// plan and blocked on its terminal state before submitting the next, so exactly
// one render was ever in flight and the GPU lane could not overlap the per-item
// pre/post chain (materialize, upload, probe, publish). On the 5+5 production
// run that showed up as 44.0 s of wall for 22.7 s of render work.
//
// The fixture makes the difference visible rather than assumed: with the pool
// disabled the SAME plan and the SAME client cannot finish at all.
func TestSeparateOverlayItemsPipelineMultipleRendersInFlight(t *testing.T) {
	plan := overlayPlanWithPhrases(2)     // 5 renderable items, 1 background skipped
	plan.RequireDriveBeforeReturn = false // ordinary generated overlay path
	// The goal is the width this batch actually runs at, resolved by the SAME
	// authority the enqueue path uses. It is deliberately not a literal: the
	// certified width follows the feed-width retune (2026-10-23, receipt off
	// the render lane) and a literal would pin a contract the retune owns.
	const inFlightGoal = defaultSeparateItemRenderWorkers

	t.Run("pooled enqueue reaches the in-flight goal", func(t *testing.T) {
		client := newPipeliningRenderClient(inFlightGoal)
		enqueuer, err := NewQueueRenderEnqueuer(client)
		if err != nil {
			t.Fatal(err)
		}
		enqueuer.SetSeparateItemRenders(true)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
		if err != nil {
			t.Fatalf("pooled enqueue failed: %v", err)
		}
		if len(ref.Items) != 5 {
			t.Fatalf("published items = %d, want 5", len(ref.Items))
		}
		submits, peak := client.snapshot()
		if submits != 5 {
			t.Fatalf("submits = %d, want one per renderable item (5)", submits)
		}
		if peak < inFlightGoal {
			t.Fatalf("peak concurrent renders = %d, want >= %d: the enqueue is still serialising", peak, inFlightGoal)
		}
		if want := resolveOverlayItemWorkers(defaultSeparateItemRenderWorkers); inFlightGoal != want {
			t.Fatalf("fixture goal = %d, want the resolved width %d (the fixture must track the certified policy)", inFlightGoal, want)
		}
		t.Logf("measured peak concurrent overlay renders = %d (sequential ceiling is 1)", peak)
	})

	t.Run("a serialised pool cannot reach it", func(t *testing.T) {
		client := newPipeliningRenderClient(inFlightGoal)
		enqueuer, err := NewQueueRenderEnqueuer(client)
		if err != nil {
			t.Fatal(err)
		}
		enqueuer.SetSeparateItemRenders(true)
		enqueuer.SetItemRenderPool(1)

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if _, err := enqueuer.EnqueueChrononPlan(ctx, plan); err == nil {
			t.Fatal("a single-slot pool reached the in-flight goal; the fixture cannot detect serialisation")
		}
		if submits, _ := client.snapshot(); submits != 1 {
			t.Fatalf("submits = %d, want 1: a serialised enqueue must not submit the next item while one is in flight", submits)
		}
	})
}

// TestSeparateOverlayItemsPreservePlanOrderAndIndices pins that concurrency does
// not renumber or reorder the artifacts. The child plan identity embeds the
// item's position in the parent plan, so the published receipts must still
// appear in plan order and each child must keep its ORIGINAL index — a work
// pool that renumbers from zero would produce a matrix whose plan ids no longer
// match the plan the caller asked for.
func TestSeparateOverlayItemsPreservePlanOrderAndIndices(t *testing.T) {
	plan := overlayPlanWithPhrases(2)
	plan.RequireDriveBeforeReturn = false
	enqueuer, err := NewQueueRenderEnqueuer(&completingRenderQueueClient{})
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetSeparateItemRenders(true)

	ref, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}

	wantIDs := []string{
		"important_phrase", "important_word", "image_overlay", "phrase_extra_1", "phrase_extra_2",
	}
	if len(ref.Items) != len(wantIDs) {
		t.Fatalf("published items = %d, want %d (the BACKGROUND item must never become its own video)", len(ref.Items), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got := ref.Items[i].ItemID; got != want {
			t.Fatalf("item[%d] = %q, want %q (order must follow the plan)", i, got, want)
		}
	}
	// The indices are the ones the parent plan assigned, not the pool's own
	// worker indices: background is index 0 and is skipped, so the first
	// published render is item 001.
	for i, want := range []string{"001", "002", "003", "004", "005"} {
		if !strings.Contains(ref.Items[i].JobID, ":item:"+want+":") {
			t.Fatalf("item[%d] job id %q lost its original plan index %s", i, ref.Items[i].JobID, want)
		}
	}
	if ref.JobID != ref.Items[0].JobID {
		t.Fatalf("returned reference job id = %q, want the first item %q", ref.JobID, ref.Items[0].JobID)
	}
}

// failingItemRenderClient fails exactly one renderable item by name, leaving the
// others completed, so the test proves the failure is attributed to the item it
// came from rather than to the batch.
type failingItemRenderClient struct {
	failContaining string
}

func (c *failingItemRenderClient) Submit(_ context.Context, _ RenderQueueJob) error { return nil }

func (c *failingItemRenderClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	if strings.Contains(id, c.failContaining) {
		return RenderQueueJob{ID: id, State: RenderQueueStateFailed, FailReason: "chronon exploded"}, nil
	}
	return RenderQueueJob{ID: id, State: RenderQueueStateCompleted}, nil
}

// TestSeparateOverlayItemsFailureNamesTheItem pins error attribution on the
// pooled path: one failing overlay must surface as that overlay's failure, not
// as an anonymous batch error, because the operator's next step is to re-render
// exactly that item.
func TestSeparateOverlayItemsFailureNamesTheItem(t *testing.T) {
	plan := overlayPlanWithPhrases(2)
	plan.RequireDriveBeforeReturn = false
	enqueuer, err := NewQueueRenderEnqueuer(&failingItemRenderClient{failContaining: "phrase_extra_1"})
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetSeparateItemRenders(true)

	_, err = enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err == nil {
		t.Fatal("a failed overlay render must fail the enqueue")
	}
	if !strings.Contains(err.Error(), `render overlay item "phrase_extra_1"`) {
		t.Fatalf("error must name the failing item, got %v", err)
	}
	if !strings.Contains(err.Error(), "chronon exploded") {
		t.Fatalf("error must keep the queue's failure reason, got %v", err)
	}
}

// TestSeparateOverlayItemsExposeMeasuredPipeliningDepth certifies the metric an
// operator reads to judge the pool: the in-flight gauge must actually track the
// renders being awaited (so its peak is the achieved pipelining depth), the
// pool-size gauge must report the bound the batch ran under, and the in-flight
// gauge must return to zero — a gauge that only increments is a leak, not a
// measurement.
func TestSeparateOverlayItemsExposeMeasuredPipeliningDepth(t *testing.T) {
	// Same derivation as the pipeline test: the measured depth must be the width
	// the batch really ran at (policy cap included), not the configured one.
	const goal = defaultSeparateItemRenderWorkers
	plan := overlayPlanWithPhrases(2)
	client := newPipeliningRenderClient(goal)
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetSeparateItemRenders(true)

	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatalf("pooled enqueue failed: %v", err)
	}

	// The fixture reads the gauge while every render is still held (in-flight is
	// incremented before Submit and decremented only after Get returns), so the
	// peak it records is the depth the pool actually reached. A gauge that was
	// never incremented reads 0 here instead of passing on an assumption.
	if peak := client.inFlightMetricPeak(); peak < goal {
		t.Fatalf("measured in-flight peak = %v, want >= %d: the in-flight gauge never observed the pool depth", peak, goal)
	}

	if got := testutil.ToFloat64(observability.OverlayItemRenderInFlight); got != 0 {
		t.Fatalf("in-flight gauge = %v after the batch, want 0 (it must decrement, not leak)", got)
	}
	if got := testutil.ToFloat64(observability.OverlayItemRenderPoolSize); got != float64(defaultSeparateItemRenderWorkers) {
		t.Fatalf("pool-size gauge = %v, want the resolved default %d", got, defaultSeparateItemRenderWorkers)
	}
}

// TestOverlayItemInFlightWidthIsCapped pins the retuned width policy itself,
// independently of any queue: an oversized request is narrowed to the cap, a
// request at or below the cap is untouched, and batch depth is deliberately
// NOT an input. With the receipt verification off the render lane
// (TICKET-OVERLAY-THROUGHPUT fix 1) one lane needs ≈1,6 in-flight children to
// stay fed through the off-lane tail, so a depth-based clamp below the feed
// width starves the lanes and pins throughput at one serialized render
// service. The in-flight width — not the batch size — bounds RAM and queue
// pressure.
func TestOverlayItemInFlightWidthIsCapped(t *testing.T) {
	cases := []struct{ requested, want int }{
		{1, 1},
		{2, 2},
		{3, 3},
		{defaultSeparateItemRenderWorkers, defaultSeparateItemRenderWorkers},
		{overlayItemMaxWidth - 1, overlayItemMaxWidth - 1},
		{overlayItemMaxWidth, overlayItemMaxWidth},
		{overlayItemMaxWidth + 1, overlayItemMaxWidth},
		{12, overlayItemMaxWidth},
	}
	for _, tc := range cases {
		if got := resolveOverlayItemWorkers(tc.requested); got != tc.want {
			t.Errorf("resolveOverlayItemWorkers(%d) = %d, want %d", tc.requested, got, tc.want)
		}
	}
	// The default is the feed width of the certified dual-lane topology and
	// sits at or below the cap: the policy must never narrow it.
	if got := resolveOverlayItemWorkers(defaultSeparateItemRenderWorkers); got != defaultSeparateItemRenderWorkers {
		t.Fatalf("default pool = %d, want the feed width %d", got, defaultSeparateItemRenderWorkers)
	}
}

// TestItemRenderPoolNeverDisablesRendering pins the resolution rule: an enqueuer
// that never opted in (zero value) and a caller that passes a non-positive value
// must both still render every item exactly once.
func TestItemRenderPoolNeverDisablesRendering(t *testing.T) {
	if got := (&QueueRenderEnqueuer{}).itemRenderWorkers(); got != defaultSeparateItemRenderWorkers {
		t.Fatalf("zero-value pool = %d, want the default %d", got, defaultSeparateItemRenderWorkers)
	}
	enqueuer := &QueueRenderEnqueuer{}
	enqueuer.SetItemRenderPool(3)
	if got := enqueuer.itemRenderWorkers(); got != 3 {
		t.Fatalf("configured pool = %d, want 3", got)
	}
	enqueuer.SetItemRenderPool(0)
	if got := enqueuer.itemRenderWorkers(); got != defaultSeparateItemRenderWorkers {
		t.Fatalf("pool reset to 0 = %d, want the default %d", got, defaultSeparateItemRenderWorkers)
	}
	var nilEnqueuer *QueueRenderEnqueuer
	if got := nilEnqueuer.itemRenderWorkers(); got != defaultSeparateItemRenderWorkers {
		t.Fatalf("nil enqueuer pool = %d, want the default %d", got, defaultSeparateItemRenderWorkers)
	}
}
