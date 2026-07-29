package channels

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	_ "github.com/mattn/go-sqlite3"
)

// openChannelsTestDB opens an in-memory SQLite instance with the
// canonical category_channels schema. The schema mirrors the
// migrations 015_create_category_channels.sql + 017/019/031/032/
// 106/107/108 (consolidation monitored_sources into category_channels)
// so the projection constant + scanFields dest list can be tested
// against the same column set production uses.
//
// Per AGENTS.md §"Database rules", driver is locked on
// mattn/go-sqlite3 — the import above is intentional and should
// not be changed by future contributors without a corresponding
// migration to a different driver + godlike/06 update.
//
// Schema defensive note: Text columns scanned into plain string
// destinations (ChannelName, DriveFolderID) are declared
// NOT NULL DEFAULT ” even when the production migration allows
// NULL. scanFields reads them with `&ch.DriveFolderID` (string,
// not sql.NullString); allowing NULL would produce a `scan error:
// converting NULL to string` on the read path. The default ”
// matches the practical case where production handlers always
// upsert a non-null value (or empty). This is purely a
// test-fixture fix; production schema is governed by the
// migrations and is not in scope of this test.
func openChannelsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3 :memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS category_channels (
			id TEXT PRIMARY KEY,
			category TEXT NOT NULL,
			channel_url TEXT NOT NULL,
			channel_name TEXT NOT NULL DEFAULT '',
			keywords TEXT NOT NULL DEFAULT '[]',
			min_views INTEGER NOT NULL DEFAULT 0,
			max_clip_duration INTEGER NOT NULL DEFAULT 60,
			drive_folder_id TEXT NOT NULL DEFAULT '',
			semantic_keywords TEXT NOT NULL DEFAULT '[]',
			min_semantic_score INTEGER NOT NULL DEFAULT 60,
			playlist_end INTEGER NOT NULL DEFAULT -1,
			check_interval TEXT NOT NULL DEFAULT '7d',
			max_videos_per_run INTEGER NOT NULL DEFAULT 3,
			priority INTEGER NOT NULL DEFAULT 2,
			lookback_days INTEGER NOT NULL DEFAULT 0,
			max_segments INTEGER NOT NULL DEFAULT 2,
			segment_prompt TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			next_check_at TEXT,
			last_checked_at TEXT,
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			last_success_at TEXT,
			lease_owner TEXT,
			lease_until TEXT,
			last_cursor TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create category_channels %v", err)
	}
	return db
}

// insetTestChannel inserts a row via ordered positional parameters.
// Plain-string scanned columns (ch.ChannelName, ch.DriveFolderID in
// production CategoryChannel) are sent as the underlying string so
// a NULL doesn't trip the scan path. Nullable columns scanned as
// sql.NullString use nullStr() — those produce NULL when empty,
// which is what the production Upsert does via toNullString.
func insertTestChannel(t *testing.T, db *sql.DB, ch *asset.CategoryChannel) {
	t.Helper()
	if ch.CreatedAt == "" {
		ch.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if ch.UpdatedAt == "" {
		ch.UpdatedAt = ch.CreatedAt
	}
	dr := ch.DriveFolderID
	if dr == "" {
		dr = ""
	}
	_, err := db.Exec(`
		INSERT INTO category_channels (
			id, category, channel_url, channel_name, keywords, min_views,
			max_clip_duration, drive_folder_id, semantic_keywords, min_semantic_score,
			playlist_end, check_interval, max_videos_per_run, priority,
			lookback_days, max_segments, segment_prompt, enabled, next_check_at,
			last_checked_at, consecutive_failures, last_error, last_success_at,
			lease_owner, lease_until, last_cursor, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		ch.ID, ch.Category, ch.ChannelURL, ch.ChannelName, ch.Keywords, ch.MinViews,
		ch.MaxClipDuration, dr, ch.SemanticKeywords, ch.MinSemanticScore,
		ch.PlaylistEnd, ch.CheckInterval, ch.MaxVideosPerRun, ch.Priority,
		ch.LookbackDays, ch.MaxSegments, ch.SegmentPrompt, ch.Enabled, nullStr(ch.NextCheckAt),
		nullStr(ch.LastCheckedAt), ch.ConsecutiveFailures, nullStr(ch.LastError), nullStr(ch.LastSuccessAt),
		nullStr(ch.LeaseOwner), nullStr(ch.LeaseUntil), ch.LastCursor, ch.CreatedAt, ch.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("insert channel %q: %v", ch.ID, err)
	}
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// TestChannelsRepository_GetByID_PopulatesLastCursor is the regression
// pin for P0 #1: pre-fix the SELECT had 27 columns but scanFields
// scanned 28 destinations, so rows.Scan failed with
// "sql: expected 27 destination arguments... got 28" on every read.
// Post-fix GetByID must return a populated LastCursor (the 27th
// column) and pass without an error.
func TestChannelsRepository_GetByID_PopulatesLastCursor(t *testing.T) {
	db := openChannelsTestDB(t)
	repo := NewChannelsRepository(db)

	now := time.Now().UTC().Format(time.RFC3339)
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "test-chan-1", Category: "boxe", ChannelURL: "https://www.youtube.com/@Test",
		CreatedAt: now, UpdatedAt: now,
		LastCursor:          "VID-ABC-123",
		ConsecutiveFailures: 2,
	})

	got, err := repo.GetByID(context.Background(), "test-chan-1")
	if err != nil {
		t.Fatalf("GetByID err: %v (pre-fix bug would fail with rows.Scan count mismatch)", err)
	}
	if got.LastCursor != "VID-ABC-123" {
		t.Errorf("LastCursor = %q, want %q", got.LastCursor, "VID-ABC-123")
	}
	if got.ConsecutiveFailures != 2 {
		t.Errorf("ConsecutiveFailures = %d, want 2", got.ConsecutiveFailures)
	}
}

// TestChannelsRepository_ListEnabled_PopulatesLastCursor pins the
// same projection fix on the rows-via-QueryContext path.
func TestChannelsRepository_ListEnabled_PopulatesLastCursor(t *testing.T) {
	db := openChannelsTestDB(t)
	repo := NewChannelsRepository(db)

	now := time.Now().UTC().Format(time.RFC3339)
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "list-1", Category: "boxe", ChannelURL: "https://www.youtube.com/@List1",
		CreatedAt: now, UpdatedAt: now, Enabled: 1, LastCursor: "VID-A",
	})
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "list-2", Category: "boxe", ChannelURL: "https://www.youtube.com/@List2",
		CreatedAt: now, UpdatedAt: now, Enabled: 1, LastCursor: "VID-B",
	})
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "list-3", Category: "boxe", ChannelURL: "https://www.youtube.com/@List3",
		CreatedAt: now, UpdatedAt: now, Enabled: 0, LastCursor: "VID-C", // disabled — excluded
	})

	rows, err := repo.ListEnabled(context.Background())
	if err != nil {
		t.Fatalf("ListEnabled err: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListEnabled len = %d, want 2", len(rows))
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.ID] = r.LastCursor
	}
	if got["list-1"] != "VID-A" || got["list-2"] != "VID-B" {
		t.Errorf("LastCursor mismatch: got=%v", got)
	}
}

// TestChannelsRepository_ListAll_ListByCategory_BothCovered pins the
// projection fix on the remaining two SELECT paths and asserts
// last_cursor round-trips through ListByCategory too.
func TestChannelsRepository_ListAll_ListByCategory_BothCovered(t *testing.T) {
	db := openChannelsTestDB(t)
	repo := NewChannelsRepository(db)

	now := time.Now().UTC().Format(time.RFC3339)
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "all-1", Category: "boxe", ChannelURL: "https://www.youtube.com/@Boxe1",
		CreatedAt: now, UpdatedAt: now, LastCursor: "VID-boxe",
	})
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "all-2", Category: "rap", ChannelURL: "https://www.youtube.com/@Rap1",
		CreatedAt: now, UpdatedAt: now, LastCursor: "VID-rap",
	})
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "all-3", Category: "boxe", ChannelURL: "https://www.youtube.com/@Boxe2",
		CreatedAt: now, UpdatedAt: now, LastCursor: "VID-boxe-2",
	})

	all, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll err: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListAll len = %d, want 3", len(all))
	}
	got := map[string]string{}
	for _, r := range all {
		got[r.ID] = r.LastCursor
	}
	if got["all-1"] != "VID-boxe" || got["all-2"] != "VID-rap" || got["all-3"] != "VID-boxe-2" {
		t.Errorf("LastCursor mismatch on ListAll: got=%v", got)
	}

	boxe, err := repo.ListByCategory(context.Background(), "boxe")
	if err != nil {
		t.Fatalf("ListByCategory err: %v", err)
	}
	if len(boxe) != 2 {
		t.Fatalf("ListByCategory('boxe') len = %d, want 2", len(boxe))
	}
	gotBoxe := map[string]string{}
	for _, r := range boxe {
		gotBoxe[r.ID] = r.LastCursor
	}
	if gotBoxe["all-1"] != "VID-boxe" || gotBoxe["all-3"] != "VID-boxe-2" {
		t.Errorf("LastCursor mismatch on ListByCategory: got=%v", gotBoxe)
	}
}

// TestChannelsRepository_MarkChecked_Fenced_SuccessHappyPath is the
// primary happy path for P1 #8: when a caller holds the lease, fenced
// MarkChecked must update scheduling state and clear lease_owner /
// lease_until so the next ClaimDue can re-claim the row.
func TestChannelsRepository_MarkChecked_Fenced_SuccessHappyPath(t *testing.T) {
	db := openChannelsTestDB(t)
	repo := NewChannelsRepository(db)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "fenced-chan", Category: "boxe", ChannelURL: "https://www.youtube.com/@Fenced",
		CreatedAt: now, UpdatedAt: now, ConsecutiveFailures: 0,
	})

	// Manually claim the lease (mirrors what ClaimDue would do).
	mustExec(t, db, `UPDATE category_channels SET lease_owner = ?, lease_until = ? WHERE id = ?`,
		"worker-A", futureRFC3339(30*time.Minute), "fenced-chan")

	// Fenced MarkChecked with matching lease_token → success.
	nextCheck := futureRFC3339(time.Hour)
	if err := repo.MarkChecked(ctx, "fenced-chan", "worker-A", nextCheck, "ok-transient-blip", false); err != nil {
		t.Fatalf("MarkChecked err: %v", err)
	}

	got, _ := repo.GetByID(ctx, "fenced-chan")
	if got.LeaseOwner != "" || got.LeaseUntil != "" {
		t.Errorf("lease should be cleared after MarkChecked: owner=%q until=%q", got.LeaseOwner, got.LeaseUntil)
	}
	if got.NextCheckAt == "" || got.LastCheckedAt == "" {
		t.Errorf("NextCheckAt/LastCheckedAt should be set: next=%q last=%q", got.NextCheckAt, got.LastCheckedAt)
	}
	if got.LastError != "ok-transient-blip" {
		t.Errorf("LastError = %q, want %q (non-empty wins over previous NULL)", got.LastError, "ok-transient-blip")
	}
	if got.ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1", got.ConsecutiveFailures)
	}
}

// TestChannelsRepository_MarkChecked_Fenced_WrongTokenReturnsErrLeaseLost
// is the regression pin for P1 #8: a worker that no longer owns the
// lease must NOT silently overwrite scheduling state. The pinned
// row's PreMarkCheckedAt is captured before the call and asserted
// unchanged afterward; ErrLeaseLost surfaces via errors.Is.
func TestChannelsRepository_MarkChecked_Fenced_WrongTokenReturnsErrLeaseLost(t *testing.T) {
	db := openChannelsTestDB(t)
	repo := NewChannelsRepository(db)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "fenced-chan", Category: "boxe", ChannelURL: "https://www.youtube.com/@Fenced",
		CreatedAt: now, UpdatedAt: now, LastError: "previous-real-error",
	})

	mustExec(t, db, `UPDATE category_channels SET lease_owner = ?, lease_until = ? WHERE id = ?`,
		"worker-A", futureRFC3339(30*time.Minute), "fenced-chan")

	pre, _ := repo.GetByID(ctx, "fenced-chan")

	// Wrong token: we are worker-B but worker-A holds the lease.
	err := repo.MarkChecked(ctx, "fenced-chan", "worker-B", futureRFC3339(2*time.Hour), "WRONG-ATTEMPT", false)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("MarkChecked err = %v, want errors.Is ErrLeaseLost", err)
	}

	post, _ := repo.GetByID(ctx, "fenced-chan")
	if post.LastError != pre.LastError {
		t.Errorf("LastError should be unchanged: pre=%q post=%q", pre.LastError, post.LastError)
	}
	if post.LeaseOwner != "worker-A" {
		t.Errorf("LeaseOwner should remain on worker-A: got=%q", post.LeaseOwner)
	}
	if post.NextCheckAt != pre.NextCheckAt {
		t.Errorf("NextCheckAt should be unchanged: pre=%q post=%q", pre.NextCheckAt, post.NextCheckAt)
	}
}

// TestChannelsRepository_MarkChecked_UnfencedBackCompatPath: passing
// an empty lease_token opts out of fencing (back-compat for admin CLIs).
// State MUST be updated and no ErrLeaseLost surfaced.
func TestChannelsRepository_MarkChecked_UnfencedBackCompatPath(t *testing.T) {
	db := openChannelsTestDB(t)
	repo := NewChannelsRepository(db)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	insertTestChannel(t, db, &asset.CategoryChannel{
		ID: "unfenced-chan", Category: "boxe", ChannelURL: "https://www.youtube.com/@Unfenced",
		CreatedAt: now, UpdatedAt: now,
	})

	// lease_owner currently NULL — empty token matches anyway, no fence.
	err := repo.MarkChecked(ctx, "unfenced-chan", "", futureRFC3339(time.Hour), "", true)
	if err != nil {
		t.Fatalf("MarkChecked err: %v", err)
	}
	got, _ := repo.GetByID(ctx, "unfenced-chan")
	if got.NextCheckAt == "" {
		t.Errorf("NextCheckAt should be set: got %q", got.NextCheckAt)
	}
	if got.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 (success)", got.ConsecutiveFailures)
	}
}

// TestChannelsRepository_ClaimDue_OrderByPriority is the regression
// pin for P1 #10: hot-priority channels must be claimed before normal
// before cold even if normal was due earlier (older next_check_at).
func TestChannelsRepository_ClaimDue_OrderByPriority(t *testing.T) {
	db := openChannelsTestDB(t)
	repo := NewChannelsRepository(db)
	ctx := context.Background()

	now := time.Now().UTC().Format(time.RFC3339)
	hourAgo := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	minAgo := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)

	// normal is the most over-due but cold priority; hot is fresh but priority 1.
	channels := []asset.CategoryChannel{
		{ID: "ord-normal-old", Category: "boxe", ChannelURL: "https://n", CreatedAt: now, UpdatedAt: now,
			Enabled: 1, Priority: 2, NextCheckAt: hourAgo},
		{ID: "ord-cold-old", Category: "boxe", ChannelURL: "https://c", CreatedAt: now, UpdatedAt: now,
			Enabled: 1, Priority: 3, NextCheckAt: hourAgo},
		{ID: "ord-hot-fresh", Category: "boxe", ChannelURL: "https://h", CreatedAt: now, UpdatedAt: now,
			Enabled: 1, Priority: 1, NextCheckAt: minAgo},
		{ID: "ord-hot-old", Category: "boxe", ChannelURL: "https://h2", CreatedAt: now, UpdatedAt: now,
			Enabled: 1, Priority: 1, NextCheckAt: hourAgo},
	}
	for i := range channels {
		insertTestChannel(t, db, &channels[i])
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	leaseUntil := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
	rows, err := repo.ClaimDue(ctx, nowStr, "worker-X", leaseUntil, 10)
	if err != nil {
		t.Fatalf("ClaimDue err: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows len = %d, want 4", len(rows))
	}
	// Expected order: hot-old (Priority 1, oldest) → hot-fresh (Priority 1)
	// → normal-old (Priority 2) → cold-old (Priority 3).
	wantOrder := []string{"ord-hot-old", "ord-hot-fresh", "ord-normal-old", "ord-cold-old"}
	for i, want := range wantOrder {
		if rows[i].ID != want {
			t.Errorf("ClaimDue order[%d] = %q, want %q", i, rows[i].ID, want)
		}
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func futureRFC3339(d time.Duration) string {
	return time.Now().UTC().Add(d).Format(time.RFC3339)
}
