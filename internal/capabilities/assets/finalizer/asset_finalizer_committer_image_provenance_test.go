package finalizer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// TestBuildCommitRequest_ImageCarriesOriginAndProvider pins the provenance
// columns the canonical image reuse read filters on. Leaving media_assets.origin
// / provider empty made a published image invisible to the origin='retrieved'
// lookup, so a warm run re-downloaded and re-uploaded bytes already in Drive.
func TestBuildCommitRequest_ImageCarriesOriginAndProvider(t *testing.T) {
	artifact := finalization.PublishedArtifact{
		ArtifactID: "entity-image-7",
		Kind:       finalization.KindImage,
		Filename:   "corrado-augias.jpg",
		MIMEType:   "image/jpeg",
		SizeBytes:  2048,
		SHA256:     taxonomyTestSHA,
		Source:     "internet_images",
		ArtifactMetadata: map[string]any{
			"origin":     "retrieved",
			"provider":   "duckduckgo",
			"subject_id": "corrado-augias",
		},
		Location: finalization.AssetLocation{
			Provider: "drive", FileID: "f", WebViewLink: "https://drive/f",
			FolderID: "folder-images", Action: finalization.PublishCreated,
		},
	}

	req := NewAssetTxFinalizer(nil, nil).buildCommitRequest(artifact)

	require.Equal(t, "retrieved", req.Origin)
	require.Equal(t, "duckduckgo", req.Provider)
	require.Equal(t, "corrado-augias", req.Metadata.Extra["subject_id"],
		"the subject projection must reach metadata_json, which is what the reuse lookup reads")
}

// TestBuildCommitRequest_NonImageKeepsProvenanceEmpty proves the scope: origin /
// provider are image-specific dimensions, so a clip carrying a metadata key with
// the same name must not have its provenance reinterpreted.
func TestBuildCommitRequest_NonImageKeepsProvenanceEmpty(t *testing.T) {
	artifact := finalization.PublishedArtifact{
		ArtifactID: "clip-1", Kind: finalization.KindVideo, Filename: "clip.mp4",
		MIMEType: "video/mp4", SizeBytes: 1024, SHA256: taxonomyTestSHA, Source: "youtube",
		ArtifactMetadata: map[string]any{"origin": "retrieved", "provider": "duckduckgo"},
		Location: finalization.AssetLocation{
			Provider: "drive", FileID: "f", WebViewLink: "https://drive/f",
			FolderID: "folder-clips", Action: finalization.PublishCreated,
		},
	}

	req := NewAssetTxFinalizer(nil, nil).buildCommitRequest(artifact)

	require.Empty(t, req.Origin)
	require.Empty(t, req.Provider)
}
