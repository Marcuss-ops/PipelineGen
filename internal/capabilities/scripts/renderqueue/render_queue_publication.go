package renderqueue

import (
	"context"
	"sync"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

type publicationBatch struct {
	wg sync.WaitGroup
	mu sync.Mutex
	// err is the FIRST publication failure of this batch. Later failures are
	// dropped on purpose: the first one is the actionable cause and the run
	// fails on it, exactly like the synchronous path.
	err error
}

func (b *publicationBatch) record(err error) {
	if b == nil || err == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.err = err
	}
}

// join blocks until every publication submitted to this batch has finished and
// returns the batch's first failure. Taking the error clears it, so a retry of
// the same run neither re-reports an old failure nor is poisoned by it.
func (b *publicationBatch) join() error {
	if b == nil {
		return nil
	}
	b.wg.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	err := b.err
	b.err = nil
	return err
}

// publicationBatchFor resolves the publication batch the work submitted with
// ctx belongs to. The per-run identity is the kernel run bound to ctx (the job
// worker binds it before the script runner is reached), so two concurrent runs
// sharing this enqueuer never share a batch. A caller with no run bound (small
// unit-test compositions, one-shot CLI calls) falls back to the single unbound
// batch, which is the historical process-wide behaviour.
func (e *QueueRenderEnqueuer) publicationBatchFor(ctx context.Context) *publicationBatch {
	e.publicationMu.Lock()
	defer e.publicationMu.Unlock()
	key := kernobs.FromContext(ctx)
	if key == nil {
		if e.publicationUnbound == nil {
			e.publicationUnbound = &publicationBatch{}
		}
		return e.publicationUnbound
	}
	if e.publicationBatches == nil {
		e.publicationBatches = make(map[*kernobs.Run]*publicationBatch)
	}
	if batch, ok := e.publicationBatches[key]; ok {
		return batch
	}
	batch := &publicationBatch{}
	e.publicationBatches[key] = batch
	return batch
}

// publicationBatchForRun returns the EXISTING batch of the run bound to ctx.
// It never creates one: a run with no queued publication has nothing to join.
func (e *QueueRenderEnqueuer) publicationBatchForRun(ctx context.Context) (*kernobs.Run, *publicationBatch) {
	key := kernobs.FromContext(ctx)
	if key == nil {
		e.publicationMu.Lock()
		defer e.publicationMu.Unlock()
		return nil, e.publicationUnbound
	}
	e.publicationMu.Lock()
	defer e.publicationMu.Unlock()
	return key, e.publicationBatches[key]
}

// Wait joins ITS OWN run's queued publication/analytics work. It is the
// completion boundary for the render publication pool: the caller (the runner,
// right before completion) must invoke it before reporting a run as COMPLETE.
//
// The publication pool is process-wide but a run only ever joins its own
// batch, keyed by the kernel run bound to ctx:
//
//   - run A never blocks on run B's in-flight uploads, so a slow concurrent
//     run cannot stretch A's tail latency;
//   - run A is never FAILED by run B's publication error. That misattribution
//     was observed live: a run died on "context canceled" from another run's
//     overlay upload, so the operator's retry could not fix its own run.
//
// The batch is retired after the join (the run is finishing, so nothing else
// can be submitted to it), which also means a later attempt of the same run
// starts clean instead of inheriting a previous failure.
func (e *QueueRenderEnqueuer) Wait(ctx context.Context) error {
	if e == nil || !e.asyncPublication {
		return nil
	}
	run, batch := e.publicationBatchForRun(ctx)
	if batch == nil {
		return nil
	}
	err := batch.join()
	if run != nil {
		e.publicationMu.Lock()
		// Only the batch that was just joined is retired: a concurrent Wait
		// for the same run (or a publication submitted between the join and
		// this lock) must never lose its own join handle.
		if e.publicationBatches[run] == batch {
			delete(e.publicationBatches, run)
		}
		e.publicationMu.Unlock()
	}
	return err
}

// scheduleFinalJobOverlayFrames moves an otherwise-valid replace overlay to
// the first free frame interval after its planned start, constrained to the
// source scene. This preserves every overlay while making the final payload
// conform to Master 51's non-overlapping mode=replace contract.
