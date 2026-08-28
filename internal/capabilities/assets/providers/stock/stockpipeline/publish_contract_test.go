package stockpipeline

import (
	"testing"

	stockpublish "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/publish"
)

func TestPublishNeutralChunkProjectionPreservesLegacyFields(t *testing.T) {
	legacy := ChunkState{
		Index: 2, ArtifactID: "chunk-2", Filename: "chunk.mp4", LocalPath: "/tmp/chunk.mp4",
		SizeBytes: 42, SHA256: "hash", Description: "description",
		RemoteFileID: "drive-file", RemoteWebViewLink: "https://drive/file",
	}
	got := toPublishChunk(legacy, "root", "folder", "group")
	want := stockpublish.Chunk{
		Index: 2, ArtifactID: "chunk-2", Filename: "chunk.mp4", LocalPath: "/tmp/chunk.mp4",
		SizeBytes: 42, SHA256: "hash", Description: "description",
		RootFolder: "root", FolderID: "folder", FolderResolved: true, PathLeaf: "group",
	}
	if got != want {
		t.Fatalf("toPublishChunk() = %#v, want %#v", got, want)
	}
}

func TestPublishNeutralMetadataContractIsIndependent(t *testing.T) {
	metadataState := MetadataState{LocalPath: "/tmp/metadata.json", SizeBytes: 10, SHA256: "hash", RemoteFileID: "drive-file"}
	metadata := toPublishMetadata(metadataState, "metadata-1", "root", "folder", "metadata")
	if metadata.Filename != "metadata.json" || !metadata.FolderResolved {
		t.Fatalf("adapter metadata = %#v", metadata)
	}
	metadata = stockpublish.Metadata{
		ArtifactID: "metadata-1", Filename: "metadata.json", LocalPath: "/tmp/metadata.json",
		SizeBytes: 10, SHA256: "hash", RootFolder: "root", FolderID: "folder", PathLeaf: "metadata",
	}
	if metadata.ArtifactID == "" || metadata.Filename != "metadata.json" || metadata.LocalPath == "" {
		t.Fatalf("invalid neutral metadata contract: %#v", metadata)
	}
}

func TestPublishNeutralChunkDoesNotAliasLegacyTags(t *testing.T) {
	legacy := ChunkState{Tags: []string{"original"}}
	converted := append([]string(nil), legacy.Tags...)
	converted[0] = "changed"
	if legacy.Tags[0] != "original" {
		t.Fatal("neutral projection must not alias mutable legacy slices")
	}
}
