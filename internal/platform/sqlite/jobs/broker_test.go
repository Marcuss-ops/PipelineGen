package jobs

import (
	"errors"
	"testing"

	"github.com/mattn/go-sqlite3"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestMapWriteError_UniqueConstraintBecomesKernelDuplicate(t *testing.T) {
	t.Parallel()
	driverErr := sqlite3.Error{Code: sqlite3.ErrConstraint, ExtendedCode: sqlite3.ErrConstraintUnique}
	err := mapWriteError(driverErr)
	if !errors.Is(err, job.ErrDuplicate) {
		t.Fatalf("mapWriteError() = %v, want errors.Is(job.ErrDuplicate)", err)
	}
	var resolved sqlite3.Error
	if !errors.As(err, &resolved) || resolved.ExtendedCode != sqlite3.ErrConstraintUnique {
		t.Fatalf("mapped error must preserve underlying sqlite3.Error: %v", err)
	}
}

func TestMapWriteError_NonUniquePassesThrough(t *testing.T) {
	t.Parallel()
	driverErr := sqlite3.Error{Code: sqlite3.ErrFull}
	err := mapWriteError(driverErr)
	if errors.Is(err, job.ErrDuplicate) {
		t.Fatalf("non-unique sqlite error classified as duplicate: %v", err)
	}
	var resolved sqlite3.Error
	if !errors.As(err, &resolved) || resolved.Code != sqlite3.ErrFull {
		t.Fatalf("non-unique error not preserved: %v", err)
	}
}
