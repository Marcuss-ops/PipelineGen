package downloader

import (
	"testing"
)

// TestYouTubeMetadata_DeriveCaptionFlags pins the T1.2 probe derivation
// on the extraction chain's dump type: HasCaptions is true iff either
// dictionary is non-empty, and CaptionLanguages is the SORTED union of
// both key sets (manual subtitles + ASR automatic_captions), deduplicated.
func TestYouTubeMetadata_DeriveCaptionFlags(t *testing.T) {
	cases := []struct {
		name     string
		meta     YouTubeMetadata
		wantHas  bool
		wantLang []string
	}{
		{
			name: "manual only",
			meta: YouTubeMetadata{
				Subtitles: map[string][]YouTubeCaptionTrack{"it": {{URL: "u"}}},
			},
			wantHas:  true,
			wantLang: []string{"it"},
		},
		{
			name: "asr only",
			meta: YouTubeMetadata{
				AutomaticCaptions: map[string][]YouTubeCaptionTrack{"en": {{URL: "u"}}},
			},
			wantHas:  true,
			wantLang: []string{"en"},
		},
		{
			name: "both dictionaries union + dedupe + sort",
			meta: YouTubeMetadata{
				Subtitles:         map[string][]YouTubeCaptionTrack{"it": {{URL: "u"}}},
				AutomaticCaptions: map[string][]YouTubeCaptionTrack{"en": {{URL: "u"}}, "it": {{URL: "u"}}},
			},
			wantHas:  true,
			wantLang: []string{"en", "it"},
		},
		{
			name:    "no dictionaries",
			meta:    YouTubeMetadata{},
			wantHas: false,
		},
		{
			name: "empty language keys are ignored",
			meta: YouTubeMetadata{
				Subtitles: map[string][]YouTubeCaptionTrack{"": {{URL: "u"}}},
			},
			wantHas: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := tc.meta
			meta.deriveCaptionFlags()
			if meta.HasCaptions != tc.wantHas {
				t.Errorf("HasCaptions=%v, want %v", meta.HasCaptions, tc.wantHas)
			}
			if len(meta.CaptionLanguages) != len(tc.wantLang) {
				t.Errorf("CaptionLanguages=%v, want %v", meta.CaptionLanguages, tc.wantLang)
				return
			}
			for i, lang := range tc.wantLang {
				if meta.CaptionLanguages[i] != lang {
					t.Errorf("CaptionLanguages=%v, want %v", meta.CaptionLanguages, tc.wantLang)
					break
				}
			}
		})
	}
}
