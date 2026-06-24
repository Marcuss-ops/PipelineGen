package health

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

const jobsSchema = `CREATE TABLE jobs (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`

func openJobsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory db")
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(jobsSchema)
	require.NoError(t, err)
	return db
}

func TestJobsChecker_Happy(t *testing.T) {
	db := openJobsTestDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO jobs(id, status, updated_at) VALUES (?, ?, ?)`,
		"j1", "running", time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, err)
	c := NewJobsChecker(db)
	res := c.CheckJobs(ctx)
	require.True(t, res["ok"].(bool), "got %v", res)
	require.Equal(t, 1, res["running_jobs"])
}

func TestJobsChecker_NoRecentActivity(t *testing.T) {
	db := openJobsTestDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO jobs(id, status, updated_at) VALUES (?, ?, ?)`,
		"j1", "running", time.Now().Add(-30*time.Minute).UTC().Format(time.RFC3339))
	require.NoError(t, err)
	c := NewJobsChecker(db)
	res := c.CheckJobs(ctx)
	require.True(t, res["ok"].(bool), "reachable-but-idle is ok=true")
	require.Equal(t, 0, res["running_jobs"])
}

func TestJobsChecker_LegacyStatusCaseInsensitive(t *testing.T) {
	db := openJobsTestDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO jobs(id, status, updated_at) VALUES (?, ?, ?)`,
		"j1", "RUNNING", time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO jobs(id, status, updated_at) VALUES (?, ?, ?)`,
		"j2", "leased", time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, err)
	c := NewJobsChecker(db)
	res := c.CheckJobs(ctx)
	require.True(t, res["ok"].(bool))
	require.Equal(t, 2, res["running_jobs"])
}

func TestJobsChecker_NoTable_IsFailure(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	c := NewJobsChecker(db)
	res := c.CheckJobs(context.Background())
	require.False(t, res["ok"].(bool))
	require.NotEmpty(t, res["error"])
}

func TestJobsChecker_ContextCancellation(t *testing.T) {
	db := openJobsTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)
	c := NewJobsChecker(db)
	res := c.CheckJobs(ctx)
	require.False(t, res["ok"].(bool), "ctx cancelled must be ok=false, got %v", res)
	require.True(t, errors.Is(ctx.Err(), context.DeadlineExceeded))
}

func TestJobsChecker_DBNil(t *testing.T) {
	c := NewJobsChecker(nil)
	ctx := context.Background()
	require.NotPanics(t, func() {
		res := c.CheckJobs(ctx)
		require.False(t, res["ok"].(bool), "nil db must yield ok=false, got %v", res)
	})
}

func TestJobsChecker_DBClosed(t *testing.T) {
	db := openJobsTestDB(t)
	require.NoError(t, db.Close())
	c := NewJobsChecker(db)
	res := c.CheckJobs(context.Background())
	require.False(t, res["ok"].(bool), "closed db must yield ok=false")
}

func TestJobsChecker_MissingStatusColumn(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE jobs (id TEXT PRIMARY KEY, updated_at TEXT)`)
	require.NoError(t, err)
	c := NewJobsChecker(db)
	res := c.CheckJobs(context.Background())
	require.False(t, res["ok"].(bool), "missing status column must yield ok=false")
	require.Contains(t, res["error"].(string), "malformed")
}

func TestJobsChecker_ZeroRows(t *testing.T) {
	db := openJobsTestDB(t)
	ctx := context.Background()
	// No rows inserted — table exists but empty.
	c := NewJobsChecker(db)
	res := c.CheckJobs(ctx)
	require.True(t, res["ok"].(bool), "empty table is reachable but idle → ok=true")
	require.Equal(t, 0, res["running_jobs"])
}

func TestJobsChecker_RecentPendingRows(t *testing.T) {
	db := openJobsTestDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO jobs(id, status, updated_at) VALUES (?, ?, ?)`,
		"j1", "pending", time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO jobs(id, status, updated_at) VALUES (?, ?, ?)`,
		"j2", "leased", time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, err)
	c := NewJobsChecker(db)
	res := c.CheckJobs(ctx)
	require.True(t, res["ok"].(bool))
	require.Equal(t, 2, res["running_jobs"])
}

func TestJobsChecker_OldRowsIgnored(t *testing.T) {
	db := openJobsTestDB(t)
	ctx := context.Background()
	// Insert old rows (> 5 minutes ago) — they should NOT be counted.
	oldTime := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, `INSERT INTO jobs(id, status, updated_at) VALUES (?, ?, ?)`,
		"j1", "running", oldTime)
	require.NoError(t, err)
	c := NewJobsChecker(db)
	res := c.CheckJobs(ctx)
	require.True(t, res["ok"].(bool), "old rows should not prevent ok=true")
	require.Equal(t, 0, res["running_jobs"], "old rows should not be counted")
}

func TestJobsChecker_SchemaDrift_MissingUpdatedAt(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE jobs (id TEXT PRIMARY KEY, status TEXT)`)
	require.NoError(t, err)
	c := NewJobsChecker(db)
	res := c.CheckJobs(context.Background())
	require.False(t, res["ok"].(bool), "missing updated_at column must be ok=false")
	require.Contains(t, res["error"].(string), "malformed", "error message pin")
}
