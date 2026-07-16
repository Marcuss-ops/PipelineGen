package visualanalysis

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

func TestDriveFileID(t *testing.T) {
	got, err := DriveFileID("https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view")
	if err != nil || got != "1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ" {
		t.Fatalf("got %q, err=%v", got, err)
	}
}
