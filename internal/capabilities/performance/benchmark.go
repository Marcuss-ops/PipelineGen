// Package performance — benchmark contract pieces still consumed by the
// performance registry: the workload version, the baseline reader and the
// median helper shared with the SQLite operations projector.
package performance

import (
	"context"
	"sort"
)

// WorkloadVersion is the canonical version stamped on every benchmark
// workload and performance run.
const WorkloadVersion = "v1"

// BaselineSource returns the recorded wall-time samples (one per past run)
// for a workload, oldest first.
type BaselineSource interface {
	WorkloadSamples(ctx context.Context, workloadID string) ([]int64, error)
}

// MedianInt64 returns the median of the values (sorted internally). Empty
// input returns 0.
func MedianInt64(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if len(sorted)%2 == 1 {
		return float64(sorted[len(sorted)/2])
	}
	mid := len(sorted) / 2
	return (float64(sorted[mid-1]) + float64(sorted[mid])) / 2
}
