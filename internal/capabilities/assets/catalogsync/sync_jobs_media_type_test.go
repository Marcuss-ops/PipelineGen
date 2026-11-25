package catalogsync

import "testing"

func TestResolveMediaTargetFallsBackToMusicAlias(t *testing.T) {
	repo := portConformanceRepository{}
	service := &Service{targets: []Target{{
		Source: "music", Repo: repo, Indexer: repo,
	}}}

	source, gotRepo, gotIndexer := service.resolveMediaTarget("drive", "audio")
	if source != "music" || gotRepo == nil || gotIndexer == nil {
		t.Fatalf("resolveMediaTarget(drive, audio) = %q/%T/%T, want the music target", source, gotRepo, gotIndexer)
	}
	source, gotRepo, gotIndexer = service.resolveMediaTarget("music", "audio")
	if source != "music" || gotRepo == nil || gotIndexer == nil {
		t.Fatalf("resolveMediaTarget(music, audio) = %q/%T/%T, want the music target", source, gotRepo, gotIndexer)
	}
}

func TestCatalogMediaTypeForDetectsMusicAndBGM(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{"BGM Collection", "drive", "audio"},
		{"", "music", "audio"},
		{"background_audio", "drive", "audio"},
		{"Clip Archive", "drive", "clip"},
		{"", "", "clip"},
	}
	for _, tc := range cases {
		if got := catalogMediaTypeFor(tc.name, tc.source); got != tc.want {
			t.Fatalf("catalogMediaTypeFor(%q, %q) = %q, want %q", tc.name, tc.source, got, tc.want)
		}
	}
}
