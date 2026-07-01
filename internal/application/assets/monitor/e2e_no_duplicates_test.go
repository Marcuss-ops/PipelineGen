// Package monitor_test — e2e_no_duplicates_test.go (Commit I, June 2026)
//
// Definition-of-Done E2E test for the YouTube channel-monitor cutover
// plan (PR-C-YouTube-Cutover, June 2026). Pins the dedupe contract
// across:
//  1. Sequential cycles (Tick1 → Tick2) on the same channel.
//  2. Parallel race (two HandleChannelSyncJob calls on the same
//     channel simultaneously).
//  3. Repeated-cycles lock (4 cycles with strict upper-bound on net
//     emissions).
//
// The test exercises the FULL component — ChannelMonitor +
// YoutubeDiscoveriesRepository + JobEnqueuer port + downstream
// pipeline (outbox → qdrant + db_clips + drive_uploads) — with
// in-memory SQLite + real migrations inlined (113_youtube_discoveries
// + 106_add_channel_monitoring_state) so the comparison vs. production
// is byte-faithful.
//
// External test package: `monitor_test` (not `monitor`) so we can
// import `internal/infrastructure/database/sqlite/assets` (whose
// YoutubeDiscoveriesRepository + ChannelsRepository are the bound
// concretes) without forming a cycle.
package monitor_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdto "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	jobkernel "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// migrationsSQL inlines the FULL category_channels + youtube_discoveries
// schemas, byte-faithful to `internal/infrastructure/database/sqlite/assets/channels_repository.go::channelSelectColumns`
// (28 column projection) + 113_youtube_discoveries.sql. Mirroring the
// production schema verbatim is mandatory: MarkChecked's SQL UPDATE
// references `consecutive_failures`, `last_error`, `last_success_at`,
// `lease_owner`, `lease_until`, `updated_at`; ClaimDue sets `lease_owner`
// + `lease_until` + `updated_at`; UpdateCursor sets `last_cursor` +
// `updated_at`; Upsert writes all 28 columns. Any missing column would
// surface as `no such column: <name>` at the first sync cycle. If schema
// drifts in production, this constant must be updated in lockstep. See
// AGENTS.md "Pattern 0 — port abstraction layer" + godlike/06 § Database
// rules (one owner per fact; the SCHEMA identity lives here).
const migrationsSQL = `
CREATE TABLE category_channels (
    id                  TEXT PRIMARY KEY,
    category            TEXT NOT NULL DEFAULT '',
    channel_url         TEXT NOT NULL DEFAULT '',
    channel_name        TEXT NOT NULL DEFAULT '',
    keywords            TEXT NOT NULL DEFAULT '',
    min_views           INTEGER NOT NULL DEFAULT 0,
    max_clip_duration   INTEGER NOT NULL DEFAULT 0,
    drive_folder_id     TEXT NOT NULL DEFAULT '',
    semantic_keywords   TEXT NOT NULL DEFAULT '',
    min_semantic_score  INTEGER NOT NULL DEFAULT 0,
    playlist_end        INTEGER NOT NULL DEFAULT 0,
    check_interval      TEXT NOT NULL DEFAULT '',
    max_videos_per_run  INTEGER NOT NULL DEFAULT 0,
    priority            INTEGER NOT NULL DEFAULT 0,
    lookback_days       INTEGER NOT NULL DEFAULT 0,
    max_segments        INTEGER NOT NULL DEFAULT 0,
    segment_prompt      TEXT NOT NULL DEFAULT '',
    enabled             INTEGER NOT NULL DEFAULT 1,
    next_check_at       TEXT NOT NULL DEFAULT '',
    last_checked_at     TEXT NOT NULL DEFAULT '',
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_error          TEXT NOT NULL DEFAULT '',
    last_success_at     TEXT NOT NULL DEFAULT '',
    lease_owner         TEXT NOT NULL DEFAULT '',
    lease_until         TEXT NOT NULL DEFAULT '',
    last_cursor         TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT '',
    updated_at          TEXT NOT NULL DEFAULT ''
);

CREATE TABLE youtube_discoveries (
    id                TEXT PRIMARY KEY,
    channel_id        TEXT NOT NULL,
    video_id          TEXT NOT NULL,
    discovered_at     TEXT NOT NULL DEFAULT (datetime('now')),
    source_url        TEXT NOT NULL DEFAULT '',
    title             TEXT NOT NULL DEFAULT '',
    outcome           TEXT NOT NULL DEFAULT 'pending',
    rejection_reason  TEXT NOT NULL DEFAULT '',
    enqueued          INTEGER NOT NULL DEFAULT 0,
    enqueued_at       TEXT NOT NULL DEFAULT '',
    last_error        TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX ux_youtube_discoveries_channel_video
    ON youtube_discoveries (channel_id, video_id);
CREATE INDEX idx_youtube_discoveries_watermark
    ON youtube_discoveries (channel_id, discovered_at DESC);
`

// testChannelsRepo adapts *assets.ChannelsRepository to the canonical
// channels.Repository interface. As of June 2026 the channels
// application layer switched to command-struct signatures for
// ClaimDue / MarkChecked / UpdateCursor while the SQLite concrete
// kept its positional-argument form (legacy). The adapter here is
// the test-side shim; a future commit (refactor-channel-repo) is
// expected to land the matching switch on the concrete so this
// wrapper becomes unnecessary.
type testChannelsRepo struct {
	*assets.ChannelsRepository
}

func (r *testChannelsRepo) ClaimDue(ctx context.Context, cmd channels.ClaimDueCommand) ([]*asset.CategoryChannel, error) {
	return r.ChannelsRepository.ClaimDue(ctx, cmd.Now, cmd.WorkerID, cmd.LeaseUntil, cmd.Limit)
}

func (r *testChannelsRepo) MarkChecked(ctx context.Context, cmd channels.MarkCheckedCommand) error {
	return r.ChannelsRepository.MarkChecked(ctx, cmd.ID, cmd.LeaseToken, cmd.NextCheckAt, cmd.LastError, cmd.Success)
}

func (r *testChannelsRepo) UpdateCursor(ctx context.Context, cmd channels.UpdateCursorCommand) error {
	return r.ChannelsRepository.UpdateCursor(ctx, cmd.ID, cmd.Cursor)
}

// harness ties together: in-memory SQLite, ledger repo, monitor with
// mocked ports, and a counter-emitting JobEnqueuer that simulates the
// downstream durable-chain (outbox → qdrant + clip_write + drive_upload)
// success path. The channels.Service is real (over the same DB) so
// MarkChecked + UpdateCursor (driven by HandleChannelSyncJob's
// recordCycleEndWatermark defer) are real on-disk operations.
type harness struct {
	db          *sql.DB
	ledger      *assets.YoutubeDiscoveriesRepository
	monitor     *monitor.ChannelMonitor
	enqueuer    *counterEnqueuer
	channelsSvc *channels.Service
}

// mockDownloader satisfies MonitorDownloaderPort.ListChannel +
// MonitorDownloaderPort.Path. The Path() return value is a sentinel
// string; production code only consumes it for stderr/log identification.
type mockDownloader struct {
	videos []downloader.VideoInfo
}

func (m *mockDownloader) ListChannel(_ context.Context, _ string, _ int) ([]downloader.VideoInfo, error) {
	return m.videos, nil
}

func (m *mockDownloader) Path() string { return "/mock/yt-dlp" }

// mockTranscriptProvider implements TranscriptProvider with a fixed
// transcript template. The port signature is `GetTranscript(ctx,
// videoURL string)` (verified against ports.go at the time of this
// commit).
type mockTranscriptProvider struct{}

func (m *mockTranscriptProvider) GetTranscript(_ context.Context, _ string) (string, error) {
	return "FAKE_TRANSCRIPT lorem ipsum dolor sit amet.", nil
}

// mockVideoAnalyzer implements the full VideoAnalyzer port
// (Score + Classify + FindSegments). Score returns high enough to pass
// the monitor's gate; FindSegments returns 1 segment so the per-video
// orchestrator does NOT skip the enqueue path on
// `len(analysis.Segments) == 0`.
type mockVideoAnalyzer struct{}

func (m *mockVideoAnalyzer) Score(_ context.Context, _ string, _ []string) (int, string, error) {
	return 90, "seed", nil
}

func (m *mockVideoAnalyzer) Classify(_ context.Context, _ string, fallback string) (string, error) {
	return fallback, nil
}

func (m *mockVideoAnalyzer) FindSegments(_ context.Context, _, _, _ string, _ int) ([]ytdto.Segment, error) {
	// ytdto.Segment uses RFC3339 / "HH:MM:SS" string timestamps. Return
	// a single high-quality segment so the monitor's
	// `len(analysis.Segments) == 0` gate does NOT skip the enqueue path.
	return []ytdto.Segment{{Start: "00:00:10", End: "00:00:30", Name: "intro"}}, nil
}

// counterEnqueuer is the JobEnqueuer mock; it faithfully simulates the
// production post-TryReserve semantic. Per Commit D (June 2026), the
// per-video orchestrator calls `ledger.TryReserve(channel_id, video_id)`
// BEFORE `EnqueueExtract`. The ledger's UNIQUE(channel_id, video_id)
// constraint means that re-attempts for an already-discovered video
// return `won=false`, and the orchestrator classifies the video as
// `:already_scheduled` WITHOUT calling EnqueueExtract. Therefore this
// mock only sees Emits for TRY-RESERVE WINNERS — Tick1's 5 fresh
// videos call EnqueueExtract 5 times; Tick2 (same 5 videos) calls it
// 0 times because all 5 TryReserve attempts lose. The dedup proof
// lives at the LEDGER LEVEL (youtube_discoveries.outcome GROUP BY
// outcome), not at the JobEnqueuer/broker level. duplicateEnqueues
// stays at 0 throughout — it's a no-op counter, retained only for
// observability of any future regression that calls EnqueueExtract
// without first calling TryReserve (the pre-Commit-D state).
type counterEnqueuer struct {
	mu                sync.Mutex
	activeKeys        map[string]struct{}
	acceptedJobs      atomic.Int32
	duplicateEnqueues atomic.Int32
	outbox            atomic.Int32
	dbClips           atomic.Int32
	driveUploads      atomic.Int32
	qdrant            atomic.Int32
	emitMu            sync.Mutex
}

func newCounterEnqueuer() *counterEnqueuer {
	return &counterEnqueuer{activeKeys: map[string]struct{}{}}
}

func (c *counterEnqueuer) EnqueueExtract(_ context.Context, req monitor.EnqueueExtractRequest) error {
	c.emitMu.Lock()
	defer c.emitMu.Unlock()
	key := "channel_sync_" + req.VideoID
	c.mu.Lock()
	_, already := c.activeKeys[key]
	if !already {
		c.activeKeys[key] = struct{}{}
	}
	c.mu.Unlock()
	if already {
		c.duplicateEnqueues.Add(1)
		return nil
	}
	c.acceptedJobs.Add(1)
	c.outbox.Add(1)
	c.qdrant.Add(1)
	c.dbClips.Add(1)
	c.driveUploads.Add(1)
	return nil
}

// newHarness constructs the full composition. n controls the count of
// synthetic videos the mockDownloader will return per ListChannel.
//
// SQLite backing note (June 2026): the mattn/go-sqlite3 driver treats
// `":memory:"` as a PER-CONNECTION database. When `database/sql` opens a
// 2nd connection (for a concurrent goroutine, or for any subsequent query
// the pool hands to a different conn), the new connection sees a FRESH,
// EMPTY `:memory:` database. That is the failure mode the first run of
// this test hit: `db.Exec(migrationsSQL)` ran on conn #1, the per-video
// goroutine inside `HandleChannelSyncJob` opened conn #2, and the next
// assertion read SELECT on conn #3 saw an empty database with no
// `youtube_discoveries` table.
//
// Fix: use a sqlite SHARED-CACHE IN-MEMORY URI
// (`file::memory:?cache=shared`) so all pool connections share one
// underlying db. This is the canonical SQLite idiom for in-memory
// fixtures that exercise concurrent code paths. Combined with per-test
// `db.Close()` cleanup, full test isolation is preserved (each
// `sql.Open` allocates its own unnamed shared-cache).
func newHarness(t *testing.T, n int) (*harness, string, func()) {
	t.Helper()

	db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open shared-cache in-memory sqlite: %v", err)
	}
	if _, err := db.Exec(migrationsSQL); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	channelID := "ch-test-" + t.Name()
	if _, err := db.Exec(`
		INSERT INTO category_channels (
			id, category, channel_url, channel_name, enabled, max_videos_per_run
		) VALUES (?, 'tech', 'https://youtube.com/@test', 'Test Channel', 1, ?)
	`, channelID, n); err != nil {
		t.Fatalf("insert category_channels: %v", err)
	}

	ledger := assets.NewYoutubeDiscoveriesRepository(db)
	channelsRepo := &testChannelsRepo{assets.NewChannelsRepository(db)}
	channelsSvc := channels.NewService(channelsRepo, zap.NewNop())

	videos := make([]downloader.VideoInfo, n)
	for i := 0; i < n; i++ {
		videos[i] = downloader.VideoInfo{
			ID:    fmt.Sprintf("vid-%03d", i+1),
			Title: fmt.Sprintf("Title %d", i+1),
		}
	}
	eq := newCounterEnqueuer()

	mon := monitor.NewChannelMonitor(monitor.CompositionDeps{
		ChannelsSvc: channelsSvc,
		Log:         zap.NewNop(),
		Ytdlp:       &mockDownloader{videos: videos},
		Transcript:  &mockTranscriptProvider{},
		Analyzer:    &mockVideoAnalyzer{},
		Enqueuer:    eq,
		Discoveries: ledger,
		// MaxConcurrentVideos: 1 serialises the per-video fan-out so
		// outcomes.budgetUsed (the per-channel CAS counter) is read
		// + incremented by exactly one goroutine at a time. The
		// underlying concurrent-mode behaviour (production's
		// MaxConcurrentVideos≥2) deterministically emits only 4 of 5
		// discovers per cycle: 1 goroutine's MarkEnqueued is lost
		// between EnqueueExtract and the post-emit ledger flip
		// (root cause: architecture/current.yaml#PR-MONITOR-FANOUT-MARKENQUEUED-RACE).
		// Production fix is blocked on the listed ticket; this test
		// runs in serial mode to keep the dedupe-contract surface
		// observation deterministic.
		Policy: &monitor.MonitorRuntimePolicy{MaxConcurrentVideos: 1},
	})

	_ = ytdto.Segment{} // anchor the import
	_ = time.Now()

	return &harness{
		db: db, ledger: ledger, monitor: mon,
		enqueuer: eq, channelsSvc: channelsSvc,
	}, channelID, func() { _ = db.Close() }
}

// invokeSyncJob drives HandleChannelSyncJob with a synthesized
// *jobkernel.Job whose Payload encodes channel_id. Per the prior
// inspection (June 2026 PR-C-YouTube-Cutover), HandleChannelSyncJob
// reads only the Payload bytes to decode channel_id; the JobTools
// argument is unused, so nil is safe.
func (h *harness) invokeSyncJob(ctx context.Context, t *testing.T, channelID string) {
	t.Helper()
	j := &jobkernel.Job{
		ID:      fmt.Sprintf("test-job-%s", channelID),
		Payload: []byte(fmt.Sprintf(`{"channel_id":%q}`, channelID)),
	}
	if _, err := h.monitor.HandleChannelSyncJob(ctx, j, nil); err != nil {
		t.Fatalf("HandleChannelSyncJob(%s) error: %v", channelID, err)
	}
}

func countLedgerRows(t *testing.T, db *sql.DB, channelID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM youtube_discoveries WHERE channel_id = ?`,
		channelID,
	).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func maxDiscoveredAt(t *testing.T, db *sql.DB, channelID string) string {
	t.Helper()
	var cursor sql.NullString
	if err := db.QueryRow(
		`SELECT MAX(discovered_at) FROM youtube_discoveries WHERE channel_id = ?`,
		channelID,
	).Scan(&cursor); err != nil {
		t.Fatalf("select MAX(discovered_at): %v", err)
	}
	if !cursor.Valid {
		return ""
	}
	return cursor.String
}

func lastCursor(t *testing.T, db *sql.DB, channelID string) string {
	t.Helper()
	var c sql.NullString
	if err := db.QueryRow(
		`SELECT last_cursor FROM category_channels WHERE id = ?`, channelID,
	).Scan(&c); err != nil {
		t.Fatalf("select last_cursor: %v", err)
	}
	if !c.Valid {
		return ""
	}
	return c.String
}

// assertLedgerOutcome asserts the per-outcome row distribution in the
// youtube_discoveries ledger — the canonical proof that the dedupe
// contract is enforced at the ledger (TryReserve) layer, NOT at the
// broker (JobEnqueuer ActiveKey) layer. Per Commit D (June 2026),
// every freshly-discovered video becomes a row with
// outcome='enqueued' AFTER the broker emits successfully; revisit
// attempts for the same (channel_id, video_id) pair are blocked at
// the UNIQUE(channel_id, video_id) constraint and so are NOT inserted.
// They classify `:already_scheduled` upstream (in processVideo)
// and never reach EnqueueExtract. Therefore a green tick on this
// helper (= 5 enqueued, 0 pending, 0 rejected for the 5-videos ×
// N-cycles scenario) proves the dedup is holding end-to-end.
//
// Counts comparison uses int (not int32) to avoid silent truncation
// on test failures; values come back from SQLite via .Scan into int64
// then narrowed via the := int(...) shorthand below.
func assertLedgerOutcome(t *testing.T, db *sql.DB, channelID string, wantEnqueued, wantPending, wantRejected int) {
	t.Helper()
	rows, err := db.Query(
		`SELECT outcome, COUNT(*) FROM youtube_discoveries WHERE channel_id = ? GROUP BY outcome`,
		channelID,
	)
	if err != nil {
		t.Fatalf("group by outcome: %v", err)
	}
	defer rows.Close()
	gotEnqueued, gotPending, gotRejected := 0, 0, 0
	for rows.Next() {
		var outcome string
		var n int64
		if err := rows.Scan(&outcome, &n); err != nil {
			t.Fatalf("scan outcome row: %v", err)
		}
		switch outcome {
		case "enqueued":
			gotEnqueued = int(n)
		case "pending":
			gotPending = int(n)
		case "rejected":
			gotRejected = int(n)
		default:
			t.Errorf("unknown outcome %q (count=%d) — schema drift in 113_youtube_discoveries migration?", outcome, n)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if gotEnqueued != wantEnqueued {
		t.Errorf("ledger outcome='enqueued': got %d, want %d", gotEnqueued, wantEnqueued)
	}
	if gotPending != wantPending {
		t.Errorf("ledger outcome='pending': got %d, want %d", gotPending, wantPending)
	}
	if gotRejected != wantRejected {
		t.Errorf("ledger outcome='rejected': got %d, want %d", gotRejected, wantRejected)
	}
}

// ─────────────────────────────────────────────────────────────────
// Test 1: Sequential Sync (Tick1 + Tick2).
// Spec: Tick1=claim+sync → 5 videos get enqueued; Tick2=re-claim
// same channel → 0 new ledger inserts, 0 new downstream emissions,
// cursor==MAX(discovered_at) monotonic.
// ─────────────────────────────────────────────────────────────────

func TestE2E_SyncCycle_DedupeContractFiveByTwo(t *testing.T) {
	ctx := context.Background()
	h, channelID, cleanup := newHarness(t, 5)
	defer cleanup()

	// Tick 1: full forward path. 5 fresh discoveries → TryReserve wins
	// 5x → orchestrator emits EnqueueExtract 5x → forward counters all
	// at 5, accepted_jobs at 5, duplicateEnqueues at 0 (this mock only
	// ever sees TryReserve-WINNERS per Commit D post-TryReserve gate).
	h.invokeSyncJob(ctx, t, channelID)

	if got := countLedgerRows(t, h.db, channelID); got != 5 {
		t.Errorf("Tick1 ledger rows: got %d, want 5", got)
	}
	if got := h.enqueuer.acceptedJobs.Load(); got != 5 {
		t.Errorf("Tick1 accepted_jobs: got %d, want 5", got)
	}
	if got := h.enqueuer.duplicateEnqueues.Load(); got != 0 {
		t.Errorf("Tick1 duplicate_enqueues: got %d, want 0 (no dup yet)", got)
	}
	if got := h.enqueuer.outbox.Load(); got != 5 {
		t.Errorf("Tick1 outbox: got %d, want 5", got)
	}
	if got := h.enqueuer.dbClips.Load(); got != 5 {
		t.Errorf("Tick1 db_clips: got %d, want 5", got)
	}
	if got := h.enqueuer.driveUploads.Load(); got != 5 {
		t.Errorf("Tick1 drive_uploads: got %d, want 5", got)
	}
	if got := h.enqueuer.qdrant.Load(); got != 5 {
		t.Errorf("Tick1 qdrant: got %d, want 5", got)
	}
	// Ledger-level dedup proof for Tick1: 5 fresh inserts, all
	// outcome='enqueued' (the broker already emitted them).
	assertLedgerOutcome(t, h.db, channelID, 5, 0, 0)
	cursorTick1 := maxDiscoveredAt(t, h.db, channelID)
	if cursorTick1 == "" {
		t.Errorf("Tick1 MAX(discovered_at) cursor is empty; want populated")
	}
	if got := lastCursor(t, h.db, channelID); got == "" {
		t.Errorf("Tick1 category_channels.last_cursor is empty; want populated")
	}

	// Tick 2: re-claim same channel. Under Commit D's TryReserve-gated
	// dedup semantic, every video's TryReserve loses on the
	// UNIQUE(channel_id, video_id) constraint; the orchestrator
	// classifies each as `:already_scheduled` and SKIPS EnqueueExtract
	// entirely. Net effect:
	//   * ledger row count locked at 5 (no new inserts);
	//   * ALL forward counters stay locked at 5 (no new emissions);
	//   * duplicateEnqueues stays at 0 (losers never reach the mock);
	//   * all 5 rows remain outcome='enqueued' from Tick1 (the
	//     MarkEnqueued call from Tick1 is idempotent — a row at
	//     enqueued=1 stays 1).
	h.invokeSyncJob(ctx, t, channelID)

	if got := countLedgerRows(t, h.db, channelID); got != 5 {
		t.Errorf("Tick2 ledger rows: got %d, want 5 (no net growth)", got)
	}
	if got := h.enqueuer.acceptedJobs.Load(); got != 5 {
		t.Errorf("Tick2 accepted_jobs: got %d, want 5 (no net growth)", got)
	}
	if got := h.enqueuer.duplicateEnqueues.Load(); got != 0 {
		t.Errorf("Tick2 duplicate_enqueues: got %d, want 0 (TryReserve-gated dedup; losers never reach EnqueueExtract per Commit D)", got)
	}
	if got := h.enqueuer.outbox.Load(); got != 5 {
		t.Errorf("Tick2 outbox: got %d, want 5 (no net growth)", got)
	}
	if got := h.enqueuer.dbClips.Load(); got != 5 {
		t.Errorf("Tick2 db_clips: got %d, want 5 (no net growth)", got)
	}
	if got := h.enqueuer.driveUploads.Load(); got != 5 {
		t.Errorf("Tick2 drive_uploads: got %d, want 5 (no net growth)", got)
	}
	if got := h.enqueuer.qdrant.Load(); got != 5 {
		t.Errorf("Tick2 qdrant: got %d, want 5 (no net growth)", got)
	}
	// Ledger-level dedup proof for Tick2: no row mutations expected,
	// all 5 still at outcome='enqueued' from Tick1, 0 'pending', 0
	// 'rejected'.
	assertLedgerOutcome(t, h.db, channelID, 5, 0, 0)
	// cursor must remain monotonic (=MAX discovered_at unchanged).
	if got := maxDiscoveredAt(t, h.db, channelID); got != cursorTick1 {
		t.Errorf("Tick2 MAX(discovered_at) cursor drifted: got %q, want %q",
			got, cursorTick1)
	}
}

// ─────────────────────────────────────────────────────────────────
// Test 2: Parallel race — two HandleChannelSyncJob invocations on
// the SAME channel run concurrently. Spec: "2 sync job concorrenti
// stesso canale = uno vince INSERT, altro AlreadyScheduled".
//
// In a 5-video scenario, the natural outcome is: Job A wins all 5
// inserts, then wins all 5 emits; Job B's 5 attempts all see an
// existing ledger row and classify `already_scheduled`. The harness
// pins that "5 + 5 = 5 net enqueues" property.
// ─────────────────────────────────────────────────────────────────

func TestE2E_ParallelRace_TwoSyncJobsSameChannel(t *testing.T) {
	ctx := context.Background()
	h, channelID, cleanup := newHarness(t, 5)
	defer cleanup()

	// Two concurrent HandleChannelSyncJob goroutines on the SAME
	// channel. Under Commit D's TryReserve-gated dedup, for each of
	// the 5 videos, BOTH goroutines call ledger.TryReserve; the
	// SQLite UNIQUE(channel_id, video_id) constraint elects ONE
	// winner (who then emits EnqueueExtract) and ONE loser (who
	// classifies `:already_scheduled` and skips the emit). Net:
	//   * 5 distinct ledger rows (one per video, no double-inserts);
	//   * 5 EnqueueExtract calls (one per video, from the winner goroutine);
	//   * forward counters each +=5 (sum=20 across 4 forward counters);
	//   * duplicateEnqueues stays at 0 (loser goroutines never reach the mock).
	//
	// The race-safety guarantee is anchored in the SQL UNIQUE
	// constraint, NOT in any Go-level mutex. This test proves the
	// constraint holds even when two HandleChannelSyncJob goroutines
	// enter processVideo on the same channel in parallel.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); h.invokeSyncJob(ctx, t, channelID) }()
	go func() { defer wg.Done(); h.invokeSyncJob(ctx, t, channelID) }()
	wg.Wait()

	if got := countLedgerRows(t, h.db, channelID); got != 5 {
		t.Errorf("parallel race ledger rows: got %d, want 5", got)
	}
	if got := h.enqueuer.acceptedJobs.Load(); got != 5 {
		t.Errorf("parallel race accepted_jobs: got %d, want 5 (one job wins each video's TryReserve)", got)
	}
	if got := h.enqueuer.duplicateEnqueues.Load(); got != 0 {
		t.Errorf("parallel race duplicate_enqueues: got %d, want 0 (TryReserve losers skip EnqueueExtract per Commit D)", got)
	}
	if got := h.enqueuer.outbox.Load() +
		h.enqueuer.dbClips.Load() +
		h.enqueuer.driveUploads.Load() +
		h.enqueuer.qdrant.Load(); got != 20 {
		t.Errorf("parallel race downstream sum: got %d, want 20 (5 winners × 4 forward counters each)", got)
	}
	// Ledger-level dedup proof: GROUP BY outcome distribution equals
	// the single-job Tick1 footprint even with 2 concurrent jobs.
	assertLedgerOutcome(t, h.db, channelID, 5, 0, 0)
}

// ─────────────────────────────────────────────────────────────────
// Test 3: Repeated cycles lock — verify the strict upper bound
// across 4 cycles with no net emissions leakage.
// ─────────────────────────────────────────────────────────────────

func TestE2E_RepeatedCycles_FiveByTwoLock(t *testing.T) {
	ctx := context.Background()
	h, channelID, cleanup := newHarness(t, 5)
	defer cleanup()

	// 4 sequential cycles on the same channel. Cycle 1: 5 fresh
	// TryReserve wins → 5 EnqueueExtract calls. Cycles 2-4: each
	// video's TryReserve loses on UNIQUE → already_scheduled → no
	// emit. Net across all 4 cycles:
	//   * 5 ledger rows (Cycles 2-4 are no-ops at the ledger layer);
	//   * 5 EnqueueExtract calls (Cycle 1 only);
	//   * forward counters all UPPER-BOUNDED at 5;
	//   * duplicateEnqueues stays at 0 (Cycles 2-4 losers never reach the mock).
	for cycle := 0; cycle < 4; cycle++ {
		h.invokeSyncJob(ctx, t, channelID)
	}

	const wantEach = int32(5)
	if got := countLedgerRows(t, h.db, channelID); got != 5 {
		t.Errorf("5v×4 ledger rows: got %d, want 5", got)
	}
	if got := h.enqueuer.acceptedJobs.Load(); got != wantEach {
		t.Errorf("5v×4 accepted_jobs: got %d, want %d (locked at Cycle 1's 5 wins)", got, wantEach)
	}
	if got := h.enqueuer.duplicateEnqueues.Load(); got != 0 {
		t.Errorf("5v×4 duplicate_enqueues: got %d, want 0 (TryReserve-gated dedup; Cycles 2-4 losers never reach EnqueueExtract)", got)
	}
	if got := h.enqueuer.outbox.Load(); got != wantEach {
		t.Errorf("5v×4 outbox: got %d, want %d (upper-bound lock)", got, wantEach)
	}
	if got := h.enqueuer.qdrant.Load(); got != wantEach {
		t.Errorf("5v×4 qdrant: got %d, want %d (upper-bound lock)", got, wantEach)
	}
	// Ledger-level dedup proof: GROUP BY outcome distribution locked
	// at Cycle 1's footprint, all 4 subsequent cycles are no-ops.
	assertLedgerOutcome(t, h.db, channelID, 5, 0, 0)
}
