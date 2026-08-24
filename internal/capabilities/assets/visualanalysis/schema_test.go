package assets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRejectsDialogueAndPreservesVisualOnlyContract(t *testing.T) {
	d := Document{SchemaVersion: SchemaVersion, Asset: AssetInput{ProposedAssetID: "ai-1", Source: "ai_generated", AssetRole: "stock", MediaType: "video", FolderPath: "Stock/AI/Ocean/Sharks", NormalizedGroup: "stock", DurationMs: 1000, Width: 720, Height: 1310, FPS: 30, HasDialogue: true}}
	if err := d.Validate(); err == nil {
		t.Fatal("expected dialogue rejection")
	}
}

func TestValidate_RequiresProposedAssetID(t *testing.T) {
	d := Document{
		SchemaVersion: SchemaVersion,
		Asset: AssetInput{
			Source:          "ai_generated",
			AssetRole:       "stock",
			MediaType:       "video",
			FolderPath:      "Stock/AI/Ocean/Sharks",
			NormalizedGroup: "stock",
			DurationMs:      1000,
			Width:           720,
			Height:          1310,
			FPS:             30,
		},
	}
	err := d.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "proposed_asset_id is required")
}

func TestValidate_AcceptsEventENInsteadOfActionEN(t *testing.T) {
	d := Document{
		SchemaVersion: SchemaVersion,
		Asset: AssetInput{
			ProposedAssetID: "ai-1",
			Source:          "ai_generated",
			AssetRole:       "stock",
			MediaType:       "video",
			FolderPath:      "Stock/AI/Ocean/Sharks",
			NormalizedGroup: "stock",
			DurationMs:      1000,
			Width:           720,
			Height:          1310,
			FPS:             30,
		},
		TimedEvents: []EventInput{
			{StartMs: 0, EndMs: 500, EventEN: "A hand appears", EventIT: "Una mano appare"},
		},
	}
	require.NoError(t, d.Validate())
	va := d.VisualAnalysis()
	require.Len(t, va.Events, 1)
	assert.Equal(t, "A hand appears", va.Events[0].Text)
}

func TestValidate_SequenceNoIsOptional(t *testing.T) {
	d := Document{
		SchemaVersion: SchemaVersion,
		Asset: AssetInput{
			ProposedAssetID: "ai-1",
			Source:          "ai_generated",
			AssetRole:       "stock",
			MediaType:       "video",
			FolderPath:      "Stock/AI/Ocean/Sharks",
			NormalizedGroup: "stock",
			DurationMs:      1000,
			Width:           720,
			Height:          1310,
			FPS:             30,
		},
		TimedEvents: []EventInput{
			{SequenceNo: 0, StartMs: 0, EndMs: 500, ActionEN: "First"},
			{SequenceNo: 99, StartMs: 500, EndMs: 1000, ActionEN: "Second"},
		},
	}
	require.NoError(t, d.Validate())
}

func TestParse_PreservesSearchText(t *testing.T) {
	data := []byte(`{
		"schema_version": "ai_stock_visual_analysis.v1",
		"asset": {
			"proposed_asset_id": "ai-1",
			"source": "ai_generated",
			"asset_role": "stock",
			"media_type": "video",
			"folder_path": "Stock/AI/Ocean/Sharks",
			"normalized_group": "stock",
			"duration_ms": 1000,
			"width": 720,
			"height": 1310,
			"fps": 30
		},
		"visual_analysis": {},
		"search_text": "custom search text"
	}`)
	doc, err := Parse(data)
	require.NoError(t, err)
	assert.Equal(t, "custom search text", doc.SearchText)
}

func TestValidate_AcceptsSoundCuesWithSuggestions(t *testing.T) {
	d := Document{
		SchemaVersion: SchemaVersion,
		Asset: AssetInput{
			ProposedAssetID: "ai-1",
			Source:          "ai_generated",
			AssetRole:       "stock",
			MediaType:       "video",
			FolderPath:      "Stock/AI/Ocean/Sharks",
			NormalizedGroup: "stock",
			DurationMs:      1000,
			Width:           720,
			Height:          1310,
			FPS:             30,
		},
		SoundCues: []SoundCueInput{
			{StartMs: 0, EndMs: 500, SoundType: "ambient", SuggestionIT: "suggerimento", SuggestionEN: "suggestion"},
		},
	}
	require.NoError(t, d.Validate())
}

func TestFolderCategory(t *testing.T) {
	tests := []struct {
		folderPath string
		want       string
	}{
		{"Stock/AI/Ocean/Sharks", "Ocean"},
		{"Stock/AI/Music", "Music"},
		{"Stock/AI", "Generico"},
		{"", "Generico"},
		{"/Stock/AI/Nature/", "Nature"},
	}
	for _, tc := range tests {
		got := FolderCategory(tc.folderPath)
		assert.Equal(t, tc.want, got, "FolderCategory(%q)", tc.folderPath)
	}
}

func TestDriveFileID(t *testing.T) {
	got, err := DriveFileID("https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view")
	if err != nil || got != "1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ" {
		t.Fatalf("got %q, err=%v", got, err)
	}
}
