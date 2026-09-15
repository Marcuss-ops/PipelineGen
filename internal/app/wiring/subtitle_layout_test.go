// Package wiring — subtitle_layout_test.go pins the two rules that the
// September 2026 subtitle regression depended on and that previously had NO
// test at the composition boundary:
//
//  1. NewSubtitleRootLayoutResolver is the single owner of the subtitle Drive
//     LAYOUT. It must resolve <subtitle root>/<SubtitleDriveGroup>/<videoID>/
//     so the per-language .ass lands next to the .txt sidecar the extraction
//     already uploaded, instead of under <clips root>/Ass Sub/.
//  2. SubtitleAcquisitionLanguages is the single owner of the ACQUISITION
//     language set: exactly one source language. Passing the configured
//     translation set made yt-dlp request ten tracks, hit HTTP 429 and return
//     no transcript at all.
//
// Both were real production bugs found by a live E2E; a test at this level
// fails the moment either rule drifts, without needing a live download.
package wiring

import (
	"context"
	"testing"

	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestNewSubtitleRootLayoutResolver_EmptyRootIsNil(t *testing.T) {
	if got := NewSubtitleRootLayoutResolver(""); got != nil {
		t.Fatalf("empty root must disable the resolver (legacy layout), got %#v", got)
	}
	if got := NewSubtitleRootLayoutResolver("   "); got != nil {
		t.Fatalf("whitespace-only root must disable the resolver, got %#v", got)
	}
}

func TestNewSubtitleRootLayoutResolver_MirrorsExtractionLayout(t *testing.T) {
	const rootID = "subtitle-root-id"
	r := NewSubtitleRootLayoutResolver(rootID)
	if r == nil {
		t.Fatal("a configured root must produce a resolver")
	}

	loc, err := r.ResolveSubtitleLocation(context.Background(), "yt_VIDEO_0_60_v1", "VIDEO")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if loc.FolderID != rootID {
		t.Fatalf("FolderID = %q, want %q", loc.FolderID, rootID)
	}
	wantSubpath := []string{ytadapters.SubtitleDriveGroup, "VIDEO"}
	if len(loc.Subpath) != len(wantSubpath) {
		t.Fatalf("Subpath = %v, want %v (the .ass must be a sibling of the .txt sidecar)", loc.Subpath, wantSubpath)
	}
	for i := range wantSubpath {
		if loc.Subpath[i] != wantSubpath[i] {
			t.Fatalf("Subpath[%d] = %q, want %q", i, loc.Subpath[i], wantSubpath[i])
		}
	}
}

func TestNewSubtitleRootLayoutResolver_FallsBackToAssetID(t *testing.T) {
	const rootID = "subtitle-root-id"
	r := NewSubtitleRootLayoutResolver(rootID)

	loc, err := r.ResolveSubtitleLocation(context.Background(), "asset-id-fallback", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(loc.Subpath) != 2 || loc.Subpath[1] != "asset-id-fallback" {
		t.Fatalf("Subpath = %v, want [%s asset-id-fallback]", loc.Subpath, ytadapters.SubtitleDriveGroup)
	}
}

func TestNewSubtitleRootLayoutResolver_NoIdentityPublishesAtRoot(t *testing.T) {
	const rootID = "subtitle-root-id"
	r := NewSubtitleRootLayoutResolver(rootID)

	loc, err := r.ResolveSubtitleLocation(context.Background(), "", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if loc.FolderID != rootID {
		t.Fatalf("FolderID = %q, want %q", loc.FolderID, rootID)
	}
	if len(loc.Subpath) != 0 {
		t.Fatalf("Subpath = %v, want empty when there is no video id and no asset id", loc.Subpath)
	}
}

// TestResolveSourcePriority pins the third acquisition rule: only the exact
// "whisper_first" token reorders the chain. It is the composition-level guard
// for media.multilingual.source_priority — a typo ("whisper-first",
// "Whisper_First ", "", unknown) MUST keep the canonical captions-first order
// rather than silently making Whisper the source.
func TestResolveSourcePriority(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{value: "whisper_first", want: true},
		{value: "  Whisper_First  ", want: true},
		{value: "WHISPER_FIRST", want: true},
		{value: "captions_first", want: false},
		{value: "whisper-first", want: false},
		{value: "whisper", want: false},
		{value: "", want: false},
		{value: "   ", want: false},
	}
	for _, tc := range cases {
		if got := resolveSourcePriority(tc.value); got != tc.want {
			t.Errorf("resolveSourcePriority(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestSubtitleAcquisitionLanguages_OnlyTheSourceLanguage(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.MultilingualConfig
		want string
	}{
		{
			name: "source language is used and the target set is ignored",
			cfg: config.MultilingualConfig{
				SourceLanguage: "it",
				Languages: config.LanguageSpecSlice{
					{Code: "it", Enabled: true, TranslateClips: true},
					{Code: "en", Enabled: true, TranslateClips: true},
					{Code: "pl", Enabled: true, TranslateClips: true},
				},
			},
			want: "it",
		},
		{
			name: "empty source language defaults to en (never empty => never 'all tracks')",
			cfg: config.MultilingualConfig{
				Languages: config.LanguageSpecSlice{
					{Code: "de", Enabled: true, TranslateClips: true},
				},
			},
			want: "en",
		},
		{
			name: "surrounding whitespace is trimmed",
			cfg:  config.MultilingualConfig{SourceLanguage: "  fr  "},
			want: "fr",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SubtitleAcquisitionLanguages(tc.cfg)
			if got != tc.want {
				t.Fatalf("SubtitleAcquisitionLanguages = %q, want %q", got, tc.want)
			}
			if got == "" {
				t.Fatal("an empty acquisition language would let yt-dlp probe every track")
			}
		})
	}
}
