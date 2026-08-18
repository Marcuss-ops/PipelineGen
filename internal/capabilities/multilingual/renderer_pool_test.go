package multilingual

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestRenderPool_StreamingOrderAndTimestamps(t *testing.T) {
	dir := t.TempDir()
	counterPath := filepath.Join(dir, "ffmpeg.counter")
	_ = os.WriteFile(counterPath, nil, 0o644)
	ffmpegPath, _ := writeFakeFfmpeg(t, dir, counterPath, 5.0)

	repo := newFakeVariantRepo()
	pub := &fakePublisher{}
	r, err := NewRenderer(repo, pub, ffmpegPath, zap.NewNop())
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	src := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(src, []byte("src"), 0o644); err != nil {
		t.Fatal(err)
	}

	enReady := time.Now().Add(-2 * time.Second)
	esReady := time.Now().Add(-1 * time.Second)

	en := variantForTest("clip-1", "en", src, 5.0, dir)
	en.Priority = 0
	en.TextReadyAt = enReady
	es := variantForTest("clip-1", "es", src, 5.0, dir)
	es.Priority = 1
	es.TextReadyAt = esReady
	for _, in := range []VariantInput{en, es} {
		if err := os.WriteFile(in.ASSPath, []byte(validASSForTest(in.Language)), 0o644); err != nil {
			t.Fatalf("write ASS %s: %v", in.ASSPath, err)
		}
	}

	pool := r.NewRenderPool(context.Background(), 2)
	pool.Submit(en)
	pool.Submit(es)
	report := pool.Wait()

	if len(report.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(report.Variants))
	}
	// Deterministic priority order (EN first), never completion order.
	if report.Variants[0].Language != "en" || report.Variants[1].Language != "es" {
		t.Fatalf("priority order not preserved: %+v", report.Variants)
	}
	for i, v := range report.Variants {
		if v.Priority != i {
			t.Errorf("variant %d priority %d, want %d", i, v.Priority, i)
		}
		if v.WorkerID != i {
			t.Errorf("variant %d worker_id %d, want %d", i, v.WorkerID, i)
		}
		if v.TextReadyAt.IsZero() || v.QueuedAt.IsZero() || v.RenderStartedAt.IsZero() || v.RenderCompletedAt.IsZero() {
			t.Errorf("variant %d has zero timestamps: %+v", i, v)
		}
		if v.RenderStartedAt.Before(v.QueuedAt) {
			t.Errorf("variant %d render_started_at before queued_at", i)
		}
		if v.RenderCompletedAt.Before(v.RenderStartedAt) {
			t.Errorf("variant %d render_completed_at before render_started_at", i)
		}
		if v.Status == "ready" && v.UploadCompletedAt.IsZero() {
			t.Errorf("variant %d ready but upload_completed_at is zero", i)
		}
	}
	if report.Variants[0].TextReadyAt != enReady {
		t.Errorf("EN text_ready_at not propagated: got %v want %v", report.Variants[0].TextReadyAt, enReady)
	}
}
