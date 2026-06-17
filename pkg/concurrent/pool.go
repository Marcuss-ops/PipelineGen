// Package concurrent provides generic concurrency helpers.
package concurrent

import "sync"

// MapResult holds a single item's result with its original index.
// Used internally by ParallelMap for ordered result collection.
type MapResult[T any] struct {
	Index int
	Value T
}

// ParallelMap processes items concurrently with a semaphore limit.
// Returns results ordered by the original slice index.
//
// The fn callback receives context, index, and item, and should return the result value.
// Errors should be handled within the callback (e.g., embedded in the result type).
// If fn panics, the panic propagates to the caller (no recovery).
func ParallelMap[T, U any](items []T, concurrency int, fn func(int, T) U) []U {
	resChan := make(chan MapResult[U], len(items))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for idx, item := range items {
		wg.Add(1)
		go func(idx int, item T) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire concurrency token
			defer func() { <-sem }() // Release token

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
