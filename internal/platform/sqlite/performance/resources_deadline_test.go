package performance

import (
	"testing"
)

// TestSamplePersistTimeoutExceedsSQLiteBusyTimeout pins the invariant the
// production incident exposed: the sampler's collect+persist deadline must
// stay above the canonical SQLite busy_timeout (5s in platform/sqlite/pool.go).
// The historical 3s deadline was shorter than the legitimate 5s busy-wait, so
// every persist under writer contention failed with "context deadline
// exceeded" — a self-inflicted instrumentation gap, not a real store fault.
func TestSamplePersistTimeoutExceedsSQLiteBusyTimeout(t *testing.T) {
	if samplePersistTimeout <= sqliteBusyTimeoutReference {
		t.Fatalf("samplePersistTimeout = %v must exceed sqlite busy_timeout %v", samplePersistTimeout, sqliteBusyTimeoutReference)
	}
}

// TestSamplerOptionsNormalizedDefaultPersistTimeout pins the default: an
// options struct without an explicit PersistTimeout must select the raised
// canonical deadline, not the historical 3s value.
func TestSamplerOptionsNormalizedDefaultPersistTimeout(t *testing.T) {
	normalized, err := SamplerOptions{}.normalized()
	if err != nil {
		t.Fatalf("normalized: %v", err)
	}
	if normalized.PersistTimeout != samplePersistTimeout {
		t.Fatalf("default PersistTimeout = %v, want %v", normalized.PersistTimeout, samplePersistTimeout)
	}
	if normalized.Interval != DefaultSampleInterval {
		t.Fatalf("default Interval = %v, want %v", normalized.Interval, DefaultSampleInterval)
	}
}
