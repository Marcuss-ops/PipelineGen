package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

func TestVerifiedFromStagedRefUsesProducerResolvedFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "final.mp4")
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	verified, err := verifiedFromStagedRef(context.Background(), &remote.StagedArtifactReference{
		ArtifactID:  "job-1:final_video",
		Destination: "youtube_clip",
		Path:        path,
		Filename:    "final.mp4",
		MIMEType:    "video/mp4",
		ArtifactMetadata: map[string]any{
			"resolved_folder_id": "job-drive-folder",
		},
	}, "job-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Kind != finalization.KindVideo {
		t.Fatalf("kind=%s, want video", verified.Kind)
	}
	if verified.ResolvedFolderID != "job-drive-folder" || !verified.RootFolderResolved {
		t.Fatalf("resolved folder was not pinned: %+v", verified)
	}
}
