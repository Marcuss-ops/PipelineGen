package assetquery

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

// mockAssetRepo is a hand-rolled fake of asset.Repository.
type mockAssetRepo struct {
	a   *asset.MediaAsset
	err error
}

func (m *mockAssetRepo) Upsert(context.Context, *asset.MediaAsset) error  { return nil }
func (m *mockAssetRepo) Get(context.Context, string) (*asset.MediaAsset, error) {
	return m.a, m.err
}
func (m *mockAssetRepo) List(context.Context, asset.Filter) ([]*asset.MediaAsset, error) {
	return nil, nil
}
func (m *mockAssetRepo) Count(context.Context, asset.Filter) (int64, error) { return 0, nil }
func (m *mockAssetRepo) SoftDelete(context.Context, string) error         { return nil }
func (m *mockAssetRepo) Restore(context.Context, string) error            { return nil }
func (m *mockAssetRepo) HardDelete(context.Context, string) error         { return nil }

// mockLocationRepo is a hand-rolled fake of asset.LocationRepository.
type mockLocationRepo struct {
	locs []*asset.Location
	err  error
}

func (m *mockLocationRepo) Upsert(context.Context, *asset.Location) error              { return nil }
func (m *mockLocationRepo) GetPrimary(context.Context, string) (*asset.Location, error) {
	return nil, nil
}
func (m *mockLocationRepo) ListByAsset(context.Context, string) ([]*asset.Location, error) {
	return m.locs, m.err
}
func (m *mockLocationRepo) SetPrimary(context.Context, string, asset.LocationKind) error { return nil }
func (m *mockLocationRepo) Delete(context.Context, string, asset.LocationKind) error     { return nil }
func (m *mockLocationRepo) DeleteAll(context.Context, string) error                     { return nil }

// mockProcessingRepo is a hand-rolled fake of asset.ProcessingRepository.
type mockProcessingRepo struct {
	recs []asset.ProcessingRecord
	err  error
}

func (m *mockProcessingRepo) Start(context.Context, string, string) error    { return nil }
func (m *mockProcessingRepo) Complete(context.Context, string, string) error { return nil }
func (m *mockProcessingRepo) Fail(context.Context, string, string, string) error {
	return nil
}
func (m *mockProcessingRepo) Transition(context.Context, string, string, asset.ProcessingStatus, asset.ProcessingStatus) error {
	return nil
}
func (m *mockProcessingRepo) Get(context.Context, string, string) (*asset.ProcessingRecord, error) {
	return nil, nil
}
func (m *mockProcessingRepo) GetByAssetID(context.Context, string) ([]asset.ProcessingRecord, error) {
	return m.recs, m.err
}
func (m *mockProcessingRepo) GetFailed(context.Context) ([]asset.ProcessingRecord, error) {
	return nil, nil
}
func (m *mockProcessingRepo) Delete(context.Context, string, string) error { return nil }
func (m *mockProcessingRepo) DeleteAll(context.Context, string) error     { return nil }

// mockVersionRepo is a hand-rolled fake of asset.VersionRepository.
type mockVersionRepo struct {
	ver *asset.Version
	err error
}

func (m *mockVersionRepo) GetCurrent(context.Context, string) (*asset.Version, error) {
	return m.ver, m.err
}
func (m *mockVersionRepo) List(context.Context, string) ([]asset.Version, error) {
	return nil, nil
}
func (m *mockVersionRepo) Append(context.Context, *asset.Version) error { return nil }

// Compile-time interface checks so any drift in the canonical contracts
// fails the build rather than the test.
var (
	_ asset.Repository           = (*mockAssetRepo)(nil)
	_ asset.LocationRepository   = (*mockLocationRepo)(nil)
	_ asset.ProcessingRepository = (*mockProcessingRepo)(nil)
	_ asset.VersionRepository    = (*mockVersionRepo)(nil)
)

func TestServiceGet_HappyPath(t *testing.T) {
	a := &asset.MediaAsset{ID: "abc", Source: "youtube"}
	locs := []*asset.Location{
		{AssetID: "abc", LocationKind: asset.LocationKindLocal, IsPrimary: true},
	}
	recs := []asset.ProcessingRecord{
		{AssetID: "abc", Step: string(asset.StageDownload), Status: asset.StatusCompleted},
	}
	ver := &asset.Version{AssetID: "abc", VersionNumber: 1, FileHash: "h1"}

	s := New(
		&mockAssetRepo{a: a},
		&mockLocationRepo{locs: locs},
		&mockProcessingRepo{recs: recs},
		&mockVersionRepo{ver: ver},
	)

	got, err := s.Get(context.Background(), "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Asset != a {
		t.Fatalf("Asset mismatch")
	}
	if len(got.Locations) != 1 || got.Locations[0].LocationKind != asset.LocationKindLocal {
		t.Fatalf("Locations mismatch: %+v", got.Locations)
	}
	if len(got.Processing) != 1 || got.Processing[0].Step != string(asset.StageDownload) {
		t.Fatalf("Processing mismatch: %+v", got.Processing)
	}
	if got.CurrentVersion == nil || got.CurrentVersion.FileHash != "h1" {
		t.Fatalf("CurrentVersion mismatch: %+v", got.CurrentVersion)
	}
}

func TestServiceGet_EmptyID(t *testing.T) {
	s := New(&mockAssetRepo{}, &mockLocationRepo{}, &mockProcessingRepo{}, &mockVersionRepo{})
	_, err := s.Get(context.Background(), "")
	if !errors.Is(err, asset.ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
}

func TestServiceGet_NotFound(t *testing.T) {
	s := New(&mockAssetRepo{a: nil}, &mockLocationRepo{}, &mockProcessingRepo{}, &mockVersionRepo{})
	_, err := s.Get(context.Background(), "nope")
	if !errors.Is(err, asset.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestServiceGet_SoftDeleted(t *testing.T) {
	s := New(
		&mockAssetRepo{err: asset.ErrSoftDeleted},
		&mockLocationRepo{},
		&mockProcessingRepo{},
		&mockVersionRepo{},
	)
	_, err := s.Get(context.Background(), "abc")
	if !errors.Is(err, asset.ErrSoftDeleted) {
		t.Fatalf("expected ErrSoftDeleted to bubble up, got %v", err)
	}
}

func TestServiceGet_AssetRepoError(t *testing.T) {
	boom := errors.New("boom")
	s := New(
		&mockAssetRepo{err: boom},
		&mockLocationRepo{},
		&mockProcessingRepo{},
		&mockVersionRepo{},
	)
	_, err := s.Get(context.Background(), "abc")
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped boom, got %v", err)
	}
}

func TestServiceGet_LocationsError(t *testing.T) {
	boom := errors.New("boom-loc")
	s := New(
		&mockAssetRepo{a: &asset.MediaAsset{ID: "abc"}},
		&mockLocationRepo{err: boom},
		&mockProcessingRepo{},
		&mockVersionRepo{},
	)
	_, err := s.Get(context.Background(), "abc")
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped boom-loc, got %v", err)
	}
}

func TestServiceGet_ProcessingError(t *testing.T) {
	boom := errors.New("boom-proc")
	s := New(
		&mockAssetRepo{a: &asset.MediaAsset{ID: "abc"}},
		&mockLocationRepo{},
		&mockProcessingRepo{err: boom},
		&mockVersionRepo{},
	)
	_, err := s.Get(context.Background(), "abc")
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped boom-proc, got %v", err)
	}
}

func TestServiceGet_VersionError(t *testing.T) {
	boom := errors.New("boom-ver")
	s := New(
		&mockAssetRepo{a: &asset.MediaAsset{ID: "abc"}},
		&mockLocationRepo{},
		&mockProcessingRepo{},
		&mockVersionRepo{err: boom},
	)
	_, err := s.Get(context.Background(), "abc")
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped boom-ver, got %v", err)
	}
}

func TestServiceGet_MissingDependency(t *testing.T) {
	// nil versions repo
	s := New(&mockAssetRepo{a: &asset.MediaAsset{ID: "abc"}}, &mockLocationRepo{}, &mockProcessingRepo{}, nil)
	_, err := s.Get(context.Background(), "abc")
	if err == nil {
		t.Fatalf("expected error from nil versions repo")
	}
}
