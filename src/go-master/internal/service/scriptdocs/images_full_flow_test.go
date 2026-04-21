package scriptdocs

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"velox/go-master/internal/imagesasset"
	"velox/go-master/internal/imagesdb"
)

func TestImagesFullRankingDownloadAndPlanExport(t *testing.T) {
	var img bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	src.Set(0, 0, color.RGBA{R: 0, G: 128, B: 255, A: 255})
	if err := png.Encode(&img, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}

	hits := 0
	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			hits++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/png"}},
				Body:       io.NopCloser(bytes.NewReader(img.Bytes())),
				Request:    req,
			}, nil
		}),
	}
	baseURL := "https://example.com"

	tmpDir := t.TempDir()
	db, err := imagesdb.Open(filepath.Join(tmpDir, "images.sqlite"))
	if err != nil {
		t.Fatalf("imagesdb.Open() error = %v", err)
	}
	defer db.Close()

	svc := &ScriptDocService{
		imagesDB: db,
		imageFinder: fakeImageFinder{results: map[string]string{
			"Canada":           baseURL + "/canada.png",
			"mountains":        baseURL + "/mountains.png",
			"Canada mountains": baseURL + "/exact.png",
		}},
		imageDownloader: imagesasset.NewWithClient(filepath.Join(tmpDir, "assets"), client),
	}

	probeRec, probeCached, probeErr := svc.resolveImageForEntity(context.Background(), "Canada mountains", "Canada mountains", "Canada mountains", ScriptChapter{
		Index:      0,
		Title:      "Canadian Rockies",
		StartTime:  0,
		EndTime:    30,
		Confidence: 0.91,
		SourceText: "The mountains of Canada are wide, cold and cinematic.",
	}, 1)
	if probeErr != nil {
		t.Fatalf("resolveImageForEntity() error = %v", probeErr)
	}
	if probeRec == nil || probeRec.ImageURL == "" {
		t.Fatalf("resolveImageForEntity() returned empty record")
	}
	if probeCached {
		t.Fatalf("first probe should not be cached")
	}

	chapters := []ScriptChapter{
		{
			Index:            0,
			Title:            "Canadian Rockies",
			StartTime:        0,
			EndTime:          30,
			Confidence:       0.91,
			DominantEntities: []string{"Canada", "mountains"},
			SourceText:       "The mountains of Canada are wide, cold and cinematic.",
		},
	}

	assocs := svc.buildImagesFullAssociations(context.Background(), "Canada mountains", chapters, map[string]string{
		"Canada":           baseURL + "/canada.png",
		"mountains":        baseURL + "/mountains.png",
		"Canada mountains": baseURL + "/exact.png",
	})
	if len(assocs) != 1 {
		t.Fatalf("buildImagesFullAssociations() len = %d, want 1", len(assocs))
	}
	if got := assocs[0].Entity; got != "Canada mountains" {
		t.Fatalf("selected entity = %q, want %q", got, "Canada mountains")
	}
	if assocs[0].LocalPath == "" {
		t.Fatalf("selected association has empty local path")
	}
	if assocs[0].DownloadedAt == "" {
		t.Fatalf("selected association has empty downloaded_at")
	}
	if hits != 1 {
		t.Fatalf("download hits = %d, want 1", hits)
	}

	records, err := db.ListAll()
	if err != nil {
		t.Fatalf("db.ListAll() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("db records = %d, want 1", len(records))
	}
	if records[0].LocalPath == "" || records[0].AssetHash == "" || records[0].FileSizeBytes == 0 {
		t.Fatalf("db record missing asset metadata: %+v", records[0])
	}
	if !strings.Contains(records[0].LocalPath, filepath.Base(tmpDir)) && !strings.Contains(records[0].LocalPath, "assets") {
		t.Fatalf("db record local path does not look cached: %q", records[0].LocalPath)
	}

	plan := svc.buildImagePlan("Canada mountains", 60, AssociationModeImagesFull, []LanguageResult{
		{
			Language:          "en",
			Chapters:          chapters,
			ImageAssociations: assocs,
		},
	})
	if plan == nil {
		t.Fatalf("buildImagePlan() returned nil")
	}
	if plan.TotalAssociations != 1 || plan.TotalDownloaded != 1 || plan.TotalCached != 1 {
		t.Fatalf("plan totals mismatch: %+v", plan)
	}

	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if !strings.Contains(string(data), "\"association_mode\": \"images_full\"") {
		t.Fatalf("plan json missing association mode: %s", string(data))
	}
	if !strings.Contains(string(data), "\"local_path\"") {
		t.Fatalf("plan json missing local_path metadata: %s", string(data))
	}

	path, err := saveImagePlanJSON("Canada mountains", plan)
	if err != nil {
		t.Fatalf("saveImagePlanJSON() error = %v", err)
	}
	if path == "" {
		t.Fatalf("saveImagePlanJSON() returned empty path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved plan file missing: %v", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
