package assets

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/stretchr/testify/require"
)

func TestExtractLocalPath_UsesTypedAccessorBeforeFilename(t *testing.T) {
	a := &asset.Asset{Filename: "fallback.mp4"}
	a.SetLocalPath("/tmp/canonical.mp4")

	require.Equal(t, "/tmp/canonical.mp4", extractLocalPath(a))
}

func TestExtractLocalPath_FallsBackToFilenameWhenMissing(t *testing.T) {
	a := &asset.Asset{Filename: "fallback.mp4"}

	require.Equal(t, "fallback.mp4", extractLocalPath(a))
}

func TestExtractVideoID_PrefersTypedProvenanceAndURLAccessors(t *testing.T) {
	tests := []struct {
		name string
		set  func(*asset.Asset)
		want string
	}{
		{
			name: "source video id",
			set: func(a *asset.Asset) {
				a.SetMetadataSourceVideoID("canonical-id")
				a.SetMetadataSourceURL("https://www.youtube.com/watch?v=legacy-id")
			},
			want: "canonical-id",
		},
		{
			name: "source url",
			set: func(a *asset.Asset) {
				a.SetMetadataSourceURL("https://www.youtube.com/watch?v=source-url-id")
			},
			want: "source-url-id",
		},
		{
			name: "youtube url",
			set: func(a *asset.Asset) {
				a.SetYouTubeURL("https://www.youtube.com/watch?v=youtube-url-id")
			},
			want: "youtube-url-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &asset.Asset{}
			tt.set(a)
			require.Equal(t, tt.want, extractVideoID(a))
		})
	}
}

func TestExtractVideoID_RetainsLegacyAliases(t *testing.T) {
	for _, key := range []string{"source_id", "video_id"} {
		t.Run(key, func(t *testing.T) {
			a := &asset.Asset{Metadata: map[string]any{key: "legacy-id"}}
			require.Equal(t, "legacy-id", extractVideoID(a))
		})
	}
}

func TestBackfillContentHashAccessorPrecedence(t *testing.T) {
	a := &asset.Asset{}
	a.SetContentHash("canonical-content-hash")
	a.SetLegacyFileMD5("legacy-file-hash")

	require.Equal(t, "canonical-content-hash", assetContentHash(a))

	a.SetContentHash("")
	require.Equal(t, "legacy-file-hash", assetContentHash(a))
}

func TestExtractVideoID_RetainsLegacyURLAlias(t *testing.T) {
	a := &asset.Asset{Metadata: map[string]any{
		"url": "https://www.youtube.com/watch?v=legacy-url-id",
	}}

	require.Equal(t, "legacy-url-id", extractVideoID(a))
}
