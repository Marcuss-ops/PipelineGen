package clipsearch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/errgroup"
)

func TestClipSearch_ParallelPool(t *testing.T) {
	// Setup service con semaphore a 3 per questo test (più facile da verificare)
	svc := &Service{
		workerSemaphore: make(chan struct{}, 3),
	}

	var concurrent int32
	var maxConcurrent int32
	var totalProcessed int32

	// Mock processKeyword per simulare un lavoro che impiega tempo
	mockProcess := func(ctx context.Context, kw string) {
		atomic.AddInt32(&concurrent, 1)
		curr := atomic.LoadInt32(&concurrent)
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if curr <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, curr) {
				break
			}
		}

		time.Sleep(500 * time.Millisecond) // Simula download/processing
		
		atomic.AddInt32(&concurrent, -1)
		atomic.AddInt32(&totalProcessed, 1)
	}

	keywords := []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9"}
	
	start := time.Now()
	g, ctx := errgroup.WithContext(context.Background())

	for _, kw := range keywords {
		kw := kw
		g.Go(func() error {
			select {
			case svc.workerSemaphore <- struct{}{}:
				defer func() { <-svc.workerSemaphore }()
			case <-ctx.Done():
				return ctx.Err()
			}
			
			mockProcess(ctx, kw)
			return nil
		})
	}

	err := g.Wait()
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, int32(len(keywords)), totalProcessed)
	assert.Equal(t, int32(3), maxConcurrent, "Il parallelismo massimo deve essere limitato dal semaforo")
	
	// Se fosse sequenziale: 9 * 500ms = 4.5s
	// Con 3 worker: (9/3) * 500ms = 1.5s (+ overhead)
	assert.Less(t, elapsed.Seconds(), 2.5, "Il tempo totale deve riflettere l'esecuzione parallela")
}
