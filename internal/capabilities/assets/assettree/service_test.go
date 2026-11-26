package assettree

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/driveurl"
	"go.uber.org/zap"
)

// NormalizeDriveNode used to call platform/drive just to spell a URL. The link
// is pure data, so this test pins the exact shape without constructing any
// Drive adapter.
func TestNormalizeDriveNodeBuildsCanonicalLinksWithoutDriveAdapter(t *testing.T) {
	svc := NewService(nil, zap.NewNop())

	tests := []struct {
		name     string
		mimeType string
		want     string
	}{
		{
			name:     "folder",
			mimeType: "application/vnd.google-apps.folder",
			want:     driveurl.FolderURLFromID("id-1"),
		},
		{
			name:     "file",
			mimeType: "video/mp4",
			want:     driveurl.FileURLFromID("id-1"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := svc.NormalizeDriveNode("id-1", "name", tc.mimeType, "", "", "", "", "", "drive", "")
			if node.DriveLink != tc.want {
				t.Fatalf("WebViewLink = %q, want %q", node.DriveLink, tc.want)
			}
		})
	}
}

func TestNormalizeDriveNodeKeepsAnExplicitLink(t *testing.T) {
	svc := NewService(nil, zap.NewNop())

	node := svc.NormalizeDriveNode("id-1", "name", "video/mp4", "https://example.test/explicit", "", "", "", "", "drive", "")
	if node.DriveLink != "https://example.test/explicit" {
		t.Fatalf("WebViewLink = %q, want the explicit link untouched", node.DriveLink)
	}
}
