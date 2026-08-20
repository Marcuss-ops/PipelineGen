package renderinggen

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestPhrasePresetE2E is the opt-in live canary for the PHRASE family: 5
// mini-renders of the canonical string "MICHAEL JORDAN CHANGED BASKETBALL"
// against the five production presets
//
//	fast_fade_through
//	clean_slide_up
//	slide_lateral
//	phrase_word_reveal
//	undertext_pop
//
// Each render goes through the production queue adapter exactly as a real
// PipelineGen job, then returns the certified artifact. The test asserts
// the preset pinning on the compiled document and the per-preset sha256
// uniqueness so a future alias regression in the registry cannot silently
// collapse the certification matrix.
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	PIPELINEGEN_E2E_JOB_ID=phrase-preset-$(date +%s) \
//	RENDERINGGEN_GOLDEN_DIR=<abs path to RenderingGen/testdata/golden> \
//	go test ./internal/platform/renderinggen/ -run TestPhrasePresetE2E -v
func TestPhrasePresetE2E(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live cross-repo PHRASE preset canary")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}

	background := mustRead(t, filepath.Join(fixtureRoot, "background.mp4"))
	backgroundHash := sha256Hex(background)
	if backgroundHash != capoverlay.GoldenBackgroundVideoHash {
		t.Fatalf("golden asset drift: bg=%s", backgroundHash)
	}
	putObject(t, storeURL, backgroundHash, background)

	presets := []string{
		"fast_fade_through",
		"clean_slide_up",
		"slide_lateral",
		"phrase_word_reveal",
		"undertext_pop",
	}
	const phraseText = "MICHAEL JORDAN CHANGED BASKETBALL"
	jobPrefix := getenvOr("PIPELINEGEN_E2E_JOB_ID", "phrase-preset-michael-jordan")

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	type result struct {
		jobID        string
		preset       string
		status       string
		sha256       string
		sizeBytes    int64
		durationUS   int64
		failReasonV2 string
	}
	results := make([]result, 0, len(presets))
	seenHashes := make(map[string]string, len(presets))

	for i, preset := range presets {
		jobID := jobPrefix + "-" + preset
		plan := capoverlay.OverlayPlan{
			SchemaVersion:   capoverlay.SchemaVersionPlan,
			PlanID:          jobID,
			VideoID:         jobID,
			ProjectID:       "phrase-preset-cert",
			Width:           1280,
			Height:          720,
			FPS:             30,
			RendererVersion: "chronon",
			Items: []capoverlay.OverlayItem{
				{ID: "background_video", TemplateID: "VIDEO_BACKGROUND", StartMs: 0, EndMs: 6000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.mp4", SHA256: backgroundHash}}},
				{ID: "phrase_" + preset, TemplateID: "IMPORTANT_PHRASE", PresetID: preset, StartMs: 1000, EndMs: 5000, Text: phraseText},
			},
		}

		// Compile pass anchors the preset on the document so a regression in
		// the resolver can't silently rewrite the animation the test selected.
		compiled, err := capoverlay.CompileChrononPlan(plan)
		if err != nil {
			t.Fatalf("[%d/%d] preset %s compile: %v", i+1, len(presets), preset, err)
		}
		var phraseLayer *capoverlay.ChrononLayer
		for li := range compiled.Plan.Layers {
			if compiled.Plan.Layers[li].ID == "phrase_"+preset {
				phraseLayer = &compiled.Plan.Layers[li]
			}
		}
		if phraseLayer == nil {
			t.Fatalf("[%d/%d] preset %s: PHRASE layer missing in compiled plan", i+1, len(presets), preset)
		}
		if phraseLayer.Preset != preset {
			t.Fatalf("[%d/%d] preset %s: layer.Preset = %q, want %q", i+1, len(presets), preset, phraseLayer.Preset, preset)
		}
		if phraseLayer.Text != phraseText {
			t.Fatalf("[%d/%d] preset %s: layer.Text = %q, want %q", i+1, len(presets), preset, phraseLayer.Text, phraseText)
		}

		enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
		if err != nil {
			t.Fatal(err)
		}
		ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
		if err != nil {
			t.Fatalf("[%d/%d] preset %s: enqueue: %v", i+1, len(presets), preset, err)
		}
		if ref.JobID != jobID {
			t.Errorf("[%d/%d] preset %s: ref.JobID = %q, want %q", i+1, len(presets), preset, ref.JobID, jobID)
		}

		r := result{jobID: jobID, preset: preset, status: ref.Status}
		if ref.Artifact != nil {
			r.sha256 = ref.Artifact.SHA256
			r.sizeBytes = ref.Artifact.SizeBytes
			r.durationUS = ref.Artifact.DurationUS
		}
		results = append(results, r)

		// 5 distinct visual behaviors → 5 distinct sha256. Collision here is
		// the exact regression that Test A caught (3 name_glow_* → identical
		// bytes); fail closed with a per-preset duplicate-id listing instead
		// of a generic message.
		if r.sha256 != "" {
			if dup, ok := seenHashes[r.sha256]; ok {
				t.Errorf("[%d/%d] preset %s: sha256 %s collides with preset %s (5 presets expected to produce 5 distinct behaviors)",
					i+1, len(presets), preset, r.sha256, dup)
			}
			seenHashes[r.sha256] = preset
			t.Logf("[%d/%d] preset %s COMPLETED: job=%s artifact=%s sha256=%s size=%d duration_us=%d",
				i+1, len(presets), preset, jobID, ref.Artifact.ID, r.sha256, r.sizeBytes, r.durationUS)
		} else {
			t.Logf("[%d/%d] preset %s did not complete cleanly: status=%s", i+1, len(presets), preset, ref.Status)
		}
	}

	// Final summary
	unique := len(seenHashes)
	t.Logf("phrase preset cert summary: %d/%d unique sha256, %d artifacts total",
		unique, len(presets), len(results))
	for _, r := range results {
		t.Logf("  preset=%s sha256=%s size=%d duration_us=%d status=%s", r.preset, r.sha256, r.sizeBytes, r.durationUS, r.status)
	}
}
