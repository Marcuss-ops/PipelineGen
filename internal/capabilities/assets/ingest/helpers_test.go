package ingest

import "testing"

func TestShouldRejectAssetInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "metadata json", in: "metadata.json", want: true},
		{name: "json sidecar", in: "clip.sidecar.json", want: true},
		{name: "temp file", in: "video.tmp", want: true},
		{name: "hidden file", in: ".DS_Store", want: true},
		{name: "media file", in: "clip.mp4", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRejectAssetInput(tc.in); got != tc.want {
				t.Fatalf("shouldRejectAssetInput(%q) = %t, want %t", tc.in, got, tc.want)
			}
		})
	}
}
