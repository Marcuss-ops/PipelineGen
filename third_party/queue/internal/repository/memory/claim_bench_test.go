package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// BenchmarkClaimStateDeferredBacklog measures the fallback scan when every
// queued job is deferred (not_before in the future), so the snapshot finds no
// claimable candidate and the concurrent-tail pass must examine the whole FIFO
// and return nil. That is the exact path that used to compare each tail id
// against every snapshot id (O(n^2)); the queue size grows 10x per step so a
// regression back to quadratic scaling is visible as a >10x per-op jump.
//
//	go test -run '^$' -bench BenchmarkClaimStateDeferredBacklog -benchmem ./internal/repository/memory/
func BenchmarkClaimStateDeferredBacklog(b *testing.B) {
	ctx := context.Background()
	future := time.Now().Add(time.Hour)
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("jobs=%d", size), func(b *testing.B) {
			repo := New(30*time.Second, 3)
			for i := 0; i < size; i++ {
				job := model.Job{
					ID:         fmt.Sprintf("job-%06d", i),
					NotBefore:  &future,
					RenderPlan: []byte(`{"n":1}`),
				}
				if err := repo.Submit(ctx, job); err != nil {
					b.Fatalf("submit: %v", err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				job, _, err := repo.ClaimState(ctx, "bench", "")
				if err != nil {
					b.Fatalf("claim: %v", err)
				}
				if job != nil {
					b.Fatalf("claimed deferred job %s", job.ID)
				}
			}
		})
	}
}
