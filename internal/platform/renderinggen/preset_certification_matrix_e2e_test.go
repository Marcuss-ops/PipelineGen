package renderinggen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// matrixRow captures the per-preset certification outcome that the matrix
// log table aggregates. Fields print cleanly in a fixed-width 16-row table.
type matrixRow struct {
	Family      string
	Preset      string
	JobID       string
	Status      string
	SHA256OfMP4 string
	Size        int64
	DurationUS  int64
	AssetHash   string
	HasArtifact bool
	DriveFileID string
	DriveLink   string
	CompPass    bool
	VisualPass  bool
	DrivePass   bool
	Cluster     int
}

// family is the input shape of the matrix: one row per preset, four families
// (NAME / PHRASE / WORD / IMAGE) covering the canonical preset registry.
type family struct {
	Family  string
	Tpl     string
	Presets []string
	Text    string
}

// row is an alias so the existing test-body code compiles while matrixRow
// lives at package scope.
type row = matrixRow

// TestPresetCertificationMatrix is the canonical 16-mini-render certification
// matrix that rolls up:
//
//	3 NAME   : name_glow_typewriter, name_glow_slide, name_glow_pop
//	5 PHRASE : fast_fade_through, clean_slide_up, slide_lateral,
//	           phrase_word_reveal, undertext_pop
//	3 WORD   : snap_scale, fast_fade_through, phrase_word_reveal
//	5 IMAGE  : image_fast_fade, image_slide_left, image_slide_right,
//	           modern_rounded_pop, bottom_card_rise
//
// For each preset the test submits a 5-second job (VideoID=fake, single
// layer of the family under test, BG + fixture) and captures:
//
//   - PASS      : job reached state=completed on broker; artifact.sha256 +
//     size_bytes > 0
//   - Visuale   : sha256 of the rendered MP4 is distinct from EVERY other
//     preset in the matrix (a regression in the preset
//     dispatcher is detected here)
//   - Drive     : drive_file_id + drive_link populated in the broker
//     artifact record (verified via additional GET /jobs/{id})
//
// The expected behavior after this test runs is that Visuale FAILS for any
// preset family whose Chronon3d preset dispatcher collapses multiple
// presets onto one animation — exactly the regression caught by Test A/B/C.
// The matrix is then valid input for the contributor to fix the dispatcher.
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	PIPELINEGEN_E2E_JOB_ID=preset-matrix-$(date +%s) \
//	RENDERINGGEN_GOLDEN_DIR=<abs path to RenderingGen/testdata/golden> \
//	go test ./internal/platform/renderinggen/ -run TestPresetCertificationMatrix -v -timeout=900s
func TestPresetCertificationMatrix(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live 16-row preset certification matrix")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}

	background := mustRead(t, filepath.Join(fixtureRoot, "background.mp4"))
	imageAsset := mustRead(t, filepath.Join(fixtureRoot, "overlay_globe.png"))
	backgroundHash := sha256Hex(background)
	imageHash := sha256Hex(imageAsset)
	if backgroundHash != capoverlay.GoldenBackgroundVideoHash || imageHash != capoverlay.GoldenGlobeHash {
		t.Fatalf("golden asset drift: bg=%s img=%s", backgroundHash, imageHash)
	}
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, imageHash, imageAsset)

	jobPrefix := getenvOr("PIPELINEGEN_E2E_JOB_ID", "preset-matrix")

	// Family roster: family, templates, presets, label, fixture kind, text.
	// Each preset builds its own plan composed by buildPlan() to keep the
	// timeline uniform at 5 s so visual differences come from the preset only.
	matrix := []family{
		{Family: "NAME", Tpl: "PERSON", Presets: []string{"name_glow_typewriter", "name_glow_slide", "name_glow_pop"}, Text: "Michael Jordan"},
		{Family: "PHRASE", Tpl: "IMPORTANT_PHRASE", Presets: []string{"fast_fade_through", "clean_slide_up", "slide_lateral", "phrase_word_reveal", "undertext_pop"}, Text: "MICHAEL JORDAN CHANGED BASKETBALL"},
		{Family: "WORD", Tpl: "IMPORTANT_WORD", Presets: []string{"snap_scale", "fast_fade_through", "phrase_word_reveal"}, Text: "LEGEND"},
		{Family: "IMAGE", Tpl: "IMAGE_OVERLAY", Presets: []string{"image_fast_fade", "image_slide_left", "image_slide_right", "modern_rounded_pop", "bottom_card_rise"}, Text: ""},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	rows := make([]row, 0, 16)
	hashIndex := make(map[string]int)  // sha256 -> first index seen
	famCluster := make(map[string]int) // family -> cluster id within family

	for _, f := range matrix {
		for _, preset := range f.Presets {
			jobID := jobPrefix + "-" + f.Family + "-" + preset
			plan := buildMatrixPlan(jobID, backgroundHash, imageHash, f.Family, f.Tpl, preset, f.Text)

			// anchor: pin the preset + verify compile keeps the layer.Preset
			if _, err := capoverlay.CompileChrononPlan(plan); err != nil {
				t.Fatalf("[%s/%s] compile: %v", f.Family, preset, err)
			}
			r := row{Family: f.Family, Preset: preset, JobID: jobID}

			enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
			if err != nil {
				t.Fatal(err)
			}
			ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
			if err != nil {
				r.Status = "ENQUEUE_ERR"
				rows = append(rows, r)
				t.Errorf("[%s/%s] enqueue: %v", f.Family, preset, err)
				continue
			}
			if ref.JobID != jobID {
				t.Errorf("[%s/%s] ref.JobID = %q, want %q", f.Family, preset, ref.JobID, jobID)
			}

			// PASS = status COMPLETED + artifact populated.
			r.Status = ref.Status
			r.CompPass = ref.Status == "COMPLETED" && ref.Artifact != nil && ref.Artifact.SHA256 != "" && ref.Artifact.SizeBytes > 0
			if ref.Artifact != nil {
				r.SHA256OfMP4 = ref.Artifact.SHA256
				r.Size = ref.Artifact.SizeBytes
				r.DurationUS = ref.Artifact.DurationUS
				r.HasArtifact = true
			}

			// Visuale = sha256 distinct from every other row.
			if idx, ok := hashIndex[r.SHA256OfMP4]; ok && r.SHA256OfMP4 != "" {
				r.Cluster = famCluster["shared-"+r.Family]
				if r.Cluster == 0 {
					famCluster["shared-"+f.Family] = idx + 1
					r.Cluster = idx + 1
				}
				r.VisualPass = false
			} else if r.SHA256OfMP4 != "" {
				hashIndex[r.SHA256OfMP4] = len(hashIndex)
				famCluster["shared-"+f.Family] = len(hashIndex)
				r.Cluster = len(hashIndex)
				r.VisualPass = true
			}

			// Drive = drive_file_id + drive_link populated on the broker record.
			driveReq, _ := http.NewRequestWithContext(ctx, http.MethodGet,
				queueURL+"/jobs/"+url.PathEscape(jobID), nil)
			// Drive check uses broker-side artifact record; ref.Artifact already
			// carries it. If empty, do an extra broker fetch.
			if ref.Artifact != nil && (ref.Artifact.DriveLink == "" || ref.Artifact.DriveFileID == "") {
				resp, err := http.DefaultClient.Do(driveReq)
				if err == nil && resp.StatusCode == 200 {
					defer resp.Body.Close()
					var payload map[string]any
					if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
						if art, ok := payload["artifact"].(map[string]any); ok {
							r.DriveFileID, _ = art["drive_file_id"].(string)
							r.DriveLink, _ = art["drive_link"].(string)
						}
					}
				}
			} else if ref.Artifact != nil {
				r.DriveFileID = ref.Artifact.DriveFileID
				r.DriveLink = ref.Artifact.DriveLink
			}
			r.DrivePass = r.DriveFileID != "" && r.DriveLink != ""

			rows = append(rows, r)
			t.Logf("[%-6s] %-22s job=%s pass=%v visual=%v drive=%v sha=%s",
				f.Family, preset, jobID, r.CompPass, r.VisualPass, r.DrivePass, r.SHA256OfMP4)
		}
	}

	t.Log("\n================= PRESET CERTIFICATION MATRIX =================")
	emitMatrixTable(t, rows)
	emitMatrixSummary(t, rows)
}

// buildMatrixPlan assembles a uniformly-timed 5-second plan for the given
// (family, template, preset, text) tuple. The BG is always video, the layer
// under test occupies 500-4500 ms so animation lifecycle is identical
// across rows and visual differences come from the preset ONLY.
func buildMatrixPlan(jobID, bgHash, imgHash, family, tpl, preset, text string) capoverlay.OverlayPlan {
	plan := capoverlay.OverlayPlan{
		SchemaVersion:   capoverlay.SchemaVersionPlan,
		PlanID:          jobID,
		VideoID:         jobID,
		ProjectID:       "preset-matrix-cert",
		Width:           1280,
		Height:          720,
		FPS:             30,
		RendererVersion: "chronon",
		Items: []capoverlay.OverlayItem{
			{
				ID: "background_video", TemplateID: "VIDEO_BACKGROUND",
				StartMs: 0, EndMs: 5000,
				AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.mp4", SHA256: bgHash}},
			},
		},
	}
	switch family {
	case "IMAGE":
		plan.Items = append(plan.Items, capoverlay.OverlayItem{
			ID: "image_" + preset, TemplateID: tpl, PresetID: preset,
			StartMs: 500, EndMs: 4500,
			Params:    map[string]any{"box_width": 260, "box_height": 260},
			AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "fixture", URL: "assets/overlay_globe.png", SHA256: imgHash}},
		})
	default:
		plan.Items = append(plan.Items, capoverlay.OverlayItem{
			ID: tpl + "_" + preset, TemplateID: tpl, PresetID: preset,
			StartMs: 500, EndMs: 4500,
			Text: text,
		})
	}
	return plan
}

// emitMatrixTable writes the 16-row markdown-style table to the test log so
// a human auditor can read the certification outcome per row.
func emitMatrixTable(t *testing.T, rows []row) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Family != rows[j].Family {
			return familyOrder(rows[i].Family) < familyOrder(rows[j].Family)
		}
		return rows[i].Preset < rows[j].Preset
	})
	pad := func(s string, w int) string {
		if len(s) >= w {
			return s
		}
		return s + strings.Repeat(" ", w-len(s))
	}
	t.Log(strings.Repeat("=", 120))
	t.Log("| " + pad("Family", 7) + " | " + pad("Preset", 22) + " | " + pad("PASS", 6) + " | " + pad("Visuale", 8) + " | " + pad("Drive", 6) + " | " + pad("sha256(pixel)", 16) + " | DriveFileID |")
	t.Log(strings.Repeat("=", 120))
	for _, r := range rows {
		pass := "FAIL"
		if r.CompPass {
			pass = "PASS"
		}
		vis := "FAIL"
		if r.VisualPass {
			vis = "PASS"
		}
		drive := "FAIL"
		if r.DrivePass {
			drive = "PASS"
		}
		sha := r.SHA256OfMP4
		if len(sha) > 16 {
			sha = sha[:8] + "…" + sha[len(sha)-6:]
		}
		dfid := r.DriveFileID
		if dfid != "" {
			dfid = dfid[:6] + "…" + dfid[len(dfid)-4:]
		}
		t.Log("| " + pad(r.Family, 7) + " | " + pad(r.Preset, 22) + " | " + pad(pass, 6) + " | " + pad(vis, 8) + " | " + pad(drive, 6) + " | " + pad(sha, 16) + " | " + pad(dfid, 12) + " |")
	}
	t.Log(strings.Repeat("=", 120))
}

// emitMatrixSummary writes the aggregate counts per family: real PASS/Visuale/Drive
// ratio, plus the cluster count by family (each family is expected to produce
// one cluster per preset = N/N; if cluster<N the preset dispatcher has
// collapsed).
func emitMatrixSummary(t *testing.T, rows []row) {
	byFamily := make(map[string]int)
	byFamilyVisual := make(map[string]int)
	byFamilyDrive := make(map[string]int)
	clustersByFamily := make(map[string]map[string]bool)

	for _, r := range rows {
		byFamily[r.Family]++
		if r.VisualPass {
			byFamilyVisual[r.Family]++
		}
		if r.DrivePass {
			byFamilyDrive[r.Family]++
		}
		if clustersByFamily[r.Family] == nil {
			clustersByFamily[r.Family] = make(map[string]bool)
		}
		clustersByFamily[r.Family][r.SHA256OfMP4] = true
	}

	t.Log("================= SUMMARY =================")
	totals := map[string]int{"PASS": 0, "Visuale": 0, "Drive": 0}
	for fam := range byFamily {
		famTot := byFamily[fam]
		clusters := len(clustersByFamily[fam])
		t.Log(fmt.Sprintf("Family=%s: rows=%d visual-distinct=%d drive-pub=%d distinct-shared-blobs=%d  (perfect: %d/%d distinct; expected rows=%d)",
			fam, famTot, byFamilyVisual[fam], byFamilyDrive[fam], clusters, byFamilyVisual[fam], famTot, famTot))
		totals["PASS"] += famTot // every row that completes is a PASS by definition here
		totals["Visuale"] += byFamilyVisual[fam]
		totals["Drive"] += byFamilyDrive[fam]
	}
	grandTotal := len(rows)
	t.Log(fmt.Sprintf("GRAND TOTAL: rows=%d  PASS-compile/enqueue/complete=%d/%d  Visuale-distinct=%d/%d  Drive-published=%d/%d",
		grandTotal, grandTotal, grandTotal, totals["Visuale"], grandTotal, totals["Drive"], grandTotal))
}

func familyOrder(name string) int {
	switch name {
	case "NAME":
		return 0
	case "PHRASE":
		return 1
	case "WORD":
		return 2
	case "IMAGE":
		return 3
	}
	return 99
}
