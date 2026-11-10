package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// TestRequeueExpiredDrainsHundredsAcrossBatches is the load-shaped acceptance
// test for batched recovery: 250 expired leases with a 25-row batch are
// recovered in ten independently committed transactions. A single-transaction
// sweep would either finish late or, under the repository's 15-second op
// deadline, roll back everything and leave the whole fleet stuck.
func TestRequeueExpiredDrainsHundredsAcrossBatches(t *testing.T) {
	const jobs = 250
	const batch = 25

	r := newRepo(t, time.Millisecond, 10)
	r.SetRequeueBatch(batch)
	ctx := context.Background()

	for i := 0; i < jobs; i++ {
		id := fmt.Sprintf("expired-scale-%03d", i)
		if err := r.Submit(ctx, model.Job{ID: id, RenderPlan: []byte(`{}`)}); err != nil {
			t.Fatalf("submit %s: %v", id, err)
		}
		if _, _, err := r.Claim(ctx, fmt.Sprintf("w-%03d", i)); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}
	time.Sleep(25 * time.Millisecond) // every 1ms lease has expired

	n, err := r.RequeueExpired(ctx, time.Now())
	if err != nil {
		t.Fatalf("RequeueExpired: %v (recovered %d before the failure)", err, n)
	}
	if n != jobs {
		t.Fatalf("RequeueExpired recovered %d jobs, want %d", n, jobs)
	}
	if stats := r.Stats(ctx); stats.Pending != jobs || stats.Running != 0 {
		t.Fatalf("after recovery: pending=%d running=%d, want pending=%d running=0", stats.Pending, stats.Running, jobs)
	}
}
