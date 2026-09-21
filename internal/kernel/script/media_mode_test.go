package script

import (
	"encoding/json"
	"errors"
	"strings"
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
		ScriptParams: ScriptSpec{TargetWords: 100},
	}
}

func clipOnlyItem() GenerationItemV2 {
	return GenerationItemV2{ID: "clip", MediaMode: MediaModeClipOnly,
		Source:       SourceSpec{Type: SourceClips, ClipIDs: []string{"clip-1"}},
		ScriptParams: ScriptSpec{TargetWords: 100}}
}

func mixedItem() GenerationItemV2 {
	return GenerationItemV2{ID: "mixed", MediaMode: MediaModeMixed,
		Source: SourceSpec{Type: SourceClips, ClipIDs: []string{"intro-clip", "body-clip"}},
		Output: OutputSpec{StockEnabled: ToggleEnabled, StockBindings: []StockBindingInput{{
			Index: 1, FolderID: testFolderID, FolderLink: testFolderLink, StartMs: 0, EndMs: 5000,
		}}},
		ScriptParams: ScriptSpec{TargetWords: 100},
	}
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

func TestMediaModeStockOnlyExpandsSegmentDriveFolder(t *testing.T) {
	item := GenerationItemV2{
		ID: "five-boxers", MediaMode: MediaModeStockOnly,
		Source: SourceSpec{Type: SourceText, Topic: "five boxers"},
		ScriptParams: ScriptSpec{Segments: []ScriptSegment{
			{ID: "boxer-1", Topic: "Boxer 1", StockFolderID: testFolderID},
		}},
	}
	env := &GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{item}}
	if err := env.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(env.Items[0].Output.StockBindings) != 1 {
		t.Fatalf("generated stock bindings = %d, want 1", len(env.Items[0].Output.StockBindings))
	}
	binding := env.Items[0].Output.StockBindings[0]
	if binding.Index != 0 || binding.SceneID != "scene-0" || binding.SegmentID != "boxer-1" {
		t.Fatalf("generated binding identity = %+v", binding)
	}
	if binding.FolderID != testFolderID || binding.FolderLink != "https://drive.google.com/drive/folders/"+testFolderID {
		t.Fatalf("generated folder binding = %+v", binding)
	}
	if item.Output.StockEnabled == ToggleEnabled {
		t.Fatal("test must verify validation mutates the envelope, not the original item")
	}
}

func TestMediaModeStockOnlyExpandsSegmentDriveFolderLink(t *testing.T) {
	item := stockOnlyItem()
	item.Output.StockBindings = nil
	item.ScriptParams = ScriptSpec{Segments: []ScriptSegment{{
		ID: "boxer-1", Topic: "Boxer 1", StockFolderLink: testFolderLink,
	}}}
	if err := (&GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{item}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMediaModeStockOnlyRejectsMismatchedSegmentDriveFolder(t *testing.T) {
	item := GenerationItemV2{
		ID: "boxer", MediaMode: MediaModeStockOnly,
		Source: SourceSpec{Type: SourceText, Topic: "boxer"},
		ScriptParams: ScriptSpec{Segments: []ScriptSegment{{
			ID: "boxer-1", Topic: "Boxer 1", StockFolderID: testFolderID,
			StockFolderLink: "https://drive.google.com/drive/folders/other-folder",
		}}},
	}
	err := (&GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{item}}).Validate()
	var pve *PayloadValidationError
	if !errors.As(err, &pve) || pve.Code != "INVALID_STOCK_FOLDER" {
		t.Fatalf("error = %v, want INVALID_STOCK_FOLDER", err)
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

func TestMediaModeStockOnlyRejectsDeprecatedIntroClipIDs(t *testing.T) {
	// source.intro_clip_ids was removed from the contract (July 2026): any
	// payload still carrying it fails closed at the envelope fence, before
	// media-mode dispatch, regardless of source type.
	i := stockOnlyItem()
	i.Source.IntroClipIDs = []string{"clip-1"}
	err := (&GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{i}}).Validate()
	if err == nil {
		t.Fatal("expected deprecation rejection for source.intro_clip_ids")
	}
	if !strings.Contains(err.Error(), "source.intro_clip_ids is deprecated") {
		t.Fatalf("error = %v, want deprecation rejection", err)
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

func TestMediaModeMixedAcceptsClipSourceAndStockBindings(t *testing.T) {
	if err := (&GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{mixedItem()}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMediaModeMixedRequiresBothMediaFamilies(t *testing.T) {
	i := mixedItem()
	i.Source.ClipIDs = nil
	if got := validateMediaItem(t, i).Code; got != "MIXED_CLIP_SOURCE_REQUIRED" {
		t.Fatalf("code=%s", got)
	}
	i = mixedItem()
	i.Output.StockBindings = nil
	if got := validateMediaItem(t, i).Code; got != "MIXED_STOCK_BINDINGS_REQUIRED" {
		t.Fatalf("code=%s", got)
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
