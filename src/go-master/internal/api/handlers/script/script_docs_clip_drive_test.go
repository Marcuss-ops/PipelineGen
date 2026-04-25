package script

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"velox/go-master/internal/ml/ollama"
)

func TestBuildClipDriveMatchingSection_MatchesRelevantClip(t *testing.T) {
	dataDir := t.TempDir()
	clipTextDir := t.TempDir()

	indexJSON := `{
		"clips": [
			{
				"id": "irrelevant-1",
				"name": "Nature walking montage",
				"filename": "nature_walking_montage.mp4",
				"folder_id": "folder-1",
				"folder_path": "Clips/Nature",
				"group": "nature",
				"media_type": "clip",
				"drive_link": "https://drive.google.com/file/d/irrelevant-1/view",
				"download_link": "https://drive.google.com/uc?export=download&id=irrelevant-1",
				"tags": ["forest", "birds"]
			},
			{
				"id": "boxing-clip-1",
				"name": "Andrew Tate boxing champion intro",
				"filename": "andrew_tate_boxing_champion_intro.mp4",
				"folder_id": "folder-2",
				"folder_path": "Clips/Boxe/AndrewTate",
				"group": "boxe",
				"media_type": "clip",
				"drive_link": "https://drive.google.com/file/d/boxing-clip-1/view",
				"download_link": "https://drive.google.com/uc?export=download&id=boxing-clip-1",
				"tags": ["andrew", "tate", "boxing", "champion", "winning"]
			}
		]
	}`

	if err := os.WriteFile(filepath.Join(dataDir, "clip_index.json"), []byte(indexJSON), 0644); err != nil {
		t.Fatalf("write clip index: %v", err)
	}

	sidecarPath := filepath.Join(clipTextDir, "Andrew Tate boxing champion intro.txt")
	sidecarText := "Andrew Tate boxing champion intro sidecar text describing training, winning, and discipline."
	if err := os.WriteFile(sidecarPath, []byte(sidecarText), 0644); err != nil {
		t.Fatalf("write sidecar text: %v", err)
	}

	req := ScriptDocsRequest{
		Topic:    "Andrew Tate boxing champion",
		Duration: 60,
		Language: "english",
		Template: "documentary",
	}
	narrative := "This narrative is only a fallback and should not be used if the analysis already provides phrases."
	analysis := &ollama.FullEntityAnalysis{
		TotalSegments: 1,
		SegmentEntities: []ollama.SegmentEntities{
			{
				SegmentIndex:    0,
				FrasiImportanti: []string{"Andrew Tate boxing champion intro about winning, training, and discipline."},
			},
		},
	}

	section := buildClipDriveMatchingSection(context.Background(), nil, req, narrative, analysis, dataDir, clipTextDir)

	if section.Title != "🎞️ Clip Drive Matching" {
		t.Fatalf("section title = %q, want %q", section.Title, "🎞️ Clip Drive Matching")
	}
	if section.Body == "None" || strings.TrimSpace(section.Body) == "" {
		t.Fatal("expected a non-empty clip drive match section")
	}
	if !strings.Contains(section.Body, "boxing-clip-1") {
		t.Fatalf("section body does not contain expected clip id: %s", section.Body)
	}
	if !strings.Contains(section.Body, "Andrew Tate boxing champion intro") {
		t.Fatalf("section body does not contain expected clip title: %s", section.Body)
	}
	if !strings.Contains(section.Body, "https://drive.google.com/file/d/boxing-clip-1/view") {
		t.Fatalf("section body does not contain expected drive link: %s", section.Body)
	}
}

func TestRankClipDriveCandidates_PrefersRelevantClip(t *testing.T) {
	clips := []clipDriveRecord{
		{
			ID:         "clip-a",
			Name:       "Nature walking montage",
			Filename:   "nature_walking_montage.mp4",
			FolderPath: "Clips/Nature",
			Group:      "nature",
			MediaType:  "clip",
			DriveLink:  "https://drive.google.com/file/d/clip-a/view",
			Tags:       []string{"forest", "birds"},
		},
		{
			ID:         "clip-b",
			Name:       "Andrew Tate boxing champion intro",
			Filename:   "andrew_tate_boxing_champion_intro.mp4",
			FolderPath: "Clips/Boxe/AndrewTate",
			Group:      "boxe",
			MediaType:  "clip",
			DriveLink:  "https://drive.google.com/file/d/clip-b/view",
			Tags:       []string{"andrew", "tate", "boxing", "champion", "winning"},
		},
	}

	candidates := rankClipDriveCandidates("Andrew Tate boxing champion speaks about winning", clips, nil, 3)
	if len(candidates) == 0 {
		t.Fatal("expected at least one ranked candidate")
	}
	if candidates[0].Record.ID != "clip-b" {
		t.Fatalf("top candidate id = %q, want %q", candidates[0].Record.ID, "clip-b")
	}
	if candidates[0].Score < 70 {
		t.Fatalf("top candidate score = %.1f, want >= 70", candidates[0].Score)
	}
}

func TestBuildClipDriveMatchingSection_DeduplicatesClipIDs(t *testing.T) {
	dataDir := t.TempDir()

	indexJSON := `{
		"clips": [
			{
				"id": "clip-1",
				"name": "Andrew Tate boxing champion intro",
				"filename": "andrew_tate_boxing_champion_intro.mp4",
				"folder_id": "folder-1",
				"folder_path": "Clips/Boxe/AndrewTate",
				"group": "boxe",
				"media_type": "clip",
				"drive_link": "https://drive.google.com/file/d/clip-1/view",
				"download_link": "https://drive.google.com/uc?export=download&id=clip-1",
				"tags": ["andrew", "tate", "boxing", "champion", "training"]
			}
		]
	}`

	if err := os.WriteFile(filepath.Join(dataDir, "clip_index.json"), []byte(indexJSON), 0644); err != nil {
		t.Fatalf("write clip index: %v", err)
	}

	req := ScriptDocsRequest{
		Topic:    "Andrew Tate boxing champion",
		Duration: 60,
		Language: "english",
		Template: "documentary",
	}
	analysis := &ollama.FullEntityAnalysis{
		TotalSegments: 1,
		SegmentEntities: []ollama.SegmentEntities{
			{
				SegmentIndex: 0,
				FrasiImportanti: []string{
					"Andrew Tate boxing champion intro about training and winning.",
					"Andrew Tate boxing champion intro about discipline and training.",
				},
			},
		},
	}

	section := buildClipDriveMatchingSection(context.Background(), nil, req, "", analysis, dataDir, "")
	if strings.Count(section.Body, "clip_id: clip-1") != 1 {
		t.Fatalf("expected clip-1 once in match block, got body: %s", section.Body)
	}
}

func TestRenderTimelineMatches_ShowsTagSuggestions(t *testing.T) {
	body := renderTimelineMatches("🎞️ CLIP ARTLIST", []scoredMatch{
		{
			Title:   "training regime",
			Source:  "artlist_suggestion",
			Score:   50,
			Details: "boxing, training, discipline",
		},
	})

	if !strings.Contains(body, "Tag suggeriti") {
		t.Fatalf("expected suggested tags in body: %s", body)
	}
	if !strings.Contains(body, "boxing, training, discipline") {
		t.Fatalf("expected tag list in body: %s", body)
	}
}
