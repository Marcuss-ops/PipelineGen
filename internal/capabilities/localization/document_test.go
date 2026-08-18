package localization

import (
	"encoding/json"
	"testing"
)

// TestLocalizedDocumentEntry_JSONRoundTrip pins every field's wire tag so
// the contract shape cannot drift silently.
func TestLocalizedDocumentEntry_JSONRoundTrip(t *testing.T) {
	entry := LocalizedDocumentEntry{
		SceneID:      "scene-7",
		ClipID:       "clip-42",
		Language:     "es",
		Priority:     1,
		TextTrackID:  202,
		VideoAssetID: "asset-9",
		DriveFileID:  "drive-file-1",
		DriveLink:    "https://drive/...",
		DurationMS:   8432,
		SHA256:       "output-sha",
	}

	out, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}
	wantKeys := []string{
		"scene_id", "clip_id", "language", "priority",
		"text_track_id",
		"video_asset_id", "drive_file_id", "drive_link",
		"duration_ms", "sha256",
	}
	for _, k := range wantKeys {
		if _, ok := wire[k]; !ok {
			t.Errorf("wire payload missing key %q", k)
		}
	}
	if len(wire) != len(wantKeys) {
		t.Errorf("wire payload has %d keys, want %d (unexpected extra field)", len(wire), len(wantKeys))
	}

	var back LocalizedDocumentEntry
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != entry {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", back, entry)
	}
}

// TestSortLocalizedDocumentEntries orders entries by Priority ascending,
// preserving input order for equal priorities (stable).
func TestSortLocalizedDocumentEntries(t *testing.T) {
	entries := []LocalizedDocumentEntry{
		{Language: "it", Priority: 2},
		{Language: "en", Priority: 0},
		{Language: "es", Priority: 1},
		{Language: "en-caption", Priority: 0}, // equal priority with "en"
	}
	SortLocalizedDocumentEntries(entries)

	want := []string{"en", "en-caption", "es", "it"}
	for i, lang := range want {
		if entries[i].Language != lang {
			t.Fatalf("entry[%d]: got %q, want %q (order: %+v)", i, entries[i].Language, lang, entries)
		}
	}
}

// TestSortLocalizedDocumentEntries_Empty verifies the helper is a no-op on
// empty and single-entry slices.
func TestSortLocalizedDocumentEntries_Empty(t *testing.T) {
	var empty []LocalizedDocumentEntry
	SortLocalizedDocumentEntries(empty)
	if len(empty) != 0 {
		t.Fatalf("empty slice must stay empty, got %d", len(empty))
	}

	single := []LocalizedDocumentEntry{{Language: "en", Priority: 0}}
	SortLocalizedDocumentEntries(single)
	if single[0].Language != "en" {
		t.Fatalf("single entry must be preserved, got %+v", single[0])
	}
}
