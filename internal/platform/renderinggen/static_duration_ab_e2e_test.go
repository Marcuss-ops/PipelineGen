package renderinggen

// Live A/B harness for two measured acceptance gates of the overlay-throughput
// work (docs/operations/TICKET-OVERLAY-THROUGHPUT-100-MIN.md):
//
//  1. TestStaticDurationInvariantAB — P1 (TICKET-STATIC-OVERLAY-DURATION-INVARIANT):
//     a provably static phrase card (no motion, no camera) must cost the same
//     render service at 30 s as at 3 s. Gate: 30 s ≤ 1.2× the 3 s card,
//     measured on the worker's render service time. Chronon3d's duplicate-frame
//     path makes this possible: every frame after the first re-emits the
//     first frame's encoded packet at its own PTS, so the render cost is O(1)
//     in the duration.
//
//  2. TestBatchTenServiceCostAB — P4 (TICKET-BATCH-SUPERJOB):
//     ten items rendered in one session must cost ≤ 40% of ten single-item
//     sessions on the same content. Gate: (batch service / 10) ≤ 0.40 ×
//     single-item service.
//
// Both gates read the same metric render_attempt_analytics.render_ms
// projects: RenderArtifact.RenderMS, the worker-measured Chronon render
// duration returned on the queue artifact. Nothing is re-timed client-side.
//
// Run with (services must be live: renderinggen queue + Chronon3d daemons):
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	go test ./internal/platform/renderinggen/ \
//	    -run 'TestStaticDurationInvariantAB|TestBatchTenServiceCostAB' -v

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

const (
	abWidth  = 1920
	abHeight = 1080
	abFPS    = 24
)

func abPhraseText() string {
	return "STATIC DURATION CERTIFIED CARD"
}

// abStaticPhraseItem is one provably static important phrase. The explicit
// static_text_smoke preset owns no motion; the phrase role must not add an
// implicit entry envelope when it has no preset or item motion. Every frame
// should be pixel-identical.
func abStaticPhraseItem(id string, startMs, endMs int64) capoverlay.OverlayItem {
	return capoverlay.OverlayItem{
		ID:         id,
		Kind:       "important_phrase",
		TemplateID: "IMPORTANT_PHRASE",
		PresetID:   "static_text_smoke",
		Text:       abPhraseText(),
		StartMs:    startMs,
		EndMs:      endMs,
	}
}

// abPlan builds the semantic overlay plan around one or more static phrase
// cards on a solid white background (no source video: the item content alone
// drives the render).
func abPlan(planID string, items []capoverlay.OverlayItem) capoverlay.OverlayPlan {
	white := 1.0
	_ = white
	return capoverlay.OverlayPlan{
		SchemaVersion:   capoverlay.SchemaVersionPlan,
		PlanID:          planID,
		VideoID:         planID,
		ProjectID:       "static-duration-ab",
		Width:           abWidth,
		Height:          abHeight,
		FPSNum:          abFPS,
		FPSDen:          1,
		RendererVersion: "chronon",
		Background: &capoverlay.OverlayBackground{
			Kind:  "color",
			Color: []float64{1, 1, 1, 1},
		},
		Items: items,
	}
}

func abEnqueue(t *testing.T, ctx context.Context, queueURL string, plan capoverlay.OverlayPlan) scriptgen.RenderReference {
	t.Helper()
	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatalf("enqueue plan %s: %v", plan.PlanID, err)
	}
	if ref.Artifact == nil {
		t.Fatalf("plan %s produced no certified artifact (status=%s)", plan.PlanID, ref.Status)
	}
	if ref.Artifact.RenderMS <= 0 {
		t.Fatalf("plan %s artifact reports no render_ms (worker metric missing): %+v", plan.PlanID, ref.Artifact)
	}
	return ref
}

func abMedian(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// abFetchArtifact downloads the certified artifact bytes from the object
// store so the pixel contract can be checked against the exact file the
// worker certified.
func abFetchArtifact(t *testing.T, storeURL, sha string) []byte {
	t.Helper()
	resp, err := http.Get(storeURL + "/objects/" + sha)
	if err != nil {
		t.Fatalf("object store GET %s: %v", sha, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("object store GET %s: HTTP %d", sha, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("object store read %s: %v", sha, err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != sha {
		t.Fatalf("object store returned bytes that do not match the certified sha256 %s", sha)
	}
	return data
}

// abAssertAllFramesIdentical streams the artifact through ffmpeg and proves
// every decoded frame is byte-identical to frame 0. This is the static-card
// pixel contract: the duplicate-frame path re-emits one encoded packet, so a
// decoded frame difference means the path did not fire or is incorrect.
func abAssertAllFramesIdentical(t *testing.T, artifact []byte, width, height, wantFrames int) {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "ab.mp4")
	if err := os.WriteFile(tmp, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", tmp,
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	frameSize := width * height * 3
	buf := make([]byte, frameSize)
	var firstHash string
	frames := 0
	for {
		if _, err := io.ReadFull(stdout, buf); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			t.Fatalf("decode frame %d: %v", frames, err)
		}
		sum := sha256.Sum256(buf)
		h := hex.EncodeToString(sum[:])
		if frames == 0 {
			firstHash = h
		} else if h != firstHash {
			t.Fatalf("static card frame %d differs from frame 0 (%s != %s): the duplicate-frame path did not hold pixel identity", frames, h, firstHash)
		}
		frames++
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("ffmpeg decode: %v: %s", err, stderr.String())
	}
	if wantFrames > 0 && frames != wantFrames {
		t.Fatalf("static card decoded %d frames, want %d", frames, wantFrames)
	}
	t.Logf("static card pixel contract: %d/%d frames byte-identical to frame 0", frames, frames)
}

// TestStaticDurationInvariantAB is the P1 gate A/B. It renders the SAME
// provably static phrase card at 3 s and 30 s (three alternating repetitions
// each) and asserts the 30 s service is within 1.2× of the 3 s service.
func TestStaticDurationInvariantAB(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live static-duration A/B")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	prefix := getenvOr("PIPELINEGEN_E2E_JOB_ID", "static-duration-ab")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	var short3s, long30s []int64
	var lastLongArtifact []byte
	var lastLongSHA string
	for rep := 1; rep <= 3; rep++ {
		for _, tc := range []struct {
			label    string
			duration int64
			collect  *[]int64
		}{
			{"3s", 3000, &short3s},
			{"30s", 30000, &long30s},
		} {
			planID := fmt.Sprintf("%s-r%d-%s", prefix, rep, tc.label)
			plan := abPlan(planID, []capoverlay.OverlayItem{
				abStaticPhraseItem("static-card", 0, tc.duration),
			})
			ref := abEnqueue(t, ctx, queueURL, plan)
			*tc.collect = append(*tc.collect, ref.Artifact.RenderMS)
			t.Logf("[rep %d %s] render_ms=%d encode_ms=%d frames=%d size=%d backend=%s codec=%s profile=%s",
				rep, tc.label, ref.Artifact.RenderMS, ref.Artifact.EncodeMS,
				ref.Artifact.FrameCount, ref.Artifact.SizeBytes,
				ref.Artifact.Backend, ref.Artifact.Codec, ref.Artifact.CodecProfile)
			if tc.label == "30s" {
				lastLongArtifact = abFetchArtifact(t, storeURL, ref.Artifact.SHA256)
				lastLongSHA = ref.Artifact.SHA256
				if dumpDir := os.Getenv("PIPELINEGEN_E2E_DUMP_ARTIFACTS_DIR"); dumpDir != "" {
					if err := os.MkdirAll(dumpDir, 0o700); err != nil {
						t.Fatalf("create artifact dump dir: %v", err)
					}
					dumpPath := filepath.Join(dumpDir, planID+".mp4")
					if err := os.WriteFile(dumpPath, lastLongArtifact, 0o600); err != nil {
						t.Fatalf("dump certified artifact: %v", err)
					}
					t.Logf("certified artifact dumped: %s sha256=%s", dumpPath, ref.Artifact.SHA256)
				}
			}
		}
	}

	med3 := abMedian(short3s)
	med30 := abMedian(long30s)
	if med3 <= 0 {
		t.Fatalf("3 s service median is zero: %v", short3s)
	}
	ratio := float64(med30) / float64(med3)
	t.Logf("P1 gate: median 3s=%d ms (%v), median 30s=%d ms (%v), ratio=%.3f (gate ≤ 1.2)",
		med3, short3s, med30, long30s, ratio)
	if ratio > 1.2 {
		t.Errorf("P1 static-duration gate NOT met: 30 s card costs %.3f× the 3 s card (gate ≤ 1.2×)", ratio)
	}
	t.Logf("P1 pixel check artifact sha256=%s", lastLongSHA)
	abAssertAllFramesIdentical(t, lastLongArtifact, abWidth, abHeight, 30*abFPS)
	if ratio <= 1.2 {
		t.Log("P1 render-duration gate PASS")
	}
}

// TestBatchTenServiceCostAB is the P4 gate A. It renders ten static phrase
// cards twice: once as ten single-item jobs (ten render sessions) and once as
// one ten-item plan (one render session), then asserts the per-item service of
// the batch is ≤ 40% of the single-item service.
func TestBatchTenServiceCostAB(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live batch-10 A/B")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	prefix := getenvOr("PIPELINEGEN_E2E_JOB_ID", "batch10-ab")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	const items = 10
	const slotMs = int64(3000)

	// Leg A: one job per item (the non-fused production path).
	var singles []int64
	for i := 0; i < items; i++ {
		planID := fmt.Sprintf("%s-single-%02d", prefix, i)
		plan := abPlan(planID, []capoverlay.OverlayItem{
			abStaticPhraseItem(fmt.Sprintf("card-%02d", i), 0, slotMs),
		})
		ref := abEnqueue(t, ctx, queueURL, plan)
		singles = append(singles, ref.Artifact.RenderMS)
		t.Logf("[single %02d] render_ms=%d", i, ref.Artifact.RenderMS)
	}

	// Leg B: one job carrying all ten items as segments of one session
	// (the P4 superjob model — one startup, one encoder session).
	batchItems := make([]capoverlay.OverlayItem, 0, items)
	for i := 0; i < items; i++ {
		start := int64(i) * slotMs
		batchItems = append(batchItems,
			abStaticPhraseItem(fmt.Sprintf("card-%02d", i), start, start+slotMs))
	}
	batchPlan := abPlan(prefix+"-batch10", batchItems)
	batch := abEnqueue(t, ctx, queueURL, batchPlan)
	t.Logf("[batch10] render_ms=%d encode_ms=%d frames=%d",
		batch.Artifact.RenderMS, batch.Artifact.EncodeMS, batch.Artifact.FrameCount)

	singleMedian := abMedian(singles)
	batchPerItem := batch.Artifact.RenderMS / items
	if singleMedian <= 0 {
		t.Fatalf("single-item service median is zero: %v", singles)
	}
	ratio := float64(batchPerItem) / float64(singleMedian)
	t.Logf("P4 gate A: single median=%d ms (%v), batch service/item=%d ms (total %d ms), ratio=%.3f (gate ≤ 0.40)",
		singleMedian, singles, batchPerItem, batch.Artifact.RenderMS, ratio)
	if ratio > 0.40 {
		t.Fatalf("P4 batch-10 gate NOT met: batch per-item service is %.3f× the single-item service (gate ≤ 0.40×)", ratio)
	}
}

// silence unused-import guards for helpers shared with the sibling live
// canaries (json is used to dump worker telemetry on failure paths).
var _ = json.Marshal
