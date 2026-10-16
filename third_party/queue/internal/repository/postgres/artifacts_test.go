package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

func TestMetricUnitUsesMetricSemantics(t *testing.T) {
	cases := map[string]string{
		"sha256_us": "us", "render_ms": "ms", "output_bytes": "bytes",
		"chronon_gpu_vram_peak_mb": "mb", "fps": "fps", "cache_hit_ratio": "ratio",
		"frame_count": "count",
	}
	for name, want := range cases {
		if got := metricUnit(name); got != want {
			t.Errorf("metricUnit(%q)=%q, want %q", name, got, want)
		}
	}
}

func TestArtifactPersistedOnComplete(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	art := model.Artifact{
		ID:                 "art-1",
		Kind:               "overlay",
		StorageKey:         "overlay/job-1/out.mp4",
		ArtifactURL:        "s3://bucket/out.mp4",
		ArtifactHash:       "abc123",
		ContentType:        "video/mp4",
		SizeBytes:          12345,
		Width:              1920,
		Height:             1080,
		FPSNum:             30,
		FPSDen:             1,
		FrameCount:         546,
		DurationUS:         18200000,
		ProfileID:          queueclient.CertifiedProfileVeloxH2641080p30V1,
		CopyEligible:       true,
		Codec:              "h264",
		CodecProfile:       "high",
		ClosedGOP:          true,
		FirstFrameKeyframe: true,
		Provenance:         &queueclient.ArtifactProvenance{SchemaVersion: "renderinggen.artifact-provenance.v1", Protocol: "renderinggen.overlay-import.v1", Producer: "pipelinegen-map", ImportJobID: "job-1", SourceSHA256: "abc123", SourceSizeBytes: 12345, IdentityVerified: true, StructureVerified: true, FullDecodeVerified: true},
	}
	if err := r.Complete(context.Background(), "job-1", "w1", art); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	var (
		sha          string
		copyEligible bool
		codec        string
		closedGOP    bool
	)
	err := db.QueryRowContext(ctx, `
		SELECT sha256, copy_eligible, codec, closed_gop
		FROM render_artifacts
		WHERE id = 'art-1'`).Scan(&sha, &copyEligible, &codec, &closedGOP)
	if err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if sha != "abc123" || !copyEligible || codec != "h264" || !closedGOP {
		t.Fatalf("artifact not persisted correctly: sha=%q copy=%v codec=%q gop=%v",
			sha, copyEligible, codec, closedGOP)
	}
	var provenanceRaw []byte
	if err := db.QueryRowContext(ctx, `SELECT provenance FROM render_artifacts WHERE id='art-1'`).Scan(&provenanceRaw); err != nil {
		t.Fatalf("query artifact provenance: %v", err)
	}
	var persisted queueclient.ArtifactProvenance
	if err := json.Unmarshal(provenanceRaw, &persisted); err != nil {
		t.Fatalf("decode artifact provenance: %v", err)
	}
	if persisted.Protocol != "renderinggen.overlay-import.v1" || persisted.Producer != "pipelinegen-map" || !persisted.FullDecodeVerified {
		t.Fatalf("artifact provenance = %+v", persisted)
	}

	var jobArtifactID string
	if err := db.QueryRowContext(ctx, `
		SELECT artifact_id FROM render_jobs WHERE id = 'job-1'`).Scan(&jobArtifactID); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if jobArtifactID != "art-1" {
		t.Fatalf("job not linked to artifact: got %q", jobArtifactID)
	}
}

func TestGetReturnsJobWithArtifact(t *testing.T) {
	r, _ := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(context.Background(), "job-1", "w1", model.Artifact{
		ID: "art-1", StorageKey: "overlay/job-1/out.mp4", ArtifactHash: "abc", ProfileID: queueclient.CertifiedProfileVeloxH2641080p30V1, CopyEligible: true,
	}); err != nil {
		t.Fatal(err)
	}

	job, err := r.Get(context.Background(), "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StateCompleted {
		t.Fatalf("want completed, got %s", job.State)
	}
	if job.Artifact == nil || job.Artifact.ID != "art-1" || !job.Artifact.CopyEligible {
		t.Fatalf("artifact not returned: %+v", job.Artifact)
	}
}

func TestGetMissingJobReturnsNotFound(t *testing.T) {
	r, _ := setupRepo(t, 30*time.Second, 3)
	if _, err := r.Get(context.Background(), "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCompleteWithoutArtifactSkipsPersistence(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if err := r.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	// Empty artifact: the job completes but no artifact row is written.
	if err := r.Complete(context.Background(), "job-1", "w1", model.Artifact{}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM render_artifacts WHERE job_id = 'job-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("want 0 artifacts, got %d", count)
	}
}
