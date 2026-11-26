package finaljob

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// TestFinalJobRenderArtifactRefUsesTheCertifiedObjectStore pins DEMOL-03 step
// 1: the hand-off payload names the certified object-store locator, never a
// Drive identity, even when the artifact still carries one from a legacy
// producer. The master must be able to consume a rendered overlay without any
// Drive lookup or upload having happened first.
func TestFinalJobRenderArtifactRefUsesTheCertifiedObjectStore(t *testing.T) {
	t.Setenv("OBJECTSTORE_PUBLIC_URL", "https://objects.example")
	t.Setenv("OBJECTSTORE_SIGNING_SECRET", strings.Repeat("s", 32))

	sha := strings.Repeat("a", 64)
	artifact := &contract.RenderArtifact{
		ID: "overlay-1", DriveFileID: "legacy-drive-id",
		SHA256: sha, StorageKey: sha, SizeBytes: 4096,
		URL:        "https://objects.example/objects/" + sha,
		DurationUS: 2_000_000, FrameCount: 48, Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
	}

	ref, err := finalJobRenderArtifactRef(context.Background(), artifact)
	if err != nil {
		t.Fatalf("finalJobRenderArtifactRef: %v", err)
	}
	if _, ok := ref["drive_file_id"]; ok {
		t.Fatalf("hand-off ref exposes drive_file_id %v; the master consumes the object store", ref["drive_file_id"])
	}
	if ref["sha256"] != sha || ref["size_bytes"] != int64(4096) {
		t.Fatalf("certified locator = %+v, want the object-store digest and size", ref)
	}
	uri, _ := ref["source_uri"].(string)
	if !strings.HasPrefix(uri, "https://objects.example/objects/") || !strings.Contains(uri, "sig=") {
		t.Fatalf("source_uri = %q, want the signed object-store locator", uri)
	}
}
