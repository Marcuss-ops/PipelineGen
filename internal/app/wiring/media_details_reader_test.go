package wiring

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

// stubConnector yields a non-nil *sql.DB without a real driver, so the helper's
// positive branch can be tested without a live PostgreSQL instance.
type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("stub connector: never connects")
}

func (stubConnector) Driver() driver.Driver { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("stub driver: never connects")
}

// fakeCommitter carries an engine handle the way pgmedia.PostgresMediaCommitter
// does, so the helper's engine derivation is exercised.
type fakeCommitter struct {
	db *sql.DB
}

var _ persistence.AssetCommitter = (*fakeCommitter)(nil)

func (f *fakeCommitter) CommitTx(context.Context, persistence.Transaction, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (f *fakeCommitter) CommitAndIndex(context.Context, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (f *fakeCommitter) CommitAsset(context.Context, persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return persistence.CommittedAsset{}, nil
}

func (f *fakeCommitter) DB() *sql.DB { return f.db }

// fakeCommitterWithoutDB has no engine handle (the legacy/degrade shape).
type fakeCommitterWithoutDB struct{}

var _ persistence.AssetCommitter = (*fakeCommitterWithoutDB)(nil)

func (f *fakeCommitterWithoutDB) CommitTx(context.Context, persistence.Transaction, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (f *fakeCommitterWithoutDB) CommitAndIndex(context.Context, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (f *fakeCommitterWithoutDB) CommitAsset(context.Context, persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return persistence.CommittedAsset{}, nil
}

// TestMediaDetailsReaderFromCommitter_DerivesFromCommitterEngine pins the
// structural invariant that makes the ingest hydration read safe: the details
// reader is derived from the SAME engine the canonical committer writes, so the
// read and the write cannot drift onto two databases — which is the exact
// split-brain this migration removed (ingest hydrated from the operational
// SQLite mirror while the committer wrote PostgreSQL).
func TestMediaDetailsReaderFromCommitter_DerivesFromCommitterEngine(t *testing.T) {
	wired := sql.OpenDB(stubConnector{})
	t.Cleanup(func() { _ = wired.Close() })

	if got := mediaDetailsReaderFromCommitter(&fakeCommitter{db: wired}); got == nil {
		t.Fatal("a committer carrying an engine handle must yield a wired media details reader")
	}
}

// TestMediaDetailsReaderFromCommitter_NoEngineYieldsNothing pins the
// fail-closed boundary: every shape that cannot name an engine produces NO
// reader, so the capability-side guard errors instead of silently reading a
// different database.
func TestMediaDetailsReaderFromCommitter_NoEngineYieldsNothing(t *testing.T) {
	cases := map[string]persistence.AssetCommitter{
		"nil committer":             nil,
		"committer without DB()":    &fakeCommitterWithoutDB{},
		"committer with nil handle": &fakeCommitter{db: nil},
	}
	for name, committer := range cases {
		if got := mediaDetailsReaderFromCommitter(committer); got != nil {
			t.Errorf("%s: expected no reader, got %T", name, got)
		}
	}
}
