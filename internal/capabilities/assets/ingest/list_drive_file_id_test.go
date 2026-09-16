package ingest

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestClipStoreListWithDriveFileID_FailsClosedWithoutLister pins MEDIA-SSOT
// P2-9 Phase 2 at the ingest boundary.
//
// ListWithDriveFileID used to run `SELECT id FROM media_assets WHERE
// drive_file_id ...` against the operational SQLite handle. PostgreSQL is the
// media SSOT and the operational mirror holds no committed media rows, so that
// statement returned an empty set and the Drive sweep could not see any
// committed asset. Reporting "no assets have a Drive file id" for an unreadable
// catalog is a successful no-op, so a media-plane-closed boot MUST fail closed:
// a caller that cannot tell "empty" from "closed" would treat every asset as
// already-swept.
func TestClipStoreListWithDriveFileID_FailsClosedWithoutLister(t *testing.T) {
	adapter := NewClipStoreAdapter(nil, nil, nil, nil, nil, nil, nil)
	recs, err := adapter.ListWithDriveFileID(context.Background(), "")
	if err == nil {
		t.Fatal("expected a fail-closed error when no media drive-file-id lister is wired")
	}
	if recs != nil {
		t.Fatalf("records = %+v, want nil alongside the error", recs)
	}
}

// TestClipStoreListWithDriveFileID_PropagatesListerError pins that a failing
// media-SSOT read surfaces as an error and is never swallowed into an empty
// result. The retired raw-SQL version returned `err` directly; the migration
// must keep that property while changing the engine.
func TestClipStoreListWithDriveFileID_PropagatesListerError(t *testing.T) {
	cause := errors.New("media SSOT unavailable")
	adapter := NewClipStoreAdapter(nil, nil, &stubDetailsReader{}, nil, nil,
		processingTestDispatcher{}, &stubDriveFileLister{err: cause})

	recs, err := adapter.ListWithDriveFileID(context.Background(), "")
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want wrapped %v", err, cause)
	}
	if recs != nil {
		t.Fatalf("records = %+v, want nil alongside the error", recs)
	}
}

// TestClipStoreListWithDriveFileID_UsesMediaListerAndFiltersSource pins the
// positive path: the ids come from the media-SSOT port (the operational `db`
// field is nil here, so a raw-SQL read is impossible), hydration goes through
// the narrow AssetDetailsReader, and the source filter still applies.
func TestClipStoreListWithDriveFileID_UsesMediaListerAndFiltersSource(t *testing.T) {
	details := &recordingDetailsReader{byID: map[string]*asset.Details{
		"keep": {Asset: &asset.Asset{ID: "keep", Source: asset.Source("youtube")}},
		"drop": {Asset: &asset.Asset{ID: "drop", Source: asset.Source("artlist")}},
	}}
	lister := &stubDriveFileLister{ids: []string{"keep", "drop"}}

	adapter := NewClipStoreAdapter(nil, nil, details, nil, nil,
		processingTestDispatcher{}, lister)

	recs, err := adapter.ListWithDriveFileID(context.Background(), "YouTube")
	if err != nil {
		t.Fatalf("ListWithDriveFileID: %v", err)
	}
	if !lister.called {
		t.Fatal("expected the media drive-file-id lister to be consulted")
	}
	if len(recs) != 1 {
		t.Fatalf("len(records) = %d, want 1 (source filter is case-insensitive)", len(recs))
	}
	if recs[0].ID != "keep" {
		t.Fatalf("record id = %q, want %q", recs[0].ID, "keep")
	}
}

type stubDriveFileLister struct {
	ids    []string
	err    error
	called bool
}

func (s *stubDriveFileLister) ListAssetIDsWithDriveFileID(context.Context) ([]string, error) {
	s.called = true
	if s.err != nil {
		return nil, s.err
	}
	return s.ids, nil
}

// Ensure the stub is a faithful stand-in for the production port.
var _ MediaDriveFileIDLister = (*stubDriveFileLister)(nil)

type recordingDetailsReader struct {
	byID map[string]*asset.Details
}

func (r *recordingDetailsReader) Get(_ context.Context, id string) (*asset.Details, error) {
	d, ok := r.byID[id]
	if !ok {
		return nil, asset.ErrNotFound
	}
	return d, nil
}

var _ AssetDetailsReader = (*recordingDetailsReader)(nil)
