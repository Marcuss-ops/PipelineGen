package postgres

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// events_batch_test.go pins the batched event-insertion contract introduced
// for SubmitBatch and the lease-expiry sweep:
//
//   - buildEventsInsertSQL emits ONE multi-row INSERT whose VALUES order is
//     the row order (render_events.id is BIGSERIAL, so the persisted order
//     equals the historical per-event insert order);
//   - the argument encoding matches recordEvent exactly (empty attempt/worker
//     → NULL, nil/"null" payload → {});
//   - a persisted batch keeps per-job event rows identical to the per-job
//     path (byte-identical VALUES, only the statement shape differs).

// TestBuildEventsInsertSQLPreservesRowOrder is the pure, DB-free shape test:
// placeholder numbering, argument order and payload encoding of the multi-row
// INSERT.
func TestBuildEventsInsertSQLPreservesRowOrder(t *testing.T) {
	rows := []eventRow{
		{eventType: eventJobCreated, jobID: "job-1"},
		{eventType: eventJobRequeued, jobID: "job-2", attemptID: "job-2:1", payload: map[string]any{"reason": "lease expired"}},
		{eventType: eventJobFailed, jobID: "job-3", workerID: "w1", payload: nil},
	}
	query, args, err := buildEventsInsertSQL(rows)
	if err != nil {
		t.Fatal(err)
	}

	// Three value groups, $1..$15 in order.
	for i := 1; i <= 15; i++ {
		if !strings.Contains(query, "$"+strconv.Itoa(i)) {
			t.Fatalf("query is missing placeholder $%d:\n%s", i, query)
		}
	}
	if strings.Contains(query, "$16") {
		t.Fatalf("query has more placeholders than rows:\n%s", query)
	}
	if len(args) != 15 {
		t.Fatalf("args = %d, want 15", len(args))
	}

	// Row 1: empty attempt/worker must be NULL, nil payload must be {}.
	if args[0] != "job-1" || args[3] != eventJobCreated {
		t.Fatalf("row 1 args out of order: %v", args[:5])
	}
	if args[1] != nil || args[2] != nil {
		t.Fatalf("empty attempt/worker must encode as NULL, got %v %v", args[1], args[2])
	}
	if got := string(args[4].([]byte)); got != "{}" {
		t.Fatalf("nil payload = %s, want {}", got)
	}

	// Row 2: values in row order, payload JSON preserved.
	if args[5] != "job-2" || args[6] != "job-2:1" || args[8] != eventJobRequeued {
		t.Fatalf("row 2 args out of order: %v", args[5:10])
	}
	var payload map[string]any
	if err := json.Unmarshal(args[9].([]byte), &payload); err != nil || payload["reason"] != "lease expired" {
		t.Fatalf("row 2 payload = %s, want {\"reason\":\"lease expired\"}", args[9])
	}

	// Row 3: worker set, nil payload → {}.
	if args[10] != "job-3" || args[12] != "w1" || args[13] != eventJobFailed {
		t.Fatalf("row 3 args out of order: %v", args[10:15])
	}
	if got := string(args[14].([]byte)); got != "{}" {
		t.Fatalf("nil payload = %s, want {}", got)
	}

	// Row order must be the VALUES order: job-1 group first, job-3 last.
	first := strings.Index(query, "($1, $2, $3, $4, $5)")
	last := strings.Index(query, "($11, $12, $13, $14, $15)")
	if first < 0 || last < 0 || first >= last {
		t.Fatalf("VALUES groups are not in row order:\n%s", query)
	}
}

// TestSubmitBatchEventsPersistInJobOrder is the PostgreSQL half of the
// batched path: one multi-row INSERT must persist exactly one JOB_CREATED per
// job, in the submitted order (queryEvents-style ORDER BY id across jobs).
func TestSubmitBatchEventsPersistInJobOrder(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	jobs := []model.Job{
		{ID: "batch-e1", RenderPlan: []byte(`{"schema":"chronon.render-plan.v2","version":2}`)},
		{ID: "batch-e2", RenderPlan: []byte(`{"schema":"chronon.render-plan.v2","version":2}`)},
		{ID: "batch-e3", RenderPlan: []byte(`{"schema":"chronon.render-plan.v2","version":2}`)},
	}
	if err := r.SubmitBatch(context.Background(), jobs); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(context.Background(), `
		SELECT job_id, event_type
		FROM render_events
		WHERE job_id LIKE 'batch-e%'
		ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][2]string
	for rows.Next() {
		var jobID, eventType string
		if err := rows.Scan(&jobID, &eventType); err != nil {
			t.Fatal(err)
		}
		got = append(got, [2]string{jobID, eventType})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("events = %v, want exactly one JOB_CREATED per job", got)
	}
	for i, job := range jobs {
		if got[i][0] != job.ID || got[i][1] != eventJobCreated {
			t.Fatalf("event[%d] = %v, want JOB_CREATED for %s", i, got[i], job.ID)
		}
	}
}

// TestRequeueExpiredEventsPersistInSweepOrder pins the sweep's batched events:
// one JOB_REQUEUED per expired job, in the sweep's job order.
func TestRequeueExpiredEventsPersistInSweepOrder(t *testing.T) {
	r, db := setupRepo(t, 10*time.Millisecond, 3)
	for _, id := range []string{"sweep-e1", "sweep-e2", "sweep-e3"} {
		if err := r.Submit(context.Background(), model.Job{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"sweep-e1", "sweep-e2", "sweep-e3"} {
		claimed, _, err := r.Claim(context.Background(), "w1")
		if err != nil || claimed == nil || claimed.ID != id {
			t.Fatalf("claim %s: got %+v err=%v", id, claimed, err)
		}
	}

	time.Sleep(30 * time.Millisecond)
	n, err := r.RequeueExpired(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("RequeueExpired = %d, want 3", n)
	}

	rows, err := db.QueryContext(context.Background(), `
		SELECT job_id, event_type
		FROM render_events
		WHERE job_id LIKE 'sweep-e%' AND event_type = $1
		ORDER BY id`, eventJobRequeued)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var jobID, eventType string
		if err := rows.Scan(&jobID, &eventType); err != nil {
			t.Fatal(err)
		}
		got = append(got, jobID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"sweep-e1", "sweep-e2", "sweep-e3"}
	if len(got) != len(want) {
		t.Fatalf("requeue events = %v, want one per expired job in sweep order %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requeue event order = %v, want %v", got, want)
		}
	}
}
