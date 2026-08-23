package replay

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	capreplay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/replay"
	_ "github.com/mattn/go-sqlite3"
)

const testSchema = `
CREATE TABLE replay_bundles (
    original_job_id TEXT PRIMARY KEY,
    version TEXT NOT NULL,
    plan_sha256 TEXT NOT NULL,
    renderer_version TEXT NOT NULL,
    rust_protocol_version TEXT NOT NULL,
    ffmpeg_version TEXT NOT NULL,
    encoder_policy_hash TEXT NOT NULL DEFAULT '',
    render_plan_json TEXT NOT NULL,
    assets_json TEXT NOT NULL,
    created_at TEXT NOT NULL
);`

func hash64(c byte) string { return strings.Repeat(string(c), 64) }

func testPlan(t *testing.T) render.RenderPlan {
	t.Helper()
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 18000000,
		Segments: []audio.TimelineSegment{
			{ID: "a", Index: 0, TimelineStartUS: 0, DurationUS: 5600000, Video: audio.VideoSegment{AssetID: "clip-a", SourceInUS: 33200000, SourceDurationUS: 5600000}, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
			{ID: "b", Index: 1, TimelineStartUS: 5600000, DurationUS: 12400000, Video: audio.VideoSegment{AssetID: "clip-b", SourceInUS: 7100000, SourceDurationUS: 12400000}, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
		},
	}
	plan, err := render.Compile(render.CompileInput{
		JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FrameRate: audio.IntegerFrameRate(30),
		Timeline: timeline,
		Manifest: []render.AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: hash64('a'), FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: hash64('b'), FrameCount: 1000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func testBundle(t *testing.T) capreplay.ReplayBundle {
	t.Helper()
	plan := testPlan(t)
	return capreplay.ReplayBundle{
		Version:             capreplay.BundleVersion,
		OriginalJobID:       "job-1",
		RenderPlan:          plan,
		PlanSHA256:          plan.PlanSHA256,
		RendererVersion:     "rust-render/v3",
		RustProtocolVersion: "1.4",
		FFmpegVersion:       "6.1",
		EncoderPolicyHash:   hash64('e'),
		Assets:              capreplay.BuildAssets(plan),
		CreatedAt:           time.Date(2026, 8, 18, 12, 0, 0, 123456789, time.UTC),
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:replay-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(testSchema); err != nil {
		t.Fatal(err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestGetMissReturnsNil(t *testing.T) {
	store := newTestStore(t)
	got, err := store.Get(context.Background(), "job-none")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("missing bundle must return nil, got %+v", got)
	}
}

func TestSaveThenGetRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	want := testBundle(t)
	if err := store.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, want.OriginalJobID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("saved bundle must be readable")
	}
	if got.PlanSHA256 != want.PlanSHA256 || got.RendererVersion != want.RendererVersion ||
		got.RustProtocolVersion != want.RustProtocolVersion || got.FFmpegVersion != want.FFmpegVersion ||
		got.EncoderPolicyHash != want.EncoderPolicyHash {
		t.Fatalf("bundle round-trip mismatch: %+v", got)
	}
	if got.RenderPlan.PlanSHA256 != want.RenderPlan.PlanSHA256 {
		t.Fatal("render plan did not round-trip")
	}
	if len(got.Assets) != len(want.Assets) {
		t.Fatalf("assets did not round-trip: got %d want %d", len(got.Assets), len(want.Assets))
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("created_at round-trip mismatch: got %v want %v", got.CreatedAt, want.CreatedAt)
	}
}

func TestSaveUpsertsSameJob(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first := testBundle(t)
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := testBundle(t)
	second.RendererVersion = "rust-render/v4"
	second.CreatedAt = second.CreatedAt.Add(time.Hour)
	if err := store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, first.OriginalJobID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.RendererVersion != "rust-render/v4" {
		t.Fatalf("re-save must converge on the same row: %+v", got)
	}
}

func TestSaveRejectsInvalidBundle(t *testing.T) {
	store := newTestStore(t)
	bundle := testBundle(t)
	bundle.PlanSHA256 = hash64('9')
	if err := store.Save(context.Background(), bundle); err == nil {
		t.Fatal("invalid bundle must not be persisted")
	}
}

func TestGetRejectsEmptyJobID(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Get(context.Background(), ""); err == nil {
		t.Fatal("empty job id must be rejected")
	}
}
