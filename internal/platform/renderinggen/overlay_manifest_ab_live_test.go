package renderinggen

// overlay_manifest_ab_live_test.go is the LIVE leg of the P5 A/B
// (docs/operations/TICKET-MANIFEST-IDENTITY.md, gate 1): one scene-local plan
// driven through BOTH render projections against the LIVE RenderingGen queue —
// the per-item MP4 path and the manifest+timeline path — with the produced
// artifacts compared at the manifest's LITERAL windows.
//
// Opt-in like TestLivePipelineGenToRenderingGen (same env contract): it
// submits real GPU work to the shared queue, so it only runs with
// PIPELINEGEN_MANIFEST_AB_LIVE=1 inside an operator measurement window:
//
//	PIPELINEGEN_MANIFEST_AB_LIVE=1   — run the live A/B
//	RENDERINGGEN_QUEUE_URL           — queue base (default localhost:8081)
//	RENDERINGGEN_STORE_URL           — object store base (default localhost:9000)
//	RENDERINGGEN_GOLDEN_DIR          — fixture root (background + font)
//
// Parity contract (declared): the two paths re-encode h264 in different GOP
// contexts (per-item clips vs one fused timeline), so decoded frames are
// compared with a tolerance (max channel delta, mean absolute delta) instead
// of byte equality; the identity projection (item order + placement windows)
// is compared exactly.
//
// Media parity follows the unit harness SSOT
// (scripts/overlay_manifest_ab_test.go, abProjectTimeline): for every entry,
// timeline[start_frame + k] == clip[k] for k in [0, window_len) — the
// manifest's LITERAL [in_us, out_us) projected with the §3 ceil grid, which
// is exactly what the final handoff places in the output (mode=replace copies
// the clip's leading window_len frames). The clip's padded tail is NOT part
// of the parity window; the test also guards that it never leaks past it.
//
// PIPELINEGEN_MANIFEST_AB_PLAN=<plan.json> runs the A/B on a materialized
// golden plan (same plan through both paths — TICKET-MANIFEST-IDENTITY gate
// 1) instead of the built-in synthetic one.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

const (
	liveABWidth           = 640
	liveABHeight          = 360
	liveABFPS             = 30
	liveABMaxChannelDelta = 10
	liveABMeanDelta       = 2.0
	// liveABResidueGuard is how many frames past its window each entry is
	// checked for tail leakage (the fused item must end at its window).
	liveABResidueGuard = 15
)

// liveABPlan is deliberately scene-local, map-free and caption-free so the
// manifest timeline path can cover it (manifestTimelineEligible contract).
func liveABPlan(jobID string) capoverlay.OverlayPlan {
	return capoverlay.OverlayPlan{
		SchemaVersion:   capoverlay.SchemaVersionPlan,
		PlanID:          jobID,
		VideoID:         jobID,
		ProjectID:       "pipelinegen-live-manifest-ab",
		Width:           liveABWidth,
		Height:          liveABHeight,
		FPSNum:          liveABFPS,
		FPSDen:          1,
		RendererVersion: "chronon",
		Background:      &capoverlay.OverlayBackground{Kind: "color", Color: []float64{0.03, 0.03, 0.03, 1}},
		Items: []capoverlay.OverlayItem{
			{ID: "ab_phrase_alpha", TemplateID: "IMPORTANT_WORD", PresetID: "static_text_smoke", Text: "ALPHA",
				StartMs: 200, EndMs: 1200, SceneID: "scene-01",
				Params: map[string]any{"position": []any{170, 60}, "box_width": 300, "box_height": 100}},
			{ID: "ab_phrase_beta", TemplateID: "IMPORTANT_WORD", PresetID: "static_text_smoke", Text: "BETA",
				StartMs: 1500, EndMs: 2500, SceneID: "scene-01",
				Params: map[string]any{"position": []any{170, 130}, "box_width": 300, "box_height": 100}},
			{ID: "ab_phrase_gamma", TemplateID: "IMPORTANT_WORD", PresetID: "static_text_smoke", Text: "GAMMA",
				StartMs: 2800, EndMs: 3800, SceneID: "scene-01",
				Params: map[string]any{"position": []any{170, 200}, "box_width": 300, "box_height": 100}},
		},
	}
}

func TestLiveOverlayManifestABParity(t *testing.T) {
	if os.Getenv("PIPELINEGEN_MANIFEST_AB_LIVE") != "1" {
		t.Skip("set PIPELINEGEN_MANIFEST_AB_LIVE=1 to run the live P5 A/B parity leg")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}

	background := mustRead(t, filepath.Join(fixtureRoot, "background.jpg"))
	poppinsPath := filepath.Join(fixtureRoot, "Poppins-Bold.ttf")
	if _, err := os.Stat(poppinsPath); err != nil {
		poppinsPath = filepath.Join(fixtureRoot, "fonts", "Poppins-Bold.ttf")
	}
	poppins := mustRead(t, poppinsPath)
	putObject(t, storeURL, sha256Hex(background), background)
	putObject(t, storeURL, sha256Hex(poppins), poppins)

	jobID := "pipelinegen-live-manifest-ab-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	plan := liveABLoadPlan(t, jobID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// ── leg A: per-item path (manifest flag explicitly cleared) ─────────
	t.Setenv(scriptgen.OverlayManifestTimelineEnv, "")
	perItem, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	perItem.SetSeparateItemRenders(true)
	refA, err := perItem.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatalf("per-item leg: %v", err)
	}
	if len(refA.Items) != len(plan.Items) {
		t.Fatalf("per-item leg rendered %d items, want %d", len(refA.Items), len(plan.Items))
	}
	if refA.Manifest != nil {
		t.Fatal("per-item leg must not produce a manifest")
	}

	// ── leg B: manifest+timeline path (explicit opt-in) ─────────────────
	t.Setenv(scriptgen.OverlayManifestTimelineEnv, "1")
	manifestPath, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	refB, err := manifestPath.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatalf("manifest leg: %v", err)
	}
	if refB.Manifest == nil {
		t.Fatal("manifest leg must return its manifest")
	}
	if refB.Artifact == nil {
		t.Fatal("manifest leg must return the timeline artifact")
	}

	// ── identity projection: same order, same literal windows ───────────
	if len(refB.Manifest.Entries) != len(plan.Items) {
		t.Fatalf("manifest entries = %d, want %d", len(refB.Manifest.Entries), len(plan.Items))
	}
	for i, entry := range refB.Manifest.Entries {
		if entry.ItemID != plan.Items[i].ID {
			t.Fatalf("manifest entry[%d] = %q, want %q (plan order must be preserved)", i, entry.ItemID, plan.Items[i].ID)
		}
		wantIn := plan.Items[i].StartUSValue()
		wantOut := plan.Items[i].EndUSValue()
		if entry.InUS != wantIn || entry.OutUS != wantOut {
			t.Fatalf("manifest entry %q window = [%d,%d), want the per-item placement [%d,%d)",
				entry.ItemID, entry.InUS, entry.OutUS, wantIn, wantOut)
		}
	}

	// ── media parity at literal windows ─────────────────────────────────
	dir := "/tmp/p5ab_live_debug"
	_ = os.MkdirAll(dir, 0o755)
	t.Logf("artifacts: timeline frames=%d dur_us=%d fps=%d/%d size=%dx%d | item0 frames=%d dur_us=%d fps=%d/%d size=%dx%d",
		refB.Artifact.FrameCount, refB.Artifact.DurationUS, refB.Artifact.FPSNum, refB.Artifact.FPSDen, refB.Artifact.Width, refB.Artifact.Height,
		refA.Items[0].Artifact.FrameCount, refA.Items[0].Artifact.DurationUS, refA.Items[0].Artifact.FPSNum, refA.Items[0].Artifact.FPSDen, refA.Items[0].Artifact.Width, refA.Items[0].Artifact.Height)
	timeline := filepath.Join(dir, "timeline.mp4") //nolint:gosec // debug dir
	downloadArtifact(t, storeURL, refB.Artifact, timeline)
	// Media parity at the manifest's LITERAL windows (§3 ceil grid — the
	// same projection as finalJobOverlayAssetsExcluding): the final handoff
	// replaces exactly these output frames with the clip's leading
	// window_len frames (mode=replace), so that is what both paths must
	// show, and nothing else. The fused timeline is decoded ONCE in a
	// bounded-memory streaming pass (a 139 s 1080p timeline is ~19 GB of
	// RGB) and every frame is compared on the liveABWidth x liveABHeight
	// grid — the same comparison grid as the synthetic leg; larger
	// artifacts are downscaled by the decoder.
	fpsNum, fpsDen := refB.Artifact.FPSNum, refB.Artifact.FPSDen
	if fpsNum <= 0 || fpsDen <= 0 {
		t.Fatalf("timeline artifact has invalid fps %d/%d", fpsNum, fpsDen)
	}
	frameDenominator := int64(1_000_000 * fpsDen)
	type abParityWindow struct {
		entry capoverlay.OverlayManifestEntry
		clips [][]byte // clip frames [0, guard)
		start int      // window start on the timeline frame grid
		end   int      // exclusive parity end (start + window_len)
		guard int      // exclusive residue-guard end
	}
	windows := make([]abParityWindow, 0, len(refB.Manifest.Entries))
	for i, entry := range refB.Manifest.Entries {
		if refA.Items[i].Artifact == nil {
			t.Fatalf("per-item leg has no artifact for %q", entry.ItemID)
		}
		itemPath := filepath.Join(dir, fmt.Sprintf("item_%d.mp4", i)) //nolint:gosec // debug dir
		downloadArtifact(t, storeURL, refA.Items[i].Artifact, itemPath)
		startFrame := int((entry.InUS*int64(fpsNum) + frameDenominator - 1) / frameDenominator)
		endFrame := int((entry.OutUS*int64(fpsNum) + frameDenominator - 1) / frameDenominator)
		windowLen := endFrame - startFrame
		if windowLen <= 0 {
			t.Fatalf("entry %q projects to an empty frame window [%d,%d)", entry.ItemID, startFrame, endFrame)
		}
		clips := liveABDecodeRGBPrefix(t, itemPath, windowLen+liveABResidueGuard)
		if len(clips) < windowLen {
			t.Fatalf("entry %q clip has %d frames for a %d-frame window", entry.ItemID, len(clips), windowLen)
		}
		windows = append(windows, abParityWindow{
			entry: entry, clips: clips, start: startFrame,
			end: startFrame + windowLen, guard: startFrame + len(clips),
		})
	}
	diverged := make(map[string][]string, len(windows))
	residue := make(map[string]int, len(windows))
	frame := make([]byte, liveABWidth*liveABHeight*3)
	decoded := 0
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", timeline, //nolint:gosec // debug artifact
		"-vf", fmt.Sprintf("scale=%d:%d", liveABWidth, liveABHeight),
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("timeline decode pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("timeline decode: %v", err)
	}
	for {
		if _, err := io.ReadFull(stdout, frame); err != nil {
			break // EOF ends the stream
		}
		for _, w := range windows {
			if decoded < w.start || decoded >= w.guard {
				continue
			}
			k := decoded - w.start
			mean, maxDelta := liveABRGBDelta(w.clips[k], frame)
			if decoded < w.end {
				if maxDelta > liveABMaxChannelDelta || mean > liveABMeanDelta {
					diverged[w.entry.ItemID] = append(diverged[w.entry.ItemID],
						fmt.Sprintf("k=%d(mean=%.2f,max=%d)", k, mean, maxDelta))
				}
			} else if maxDelta <= liveABMaxChannelDelta && mean <= liveABMeanDelta {
				// Residue guard: the clip's padded tail must not persist past
				// the window in the fused timeline (the pre-fix fused plan
				// leaked tails into later windows — visible text stacking,
				// live A/B 2026-10-24).
				residue[w.entry.ItemID]++
			}
		}
		decoded++
	}
	_ = cmd.Wait()
	for _, w := range windows {
		if decoded < w.guard {
			t.Fatalf("timeline too short for entry %q: need %d frames, decoded %d", w.entry.ItemID, w.guard, decoded)
		}
		t.Logf("entry %q: window=[%d,%d) frames=[%d,%d) clipFrames=%d diverged=%d/%d %v",
			w.entry.ItemID, w.entry.InUS, w.entry.OutUS, w.start, w.end, len(w.clips), len(diverged[w.entry.ItemID]), w.end-w.start, diverged[w.entry.ItemID])
		if len(diverged[w.entry.ItemID]) > 0 {
			t.Errorf("entry %q differs across paths in %d/%d window frames: %v",
				w.entry.ItemID, len(diverged[w.entry.ItemID]), w.end-w.start, diverged[w.entry.ItemID])
		}
		if residue[w.entry.ItemID] > 0 {
			t.Errorf("entry %q persists past its window [%d,%d) in %d frames", w.entry.ItemID, w.entry.InUS, w.entry.OutUS, residue[w.entry.ItemID])
		}
	}
}

// liveABLoadPlan returns the plan under test: the materialized golden A/B
// plan when PIPELINEGEN_MANIFEST_AB_PLAN points at its JSON wire form (the
// SAME materialized plan driven through both render projections —
// TICKET-MANIFEST-IDENTITY gate 1), otherwise the built-in synthetic plan.
// Plan identity is re-stamped per run so both paths render fresh artifacts
// under the running renderer instead of reusing earlier receipts.
func liveABLoadPlan(t *testing.T, jobID string) capoverlay.OverlayPlan {
	t.Helper()
	path := os.Getenv("PIPELINEGEN_MANIFEST_AB_PLAN")
	if path == "" {
		return liveABPlan(jobID)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied fixture path
	if err != nil {
		t.Fatalf("read PIPELINEGEN_MANIFEST_AB_PLAN: %v", err)
	}
	var plan capoverlay.OverlayPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("decode plan fixture %s: %v", path, err)
	}
	if len(plan.Items) == 0 {
		t.Fatalf("plan fixture %s has no items", path)
	}
	plan.PlanID = jobID
	plan.VideoID = jobID
	plan.DriveJobID = jobID
	plan.Fingerprint = ""
	if strings.TrimSpace(plan.RendererVersion) == "" {
		// The queue handler stamps its configured renderer when the plan
		// carries none (wire field is omitempty); do the same locally so
		// plan fingerprints and render-cache keys match the live path.
		plan.RendererVersion = "chronon"
	}
	for i := range plan.Items {
		plan.Items[i].RenderKey = ""
	}
	return plan
}

// liveABDecodeRGBPrefix decodes the first maxFrames frames of a video as
// packed RGB24 on the comparison grid (liveABWidth x liveABHeight — artifacts
// of any resolution are downscaled by the decoder, so both legs are compared
// on the same grid).
func liveABDecodeRGBPrefix(t *testing.T, path string, maxFrames int) [][]byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatalf("the live A/B needs ffmpeg: %v", err)
	}
	args := []string{"-v", "error", "-i", path,
		"-vf", fmt.Sprintf("scale=%d:%d", liveABWidth, liveABHeight),
		"-f", "rawvideo", "-pix_fmt", "rgb24"}
	if maxFrames > 0 {
		args = append(args, "-frames:v", strconv.Itoa(maxFrames))
	}
	args = append(args, "-")
	out, err := exec.Command("ffmpeg", args...).Output()
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	frameSize := liveABWidth * liveABHeight * 3
	if len(out) == 0 || len(out)%frameSize != 0 {
		t.Fatalf("decode %s: %d bytes is not a whole number of %d-byte frames", path, len(out), frameSize)
	}
	frames := make([][]byte, 0, len(out)/frameSize)
	for off := 0; off < len(out); off += frameSize {
		frame := make([]byte, frameSize)
		copy(frame, out[off:off+frameSize])
		frames = append(frames, frame)
	}
	return frames
}

// liveABRGBDelta compares two packed RGB frames and returns the mean absolute
// per-channel delta plus the maximum channel delta.
func liveABRGBDelta(a, b []byte) (mean float64, max int) {
	if len(a) != len(b) {
		return 1e9, 255
	}
	var sum int64
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += int64(d)
		if d > max {
			max = d
		}
	}
	return float64(sum) / float64(len(a)), max
}

// downloadArtifact fetches a certified artifact from the object store by its
// content key and writes it to path.
func downloadArtifact(t *testing.T, storeURL string, artifact *scriptgen.RenderArtifact, path string) {
	t.Helper()
	key := strings.TrimSpace(artifact.StorageKey)
	if key == "" {
		key = strings.TrimSpace(artifact.SHA256)
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(storeURL, "/")+"/objects/"+key, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("objectstore GET %s: %v", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("objectstore GET %s: HTTP %d", key, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
