package jobs

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// concurrentEnqueueBroker deliberately blocks every Create call until all
// callers have reached it. A process-wide enqueue mutex would prevent the
// barrier from opening; the database's UNIQUE constraints and the Create
// rescue path, rather than a service-local lock, must provide idempotency.
type concurrentEnqueueBroker struct {
	nakedJobBroker
	want    int32
	entered chan struct{}
	release chan struct{}
	count   atomic.Int32
	once    sync.Once
}

func newConcurrentEnqueueBroker(want int) *concurrentEnqueueBroker {
	return &concurrentEnqueueBroker{
		want:    int32(want),
		entered: make(chan struct{}, want),
		release: make(chan struct{}),
	}
}

func (b *concurrentEnqueueBroker) FindByTypeAndCorrelation(context.Context, string, string) (*job.Job, error) {
	return nil, nil
}

func (b *concurrentEnqueueBroker) FindByClientAndIdempotencyKey(context.Context, string, string) (*job.Job, error) {
	return nil, nil
}

func (b *concurrentEnqueueBroker) Create(ctx context.Context, _ *job.Job) error {
	b.entered <- struct{}{}
	if b.count.Add(1) == b.want {
		b.once.Do(func() { close(b.release) })
	}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestEnqueue_DifferentCorrelationIDsProceedConcurrently(t *testing.T) {
	const callers = 8
	broker := newConcurrentEnqueueBroker(callers)
	registry := newWiringRegistry(t, time.Minute, 1)
	svc, err := NewService(broker, nil, zap.NewNop(), registry)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, enqueueErr := svc.Enqueue(ctx, &job.EnqueueRequest{
				Type:          wiringTestType,
				CorrelationID: "independent-key-" + string(rune('a'+i)),
			})
			errs <- enqueueErr
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent enqueue failed: %v", err)
		}
	}
	if got := broker.count.Load(); got != callers {
		t.Fatalf("Create calls reaching the concurrent barrier = %d, want %d", got, callers)
	}
}
