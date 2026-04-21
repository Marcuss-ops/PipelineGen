package scriptdocs

import (
	"context"
	"path/filepath"
	"testing"

	"velox/go-master/internal/imagesdb"
)

type fakeImageFinder struct {
	results map[string]string
}

func (f fakeImageFinder) Find(entity string) string {
	if f.results == nil {
		return ""
	}
	if url, ok := f.results[entity]; ok {
		return url
	}
	return ""
}

func TestValidateRequestImagesFull(t *testing.T) {
	req := ScriptDocRequest{
		Topic:           "Canada mountains",
		AssociationMode: AssociationModeImagesFull,
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if req.AssociationMode != AssociationModeImagesFull {
		t.Fatalf("Validate() normalized mode = %q, want %q", req.AssociationMode, AssociationModeImagesFull)
	}
}

func TestBuildImagesFullAssociations(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := imagesdb.Open(filepath.Join(tmpDir, "images.sqlite"))
	if err != nil {
		t.Fatalf("imagesdb.Open() error = %v", err)
	}
	defer db.Close()

	svc := &ScriptDocService{
		imagesDB: db,
		imageFinder: fakeImageFinder{
			results: map[string]string{
				"Canada":           "https://example.com/canada.jpg",
				"mountains":        "https://example.com/mountains.jpg",
				"Canada mountains": "https://example.com/canada-mountains.jpg",
			},
		},
	}

	chapters := []ScriptChapter{
		{
			Index:            0,
			Title:            "Opening",
			StartTime:        0,
			EndTime:          30,
			DominantEntities: []string{"Canada", "mountains"},
			SourceText:       "The mountains of Canada are wide and cold.",
		},
	}

	assocs := svc.buildImagesFullAssociations(context.Background(), "Canada mountains", chapters, map[string]string{
		"Canada":    "https://example.com/canada.jpg",
		"mountains": "https://example.com/mountains.jpg",
	})
	if len(assocs) != 1 {
		t.Fatalf("buildImagesFullAssociations() len = %d, want 1", len(assocs))
	}
	if assocs[0].ImageURL != "https://example.com/canada-mountains.jpg" {
		t.Fatalf("buildImagesFullAssociations() image_url = %q, want %q", assocs[0].ImageURL, "https://example.com/canada-mountains.jpg")
	}
	if assocs[0].ChapterIndex != 1 {
		t.Fatalf("buildImagesFullAssociations() chapter_index = %d, want 1", assocs[0].ChapterIndex)
	}
	if assocs[0].Entity == "" {
		t.Fatalf("buildImagesFullAssociations() entity is empty")
	}
}
