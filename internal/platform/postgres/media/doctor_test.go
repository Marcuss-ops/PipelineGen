package media_test

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/mediaops"
)

// DB-08: the PostgreSQL doctor must run against the canonical fixture and
// agree that it has no FK/index errors. Dead-tuple warnings are allowed.
func TestDoctorFindingsCleanFixture(t *testing.T) {
	if _, ok := requirePostgresDSN(t); !ok {
		t.Skip("TEST_POSTGRES_DSN not set")
	}
	db := newMediaTestDB(t)
	findings, err := mediaops.DoctorFindings(context.Background(), db)
	if err != nil {
		t.Fatalf("DoctorFindings: %v", err)
	}
	for _, f := range findings {
		if f.Severity == "error" {
			t.Errorf("unexpected error finding: %s", f.String())
		}
	}
}
