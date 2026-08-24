// Package e2e — replay_resume_e2e_test.go.
//
// Hermetic end-to-end test for the deterministic execution engine:
//
//	render (8 scenes) → crash at scene 8
//	  ↓
//	resume → scenes 1–7 SKIP (checkpoint + artifact verified), scene 8 EXECUTE
//	  ↓
//	complete (8 checkpoints, 8 artifacts, 8 performance ops)
//	  ↓
//	restart (close + reopen SQLite, rebuild every adapter from disk)
//	  ↓
//	replay (load bundle → materialize+verify assets → resolve strategy)
//	  ↓
//	verify hashes, artifacts, performance (RTF)
//
// Everything is wired with the REAL durable adapters: SQLite checkpoint +
// replay + performance_operations stores (migrations 216/217/218) and the
// filesystem CAS (with the real LocalStore stager). No fakes for persistence:
// durability across the simulated restart is the point.
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	capreplay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/replay"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artifacts"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	sqlitecheckpoint "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/checkpoint"
	sqlitereplay "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/replay"

	_ "github.com/mattn/go-sqlite3"
)

const (
	replayJobID        = "job-e2e"
	replayProcessor    = "rust-render/v3"
	replayRustProtocol = "1.4"
	replayFFmpeg       = "6.1"
)

var replayScenes = []string{
	"scene_01", "scene_02", "scene_03", "scene_04",
	"scene_05", "scene_06", "scene_07", "scene_08",
}

// ── fakes (decision surface only; persistence is real) ───────────────

// alwaysHitCache reports a hit for every key: the final-render artifact is
// already in the cache, so a replay resolves to CACHE_HIT (zero encoding).
type alwaysHitCache struct{}

func (alwaysHitCache) Lookup(_ context.Context, key artifactcache.Key, _ int64) (*artifactcache.Entry, bool, error) {
	digest, err := key.Digest()
	if err != nil {
		return nil, false, err
	}
	return &artifactcache.Entry{CacheKey: digest, ArtifactSHA256: hexRepeat('a'), Status: "READY"}, true, nil
}
func (alwaysHitCache) Store(context.Context, artifactcache.Key, io.Reader, string, int64) (*artifactcache.Entry, error) {
	return nil, errors.New("unused")
}
func (alwaysHitCache) Open(context.Context, *artifactcache.Entry) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (alwaysHitCache) Invalidate(context.Context, artifactcache.Key) error { return nil }
func (alwaysHitCache) Metrics(context.Context, string) (artifactcache.Metrics, error) {
	return artifactcache.Metrics{}, nil
}

var _ artifactcache.Cache = alwaysHitCache{}

type replayE2EDispatcher struct{ got *capreplay.PreparedReplay }

func (d *replayE2EDispatcher) Dispatch(_ context.Context, p capreplay.PreparedReplay) (string, error) {
	d.got = &p
	return "queued", nil
}

var _ capreplay.Dispatcher = (*replayE2EDispatcher)(nil)

// ── helpers ──────────────────────────────────────────────────────────

func hexRepeat(c byte) string { return strings.Repeat(string(c), 64) }

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func replayE2EMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../migrations/sqlite")
	require.NoError(t, err)
	return dir
}

// replayE2EApplyMigrations copies only the 3 relevant migrations into a temp
// dir and applies them to the dbPath, then closes the connection so the WAL
// is checkpointed (the test reopens a fresh handle later).
func replayE2EApplyMigrations(t *testing.T, dbPath string) {
	t.Helper()
	src := replayE2EMigrationsDir(t)
	dst := t.TempDir()
	for _, name := range []string{
		"216_job_checkpoints.sql",
		"217_performance_operations.sql",
		"218_replay_bundles.sql",
	} {
		data, err := os.ReadFile(filepath.Join(src, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dst, name), data, 0o600))
	}
	require.NoError(t, storage.RunMigrationsOnDB(dbPath, nil, dst, "primary"))
}

func replayE2EOpenDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	return db
}

func replayE2ECAS(t *testing.T) (*cas.Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "cas")
	workspace := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o700))
	stager, err := artifacts.NewLocalStore(artifacts.Config{Workspace: workspace})
	require.NoError(t, err)
	store, err := cas.NewStore(cas.Config{Root: root, Stager: stager})
	require.NoError(t, err)
	return store, root
}

type replayE2ECounters struct{ skips, renders int }

func sceneFingerprint(scene string) string { return sha256Hex([]byte("scene:" + scene)) }

// renderScenes runs the scene loop: Decide (SKIP or EXECUTE) per scene, render
// on EXECUTE (write artifact to CAS + complete checkpoint + record the perf
// operation). It mutates counters to prove the resume behaviour.
func renderScenes(t *testing.T, ctx context.Context, resolver *capcheckpoint.Resolver, casStore *cas.Store, db *sql.DB, scenes []string, counters *replayE2ECounters) {
	t.Helper()
	for _, scene := range scenes {
		fp := sceneFingerprint(scene)
		decision, _, err := resolver.Decide(ctx, replayJobID, capcheckpoint.StageRenderScene, scene, capcheckpoint.ExpectedInput{
			InputFingerprint: fp,
			ProcessorVersion: replayProcessor,
		})
		require.NoError(t, err)
		if decision == capcheckpoint.DecisionSkip {
			counters.skips++
			continue
		}
		require.Equal(t, capcheckpoint.DecisionExecute, decision)

		counters.renders++
		obj, err := casStore.Put(ctx, strings.NewReader("rendered:"+scene))
		require.NoError(t, err)
		require.NoError(t, resolver.Complete(ctx, capcheckpoint.Checkpoint{
			JobID:            replayJobID,
			Stage:            capcheckpoint.StageRenderScene,
			UnitID:           scene,
			InputFingerprint: fp,
			Status:           capcheckpoint.StatusCompleted,
			ArtifactSHA256:   obj.SHA256,
			ArtifactURI:      "cas://" + obj.SHA256,
			ProcessorVersion: replayProcessor,
			CompletedAt:      time.Now().UTC(),
		}))

		// Performance: one render_scene operation per scene (source 60s,
		// 1.2s elapsed → RTF 0.02). Recorded into the real table.
		_, err = db.Exec(`INSERT INTO performance_operations
			(operation_id, run_id, job_id, step_id, operation, source_sha256, source_duration_ms, elapsed_ms, cache_hit, strategy, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			"op-"+scene, "run-e2e", replayJobID, capcheckpoint.StageRenderScene, capcheckpoint.StageRenderScene,
			sha256Hex([]byte("src:"+scene)), 60000, 1200, 0, "FULL_RENDER", time.Now().UTC().Format(time.RFC3339))
		require.NoError(t, err)
	}
}

func checkpointCount(t *testing.T, db *sql.DB, jobID string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM job_checkpoints WHERE job_id = ?`, jobID).Scan(&n))
	return n
}

func compileReplayPlan(t *testing.T, shaA, shaB string) render.RenderPlan {
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
		JobID: replayJobID, Revision: "rev-1", OutputPath: "final.mp4", FrameRate: audio.IntegerFrameRate(30),
		Timeline: timeline,
		Manifest: []render.AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: shaA, FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: shaB, FrameCount: 1000},
		},
		ExecutionPolicy: &render.RenderExecutionPolicy{
			AllowStreamCopy:   true,
			TargetProfileHash: hexRepeat('1'),
			RendererVersion:   replayProcessor,
			EncoderPolicyHash: hexRepeat('2'),
		},
	})
	require.NoError(t, err)
	return plan
}

// ── The E2E ──────────────────────────────────────────────────────────

func TestE2E_RenderCrashResumeRestartReplay(t *testing.T) {
	ctx := context.Background()

	// 1. Durable SQLite (file) + CAS (filesystem).
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	replayE2EApplyMigrations(t, dbPath)
	casStore, _ := replayE2ECAS(t)

	db := replayE2EOpenDB(t, dbPath)
	cpStore, err := sqlitecheckpoint.New(db)
	require.NoError(t, err)
	resolver := capcheckpoint.NewResolver(cpStore, cas.NewArtifactVerifier(casStore))

	// 2. Source assets (the clips the final render replays).
	clipA := []byte("clip-a source bytes")
	clipB := []byte("clip-b source bytes")
	objA, err := casStore.Put(ctx, bytes.NewReader(clipA))
	require.NoError(t, err)
	objB, err := casStore.Put(ctx, bytes.NewReader(clipB))
	require.NoError(t, err)

	// 3. Render scenes 1–7, then CRASH before scene 8.
	counters := &replayE2ECounters{}
	renderScenes(t, ctx, resolver, casStore, db, replayScenes[:7], counters)
	require.Equal(t, 7, counters.renders, "first run renders 7 scenes")
	require.Equal(t, 0, counters.skips, "first run skips nothing")
	require.Equal(t, 7, checkpointCount(t, db, replayJobID), "7 checkpoints after crash")

	// 4. Resume: scenes 1–7 SKIP (checkpoint + artifact verified), scene 8 EXECUTE.
	renderScenes(t, ctx, resolver, casStore, db, replayScenes, counters)
	require.Equal(t, 7, counters.skips, "resume skips scenes 1–7")
	require.Equal(t, 8, counters.renders, "resume renders only scene 8")
	require.Equal(t, 8, checkpointCount(t, db, replayJobID), "8 checkpoints after resume")

	// 5. Save the replay bundle (final render identity + source assets).
	plan := compileReplayPlan(t, objA.SHA256, objB.SHA256)
	bundleStore, err := sqlitereplay.New(db)
	require.NoError(t, err)
	require.NoError(t, bundleStore.Save(ctx, capreplay.ReplayBundle{
		Version:             capreplay.BundleVersion,
		OriginalJobID:       replayJobID,
		RenderPlan:          plan,
		PlanSHA256:          plan.PlanSHA256,
		RendererVersion:     replayProcessor,
		RustProtocolVersion: replayRustProtocol,
		FFmpegVersion:       replayFFmpeg,
		EncoderPolicyHash:   hexRepeat('e'),
		Assets:              capreplay.BuildAssets(plan),
		CreatedAt:           time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	}))

	// 6. RESTART: close the DB handle and rebuild every adapter from disk.
	require.NoError(t, db.Close())
	db = replayE2EOpenDB(t, dbPath)
	cpStore2, err := sqlitecheckpoint.New(db)
	require.NoError(t, err)
	bundleStore2, err := sqlitereplay.New(db)
	require.NoError(t, err)
	replaySource, err := cas.NewReplayAssetSource(casStore, filepath.Join(t.TempDir(), "staging"))
	require.NoError(t, err)

	// Durability check: checkpoints survived the restart.
	cp, err := cpStore2.Get(ctx, replayJobID, capcheckpoint.StageRenderScene, "scene_01")
	require.NoError(t, err)
	require.NotNil(t, cp, "checkpoint must survive the restart")
	verified, err := casStore.Verify(ctx, cp.ArtifactSHA256)
	require.NoError(t, err)
	require.True(t, verified.Verified, "scene artifact must still verify after restart")

	// 7. Replay: exact mode against the current environment; zero-encoding via
	//    the real SmartResolver (artifact cache hit).
	strategy := render.NewSmartResolver(nil, alwaysHitCache{}, render.TargetProfile{Codec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080})
	engine := capreplay.NewEngine(bundleStore2, replaySource, strategy)
	engine.SetIDGenerator(func(original string) string { return original + "-replay-001" })

	prepared, err := engine.Prepare(ctx, replayJobID, capreplay.ModeExact, capreplay.Environment{
		RendererVersion:     replayProcessor,
		RustProtocolVersion: replayRustProtocol,
		FFmpegVersion:       replayFFmpeg,
		EncoderPolicyHash:   hexRepeat('e'),
	})
	require.NoError(t, err)

	// 8. Verify hashes, artifacts, zero-encoding.
	require.Equal(t, "job-e2e-replay-001", prepared.ReplayJobID)
	require.NotEqual(t, prepared.ReplayJobID, prepared.OriginalJobID)
	require.Equal(t, plan.PlanSHA256, prepared.PlanSHA256, "plan sha must match the sealed bundle")
	require.Equal(t, render.ExecutionCache, prepared.Strategy.Mode, "replay must resolve to CACHE_HIT (zero encoding)")
	require.Len(t, prepared.Materialized, 2)

	byID := map[string]capreplay.MaterializedAsset{}
	for _, m := range prepared.Materialized {
		byID[m.AssetID] = m
		// Byte-for-byte verification of each materialized asset.
		data, err := os.ReadFile(m.LocalPath)
		require.NoError(t, err)
		require.Equal(t, m.SHA256, sha256Hex(data), "materialized bytes must hash to the recorded SHA")
	}
	require.Equal(t, objA.SHA256, byID["clip-a"].SHA256)
	require.Equal(t, objB.SHA256, byID["clip-b"].SHA256)

	// 9. Dispatch → new replay job, separate identity.
	dispatcher := &replayE2EDispatcher{}
	status, err := dispatcher.Dispatch(ctx, *prepared)
	require.NoError(t, err)
	require.Equal(t, "queued", status)
	require.Equal(t, "job-e2e-replay-001", dispatcher.got.ReplayJobID)

	// 10. Performance: 8 render_scene operations recorded; RTF = 1.2s / 60s = 0.02.
	var ops int
	var rtf float64
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(elapsed_ms) * 1.0 / NULLIF(SUM(source_duration_ms), 0), 0)
		 FROM performance_operations WHERE operation = ?`, capcheckpoint.StageRenderScene,
	).Scan(&ops, &rtf))
	require.Equal(t, 8, ops, "8 render_scene operations must be recorded")
	require.InDelta(t, 0.02, rtf, 1e-9, "RTF must be elapsed/source duration")
}
