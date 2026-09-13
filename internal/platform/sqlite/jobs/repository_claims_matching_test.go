package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// seedQueuedJob inserts one QUEUED job row carrying the given payload. Only the
// columns the canonical projection requires are written; the schema defaults
// cover the rest.
func seedClaimQueuedJob(t *testing.T, db *sql.DB, id, jobType string, priority int, payload string) {
	t.Helper()
	now := timeutil.FormatRFC3339(time.Now().UTC())
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO jobs (id, type, status, priority, payload_json, created_at, updated_at, revision)
		 VALUES (?, ?, 'QUEUED', ?, ?, ?, ?, 1)`,
		id, jobType, priority, payload, now, now); err != nil {
		t.Fatalf("seed QUEUED job %s: %v", id, err)
	}
}

// TestClaimNextMatching_SelectsOnlyMatchingPhase pins the P0.5 guardrail
// capability: a payload-scoped claim must reach the settle continuation BEHIND a
// higher-priority submit job without claiming the submit job itself, and the
// submit job must remain available to the unscoped pool.
func TestClaimNextMatching_SelectsOnlyMatchingPhase(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())
	ctx := context.Background()

	// submit is higher priority AND older: any unscoped claim takes it first.
	seedClaimQueuedJob(t, db, "job-submit", "clip.render", 10, `{"render_phase":"submit"}`)
	seedClaimQueuedJob(t, db, "job-settle", "clip.render", 1, `{"render_phase":"settle"}`)

	claimed, err := store.ClaimNextMatching(ctx, "settle-worker", time.Minute,
		[]string{"clip.render"}, job.PayloadMatch{"render_phase": "settle"})
	if err != nil {
		t.Fatalf("ClaimNextMatching: %v", err)
	}
	if claimed == nil || claimed.ID != "job-settle" {
		t.Fatalf("scoped claim = %+v, want job-settle", claimed)
	}
	if !job.MatchesPayload(claimed.Payload, job.PayloadMatch{"render_phase": "settle"}) {
		t.Fatalf("claimed payload = %s, want the hydrated settle payload", claimed.Payload)
	}

	remaining, err := store.ClaimNext(ctx, "general-worker", time.Minute, []string{"clip.render"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if remaining == nil || remaining.ID != "job-submit" {
		t.Fatalf("unscoped claim = %+v, want job-submit (the scoped pool must not consume it)", remaining)
	}
}

// TestClaimNextMatching_NoMatchingJobReturnsNil pins that a scoped claim on a
// queue holding only non-matching jobs reports an empty poll instead of
// degrading to an unscoped claim.
func TestClaimNextMatching_NoMatchingJobReturnsNil(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())

	seedClaimQueuedJob(t, db, "job-submit", "clip.render", 10, `{"render_phase":"submit"}`)

	claimed, err := store.ClaimNextMatching(context.Background(), "settle-worker", time.Minute,
		[]string{"clip.render"}, job.PayloadMatch{"render_phase": "settle"})
	if err != nil {
		t.Fatalf("ClaimNextMatching: %v", err)
	}
	if claimed != nil {
		t.Fatalf("claimed %+v, want an empty poll", claimed)
	}
}

// TestClaimNextMatching_EmptyMatchIsUnscoped pins backwards compatibility: an
// empty matcher is the historical ClaimNext, priority order included.
func TestClaimNextMatching_EmptyMatchIsUnscoped(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())

	seedClaimQueuedJob(t, db, "job-low", "clip.render", 1, `{"render_phase":"settle"}`)
	seedClaimQueuedJob(t, db, "job-high", "clip.render", 10, `{"render_phase":"submit"}`)

	claimed, err := store.ClaimNextMatching(context.Background(), "any-worker", time.Minute, []string{"clip.render"}, nil)
	if err != nil {
		t.Fatalf("ClaimNextMatching(nil match): %v", err)
	}
	if claimed == nil || claimed.ID != "job-high" {
		t.Fatalf("unscoped claim = %+v, want job-high (priority order)", claimed)
	}
}

// TestClaimNextMatching_RespectsTypeFilter pins that the payload scope is applied
// on top of the job-type capability, not instead of it.
func TestClaimNextMatching_RespectsTypeFilter(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())

	seedClaimQueuedJob(t, db, "job-clip", "clip.render", 10, `{"render_phase":"settle"}`)
	seedClaimQueuedJob(t, db, "job-other", "other.job", 1, `{"render_phase":"settle"}`)

	claimed, err := store.ClaimNextMatching(context.Background(), "settle-worker", time.Minute,
		[]string{"other.job"}, job.PayloadMatch{"render_phase": "settle"})
	if err != nil {
		t.Fatalf("ClaimNextMatching: %v", err)
	}
	if claimed == nil || claimed.ID != "job-other" {
		t.Fatalf("scoped claim = %+v, want job-other (type filter must still apply)", claimed)
	}
}
