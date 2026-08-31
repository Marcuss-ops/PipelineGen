package job

// JobStats is the read-only aggregate returned by job statistics readers.
// It belongs to the kernel contract so application consumers do not depend on
// a particular persistence adapter (SQLite, PostgreSQL, or another store).
type JobStats struct {
	Total      int                       `json:"total"`
	ByStatus   map[Status]int            `json:"by_status"`
	ByType     map[string]map[Status]int `json:"by_type"`
	DurationMs struct {
		Overall float64 `json:"overall_ms"`
		ByType  map[string]struct {
			Count           int     `json:"count"`
			AvgDurationMs   float64 `json:"avg_duration_ms"`
			ImagesGenerated int     `json:"images_generated,omitempty"`
			Errors          int     `json:"errors,omitempty"`
		} `json:"by_type"`
	} `json:"durations"`
	StaleRunning int `json:"stale_running"`
	Recent24h    struct {
		Completed       int `json:"completed"`
		Failed          int `json:"failed"`
		ImagesGenerated int `json:"images_generated"`
	} `json:"recent_24h"`
}
