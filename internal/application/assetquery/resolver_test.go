package assetquery

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

func TestResolver_EmptyID(t *testing.T) {
	r := NewResolver(&mockLocationRepo{})
	_, err := r.ResolveReadable(context.Background(), "", nil)
	if !errors.Is(err, assets.ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
}

func TestResolver_NoLocations(t *testing.T) {
	r := NewResolver(&mockLocationRepo{locs: nil})
	_, err := r.ResolveReadable(context.Background(), "abc", nil)
	if !errors.Is(err, ErrNoReadableLocation) {
		t.Fatalf("expected ErrNoReadableLocation, got %v", err)
	}
}

func TestResolver_ListError(t *testing.T) {
	boom := errors.New("boom")
	r := NewResolver(&mockLocationRepo{err: boom})
	_, err := r.ResolveReadable(context.Background(), "abc", nil)
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped boom, got %v", err)
	}
}

func TestResolver_PrefersLocalOverDrive(t *testing.T) {
	locs := []*assets.Location{
		{AssetID: "abc", LocationKind: assets.LocationKindDrive, IsPrimary: true},
		{AssetID: "abc", LocationKind: assets.LocationKindLocal, IsPrimary: true},
	}
	r := NewResolver(&mockLocationRepo{locs: locs})
	got, err := r.ResolveReadable(context.Background(), "abc",
		[]assets.LocationKind{assets.LocationKindLocal, assets.LocationKindDrive})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LocationKind != assets.LocationKindLocal || !got.IsPrimary {
		t.Fatalf("expected primary local, got %+v", got)
	}
}

func TestResolver_PrefersPrimaryThenFallsBackToNonPrimary(t *testing.T) {
	locs := []*assets.Location{
		{AssetID: "abc", LocationKind: assets.LocationKindLocal}, // not primary
	}
	r := NewResolver(&mockLocationRepo{locs: locs})
	got, err := r.ResolveReadable(context.Background(), "abc",
		[]assets.LocationKind{assets.LocationKindLocal})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.LocationKind != assets.LocationKindLocal {
		t.Fatalf("expected non-primary local, got %+v", got)
	}
}

func TestResolver_FallbackPrimaryWhenNoPreferenceMatches(t *testing.T) {
	locs := []*assets.Location{
		{AssetID: "abc", LocationKind: assets.LocationKindDrive, IsPrimary: true},
	}
	r := NewResolver(&mockLocationRepo{locs: locs})
	got, err := r.ResolveReadable(context.Background(), "abc",
		[]assets.LocationKind{assets.LocationKindLocal})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LocationKind != assets.LocationKindDrive || !got.IsPrimary {
		t.Fatalf("expected fallback to primary drive, got %+v", got)
	}
}

func TestResolver_FallbackToFirstNonNil(t *testing.T) {
	locs := []*assets.Location{
		nil,
		{AssetID: "abc", LocationKind: assets.LocationKindObjectStorage},
	}
	r := NewResolver(&mockLocationRepo{locs: locs})
	got, err := r.ResolveReadable(context.Background(), "abc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.LocationKind != assets.LocationKindObjectStorage {
		t.Fatalf("expected fallback to first non-nil, got %+v", got)
	}
}

func TestResolver_NilReceiver(t *testing.T) {
	var r *Resolver
	_, err := r.ResolveReadable(context.Background(), "abc", nil)
	if err == nil {
		t.Fatalf("expected error from nil receiver")
	}
}

func TestResolver_NilLocRepo(t *testing.T) {
	r := NewResolver(nil)
	_, err := r.ResolveReadable(context.Background(), "abc", nil)
	if err == nil {
		t.Fatalf("expected error from nil locRepo")
	}
}
