package youtube

import "testing"

func TestBuildDriveFolderPath(t *testing.T) {
	tests := []struct {
		name      string
		group     string
		videoSlug string
		want      string
	}{
		{
			name:      "group and slug",
			group:     "Saggezza",
			videoSlug: "yt_J7GpwFrGsiU_0_13_v1",
			want:      "Saggezza/yt_J7GpwFrGsiU_0_13_v1",
		},
		{
			name:      "empty group falls back to youtube_uncategorized",
			group:     "",
			videoSlug: "yt_J7GpwFrGsiU_0_13_v1",
			want:      "youtube_uncategorized/yt_J7GpwFrGsiU_0_13_v1",
		},
		{
			name:      "slashes are trimmed",
			group:     "/Boxe/",
			videoSlug: "/yt_J7GpwFrGsiU_0_13_v1/",
			want:      "Boxe/yt_J7GpwFrGsiU_0_13_v1",
		},
		{
			name:      "empty slug yields empty path",
			group:     "Boxe",
			videoSlug: "",
			want:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildDriveFolderPath(tt.group, tt.videoSlug); got != tt.want {
				t.Fatalf("buildDriveFolderPath(%q, %q) = %q, want %q", tt.group, tt.videoSlug, got, tt.want)
			}
		})
	}
}
