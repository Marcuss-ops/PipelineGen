package voiceover

import "testing"

func TestBatchRequestPayloadMapIncludesDestinationAndMetadata(t *testing.T) {
	removeSilence := true
	req := &BatchRequest{
		Text:             "Hello world",
		Languages:        []string{"it", "en"},
		FilenameTemplate: "{slug}_{lang}.mp3",
		RemoveSilence:    &removeSilence,
		Strategy:         "replace",
		Destination: &DestinationRequest{
			Group:           "Mike Tyson",
			FolderID:        "folder-123",
			FolderPath:      "/voiceover/mike-tyson",
			SubfolderName:   "intro",
			CreateSubfolder: true,
		},
		Metadata: map[string]any{"source": "manual"},
	}

	payload := req.PayloadMap()
	if payload["text"] != "Hello world" {
		t.Fatalf("expected text to be preserved, got %#v", payload["text"])
	}
	if payload["strategy"] != "replace" {
		t.Fatalf("expected strategy to be preserved, got %#v", payload["strategy"])
	}

	dest, ok := payload["destination"].(map[string]any)
	if !ok {
		t.Fatalf("expected destination map, got %#v", payload["destination"])
	}
	if dest["folder_id"] != "folder-123" || dest["create_subfolder"] != true {
		t.Fatalf("unexpected destination payload: %#v", dest)
	}

	meta, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %#v", payload["metadata"])
	}
	if meta["source"] != "manual" {
		t.Fatalf("expected metadata to be preserved, got %#v", meta)
	}
}

func TestRandomSuffixLength(t *testing.T) {
	if got := randomSuffix(6); len(got) != 6 {
		t.Fatalf("expected suffix length 6, got %d (%q)", len(got), got)
	}
}
