package multilingual

// benchmark.go — speedup benchmark projection. One sample per concurrency
// level (1/2/4/8) of the render fan-out; ComputeSpeedup derives the speedup
// vs the serial run and the parallel efficiency, so "should I add more
// workers?" is answered by numbers, not by eye.
//
// Speedup(N)   = wall(serial) / wall(N)
// Efficiency(N) = Speedup(N) / N          (1.0 = perfect linear scaling)

// BenchmarkSample is one concurrency level's measured render fan-out.
type BenchmarkSample struct {
	Concurrency int     `json:"concurrency"`
	WallMS      int64   `json:"wall_ms"`
	WorkMS      int64   `json:"work_ms"` // summed per-clip render work (≠ wall)
	MaxObserved int     `json:"max_observed"`
	AvgObserved float64 `json:"avg_observed"`
	Speedup     float64 `json:"speedup"`
	Efficiency  float64 `json:"parallel_efficiency"`
}

// ComputeSpeedup fills Speedup (vs the first, serial, sample) and
// parallel_efficiency (speedup / concurrency) in place. The first sample MUST
// be the serial run (concurrency 1). Returns the same slice for call chaining.
// Samples with non-positive wall or concurrency keep zero speedup/efficiency.
func ComputeSpeedup(samples []BenchmarkSample) []BenchmarkSample {
	if len(samples) == 0 {
		return samples
	}
	serial := samples[0].WallMS
	for i := range samples {
		s := &samples[i]
		if serial > 0 && s.WallMS > 0 && s.Concurrency > 0 {
			s.Speedup = float64(serial) / float64(s.WallMS)
			s.Efficiency = s.Speedup / float64(s.Concurrency)
		}
	}
	return samples
}
