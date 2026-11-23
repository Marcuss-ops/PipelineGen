package maintenance

import (
	"context"
	"errors"
	"testing"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

type recordingStatusWriter struct {
	statuses map[int64]string
	fail     map[int64]error
}

func (w *recordingStatusWriter) SetCandidateStatus(_ context.Context, id int64, status string) error {
	if err, ok := w.fail[id]; ok {
		return err
	}
	if w.statuses == nil {
		w.statuses = make(map[int64]string)
	}
	w.statuses[id] = status
	return nil
}

// TestRetireCatalogCandidatesUsesTheCanonicalTransition pins the action: the
// only write is status=retired through the repository, and every id is
// attempted.
func TestRetireCatalogCandidatesUsesTheCanonicalTransition(t *testing.T) {
	writer := &recordingStatusWriter{}
	retired, err := retireCatalogCandidates(context.Background(), writer, []int64{7, 9})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retired != 2 {
		t.Fatalf("retired = %d, want 2", retired)
	}
	for _, id := range []int64{7, 9} {
		if got := writer.statuses[id]; got != capentity.CandidateStatusRetired {
			t.Fatalf("candidate %d status = %q, want %q", id, got, capentity.CandidateStatusRetired)
		}
	}
}

// TestRetireCatalogCandidatesReportsPartialApply proves a failure does not hide
// how far the apply got: the successful count is returned with the error.
func TestRetireCatalogCandidatesReportsPartialApply(t *testing.T) {
	writer := &recordingStatusWriter{fail: map[int64]error{9: errors.New("database is locked")}}
	retired, err := retireCatalogCandidates(context.Background(), writer, []int64{7, 9, 11})
	if err == nil {
		t.Fatal("a failed retirement must surface an error")
	}
	if retired != 2 {
		t.Fatalf("retired = %d, want 2", retired)
	}
	if writer.statuses[7] != capentity.CandidateStatusRetired || writer.statuses[11] != capentity.CandidateStatusRetired {
		t.Fatalf("every id must be attempted, got %+v", writer.statuses)
	}
}
