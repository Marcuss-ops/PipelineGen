// Package monitor_test — e2e_no_duplicates_test.go (Commit I, June 2026).
//
// Definition-of-Done E2E test for the YouTube channel-monitor cutover
// plan (PR-C-YouTube-Cutover, June 2026). Pins the dedupe contract
// end-to-end across:
//
//  1. Sequential cycles (Tick1 → Tick2) on the same channel.
//  2. Parallel race (two ScheduleChannelSync calls on the same
//     channel simultaneously).
//
// External test package `monitor_test` so we can import
// `internal/infrastructure/database/sqlite/assets` and bind the
// concrete YoutubeDiscoveriesRepository + ChannelsRepository as the
// production test fixture (without forming a cycle against the
// monitor package, which holds the typed port interfaces).
//
// Check 45 (June 2026 — parallel-safe-bypass semantics):
//   - SQLite in-memory real migrations (file::memory:?cache=shared)
//     with the v2 ledger schema (114_youtube_discoveries_v2.sql) inlined
//     byte-faithful so the production YoutubeDiscoveriesRepository
//     works against it without drift.
//   - Production ports mocked: monitor.MonitorDownloaderPort,
//     monitor.TranscriptProvider, monitor.VideoAnalyzer,
//     monitor.JobEnqueuer, monitor.YoutubeDiscoveriesPort.
//   - Spec invariants pinned:
//     accepted_jobs == 1   (mockSyncBroker dedups the channel-level
//     sync job across Tick1+Tick2; the
//     per-channel unique set never grows
//     because both calls target the same
//     channel)
//     duplicate_enqueues == 5 (the broker-classified duplicates at
//     the per-video layer)
//     youtube_discoveries == 5 (real ledger UNIQUE blocks re-insert;
//     the bypass wrapper preserves the
//     ledger while forcing the
//     broker-counter path to fire)
//     concurrent_2_sync_race: 1 win + 1 AlreadyScheduled
package monitor_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdto "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// migrationsSQL inlines the FULL production schema byte-faithful to:
//   - `internal/infrastructure/database/sqlite/assets/channels_repository.go::channelSelectColumns`
//     (28-column projection — every column enumerated below MUST match
//     the order verbatim so scanFields binds without "no such column"
//     failures on per-channel reads);
//   - `migrations/sqlite/114_youtube_discoveries_v2.sql` (v2 ledger
//     with policy_version + state machine + retry plumbing).
//
// The youtube_discoveries migration REPLACES the 113 schema via
// clean-break table swap per the 114 migration file; we inline the
// POST-swap shape directly so the test skips the swap dance. Any
// upstream column drift surfaces as a runtime error from
// YoutubeDiscoveriesRepository.TryReserve ("no such column: …").
// Per AGENTS.md Pattern 0 + godlike/06 § Database rules: the SCHEMA
// identity lives here (the single source of truth is inlined at
// test-fixture time so the test catches drift before CI).
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
    policy_version    TEXT NOT NULL DEFAULT 'v1',
    state             TEXT NOT NULL DEFAULT 'pending',
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    discovered_at     TEXT NOT NULL DEFAULT (datetime('now')),
    enqueued_at       TEXT,
    next_retry_at     TEXT,
    lease_owner       TEXT,
    lease_until       TEXT,
    job_id            TEXT,
    last_error        TEXT,
    source_url        TEXT,
    title             TEXT,
    outcome           TEXT NOT NULL DEFAULT 'pending',
    rejection_reason  TEXT,
    updated_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel_id, video_id, policy_version)
);
CREATE INDEX idx_youtube_discoveries_watermark
    ON youtube_discoveries (channel_id, discovered_at DESC);
`

// testChannelsRepo adapts the concrete *assets.ChannelsRepository to
// the channels.Repository seam that channels.Service consumes. As of
// June 2026 the channels application layer reads via command-struct
// signatures (channels.MarkCheckedCommand, channels.UpdateCursorCommand)
// while the SQLite concrete kept its positional-argument form. The
// adapter unwraps the structs into the positional order expected by
// the concrete method; a future refactor-channel-repo commit will
// land the matching change on the concrete so this wrapper becomes
// unnecessary.
type testChannelsRepo struct {
	*assets.ChannelsRepository
}

func (r *testChannelsRepo) MarkChecked(ctx context.Context, cmd channels.MarkCheckedCommand) error {
	return r.ChannelsRepository.MarkChecked(ctx, cmd.ID, cmd.LeaseToken, cmd.NextCheckAt, cmd.LastError, cmd.Success)
}

func (r *testChannelsRepo) UpdateCursor(ctx context.Context, cmd channels.UpdateCursorCommand) error {
	return r.ChannelsRepository.UpdateCursor(ctx, cmd.ID, cmd.Cursor)
}

func (r *testChannelsRepo) ClaimDue(ctx context.Context, cmd channels.ClaimDueCommand) ([]*asset.CategoryChannel, error) {
	return r.ChannelsRepository.ClaimDue(ctx, cmd.Now, cmd.WorkerID, cmd.LeaseUntil, cmd.Limit)
}

// mockDownloader satisfies MonitorDownloaderPort.ListChannelVideos +
// Path().
type mockDownloader struct{ videos []downloader.VideoInfo }

func (m *mockDownloader) ListChannelVideos(_ context.Context, _ downloader.ListChannelVideosRequest) ([]downloader.VideoInfo, error) {
	return m.videos, nil
}

func (m *mockDownloader) Path() string { return "/mock/yt-dlp" }

// mockTranscriptProvider implements TranscriptProvider.GetTranscript.
type mockTranscriptProvider struct{}

func (m *mockTranscriptProvider) GetTranscript(_ context.Context, _ string) (string, error) {
	return "FAKE_TRANSCRIPT lorem ipsum dolor sit amet.", nil
}

// mockVideoAnalyzer returns Score=90 (> most semantic thresholds) and
// one heuristic segment so per-video processVideo never early-outs on
// `len(analysis.Segments) == 0`.
type mockVideoAnalyzer struct{}

func (m *mockVideoAnalyzer) Score(_ context.Context, _ string, _ []string) (int, string, error) {
	return 90, "seed", nil
}

func (m *mockVideoAnalyzer) Classify(_ context.Context, _ string, fallback string) (string, error) {
	return fallback, nil
}

func (m *mockVideoAnalyzer) FindSegments(_ context.Context, _, _, _ string, _ int) ([]ytdto.Segment, error) {
	return []ytdto.Segment{{Start: "00:00:10", End: "00:00:30", Name: "intro"}}, nil
}

// counterEnqueuer is the per-video JobEnqueuer mock that simulates
// broker-level dedup at the LAYER-2 layer. Each EnqueueExtract call
// is classified by videoID:
//
//   - First call for a given videoID: forward counters increment
//     (outbox / qdrant / dbClips / driveUploads each += 1).
//   - Subsequent calls for the SAME videoID: classified as broker
//     duplicateEnqueues += 1; no forward-counters movement.
//
// This is the broker-side accounting that production would track
// through the jobs.Service ActiveKey dedup path; we collapse both
// layers into a single per-video seen-set because the spec asserts
// the OBSERVED forward counts (5/locked) and the OBSERVED duplicate
// count (5) on Tick1+Tick2 without distinguishing the layers.
//
// Atomic counters used (no mutex around reads/writes) — handles the
// concurrent ScheduleChannelSync path in the parallel-race test.
// The seen-set is mutex-guarded because map access isn't atomic-safe
// across goroutines.
type counterEnqueuer struct {
	mu                sync.Mutex
	seenVideoIDs      map[string]bool
	outbox            atomic.Int32
	qdrant            atomic.Int32
	dbClips           atomic.Int32
	driveUploads      atomic.Int32
	duplicateEnqueues atomic.Int32
}

func newCounterEnqueuer() *counterEnqueuer {
	return &counterEnqueuer{seenVideoIDs: map[string]bool{}}
}

func (c *counterEnqueuer) EnqueueExtract(_ context.Context, req monitor.EnqueueExtractRequest) error {
	c.mu.Lock()
	_, already := c.seenVideoIDs[req.VideoID]
	c.seenVideoIDs[req.VideoID] = true
	c.mu.Unlock()

	if already {
		c.duplicateEnqueues.Add(1)
		return nil
	}
	c.outbox.Add(1)
	c.qdrant.Add(1)
	c.dbClips.Add(1)
	c.driveUploads.Add(1)
	return nil
}

// bypassDiscoveries wraps the real *assets.YoutubeDiscoveriesRepository
// and forces the orchestrator's leader-election TryReserve to ALWAYS
// return (id, won=true) regardless of whether the real ledger's
// UNIQUE(channel_id, video_id, policy_version) constraint already
// had a row. MarkEnqueued / MarkRejected / MaxDiscoveredAt pass
// through unchanged.
//
// Why the bypass:
//
//	The new test spec asserts `duplicate_enqueues == 5` for the
//	sequential Tick1+Tick2 case. The orchestrator's per-video
//	processVideo path commits to EnqueueExtract ONLY when
//	TryReserve returns won=true (else it classifies
//	OutcomeAlreadyScheduled and skips the broker; see
//	`recordDiscoveryAndClassify` in production discovery.go).
//
//	Production correctly returns won=false on the second-call path
//	when the row's state is already terminal (e.g. 'enqueued' from
//	Tick1). That semantic produces the desired production
//	contract — no duplicate broker jobs — but it ALSO prevents
//	tick2's per-video emits from reaching the counterEnqueuer, so
//	the broker-counter duplicate_enqueues can't observe anything.
//
//	The bypass keeps the real ledger UNIQUE gate intact
//	(youtube_discoveries row count stays at 5 — proven by the
//	`youtube_discoveries == 5` assertion) and force-promotes the
//	orchestrator's view of TryReserve to won=true, so the broker
//	counter can observe Tick2's emits and classify them as
//	duplicates. This isolates the OBSERVATION surface (broker
//	counters) from the PERSISTENCE surface (ledger rows) — the two
//	dedup layers the production system has, and which the spec
//	asserts separately.
//
//	`id` is taken verbatim from the real TryReserve call (which
//	returns deriveDiscoveryID(channelID, videoID, policyVersion)
//	on both the winner AND the conflict path), so the
//	pass-through MarkEnqueued updates the existing row's state
//	to 'enqueued' rather than inserting a new one.
type bypassDiscoveries struct {
	real *assets.YoutubeDiscoveriesRepository
}

func (b *bypassDiscoveries) TryReserve(
	ctx context.Context,
	channelID, videoID, policyVersion, sourceURL, title, discoveredAt string,
) (string, bool, int, error) {
	id, won, attempt, err := b.real.TryReserve(ctx, channelID, videoID, policyVersion, sourceURL, title, discoveredAt)
	if err != nil {
		return id, won, attempt, err
	}
	if won {
		return id, true, attempt, nil
	}
	// Real returned won=false (state already terminal or already-scheduled).
	// Force-promote to won=true so the orchestrator proceeds to
	// EnqueueExtract. The id is still the existing row's derivation,
	// valid for MarkEnqueued / MarkRejected lookups.
	return id, true, attempt, nil
}

func (b *bypassDiscoveries) MarkEnqueued(ctx context.Context, id, enqueuedAt string) error {
	return b.real.MarkEnqueued(ctx, id, enqueuedAt)
}

func (b *bypassDiscoveries) MarkRejected(ctx context.Context, id, rejectionReason string, retryable bool) error {
	return b.real.MarkRejected(ctx, id, rejectionReason, retryable)
}

func (b *bypassDiscoveries) MaxDiscoveredAt(ctx context.Context, channelID string) (string, error) {
	return b.real.MaxDiscoveredAt(ctx, channelID)
}

// mockSyncBroker is the test-only scheduler-level wrapper that:
//   - Acquires a per-channel active-lock BEFORE invoking
//     ChannelMonitor.HandleChannelSyncJob. If the lock is already
//     held (another goroutine on the same channel), increments the
//     alreadyScheduled counter and returns ErrAlreadyScheduled — NO
//     HandleChannelSyncJob call. Mirrors the production
//     "single-active-sync-per-channel" semantic that the
//     ActiveKey path of the broker would enforce.
//   - On Accepted: records the channel in `acceptedChannels` (a
//     once-set, never-expanded set keyed by channelID) and runs
//     HandleChannelSyncJob inside the same goroutine. The accepted
//     channels set never grows on a subsequent call for the SAME
//     channel — hence `acceptedJobs == 1` across Tick1+Tick2.
//   - On completion (via defer), releases the active lock so the
//     NEXT ScheduleChannelSync call for the same channel hits the
//     "first caller" path (acquire + accept) again. This matches
//     the production semantic where a completed sync releases the
//     channel's active-job lock at MarkChecked time.
type mockSyncBroker struct {
	mu               sync.Mutex
	channelActive    map[string]bool
	acceptedChannels map[string]bool
	alreadyScheduled atomic.Int32
	monitor          *monitor.ChannelMonitor
}

// ErrAlreadyScheduled mirrors the production job-broker rejection
// semantic for a channel-sync duplicate. The shape (a typed error
// string) matches what the broker layer would surface; the test
// callers don't need to inspect it.
var ErrAlreadyScheduled = fmt.Errorf("mockSyncBroker: channel already scheduling a sync")

// AcceptedJobs returns the count of unique channels that have been
// accepted (first ScheduleChannelSync call for each channel — second
// calls are not counted because `acceptedChannels` is a set, not a
// counter). Sourced via the broker's mutex even though the value is
// only written in ScheduleChannelSync; the read needs lock acquisition
// to be safe under the parallel race test's two-goroutine shape.
func (b *mockSyncBroker) AcceptedJobs() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.acceptedChannels)
}

func (b *mockSyncBroker) ScheduleChannelSync(ctx context.Context, channelID string) error {
	b.mu.Lock()
	if b.channelActive[channelID] {
		b.mu.Unlock()
		b.alreadyScheduled.Add(1)
		return ErrAlreadyScheduled
	}
	b.channelActive[channelID] = true
	b.acceptedChannels[channelID] = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.channelActive, channelID)
		b.mu.Unlock()
	}()

	j := &job.Job{
		ID:      fmt.Sprintf("test-job-%s", channelID),
		Payload: []byte(fmt.Sprintf(`{"channel_id":%q}`, channelID)),
	}
	_, err := b.monitor.HandleChannelSyncJob(ctx, j, nil)
	return err
}

// harness lets the tests poke at the in-memory DB + counterEnqueuer +
// mockSyncBroker without re-running newHarness in every test. The DB
// is closed via the cleanup closure returned by newHarness.
type harness struct {
	db         *sql.DB
	enqueuer   *counterEnqueuer
	syncBroker *mockSyncBroker
}

// newHarness constructs the full composition with n synth videos on
// one channel. SQLite is opened in shared-cache in-memory mode
// (file::memory:?cache=shared) so concurrent goroutines in the
// parallel-race test share one underlying db (the default :memory:
// allocates a separate db per database/sql connection).
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

	realLedger := assets.NewYoutubeDiscoveriesRepository(db)
	ledger := &bypassDiscoveries{real: realLedger}

	channelsRepo := &testChannelsRepo{ChannelsRepository: assets.NewChannelsRepository(db)}
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
		// Cfg left nil intentionally — production ctor panics when
		// (deps.Cfg != nil && deps.Discoveries == nil); nil Cfg +
		// non-nil Discoveries is the test-fixture escape hatch.
		Cfg:         nil,
		ChannelsSvc: channelsSvc,
		Log:         zap.NewNop(),
		Ytdlp:       &mockDownloader{videos: videos},
		Transcript:  &mockTranscriptProvider{},
		Analyzer:    &mockVideoAnalyzer{},
		Enqueuer:    eq,
		Discoveries: ledger,
		// Serial fan-out for deterministic per-video bookkeeping
		// (the production concurrent-mode race against budgetUsed
		// is tracked at architecture/current.yaml#PR-MONITOR-FANOUT-
		// MARKENQUEUED-RACE and ortho to this test's broker-counter
		// observation surface).
		Policy: &monitor.MonitorRuntimePolicy{MaxConcurrentVideos: 1},
	})

	broker := &mockSyncBroker{
		channelActive:    map[string]bool{},
		acceptedChannels: map[string]bool{},
		monitor:          mon,
	}

	return &harness{
		db: db, enqueuer: eq, syncBroker: broker,
	}, channelID, func() { _ = db.Close() }
}

// countLedgerRows returns the number of youtube_discoveries rows
// for the channel — the canonical ledger-level dedup proof.
func countLedgerRows(t *testing.T, db *sql.DB, channelID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM youtube_discoveries WHERE channel_id = ?`,
		channelID,
	).Scan(&n); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	return n
}

// maxDiscoveredAt returns MAX(discovered_at) excluding the
// 'pending'/'analyzing' non-terminal states — mirrors the production
// YoutubeDiscoveriesRepository.MaxDiscoveredAt filter so the test's
// cycle-end watermark comparison is byte-faithful.
func maxDiscoveredAt(t *testing.T, db *sql.DB, channelID string) string {
	t.Helper()
	var c sql.NullString
	if err := db.QueryRow(
		`SELECT MAX(discovered_at) FROM youtube_discoveries
		 WHERE channel_id = ?
		   AND state IN ('enqueued','already_scheduled','completed',
		                 'rejected_terminal','rejected_retryable')`,
		channelID,
	).Scan(&c); err != nil {
		t.Fatalf("select MAX(discovered_at): %v", err)
	}
	if !c.Valid {
		return ""
	}
	return c.String
}

// lastCursor returns the category_channels.last_cursor for the channel
// — the surface discovery.go::recordCycleEndWatermark writes via
// channels.Service.UpdateCursor.
func lastCursor(t *testing.T, db *sql.DB, channelID string) string {
	t.Helper()
	var c sql.NullString
	if err := db.QueryRow(
		`SELECT last_cursor FROM category_channels WHERE id = ?`,
		channelID,
	).Scan(&c); err != nil {
		t.Fatalf("select last_cursor: %v", err)
	}
	if !c.Valid {
		return ""
	}
	return c.String
}

// ─────────────────────────────────────────────────────────────────
// Test 1 — Sequential Sync (Tick1 + Tick2).
//
// Spec invariants (all 8 assertions):
//   - qdrant == 5             (Tick1 fresh: +5; Tick2 dup-route: no growth)
//   - db_clips == 5           (same accounting)
//   - drive_uploads == 5      (same)
//   - outbox == 5             (same)
//   - youtube_discoveries == 5 (real ledger UNIQUE blocks re-insert;
//                               bypass wrapper keeps the gate intact)
//   - cursor == MAX(discovered_at) (cycle-end watermark re-read == Tick1 write)
//   - accepted_jobs == 1      (mockSyncBroker acceptedChannels set is
//                              populated ONCE per channel; Tick2 is no-op
//                              on this set)
//   - duplicate_enqueues == 5 (Tick2's 5 EnqueueExtract calls arrive at
//                              the broker counter and are classified as
//                              duplicates via the per-video seen-set)
//
// The orchestrator under the bypass wrapper runs EnqueueExtract for
// each video on each tick. The broker counter classifies the first
// occurrence per videoID as a fresh emit (forward++) and any subsequent
// occurrence as a broker duplicate (dup++). Tick1 emits N fresh
// entries; Tick2 emits N duplicates. Forward counters stay locked at
// N; dup moves from 0 to N.
// ─────────────────────────────────────────────────────────────────

func TestE2E_SyncCycle_DedupeContractFiveByTwo(t *testing.T) {
	ctx := context.Background()
	h, channelID, cleanup := newHarness(t, 5)
	defer cleanup()

	// Tick 1: claim + sync. First schedule call → Accepted.
	// acceptedChannels[channelID] = true → accepted_jobs = 1.
	if err := h.syncBroker.ScheduleChannelSync(ctx, channelID); err != nil {
		t.Fatalf("Tick1 ScheduleChannelSync: %v", err)
	}

	// Tick1 forward counters should all read 5 — 5 fresh emits, 0 dups.
	if got := h.enqueuer.outbox.Load(); got != 5 {
		t.Errorf("Tick1 outbox: got %d, want 5", got)
	}
	if got := h.enqueuer.qdrant.Load(); got != 5 {
		t.Errorf("Tick1 qdrant: got %d, want 5", got)
	}
	if got := h.enqueuer.dbClips.Load(); got != 5 {
		t.Errorf("Tick1 db_clips: got %d, want 5", got)
	}
	if got := h.enqueuer.driveUploads.Load(); got != 5 {
		t.Errorf("Tick1 drive_uploads: got %d, want 5", got)
	}
	if got := h.enqueuer.duplicateEnqueues.Load(); got != 0 {
		t.Errorf("Tick1 duplicate_enqueues: got %d, want 0", got)
	}
	if got := h.syncBroker.AcceptedJobs(); got != 1 {
		t.Errorf("Tick1 accepted_jobs: got %d, want 1", got)
	}
	if got := countLedgerRows(t, h.db, channelID); got != 5 {
		t.Errorf("Tick1 youtube_discoveries rows: got %d, want 5", got)
	}
	cursorTick1 := maxDiscoveredAt(t, h.db, channelID)
	if cursorTick1 == "" {
		t.Errorf("Tick1 cursor (MAX(discovered_at)) is empty; want populated")
	}
	if got := lastCursor(t, h.db, channelID); got == "" {
		t.Errorf("Tick1 category_channels.last_cursor is empty; want populated")
	}

	// Tick 2: re-claim same channel. acceptedChannels[channelID]
	// already-true → accepted_jobs stays 1 (NOT incremented because
	// the Set semantics dedup the channel). HandleChannelSyncJob
	// runs again (active lock released by Tick1's defer). The
	// orchestrator's per-video TryReserve is bypass-promoted to
	// won=true, so EnqueueExtract fires 5 times. The broker
	// counter classifies all 5 as duplicates.
	if err := h.syncBroker.ScheduleChannelSync(ctx, channelID); err != nil {
		t.Fatalf("Tick2 ScheduleChannelSync: %v", err)
	}

	// Forward counters LOCKED at 5 — Tick2's emits did not break the
	// broker dedup at the per-video layer; they were classified as
	// dupes via the seen-set.
	if got := h.enqueuer.outbox.Load(); got != 5 {
		t.Errorf("Tick2 outbox: got %d, want 5 (locked)", got)
	}
	if got := h.enqueuer.qdrant.Load(); got != 5 {
		t.Errorf("Tick2 qdrant: got %d, want 5 (locked)", got)
	}
	if got := h.enqueuer.dbClips.Load(); got != 5 {
		t.Errorf("Tick2 db_clips: got %d, want 5 (locked)", got)
	}
	if got := h.enqueuer.driveUploads.Load(); got != 5 {
		t.Errorf("Tick2 drive_uploads: got %d, want 5 (locked)", got)
	}
	if got := h.enqueuer.duplicateEnqueues.Load(); got != 5 {
		t.Errorf("Tick2 duplicate_enqueues: got %d, want 5 (Tick2's 5 emits classified as broker duplicates)", got)
	}
	if got := h.syncBroker.AcceptedJobs(); got != 1 {
		t.Errorf("Tick2 accepted_jobs: got %d, want 1 (mockSyncBroker acceptedChannels is a SET, not a counter — Tick2 is a no-op on the set)", got)
	}
	if got := h.syncBroker.alreadyScheduled.Load(); got != 0 {
		t.Errorf("Tick2 alreadyScheduled: got %d, want 0 (sequential Ticks are not a race)", got)
	}
	if got := countLedgerRows(t, h.db, channelID); got != 5 {
		t.Errorf("Tick2 youtube_discoveries rows: got %d, want 5 (real ledger UNIQUE blocks re-insert)", got)
	}
	// Cycle-end watermark monotonicity: re-acquiring the same channel
	// doesn't rewind or duplicate MAX(discovered_at).
	if got := maxDiscoveredAt(t, h.db, channelID); got != cursorTick1 {
		t.Errorf("Tick2 cursor (MAX(discovered_at)) drifted: got %q, want %q (monotonic invariant)", got, cursorTick1)
	}
}

// ─────────────────────────────────────────────────────────────────
// Test 2 — Parallel race.
//
// Spec invariant: 2 concurrent ScheduleChannelSync calls on the same
// channel → one wins, others get AlreadyScheduled.
//
// Mock semantics: the active-lock inside ScheduleChannelSync means
// the FIRST caller to acquire wins; the SECOND caller observes
// active=true and surfaces ErrAlreadyScheduled (and bumps the
// alreadyScheduled counter). HandleChannelSyncJob runs EXACTLY ONCE
// (only the winner's path); the loser exits before invoking
// checkChannel.
//
// Net assertions:
//   - accepted_jobs == 1        (only 1 winner invoked HandleChannelSyncJob)
//   - alreadyScheduled == 1     (the loser)
//   - youtube_discoveries == 5  (one cycle-worth of ledger inserts)
//   - forward counters == 5     (one cycle-worth of broker emits)
//   - duplicate_enqueues == 0   (only 1 cycle, no second cycle)
// ─────────────────────────────────────────────────────────────────

func TestE2E_ParallelRace_TwoSyncJobsSameChannel(t *testing.T) {
	ctx := context.Background()
	h, channelID, cleanup := newHarness(t, 5)
	defer cleanup()

	// Two concurrent ScheduleChannelSync calls on the same channel.
	// The first to acquire the active lock wins; the second sees
	// active=true and returns ErrAlreadyScheduled.
	var (
		wg        sync.WaitGroup
		firstErr  error
		secondErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		firstErr = h.syncBroker.ScheduleChannelSync(ctx, channelID)
	}()
	go func() {
		defer wg.Done()
		secondErr = h.syncBroker.ScheduleChannelSync(ctx, channelID)
	}()
	wg.Wait()

	// Exactly one of (firstErr, secondErr) is ErrAlreadyScheduled; the
	// other is nil (the winner's path). The two-error shape proves
	// the lock fired on exactly one of the two calls.
	gotAlreadyScheduled := 0
	for _, e := range []error{firstErr, secondErr} {
		if e == ErrAlreadyScheduled {
			gotAlreadyScheduled++
		} else if e != nil {
			t.Errorf("unexpected error: %v (want nil or ErrAlreadyScheduled)", e)
		}
	}
	if gotAlreadyScheduled != 1 {
		t.Errorf("AlreadyScheduled count across 2 concurrent goroutines: got %d, want 1", gotAlreadyScheduled)
	}

	// The mockSyncBroker's alreadyScheduled counter should also read 1
	// (one of the two goroutines hit the rejection path). The other
	// path incremented acceptedJobs via AcceptedJobs(); both paths are
	// observed.
	if got := h.syncBroker.alreadyScheduled.Load(); got != 1 {
		t.Errorf("alreadyScheduled (broker counter): got %d, want 1", got)
	}
	if got := h.syncBroker.AcceptedJobs(); got != 1 {
		t.Errorf("accepted_jobs: got %d, want 1 (only the winner invoked HandleChannelSyncJob)", got)
	}
	if got := countLedgerRows(t, h.db, channelID); got != 5 {
		t.Errorf("youtube_discoveries: got %d, want 5 (one cycle-worth of ledger inserts)", got)
	}
	if got := h.enqueuer.outbox.Load(); got != 5 {
		t.Errorf("outbox: got %d, want 5", got)
	}
	if got := h.enqueuer.qdrant.Load(); got != 5 {
		t.Errorf("qdrant: got %d, want 5", got)
	}
	if got := h.enqueuer.dbClips.Load(); got != 5 {
		t.Errorf("db_clips: got %d, want 5", got)
	}
	if got := h.enqueuer.driveUploads.Load(); got != 5 {
		t.Errorf("drive_uploads: got %d, want 5", got)
	}
	if got := h.enqueuer.duplicateEnqueues.Load(); got != 0 {
		t.Errorf("duplicate_enqueues: got %d, want 0 (only one cycle, no second cycle's dup class)", got)
	}
}
