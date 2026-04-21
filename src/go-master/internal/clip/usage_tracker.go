package clip

import (
	"sync"
	"time"
)

// UsageTracker tracks clip usage to penalize overused clips
type UsageTracker struct {
	mu          sync.RWMutex
	usageCounts map[string]int       // clipID -> count of times suggested
	lastUsedAt  map[string]time.Time // clipID -> last time suggested
	maxHistory  int                  // maximum number of tracked clips
}

// NewUsageTracker creates a new usage tracker
func NewUsageTracker(maxHistory int) *UsageTracker {
	if maxHistory == 0 {
		maxHistory = 5000
	}
	return &UsageTracker{
		usageCounts: make(map[string]int),
		lastUsedAt:  make(map[string]time.Time),
		maxHistory:  maxHistory,
	}
}

// RecordUsage records that a clip was suggested/used
func (t *UsageTracker) RecordUsage(clipID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.usageCounts[clipID]++
	t.lastUsedAt[clipID] = time.Now()

	// Cleanup if too many entries
	if len(t.usageCounts) > t.maxHistory {
		t.cleanup()
	}
}

// RecordMultipleUsage records multiple clips as used (batch)
func (t *UsageTracker) RecordMultipleUsage(clipIDs []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	for _, id := range clipIDs {
		t.usageCounts[id]++
		t.lastUsedAt[id] = now
	}

	if len(t.usageCounts) > t.maxHistory {
		t.cleanup()
	}
}

// GetPenalty returns the usage penalty for a clip (0 to 30 points)
// Clips used more recently and more frequently get higher penalties
func (t *UsageTracker) GetPenalty(clipID string) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	count := t.usageCounts[clipID]
	if count == 0 {
		return 0
	}

	lastUsed := t.lastUsedAt[clipID]

	// Frequency penalty: 0-20 points based on usage count
	freqPenalty := 0.0
	if count >= 10 {
		freqPenalty = 20
	} else if count >= 5 {
		freqPenalty = 15
	} else if count >= 3 {
		freqPenalty = 10
	} else if count >= 2 {
		freqPenalty = 5
	} else {
		freqPenalty = 2
	}

	// Recency penalty: 0-10 points, decays over 1 hour
	recencyPenalty := 0.0
	if !lastUsed.IsZero() {
		ageMinutes := time.Since(lastUsed).Minutes()
		if ageMinutes < 5 {
			recencyPenalty = 10 // Very recent
		} else if ageMinutes < 15 {
			recencyPenalty = 7
		} else if ageMinutes < 30 {
			recencyPenalty = 4
		} else if ageMinutes < 60 {
			recencyPenalty = 2
		}
	}

	return freqPenalty + recencyPenalty
}

// GetStats returns usage statistics for a clip
func (t *UsageTracker) GetStats(clipID string) (count int, lastUsed time.Time) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.usageCounts[clipID], t.lastUsedAt[clipID]
}

// Reset clears all usage data
func (t *UsageTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usageCounts = make(map[string]int)
	t.lastUsedAt = make(map[string]time.Time)
}

// cleanup removes the oldest entries when the map is too large
func (t *UsageTracker) cleanup() {
	// Remove clips not used in the last 24 hours
	cutoff := time.Now().Add(-24 * time.Hour)
	for id, lastUsed := range t.lastUsedAt {
		if lastUsed.Before(cutoff) {
			delete(t.usageCounts, id)
			delete(t.lastUsedAt, id)
		}
	}
}

// GlobalUsageTracker is the global instance of the usage tracker
var GlobalUsageTracker = NewUsageTracker(5000)

// RecordClipUsage records that a clip was suggested (call this from handlers)
func RecordClipUsage(clipID string) {
	GlobalUsageTracker.RecordUsage(clipID)
}

// RecordMultipleClipUsage records multiple clips as used
func RecordMultipleClipUsage(clipIDs []string) {
	GlobalUsageTracker.RecordMultipleUsage(clipIDs)
}

// GetUsagePenalty returns the usage penalty for a clip
func GetUsagePenalty(clipID string) float64 {
	return GlobalUsageTracker.GetPenalty(clipID)
}
