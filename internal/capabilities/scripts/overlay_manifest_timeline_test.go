package scriptgeneration

// overlay_manifest_timeline_test.go exercises path (b) of the P5 A/B
// (TICKET-MANIFEST-IDENTITY): one certified artifact per timeline plus the
// manifest, behind the rollback flag, with manifest-digest retry idempotence
// and the per-item path preserved as the default.

import (
	"context"
	"strings"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func TestManifestTimelineEligibility(t *testing.T) {
	eligible, reason := manifestTimelineEligible(overlayPlanWithPhrases(2))
	if !eligible {
		t.Fatalf("golden overlay plan must be manifest-eligible, refused: %s", reason)
	}
	for name, mutate := range map[string]func(*capoverlay.OverlayPlan){
		"final-job handoff": func(p *capoverlay.OverlayPlan) { p.RequireDriveBeforeReturn = true },
		"source composite": func(p *capoverlay.OverlayPlan) {
			p.Source = &capoverlay.OverlaySource{AssetID: "s", SHA256: strings.Repeat("a", 64)}
		},
		"map item":        func(p *capoverlay.OverlayPlan) { p.Items[1].Map = &capoverlay.MapOverlay{} },
		"chain item":      func(p *capoverlay.OverlayPlan) { p.Items[1].Kind = string(capoverlay.KindPhraseChain) },
		"two scenes":      func(p *capoverlay.OverlayPlan) { p.Items[1].SceneID = "s1"; p.Items[2].SceneID = "s2" },
		"two captions":    func(p *capoverlay.OverlayPlan) { p.Items[1].EntityCaption = "A"; p.Items[2].EntityCaption = "B" },
		"single item":     func(p *capoverlay.OverlayPlan) { p.Items = p.Items[1:2] },
		"only background": func(p *capoverlay.OverlayPlan) { p.Items = p.Items[:1] },
	} {
		plan := overlayPlanWithPhrases(2)
		mutate(&plan)
		if ok, why := manifestTimelineEligible(plan); ok {
			t.Errorf("%s: plan must not be manifest-eligible", name)
		} else if strings.TrimSpace(why) == "" {
			t.Errorf("%s: refusal must name a reason", name)
		}
	}
}

func TestEnqueueOverlayManifestTimelineRendersOneArtifactAndManifest(t *testing.T) {
	enqueuer, _ := newManifestIdentityTestEnqueuer(t)
	client := &countingRenderQueueClient{artifact: certifiedOverlayArtifact()}
	enqueuer.client = client
	plan := overlayPlanWithPhrases(2)
	ref, err := enqueuer.enqueueOverlayManifestTimeline(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if client.submits != 1 {
		t.Fatalf("submits = %d, want 1 (single compositing, single encode)", client.submits)
	}
	if ref.Artifact == nil {
		t.Fatal("timeline reference must carry its certified artifact")
	}
	if ref.Manifest == nil {
		t.Fatal("timeline reference must carry its manifest")
	}
	manifest := ref.Manifest
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest rejected: %v", err)
	}
	if manifest.TimelineSHA256 != strings.ToLower(ref.Artifact.SHA256) {
		t.Fatalf("timeline_sha256 = %q, want the artifact identity %q", manifest.TimelineSHA256, ref.Artifact.SHA256)
	}
	if len(manifest.Entries) != 5 {
		t.Fatalf("manifest entries = %d, want 5", len(manifest.Entries))
	}
	// Order and z-order survive the manifest projection unchanged.
	wantIDs := []string{"important_phrase", "important_word", "image_overlay", "phrase_extra_1", "phrase_extra_2"}
	wantTracks := []int{0, 0, 1, 1, 1}
	for i, entry := range manifest.Entries {
		if entry.ItemID != wantIDs[i] {
			t.Fatalf("entry[%d] = %q, want %q (order must follow the plan)", i, entry.ItemID, wantIDs[i])
		}
		if entry.Track != wantTracks[i] {
			t.Fatalf("entry %q track = %d, want %d", entry.ItemID, entry.Track, wantTracks[i])
		}
	}
	if len(ref.Items) != 5 {
		t.Fatalf("item rows = %d, want 5 (one identity row per voice, one artifact)", len(ref.Items))
	}
	for i, row := range ref.Items {
		if row.ItemID != wantIDs[i] {
			t.Fatalf("item row %d = %q, want %q", i, row.ItemID, wantIDs[i])
		}
		if row.BatchID != manifest.Digest() {
			t.Fatalf("item row %q batch id must bind to the manifest digest", row.ItemID)
		}
	}
	if len(ref.Batches) != 1 {
		t.Fatalf("batch rows = %d, want 1", len(ref.Batches))
	}
	batch := ref.Batches[0]
	if batch.BatchID != manifest.Digest() {
		t.Fatalf("batch id = %q, want the manifest digest %q", batch.BatchID, manifest.Digest())
	}
	if len(batch.ItemIDs) != 5 || batch.Artifact != ref.Artifact {
		t.Fatalf("batch must bind all 5 voices to the single artifact")
	}
	if batch.StartUS > 667_000 || batch.EndUS < 4_533_000 {
		t.Fatalf("batch window [%d,%d) must cover the timeline", batch.StartUS, batch.EndUS)
	}
}

func TestEnqueueOverlayManifestTimelineRetryDoesNoRepeatedWork(t *testing.T) {
	enqueuer, cache := newManifestIdentityTestEnqueuer(t)
	client := &countingRenderQueueClient{artifact: certifiedOverlayArtifact()}
	enqueuer.client = client
	plan := overlayPlanWithPhrases(2)
	first, err := enqueuer.enqueueOverlayManifestTimeline(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if client.submits != 1 {
		t.Fatalf("first run submits = %d, want 1", client.submits)
	}
	rows := cache.storedCount()
	if rows == 0 {
		t.Fatal("first run must certify the composition")
	}

	// Retry the same manifest: same voices (item_id + content-hash) and same
	// placement ⇒ the composition is already certified. No render, no second
	// artifact.
	second, err := enqueuer.enqueueOverlayManifestTimeline(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if client.submits != 1 {
		t.Fatalf("retry submits = %d, want 1 (no repeated work)", client.submits)
	}
	if cache.storedCount() != rows {
		t.Fatalf("retry stored rows: %d → %d, want unchanged (no duplicate artifact)", rows, cache.storedCount())
	}
	if second.Artifact == nil || second.Artifact.SHA256 != first.Artifact.SHA256 {
		t.Fatalf("retry must reuse the certified artifact")
	}
	if second.Manifest == nil || second.Manifest.Digest() != first.Manifest.Digest() {
		t.Fatalf("retry must return the same manifest identity")
	}

	// One voice changes content: the manifest digest changes with it, so the
	// composition must be re-rendered exactly once.
	plan.Items[1].Text = "CHANGED CONTENT"
	third, err := enqueuer.enqueueOverlayManifestTimeline(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if client.submits != 2 {
		t.Fatalf("changed-voice submits = %d, want 2 (new content ⇒ new work)", client.submits)
	}
	if third.Manifest == nil || third.Manifest.Digest() == first.Manifest.Digest() {
		t.Fatal("changed voice must change the manifest identity")
	}
	fourth, err := enqueuer.enqueueOverlayManifestTimeline(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if client.submits != 2 || fourth.Artifact.SHA256 != third.Artifact.SHA256 {
		t.Fatalf("retried changed manifest must reuse its artifact (submits=%d)", client.submits)
	}
}

func TestEnqueueChrononPlanManifestTimelineDispatch(t *testing.T) {
	// Flag OFF (default): the per-item path runs unchanged and no manifest is
	// produced.
	perItem, _ := newManifestIdentityTestEnqueuer(t)
	perItemClient := &countingRenderQueueClient{artifact: certifiedOverlayArtifact()}
	perItem.client = perItemClient
	perItem.SetSeparateItemRenders(true)
	ref, err := perItem.EnqueueChrononPlan(context.Background(), overlayPlanWithPhrases(2))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Manifest != nil {
		t.Fatal("flag off must never take the manifest timeline path")
	}
	if perItemClient.submits <= 1 {
		t.Fatalf("flag off submits = %d, want the per-item path (>1)", perItemClient.submits)
	}

	// Flag ON: one artifact + manifest for an eligible plan.
	t.Setenv(OverlayManifestTimelineEnv, "1")
	manifestPath, _ := newManifestIdentityTestEnqueuer(t)
	manifestClient := &countingRenderQueueClient{artifact: certifiedOverlayArtifact()}
	manifestPath.client = manifestClient
	ref, err = manifestPath.EnqueueChrononPlan(context.Background(), overlayPlanWithPhrases(2))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Manifest == nil {
		t.Fatal("flag on must take the manifest timeline path for eligible plans")
	}
	if manifestClient.submits != 1 {
		t.Fatalf("flag on submits = %d, want 1", manifestClient.submits)
	}

	// Flag ON but ineligible (two scenes): fail closed to the per-item path.
	twoScenes := overlayPlanWithPhrases(2)
	twoScenes.Items[1].SceneID = "s1"
	twoScenes.Items[2].SceneID = "s2"
	fallback, _ := newManifestIdentityTestEnqueuer(t)
	fallbackClient := &countingRenderQueueClient{artifact: certifiedOverlayArtifact()}
	fallback.client = fallbackClient
	fallback.SetSeparateItemRenders(true)
	ref, err = fallback.EnqueueChrononPlan(context.Background(), twoScenes)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Manifest != nil {
		t.Fatal("ineligible plans must fall back to the per-item path")
	}
	if fallbackClient.submits <= 1 {
		t.Fatalf("fallback submits = %d, want the per-item path (>1)", fallbackClient.submits)
	}
}

// TestEnqueueChrononPlanFinalJobFailsClosedWithoutPublisher pins the handoff
// invariant the manifest contract must never weaken: a plan that requires
// Drive delivery fails — never silently proceeds — when no overlay publisher
// is configured.
func TestEnqueueChrononPlanFinalJobFailsClosedWithoutPublisher(t *testing.T) {
	enqueuer, _ := newManifestIdentityTestEnqueuer(t)
	plan := overlayPlanWithPhrases(0)
	plan.RequireDriveBeforeReturn = true
	_, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "requires Drive delivery but no overlay publisher is configured") {
		t.Fatalf("final-job handoff must fail closed without a publisher, got %v", err)
	}
}
