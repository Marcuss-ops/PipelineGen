package wiring

import (
	"context"
	"errors"
	"testing"

	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
)

type stubImageListRepository struct {
	rows []imagesrouting.ImageSearchResult
	err  error
}

func (s stubImageListRepository) ListImages(context.Context, imagesrouting.ImageFilter) ([]imagesrouting.ImageSearchResult, error) {
	return s.rows, s.err
}

func (s stubImageListRepository) ListImagesByAssetIDs(context.Context, []string) ([]imagesrouting.ImageSearchResult, error) {
	return s.rows, s.err
}

// TestCompositeImageListRepositoryPrefersCanonicalRows pins the chain
// precedence: the PostgreSQL SSOT answers first, a legacy SQLite row with the
// same asset id cannot duplicate it, and a failing secondary repository does
// not discard the rows the canonical one returned.
func TestCompositeImageListRepositoryPrefersCanonicalRows(t *testing.T) {
	pg := stubImageListRepository{rows: []imagesrouting.ImageSearchResult{
		{AssetID: "entity-image-1", DriveLink: "https://drive.example/1"},
		{AssetID: "entity-image-2", DriveLink: "https://drive.example/2"},
	}}
	legacy := stubImageListRepository{rows: []imagesrouting.ImageSearchResult{
		{AssetID: "entity-image-1", DriveLink: "https://legacy.example/1"},
		{AssetID: "legacy-3", DriveLink: "https://legacy.example/3"},
	}}
	chain := &compositeImageListRepository{repos: []imagesrouting.ImageListRepository{pg, legacy}}
	rows, err := chain.ListImages(context.Background(), imagesrouting.ImageFilter{SubjectID: "corrado-augias", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"entity-image-1", "entity-image-2", "legacy-3"}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %v", rows, want)
	}
	for i, id := range want {
		if rows[i].AssetID != id {
			t.Fatalf("row %d = %q, want %q (canonical first, deduplicated)", i, rows[i].AssetID, id)
		}
	}
	if rows[0].DriveLink != "https://drive.example/1" {
		t.Fatalf("duplicate kept the legacy row: %+v", rows[0])
	}

	broken := &compositeImageListRepository{repos: []imagesrouting.ImageListRepository{
		stubImageListRepository{rows: pg.rows},
		stubImageListRepository{err: errors.New("sqlite unavailable")},
	}}
	rows, err = broken.ListImages(context.Background(), imagesrouting.ImageFilter{SubjectID: "corrado-augias", Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, err = %v — a failing mirror must not erase canonical rows", rows, err)
	}
}

func TestCompositeImageListRepositoryHonoursLimit(t *testing.T) {
	chain := &compositeImageListRepository{repos: []imagesrouting.ImageListRepository{
		stubImageListRepository{rows: []imagesrouting.ImageSearchResult{{AssetID: "a"}, {AssetID: "b"}}},
		stubImageListRepository{rows: []imagesrouting.ImageSearchResult{{AssetID: "c"}}},
	}}
	rows, err := chain.ListImages(context.Background(), imagesrouting.ImageFilter{SubjectID: "x", Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].AssetID != "a" {
		t.Fatalf("rows = %+v, err = %v, want the first canonical row only", rows, err)
	}
}
