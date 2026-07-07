package stockpipeline

import "testing"

func TestBuildStockRunMetadata_IncludesTimestampFields(t *testing.T) {
	in := &RunInput{
		FolderID:      "workflow-123",
		Subfolder:     "boxing",
		ClipDuration:  12,
		ChunkDuration: 12,
		PolicyVersion: "policy-v1",
	}
	chunks := []ChunkState{
		{
			Index:        0,
			ArtifactID:   "stock:fp:timestamp:0:video",
			Filename:     "stock_fp_chunk_0.mp4",
			LocalPath:    "/tmp/clip-0.mp4",
			SourceURL:    "https://www.youtube.com/watch?v=abc",
			StartSec:     32,
			EndSec:       51,
			Title:        "Round 1",
			SHA256:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SizeBytes:    1234,
			RemoteFileID: "drive-file-1",
		},
	}

	meta := buildStockRunMetadata(in, chunks, "run-fp")
	if len(meta.Chunks) != 1 {
		t.Fatalf("expected 1 chunk entry, got %d", len(meta.Chunks))
	}
	entry := meta.Chunks[0]
	if entry.SourceURL != chunks[0].SourceURL {
		t.Fatalf("expected SourceURL %q, got %q", chunks[0].SourceURL, entry.SourceURL)
	}
	if entry.StartSec != chunks[0].StartSec {
		t.Fatalf("expected StartSec %v, got %v", chunks[0].StartSec, entry.StartSec)
	}
	if entry.EndSec != chunks[0].EndSec {
		t.Fatalf("expected EndSec %v, got %v", chunks[0].EndSec, entry.EndSec)
	}
	if entry.Title != chunks[0].Title {
		t.Fatalf("expected Title %q, got %q", chunks[0].Title, entry.Title)
	}
}
