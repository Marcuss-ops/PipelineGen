// Package voiceover — e2e_fixtures.go holds the small shared helpers used
// by the voiceover-service E2E fixtures (see e2e_fixtures_test.go).
//
// Extracted 2026-09-12 from the demolished qdrant_indexing_e2e_test.go; the
// fixtures themselves live in e2e_fixtures_test.go, while these two helpers
// are referenced from the adapter + timestamp paths the fixtures build.
package voiceover

import (
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// errNilRecord sentinel for the e2eRepoAdapter.InsertTx nil-guard.
var errNilRecord = errStr("e2eRepoAdapter.InsertTx: nil record")

type errStr string

func (e errStr) Error() string { return string(e) }

// parseTimeOrNow normalizes a persisted RFC3339 timestamp, falling back to
// the current time when the value is empty or unparseable.
func parseTimeOrNow(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	t := timeutil.ParseRFC3339(s)
	if !t.IsZero() {
		return t
	}
	return time.Now()
}
