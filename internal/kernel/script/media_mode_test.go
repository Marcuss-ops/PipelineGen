package script

import (
	"encoding/json"
	"errors"
	"testing"
)

const testFolderID = "1xxnNHfperYJ6sZiLcNadgvYIR6wG_jB8"
const testFolderLink = "https://drive.google.com/drive/folders/" + testFolderID + "?usp=drive_link"

func stockOnlyItem() GenerationItemV2 {
	return GenerationItemV2{
		ID: "stock", MediaMode: MediaModeStockOnly,
		Source: SourceSpec{Type: SourceText, Topic: "topic", GroundingPolicy: GroundingPolicySourcePrimary, FallbackPolicy: FallbackPolicyStrict},
		Output: OutputSpec{StockEnabled: ToggleEnabled, StockBindings: []StockBindingInput{{
			Index: 0, FolderID: testFolderID, FolderLink: testFolderLink, StartMs: 0, EndMs: 5000,
		}}},
	}
}

func clipOnlyItem() GenerationItemV2 {
	return GenerationItemV2{ID: "clip", MediaMode: MediaModeClipOnly,
		Source: SourceSpec{Type: SourceClips, ClipIDs: []string{"clip-1"}}}
}

func validateMediaItem(t *testing.T, item GenerationItemV2) *PayloadValidationError {
	t.Helper()
	err := (&GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{item}}).Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	var pve *PayloadValidationError
	if !errors.As(err, &pve) {
		t.Fatalf("error type = %T, want PayloadValidationError: %v", err, err)
	}
	return pve
}

func TestMediaModeStockOnlyAcceptsFolderBindings(t *testing.T) {
	if err := (&GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{stockOnlyItem()}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMediaModeStockOnlyRejectsSourceClips(t *testing.T) {
	i := stockOnlyItem()
	i.Source = SourceSpec{Type: SourceClips, ClipIDs: []string{"clip-1"}}
	if got := validateMediaItem(t, i).Code; got != "MEDIA_MODE_CONFLICT" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeStockOnlyRejectsClipIDs(t *testing.T) {
	i := stockOnlyItem()
	i.Source.ClipIDs = []string{"clip-1"}
	if got := validateMediaItem(t, i).Code; got != "MEDIA_MODE_CONFLICT" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeStockOnlyRejectsIntroClipIDs(t *testing.T) {
	i := stockOnlyItem()
	i.Source.IntroClipIDs = []string{"clip-1"}
	if got := validateMediaItem(t, i).Code; got != "MEDIA_MODE_CONFLICT" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeStockOnlyRejectsAssetID(t *testing.T) {
	i := stockOnlyItem()
	i.Output.StockBindings[0].AssetID = "file-1"
	if got := validateMediaItem(t, i).Code; got != "STOCK_ONLY_FILE_REFERENCE_FORBIDDEN" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeStockOnlyRejectsDriveFileLink(t *testing.T) {
	i := stockOnlyItem()
	i.Output.StockBindings[0].DriveLink = "https://drive.google.com/file/d/file-1/view"
	if got := validateMediaItem(t, i).Code; got != "STOCK_ONLY_FILE_REFERENCE_FORBIDDEN" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeStockOnlyRequiresFolderIDAndLink(t *testing.T) {
	i := stockOnlyItem()
	i.Output.StockBindings[0].FolderID = ""
	if got := validateMediaItem(t, i).Code; got != "STOCK_ONLY_FOLDER_ID_REQUIRED" {
		t.Fatalf("code=%s", got)
	}
	i = stockOnlyItem()
	i.Output.StockBindings[0].FolderLink = ""
	if got := validateMediaItem(t, i).Code; got != "STOCK_ONLY_FOLDER_LINK_REQUIRED" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeStockOnlyRequiresMatchingFolderIDAndLink(t *testing.T) {
	i := stockOnlyItem()
	i.Output.StockBindings[0].FolderID = "other-folder"
	if got := validateMediaItem(t, i).Code; got != "STOCK_ONLY_FOLDER_MISMATCH" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeClipOnlyAcceptsSourceClips(t *testing.T) {
	if err := (&GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{clipOnlyItem()}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMediaModeClipOnlyRequiresClipIDs(t *testing.T) {
	i := clipOnlyItem()
	i.Source.ClipIDs = nil
	if got := validateMediaItem(t, i).Code; got != "CLIP_ONLY_SOURCE_REQUIRED" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeClipOnlyRejectsStockEnabled(t *testing.T) {
	i := clipOnlyItem()
	i.Output.StockEnabled = ToggleEnabled
	if got := validateMediaItem(t, i).Code; got != "CLIP_ONLY_STOCK_REFERENCE_FORBIDDEN" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeClipOnlyRejectsStockBindings(t *testing.T) {
	i := clipOnlyItem()
	i.Output.StockBindings = []StockBindingInput{{FolderID: testFolderID, FolderLink: testFolderLink}}
	if got := validateMediaItem(t, i).Code; got != "CLIP_ONLY_STOCK_REFERENCE_FORBIDDEN" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeAbsentRejectsMixedReferences(t *testing.T) {
	i := clipOnlyItem()
	i.MediaMode = ""
	i.Output.StockEnabled = ToggleEnabled
	if got := validateMediaItem(t, i).Code; got != "MEDIA_MODE_REQUIRED_FOR_MIXED_REFERENCES" {
		t.Fatalf("code=%s", got)
	}
}

func TestMediaModeSerializesAndBuildsPlan(t *testing.T) {
	var item GenerationItemV2
	data := []byte(`{"media_mode":"stock_only","source":{"type":"text","topic":"x"}}`)
	if err := json.Unmarshal(data, &item); err != nil {
		t.Fatal(err)
	}
	if item.MediaMode != MediaModeStockOnly {
		t.Fatalf("media_mode=%q", item.MediaMode)
	}
}
