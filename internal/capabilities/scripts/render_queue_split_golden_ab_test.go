package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestGoldenPlanPackedAB is a host-gated offline A/B on a frozen job.full.json.
// It does not render, submit or touch the database: it extracts the persisted
// overlay plan, builds the certified per-scene runtime groups, the packed
// super-batches and the diagnostic micro-batch partition, then reports batch
// count, packed canvas cost and load imbalance. Set GOLDEN_PLAN_PATH to the
// job.full.json; set GOLDEN_PLAN_AB_OUT to persist the report.
func TestGoldenPlanPackedAB(t *testing.T) {
	path := os.Getenv("GOLDEN_PLAN_PATH")
	if path == "" {
		t.Skip("GOLDEN_PLAN_PATH not set; offline golden plan A/B is host-gated")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden job: %v", err)
	}
	var envelope struct {
		Result struct {
			Result struct {
				OverlayPlan json.RawMessage `json:"overlay_plan"`
			} `json:"result"`
		} `json:"result"`
		Job struct {
			Result struct {
				Result struct {
					OverlayPlan json.RawMessage `json:"overlay_plan"`
				} `json:"result"`
			} `json:"result"`
		} `json:"job"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("parse golden job: %v", err)
	}
	planRaw := envelope.Result.Result.OverlayPlan
	if len(planRaw) == 0 {
		planRaw = envelope.Job.Result.Result.OverlayPlan
	}
	if len(planRaw) == 0 {
		t.Fatal("golden job carries no overlay_plan")
	}
	var plan capoverlay.OverlayPlan
	if err := json.Unmarshal(planRaw, &plan); err != nil {
		t.Fatalf("parse overlay plan: %v", err)
	}
	manifest, dedicated := partitionOverlayRenderWork(plan)
	if len(manifest) == 0 {
		t.Fatal("golden plan has no manifest candidates")
	}
	sceneGroups := groupManifestCandidatesByScene(manifest)
	microGroups, err := groupManifestCandidatesIntoMicroBatches(plan, manifest)
	if err != nil {
		t.Fatalf("micro-batches: %v", err)
	}
	t.Setenv("OVERLAY_RENDER_PACK_SUPERBATCH", "enabled")
	packedGroups, err := packManifestGroupsIntoSuperBatches(plan, sceneGroups, 3)
	if err != nil {
		t.Fatalf("packed super-batches: %v", err)
	}

	type batchReport struct {
		SceneID     string   `json:"scene_id"`
		Items       int      `json:"items"`
		PackedEndUS int64    `json:"packed_end_us"`
		ItemIDs     []string `json:"item_ids"`
	}
	type report struct {
		Source        string        `json:"source"`
		PlanID        string        `json:"plan_id"`
		Items         int           `json:"items"`
		Dedicated     int           `json:"dedicated"`
		SceneBatches  []batchReport `json:"scene_batches"`
		MicroBatches  []batchReport `json:"micro_batches"`
		PackedBatches []batchReport `json:"packed_batches"`
		SceneCount    int           `json:"scene_count"`
		MicroCount    int           `json:"micro_count"`
		PackedCount   int           `json:"packed_count"`
		SceneTotalUS  int64         `json:"scene_packed_total_us"`
		MicroTotalUS  int64         `json:"micro_packed_total_us"`
		PackedTotalUS int64         `json:"packed_total_us"`
		PackedMaxUS   int64         `json:"packed_max_us"`
		PackedMinUS   int64         `json:"packed_min_us"`
	}
	out := report{Source: path, PlanID: plan.PlanID, Items: len(manifest), Dedicated: len(dedicated)}
	build := func(groups []sceneRenderGroup) ([]batchReport, int64) {
		rows := make([]batchReport, 0, len(groups))
		var total int64
		for _, group := range groups {
			_, end, err := computePackedPlacements(plan, group.candidates)
			if err != nil {
				t.Fatalf("packed placements for %q: %v", group.sceneID, err)
			}
			ids := make([]string, 0, len(group.candidates))
			for _, candidate := range group.candidates {
				ids = append(ids, candidate.source.ID)
			}
			rows = append(rows, batchReport{SceneID: group.sceneID, Items: len(ids), PackedEndUS: end, ItemIDs: ids})
			total += end
		}
		return rows, total
	}
	out.SceneBatches, out.SceneTotalUS = build(sceneGroups)
	out.MicroBatches, out.MicroTotalUS = build(microGroups)
	out.PackedBatches, out.PackedTotalUS = build(packedGroups)
	out.SceneCount = len(sceneGroups)
	out.MicroCount = len(microGroups)
	out.PackedCount = len(packedGroups)

	seen := make(map[string]int, len(manifest))
	for _, group := range packedGroups {
		for _, candidate := range group.candidates {
			seen[candidate.source.ID]++
		}
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("packed super-batches: item %q appears %d times", id, count)
		}
	}
	if len(seen) != len(manifest) {
		t.Fatalf("packed super-batches: %d items, want %d", len(seen), len(manifest))
	}
	for _, row := range out.PackedBatches {
		if out.PackedMaxUS < row.PackedEndUS {
			out.PackedMaxUS = row.PackedEndUS
		}
		if out.PackedMinUS == 0 || row.PackedEndUS < out.PackedMinUS {
			out.PackedMinUS = row.PackedEndUS
		}
	}
	sort.SliceStable(out.SceneBatches, func(i, j int) bool {
		return out.SceneBatches[i].PackedEndUS > out.SceneBatches[j].PackedEndUS
	})
	t.Logf("golden plan %s: items=%d dedicated=%d scene=%d micro=%d packed=%d scene_total=%.3fs micro_total=%.3fs packed_total=%.3fs packed_range=[%.3fs,%.3fs]",
		out.PlanID, out.Items, out.Dedicated, out.SceneCount, out.MicroCount, out.PackedCount,
		float64(out.SceneTotalUS)/1e6, float64(out.MicroTotalUS)/1e6, float64(out.PackedTotalUS)/1e6,
		float64(out.PackedMinUS)/1e6, float64(out.PackedMaxUS)/1e6)

	outPath := os.Getenv("GOLDEN_PLAN_AB_OUT")
	if outPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatalf("create A/B report dir: %v", err)
	}
	encoded, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		t.Fatalf("encode A/B report: %v", err)
	}
	if err := os.WriteFile(outPath, append(encoded, '\n'), 0o644); err != nil {
		t.Fatalf("write A/B report: %v", err)
	}
}
