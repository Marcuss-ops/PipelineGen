// Package concurrent provides generic concurrency helpers.
package platform

import "sync"

// MapResult holds a single item's result with its original index.
type MapResult[T any] struct {
	Index int
	Value T
}

// ParallelMap processes items concurrently with a semaphore limit.
// Returns results ordered by the original slice index.
// The fn callback receives index and item and should return the result value.
func ParallelMap[T, U any](items []T, concurrency int, fn func(int, T) U) []U {
	if len(items) == 0 {
		return nil
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(items) {
		concurrency = len(items)
	}

	resChan := make(chan MapResult[U], len(items))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for idx, item := range items {
		wg.Add(1)
		go func(idx int, item T) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			resChan <- MapResult[U]{Index: idx, Value: fn(idx, item)}
		}(idx, item)
	}

	wg.Wait()
	close(resChan)

	results := make([]U, len(items))
	for res := range resChan {
		results[res.Index] = res.Value
	}
	return results
}

// Pool provides a bounded worker pool for concurrent operations.
type Pool struct {
	workers int
	sem     chan struct{}
	mu      sync.Mutex
}

func NewPool(workers int) *Pool {
	if workers <= 0 {
		workers = 1
	}
	return &Pool{workers: workers, sem: make(chan struct{}, workers)}
}

func (p *Pool) Go(fn func()) {
	p.sem <- struct{}{}
	go func() {
		defer func() { <-p.sem }()
		fn()
	}()
}
