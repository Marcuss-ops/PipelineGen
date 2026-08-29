package cliprender

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

type recordingRecorder struct {
	reports []kernobs.OperationReport
	err     error
}

func (r *recordingRecorder) RecordOperationReport(_ context.Context, m kernobs.OperationReport) error {
	r.reports = append(r.reports, m)
	return r.err
}

func TestChrononMetricsAdapterProjectPublishesOneRowPerMeasuredPhase(t *testing.T) {
	sc, err := ParseChrononSidecar([]byte(chrononSidecarFixture))
	if err != nil {
		t.Fatal(err)
	}
	a := NewChrononMetricsAdapter(nil, zap.NewNop())
	daemonReused := true
	reports := a.Project(sc, ChrononMetricsPublishOptions{
		DaemonReused:     &daemonReused,
		SourceSHA256:     "abc123",
		SourceDurationMS: 45000,
		OutputSizeBytes:  2_500_000,
		Width:            1920,
		Height:           1080,
		FPS:              30,
	})

	wantOps := []string{
		ChrononOperationStartup,
		ChrononOperationInputOpen,
		ChrononOperationPrepare,
		ChrononOperationRenderLoop,
		ChrononOperationEncoderDrain,
		ChrononOperationFFprobe,
		ChrononOperationSHA256,
	}
	if len(reports) != len(wantOps) {
		t.Fatalf("projected %d reports, want %d", len(reports), len(wantOps))
	}
	wantMS := []int64{5078, 0, 2620, 24971, 554, 375, 0}
	for i, want := range wantOps {
		got := reports[i]
		if got.Operation != want {
			t.Fatalf("report[%d].operation = %q, want %q", i, got.Operation, want)
		}
		if got.DurationMs != wantMS[i] {
			t.Fatalf("report[%d].duration_ms = %d, want %d", i, got.DurationMs, wantMS[i])
		}
		if got.Component != string(kernobs.ComponentChronon) {
			t.Fatalf("report[%d].component = %q, want chronon", i, got.Component)
		}
		if got.Stage != string(StageClipRender) {
			t.Fatalf("report[%d].stage = %q, want clip.render", i, got.Stage)
		}
		if got.Status != kernobs.StageStatusCompleted {
			t.Fatalf("report[%d].status = %q, want completed", i, got.Status)
		}
		if got.SourceSHA256 != "abc123" || got.SourceDurationMS != 45000 ||
			got.OutputSizeBytes != 2_500_000 || got.Width != 1920 || got.Height != 1080 || got.FPS != 30 {
			t.Fatalf("report[%d] certified columns not projected: %+v", i, got)
		}
		if got.ObservationID == "" {
			t.Fatalf("report[%d] missing observation id", i)
		}
	}
	// Every row of one attempt shares the identical structured metadata.
	meta := reports[0].MetadataJSON
	for i := 1; i < len(reports); i++ {
		if reports[i].MetadataJSON != meta {
			t.Fatalf("report[%d] metadata differs from report[0]: %s vs %s", i, reports[i].MetadataJSON, meta)
		}
	}
	assertMetadata(t, meta, map[string]any{
		"backend":                    "direct_yuv_cuda",
		"decoder":                    "nvdec",
		"encoder":                    "nvenc",
		"daemon_reused":              true,
		"asset_cache_hit":            true,
		"gpu_asset_cache_hits":       float64(1),
		"gpu_asset_cache_misses":     float64(0),
		"cuda_upload_bytes":          float64(4194304),
		"cuda_readback_bytes":        float64(1048576),
		"encoder_staging_copy_bytes": float64(2048),
		"process_wall_ms":            float64(32222),
		"accounted_percent":          104.27911889519139,
		"end_to_end_fps":             29.97,
		"render_loop_fps":            30.1,
		"realtime_factor":            0.0042,
		"graph_reused_frames":        float64(12),
		"fast_path_reused_frames":    float64(0),
	})
}

func TestChrononMetricsAdapterProjectSkipsUnmeasuredPhases(t *testing.T) {
	sc := &ChrononSidecar{
		StartupMS: ptrFloat(100.4),
		// prepare/render_loop/... absent → must not appear.
	}
	a := NewChrononMetricsAdapter(nil, zap.NewNop())
	reports := a.Project(sc, ChrononMetricsPublishOptions{})
	if len(reports) != 1 {
		t.Fatalf("projected %d reports, want 1", len(reports))
	}
	if reports[0].Operation != ChrononOperationStartup || reports[0].DurationMs != 100 {
		t.Fatalf("unexpected report: %+v", reports[0])
	}
	if reports[0].MetadataJSON != "{}" {
		t.Fatalf("empty attempt context must yield empty metadata, got %s", reports[0].MetadataJSON)
	}
}

func TestChrononMetricsAdapterProjectOmitsUnknownAndUnsuppliedFacts(t *testing.T) {
	sc := &ChrononSidecar{
		StartupMS: ptrFloat(1),
		Backend:   "unknown",
		Decoder:   "software",
	}
	a := NewChrononMetricsAdapter(nil, zap.NewNop())
	reports := a.Project(sc, ChrononMetricsPublishOptions{})
	meta := reports[0].MetadataJSON
	assertMetadata(t, meta, map[string]any{
		// "unknown" backend sentinel and every nil counter/flag stay absent.
		"decoder": "software",
	})
	var doc map[string]any
	if err := json.Unmarshal([]byte(meta), &doc); err != nil {
		t.Fatal(err)
	}
	if _, present := doc["daemon_reused"]; present {
		t.Fatalf("daemon_reused must be absent when unknown, got %s", meta)
	}
	if _, present := doc["renderer_created"]; present {
		t.Fatalf("renderer_created must be absent when unknown, got %s", meta)
	}
	if _, present := doc["backend"]; present {
		t.Fatalf("unknown backend sentinel must be omitted, got %s", meta)
	}
}

func TestChrononMetricsAdapterPublishRecordsThroughTheSeam(t *testing.T) {
	sc, err := ParseChrononSidecar([]byte(chrononSidecarFixture))
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordingRecorder{}
	a := NewChrononMetricsAdapter(rec, zap.NewNop())
	rendererCreated := false
	a.Publish(context.Background(), sc, ChrononMetricsPublishOptions{RendererCreated: &rendererCreated})
	if len(rec.reports) != 7 {
		t.Fatalf("recorded %d reports, want 7", len(rec.reports))
	}
	var found bool
	for _, r := range rec.reports {
		if r.Operation == ChrononOperationPrepare {
			found = true
			if r.DurationMs != 2620 {
				t.Fatalf("prepare duration_ms = %d, want 2620", r.DurationMs)
			}
		}
	}
	if !found {
		t.Fatal("prepare phase was not recorded")
	}
}

func TestChrononMetricsAdapterPublishNeverFailsTheRender(t *testing.T) {
	sc, err := ParseChrononSidecar([]byte(chrononSidecarFixture))
	if err != nil {
		t.Fatal(err)
	}
	// A failing recorder must be logged, never returned to the caller.
	a := NewChrononMetricsAdapter(&recordingRecorder{err: errors.New("write failed")}, zap.NewNop())
	a.Publish(context.Background(), sc, ChrononMetricsPublishOptions{})
	// Nil adapter / nil recorder are silent no-ops.
	var nilAdapter *ChrononMetricsAdapter
	nilAdapter.Publish(context.Background(), sc, ChrononMetricsPublishOptions{})
	NewChrononMetricsAdapter(nil, zap.NewNop()).Publish(context.Background(), sc, ChrononMetricsPublishOptions{})
	// Nil document produces no records.
	rec := &recordingRecorder{}
	NewChrononMetricsAdapter(rec, zap.NewNop()).Publish(context.Background(), nil, ChrononMetricsPublishOptions{})
	if len(rec.reports) != 0 {
		t.Fatalf("nil document recorded %d reports, want 0", len(rec.reports))
	}
}

func assertMetadata(t *testing.T, raw string, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("metadata_json is not valid JSON: %s", raw)
	}
	for key, wantVal := range want {
		gotVal, present := got[key]
		if !present {
			t.Fatalf("metadata missing %q in %s", key, raw)
		}
		if gotVal != wantVal {
			t.Fatalf("metadata[%q] = %v, want %v", key, gotVal, wantVal)
		}
	}
}

func ptrFloat(v float64) *float64 { return &v }
