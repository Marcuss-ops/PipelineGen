package wiring

// clampUnit normalizes a raw relevance/score value into [0,1]. It is a
// provider-neutral helper owned by the wiring package so discovery adapters do
// not depend on a specific capability package for score math.
func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
