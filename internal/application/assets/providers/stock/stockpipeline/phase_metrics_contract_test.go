package stockpipeline

import (
	"context"
	"testing"

	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// metricPublishRunner adapts the existing publish fixture with the optional
// recorder seam used by the production orchestrator runner.
type metricPublishRunner struct {
	*publishFakeRunner
	recorder appmetrics.Recorder
}

func (r *metricPublishRunner) MetricsRecorder() appmetrics.Recorder { return r.recorder }

func TestStockPhaseMetricContract_AllRequestedPhases(t *testing.T) {
	cases := []struct {
		phase     string
		detailKey string
		detail    any
		itemsIn   int64
		itemsOut  int64
	}{
		{"stock.search", "videos_found", 3, 3, 3},
		{"stock.stage_sources", "videos_downloaded", 2, 2, 2},
		{"stock.youtube_download", "download_bytes", int64(285000000), 2, 2},
		{"stock.extract", "segments_completed", 2, 2, 2},
		{"stock.compose", "assets_generated", 2, 2, 2},
		{"stock.drive_upload", "assets_generated", 2, 2, 2},
		{"stock.database_save", "assets_generated", 2, 2, 2},
		{"stock.index", "index_mode", "outbox_enqueue", 2, 2},
	}

	repo := &phaseMetricRepository{}
	recorder := appmetrics.NewRecorder(repo)
	for _, tc := range cases {
		t.Run(tc.phase, func(t *testing.T) {
			handle := startStockPhaseWithRecorder(context.Background(), recorder, tc.phase, "stock-contract-job")
			if handle == nil {
				t.Fatal("startStockPhaseWithRecorder returned nil")
			}
			handle.SetItems(tc.itemsIn, tc.itemsOut)
			handle.SetDetails(map[string]any{tc.detailKey: tc.detail})
			if err := handle.End(nil); err != nil {
				t.Fatalf("End: %v", err)
			}
		})
	}

	if got, want := len(repo.metrics), len(cases); got != want {
		t.Fatalf("persisted metrics = %d, want %d", got, want)
	}
	for i, tc := range cases {
		got := repo.metrics[i]
		if got.ProcessType != stockProcessType || got.Provider != stockProcessType {
			t.Errorf("metric[%d] identity = process=%q provider=%q, want stock/stock", i, got.ProcessType, got.Provider)
		}
		if got.Phase != tc.phase {
			t.Errorf("metric[%d] phase = %q, want %q", i, got.Phase, tc.phase)
		}
		if got.Status != "success" || got.ErrorCode != "" {
			t.Errorf("metric[%d] status/error = %q/%q, want success/empty", i, got.Status, got.ErrorCode)
		}
		if got.ItemsIn != tc.itemsIn || got.ItemsOut != tc.itemsOut {
			t.Errorf("metric[%d] items = %d/%d, want %d/%d", i, got.ItemsIn, got.ItemsOut, tc.itemsIn, tc.itemsOut)
		}
		if _, ok := got.Details[tc.detailKey]; !ok {
			t.Errorf("metric[%d] details = %#v, missing %q", i, got.Details, tc.detailKey)
		}
	}
}

func TestPrepareStockDriveArtifact_RecordsCommonUploadMetric(t *testing.T) {
	repo := &phaseMetricRepository{}
	runner := &metricPublishRunner{
		publishFakeRunner: &publishFakeRunner{
			artifactPrep: &recordingArtifactPreparation{},
			state:        &RunState{},
		},
		recorder: appmetrics.NewRecorder(repo),
	}
	artifact := finalization.VerifiedArtifact{
		ArtifactID: "stock:contract:chunk:0",
		Filename:   "clip_001.mp4",
		SizeBytes:  4096,
	}

	if _, err := prepareStockDriveArtifact(context.Background(), runner, artifact, map[string]any{
		"assets_generated":        1,
		"output_duration_seconds": 5,
	}); err != nil {
		t.Fatalf("prepareStockDriveArtifact: %v", err)
	}
	if len(repo.metrics) != 1 {
		t.Fatalf("persisted metrics = %d, want 1", len(repo.metrics))
	}
	got := repo.metrics[0]
	if got.Phase != "stock.drive_upload" || got.Status != "success" {
		t.Fatalf("metric identity/status = %q/%q, want stock.drive_upload/success", got.Phase, got.Status)
	}
	if got.ItemsIn != 1 || got.ItemsOut != 1 {
		t.Fatalf("metric items = %d/%d, want 1/1", got.ItemsIn, got.ItemsOut)
	}
	if got.BytesIn != artifact.SizeBytes || got.BytesOut != artifact.SizeBytes {
		t.Fatalf("metric bytes = %d/%d, want %d/%d", got.BytesIn, got.BytesOut, artifact.SizeBytes, artifact.SizeBytes)
	}
	if got.Details["assets_generated"] != 1 {
		t.Fatalf("metric details = %#v, want assets_generated=1", got.Details)
	}
}
