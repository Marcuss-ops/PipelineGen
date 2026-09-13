package deletion

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// fakeMediaLookup is a recording MediaAssetLookupPort. The deletion tests use
// it to prove the entry point reads the media SSOT port instead of the SQLite
// mirror (no SQLite fixture is created in these tests).
type fakeMediaLookup struct {
	result *asset.Asset
	err    error
	gotIDs []string
}

func (f *fakeMediaLookup) GetClip(_ context.Context, id string) (*asset.Asset, error) {
	f.gotIDs = append(f.gotIDs, id)
	return f.result, f.err
}

// fakeMediaDriveLookup is a recording MediaAssetDriveLookupPort.
type fakeMediaDriveLookup struct {
	result   *asset.Asset
	err      error
	gotFiles []string
}

func (f *fakeMediaDriveLookup) GetClipByDriveFileID(_ context.Context, fileID string) (*asset.Asset, error) {
	f.gotFiles = append(f.gotFiles, fileID)
	return f.result, f.err
}

// TestDeleteAsset_ResolvesFromMediaSSOTPort pins the read-side cutover: with
// the PostgreSQL lookup port wired, DeleteAsset resolves the asset there and
// never touches the SQLite ClipsRepository (deliberately nil here — a SQLite
// read would fail with ErrAssetRepositoryUnavailable).
func TestDeleteAsset_ResolvesFromMediaSSOTPort(t *testing.T) {
	lookup := &fakeMediaLookup{result: &asset.Asset{ID: "asset-pg", Source: "clip"}}
	dispatcher := &recordingDispatcher{}
	svc := NewDeletionService(DeletionServiceDeps{
		Lookup:     DeletionLookupDeps{ByID: lookup},
		Dispatcher: dispatcher,
		Log:        zap.NewNop(),
	})

	if err := svc.DeleteAsset(context.Background(), "asset-pg", true); err != nil {
		t.Fatalf("DeleteAsset via media SSOT port: %v", err)
	}
	if len(lookup.gotIDs) != 1 || lookup.gotIDs[0] != "asset-pg" {
		t.Fatalf("media SSOT lookup ids: want [asset-pg], got %v", lookup.gotIDs)
	}
	if len(dispatcher.calls) != 1 {
		t.Fatalf("EnqueueDriveDelete must be called exactly once; got %d", len(dispatcher.calls))
	}
	if dispatcher.calls[0].assetID != "asset-pg" || !dispatcher.calls[0].permanently {
		t.Errorf("dispatcher call: want (asset-pg, true), got (%q, %v)",
			dispatcher.calls[0].assetID, dispatcher.calls[0].permanently)
	}
}

// TestDeleteAsset_MediaSSOTNotFound pins that a miss on the authoritative plane
// is terminal and never reaches the dispatcher.
func TestDeleteAsset_MediaSSOTNotFound(t *testing.T) {
	lookup := &fakeMediaLookup{result: nil}
	dispatcher := &recordingDispatcher{}
	svc := NewDeletionService(DeletionServiceDeps{
		Lookup:     DeletionLookupDeps{ByID: lookup},
		Dispatcher: dispatcher,
		Log:        zap.NewNop(),
	})

	err := svc.DeleteAsset(context.Background(), "asset-absent", false)
	if !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("want ErrAssetNotFound; got %v", err)
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("dispatcher must not be called for a missing asset; got %d calls", len(dispatcher.calls))
	}
}

// TestDeleteAsset_MediaSSOTLookupErrorPropagates pins fail-closed semantics on
// a transport/lookup error: it is wrapped, not converted into not-found.
func TestDeleteAsset_MediaSSOTLookupErrorPropagates(t *testing.T) {
	boom := errors.New("postgres unavailable")
	lookup := &fakeMediaLookup{err: boom}
	svc := NewDeletionService(DeletionServiceDeps{
		Lookup:     DeletionLookupDeps{ByID: lookup},
		Dispatcher: &recordingDispatcher{},
		Log:        zap.NewNop(),
	})

	err := svc.DeleteAsset(context.Background(), "asset-pg", false)
	if !errors.Is(err, boom) {
		t.Fatalf("lookup error must propagate; got %v", err)
	}
	if errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("a lookup error must not be reported as not-found; got %v", err)
	}
}

// TestDeleteAsset_MissingDispatcherFailsAfterMediaSSOTLookup pins the
// fail-closed ordering on the PostgreSQL read path: the asset is resolved from
// the media SSOT first, then the nil dispatcher aborts before any mutation.
func TestDeleteAsset_MissingDispatcherFailsAfterMediaSSOTLookup(t *testing.T) {
	lookup := &fakeMediaLookup{result: &asset.Asset{ID: "asset-pg", Source: "clip"}}
	svc := NewDeletionService(DeletionServiceDeps{
		Lookup: DeletionLookupDeps{ByID: lookup},
		Log:    zap.NewNop(),
	})

	err := svc.DeleteAsset(context.Background(), "asset-pg", false)
	if !errors.Is(err, ErrDeletionDispatcherUnavailable) {
		t.Fatalf("want ErrDeletionDispatcherUnavailable; got %v", err)
	}
	if len(lookup.gotIDs) != 1 {
		t.Fatalf("the asset must be resolved before the dispatcher guard; lookup calls=%v", lookup.gotIDs)
	}
}

// TestDeleteAsset_NilMediaSSOTFallsBackToSQLite pins that the port is optional:
// non-PostgreSQL deployments keep the legacy ClipsRepository lookup.
func TestDeleteAsset_NilMediaSSOTFallsBackToSQLite(t *testing.T) {
	db := memoryDB(t)
	minimalMediaAssetsFixture(t, db)
	seedAssetRowWithSource(t, db, "asset-sqlite", "clip")
	dispatcher := &recordingDispatcher{}
	svc := newTestService(t, db, dispatcher)

	if err := svc.DeleteAsset(context.Background(), "asset-sqlite", false); err != nil {
		t.Fatalf("legacy fallback DeleteAsset: %v", err)
	}
	if len(dispatcher.calls) != 1 {
		t.Fatalf("dispatcher must be called once on the fallback path; got %d", len(dispatcher.calls))
	}
}

// TestFindClipByDriveFileID_ResolvesFromMediaSSOTPort pins the drive-file read
// cutover: with the PostgreSQL drive-file port wired, DeleteByDriveFile resolves
// the asset there (the SourceCatalog is deliberately nil) and dispatches by the
// resolved asset id.
func TestFindClipByDriveFileID_ResolvesFromMediaSSOTPort(t *testing.T) {
	driveLookup := &fakeMediaDriveLookup{result: &asset.Asset{ID: "asset-drive", Source: "clip"}}
	dispatcher := &recordingDispatcher{}
	// Production PostgreSQL wiring supplies BOTH read ports: the drive-file
	// lookup resolves the id, then DeleteAsset re-reads it by id.
	svc := NewDeletionService(DeletionServiceDeps{
		Lookup: DeletionLookupDeps{
			ByID:          &fakeMediaLookup{result: &asset.Asset{ID: "asset-drive", Source: "clip"}},
			ByDriveFileID: driveLookup,
		},
		Dispatcher: dispatcher,
		Log:        zap.NewNop(),
	})

	if err := svc.DeleteByDriveFile(context.Background(), "1AbCdEf", "all", true); err != nil {
		t.Fatalf("DeleteByDriveFile via media SSOT port: %v", err)
	}
	if len(driveLookup.gotFiles) != 1 || driveLookup.gotFiles[0] != "1AbCdEf" {
		t.Fatalf("drive-file lookup ids: want [1AbCdEf], got %v", driveLookup.gotFiles)
	}
	if len(dispatcher.calls) != 1 || dispatcher.calls[0].assetID != "asset-drive" {
		t.Fatalf("dispatcher must receive the resolved asset id; got %+v", dispatcher.calls)
	}
}

// TestFindClipByDriveFileID_MediaSSOTMissReturnsNil pins the nil-on-not-found
// parity with the SQLite reader: an unknown Drive id is not an error.
func TestFindClipByDriveFileID_MediaSSOTMissReturnsNil(t *testing.T) {
	driveLookup := &fakeMediaDriveLookup{result: nil}
	svc := NewDeletionService(DeletionServiceDeps{
		Lookup:     DeletionLookupDeps{ByDriveFileID: driveLookup},
		Dispatcher: &recordingDispatcher{},
		Log:        zap.NewNop(),
	})

	clip, source, err := svc.FindClipByDriveFileID(context.Background(), "missing", "all")
	if err != nil {
		t.Fatalf("a miss must not be an error; got %v", err)
	}
	if clip != nil || source != "" {
		t.Fatalf("want (nil, \"\"); got (%v, %q)", clip, source)
	}
}
