package asset_test

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestMetadataStringAccessorsRoundTrip verifies every string-typed accessor
// added for the youtube/autotag bare-key migration (godlike/07 migration
// window) round-trips through Asset.Metadata with the same storage key the
// legacy GetMetadataString("key") call sites used.
func TestMetadataStringAccessorsRoundTrip(t *testing.T) {
	pairs := []struct {
		name string
		set  func(a *asset.Asset, v string)
		get  func(a *asset.Asset) string
	}{
		{"YouTubeTitle", (*asset.Asset).SetYouTubeTitle, (*asset.Asset).YouTubeTitle},
		{"YouTubeDescription", (*asset.Asset).SetYouTubeDescription, (*asset.Asset).YouTubeDescription},
		{"YouTubeLanguage", (*asset.Asset).SetYouTubeLanguage, (*asset.Asset).YouTubeLanguage},
		{"YouTubeUploader", (*asset.Asset).SetYouTubeUploader, (*asset.Asset).YouTubeUploader},
		{"YouTubeUploadDate", (*asset.Asset).SetYouTubeUploadDate, (*asset.Asset).YouTubeUploadDate},
		{"YouTubeViewCount", (*asset.Asset).SetYouTubeViewCount, (*asset.Asset).YouTubeViewCount},
		{"YouTubeDuration", (*asset.Asset).SetYouTubeDuration, (*asset.Asset).YouTubeDuration},
		{"YouTubeVideoID", (*asset.Asset).SetYouTubeVideoID, (*asset.Asset).YouTubeVideoID},
		{"YouTubeURL", (*asset.Asset).SetYouTubeURL, (*asset.Asset).YouTubeURL},
		{"YouTubeCategories", (*asset.Asset).SetYouTubeCategories, (*asset.Asset).YouTubeCategories},
		{"YouTubeChapters", (*asset.Asset).SetYouTubeChapters, (*asset.Asset).YouTubeChapters},
		{"YouTubeThumbnail", (*asset.Asset).SetYouTubeThumbnail, (*asset.Asset).YouTubeThumbnail},
		{"ClipSummary", (*asset.Asset).SetClipSummary, (*asset.Asset).ClipSummary},
		{"Hook", (*asset.Asset).SetHook, (*asset.Asset).Hook},
		{"CleanTitle", (*asset.Asset).SetCleanTitle, (*asset.Asset).CleanTitle},
		{"ShortTitle", (*asset.Asset).SetShortTitle, (*asset.Asset).ShortTitle},
		{"EmbeddingText", (*asset.Asset).SetEmbeddingText, (*asset.Asset).EmbeddingText},
		{"CleanTranscript", (*asset.Asset).SetCleanTranscript, (*asset.Asset).CleanTranscript},
		{"RawTranscript", (*asset.Asset).SetRawTranscript, (*asset.Asset).RawTranscript},
		{"SearchVisibility", (*asset.Asset).SetSearchVisibility, (*asset.Asset).SearchVisibility},
		{"QualityTier", (*asset.Asset).SetQualityTier, (*asset.Asset).QualityTier},
		{"Language", (*asset.Asset).SetLanguage, (*asset.Asset).Language},
		{"ContentHash", (*asset.Asset).SetContentHash, (*asset.Asset).ContentHash},
		{"SponsorConfidence", (*asset.Asset).SetSponsorConfidence, (*asset.Asset).SponsorConfidence},
		{"DuplicateGroupID", (*asset.Asset).SetDuplicateGroupID, (*asset.Asset).DuplicateGroupID},
		{"DuplicateOf", (*asset.Asset).SetDuplicateOf, (*asset.Asset).DuplicateOf},
		{"DuplicateReason", (*asset.Asset).SetDuplicateReason, (*asset.Asset).DuplicateReason},
		{"TopicClusterID", (*asset.Asset).SetTopicClusterID, (*asset.Asset).TopicClusterID},
		{"TopicClusterLabel", (*asset.Asset).SetTopicClusterLabel, (*asset.Asset).TopicClusterLabel},
		{"VLMTagged", (*asset.Asset).SetVLMTagged, (*asset.Asset).VLMTagged},
		{"VLMTagError", (*asset.Asset).SetVLMTagError, (*asset.Asset).VLMTagError},
		{"VLMModel", (*asset.Asset).SetVLMModel, (*asset.Asset).VLMModel},
		{"VLMModelVersion", (*asset.Asset).SetVLMModelVersion, (*asset.Asset).VLMModelVersion},
		{"VLMSceneTypes", (*asset.Asset).SetVLMSceneTypes, (*asset.Asset).VLMSceneTypes},
		{"VLMMoods", (*asset.Asset).SetVLMMoods, (*asset.Asset).VLMMoods},
		{"VLMVisualObjects", (*asset.Asset).SetVLMVisualObjects, (*asset.Asset).VLMVisualObjects},
		{"VLMOCRText", (*asset.Asset).SetVLMOCRText, (*asset.Asset).VLMOCRText},
		{"VLMAggregateDescription", (*asset.Asset).SetVLMAggregateDescription, (*asset.Asset).VLMAggregateDescription},
		{"TextOnScreen", (*asset.Asset).SetTextOnScreen, (*asset.Asset).TextOnScreen},
		{"Lighting", (*asset.Asset).SetLighting, (*asset.Asset).Lighting},
		{"Composition", (*asset.Asset).SetComposition, (*asset.Asset).Composition},
		{"DominantColors", (*asset.Asset).SetDominantColors, (*asset.Asset).DominantColors},
	}

	want := "round-trip-value"
	for _, p := range pairs {
		a := &asset.Asset{}
		p.set(a, want)
		if got := p.get(a); got != want {
			t.Errorf("%s: got %q, want %q", p.name, got, want)
		}
		p.set(a, "")
		if got := p.get(a); got != "" {
			t.Errorf("%s: empty overwrite failed, got %q", p.name, got)
		}
	}
}

// TestMetadataSliceAccessorsRoundTrip covers the []string-typed accessors.
func TestMetadataSliceAccessorsRoundTrip(t *testing.T) {
	pairs := []struct {
		name string
		set  func(a *asset.Asset, v []string)
		get  func(a *asset.Asset) []string
	}{
		{"YouTubeTags", (*asset.Asset).SetYouTubeTags, (*asset.Asset).YouTubeTags},
		{"Topics", (*asset.Asset).SetTopics, (*asset.Asset).Topics},
		{"Speakers", (*asset.Asset).SetSpeakers, (*asset.Asset).Speakers},
		{"MentionedPeople", (*asset.Asset).SetMentionedPeople, (*asset.Asset).MentionedPeople},
		{"People", (*asset.Asset).SetPeople, (*asset.Asset).People},
		{"SourceTags", (*asset.Asset).SetSourceTags, (*asset.Asset).SourceTags},
		{"ClipTags", (*asset.Asset).SetClipTags, (*asset.Asset).ClipTags},
		{"SearchKeywords", (*asset.Asset).SetSearchKeywords, (*asset.Asset).SearchKeywords},
		{"SemanticTags", (*asset.Asset).SetSemanticTags, (*asset.Asset).SemanticTags},
	}

	want := []string{"a", "b", "c"}
	for _, p := range pairs {
		a := &asset.Asset{}
		p.set(a, want)
		got := p.get(a)
		if len(got) != len(want) || got[0] != "a" || got[2] != "c" {
			t.Errorf("%s: got %v, want %v", p.name, got, want)
		}
		p.set(a, nil)
		if got := p.get(a); got != nil {
			t.Errorf("%s: nil overwrite failed, got %v", p.name, got)
		}
	}
}

// TestMetadataScalarAccessorsRoundTrip covers the bool/float/int accessors.
func TestMetadataScalarAccessorsRoundTrip(t *testing.T) {
	a := &asset.Asset{}
	a.SetIsSponsorSegment(true)
	if !a.IsSponsorSegment() {
		t.Error("IsSponsorSegment: got false, want true")
	}
	a.SetIsSponsorSegment(false)
	if a.IsSponsorSegment() {
		t.Error("IsSponsorSegment: got true, want false")
	}

	a.SetIsDuplicate(true)
	if !a.IsDuplicate() {
		t.Error("IsDuplicate: got false, want true")
	}
	a.SetIsBestVersion(true)
	if !a.IsBestVersion() {
		t.Error("IsBestVersion: got false, want true")
	}

	a.SetDuplicateScore(0.85)
	if got := a.DuplicateScore(); got != 0.85 {
		t.Errorf("DuplicateScore: got %v, want 0.85", got)
	}

	a.SetTopicClusterSize(4)
	if got := a.TopicClusterSize(); got != 4 {
		t.Errorf("TopicClusterSize: got %d, want 4", got)
	}
	a.SetTopicClusterRank(2)
	if got := a.TopicClusterRank(); got != 2 {
		t.Errorf("TopicClusterRank: got %d, want 2", got)
	}
	a.SetVLMAnalysisDurationMs(123)
	if got := a.VLMAnalysisDurationMs(); got != 123 {
		t.Errorf("VLMAnalysisDurationMs: got %d, want 123", got)
	}
	a.SetVLMFramesAnalyzed(5)
	if got := a.VLMFramesAnalyzed(); got != 5 {
		t.Errorf("VLMFramesAnalyzed: got %d, want 5", got)
	}
}

// TestMetadataAccessorsNilSafe verifies every new accessor returns its zero
// value on an Asset with a nil Metadata map (no panic).
func TestMetadataAccessorsNilSafe(t *testing.T) {
	a := &asset.Asset{} // Metadata is nil

	if got := a.YouTubeTitle(); got != "" {
		t.Errorf("nil-safe YouTubeTitle: got %q", got)
	}
	if got := a.ClipSummary(); got != "" {
		t.Errorf("nil-safe ClipSummary: got %q", got)
	}
	if got := a.Topics(); got != nil {
		t.Errorf("nil-safe Topics: got %v", got)
	}
	if a.IsSponsorSegment() {
		t.Error("nil-safe IsSponsorSegment: got true")
	}
	if got := a.DuplicateScore(); got != 0 {
		t.Errorf("nil-safe DuplicateScore: got %v", got)
	}
	if got := a.TopicClusterSize(); got != 0 {
		t.Errorf("nil-safe TopicClusterSize: got %d", got)
	}
	if got := a.VLMTagged(); got != "" {
		t.Errorf("nil-safe VLMTagged: got %q", got)
	}
	if got := a.Language(); got != "" {
		t.Errorf("nil-safe Language: got %q", got)
	}
}

// TestSourceURLConvergenceAccessors covers the source_url convergence
// family (godlike/06): the typed SourceURL field is canonical; the
// metadata key accessors preserve the legacy storage keys so the two
// surfaces stay in sync without a data migration.
func TestSourceURLConvergenceAccessors(t *testing.T) {
	a := &asset.Asset{}
	a.SetMetadataSourceURL("https://example.com/clip")
	a.SetMetadataSourceProvider("youtube")
	a.SetMetadataSourceVideoID("abc123")
	a.SetStartSec(12.5)
	a.SetEndSec(30)
	if got := a.MetadataSourceURL(); got != "https://example.com/clip" {
		t.Fatalf("MetadataSourceURL=%q", got)
	}
	if got := a.MetadataSourceProvider(); got != "youtube" {
		t.Fatalf("MetadataSourceProvider=%q", got)
	}
	if got := a.MetadataSourceVideoID(); got != "abc123" {
		t.Fatalf("MetadataSourceVideoID=%q", got)
	}
	if got := a.StartSec(); got != 12.5 {
		t.Fatalf("StartSec=%v", got)
	}
	if got := a.EndSec(); got != 30 {
		t.Fatalf("EndSec=%v", got)
	}

	// Nil-safety on a zero-value Asset.
	b := &asset.Asset{}
	if got := b.MetadataSourceURL(); got != "" {
		t.Fatalf("nil-safe MetadataSourceURL=%q", got)
	}
	if got := b.MetadataSourceProvider(); got != "" {
		t.Fatalf("nil-safe MetadataSourceProvider=%q", got)
	}
	if got := b.MetadataSourceVideoID(); got != "" {
		t.Fatalf("nil-safe MetadataSourceVideoID=%q", got)
	}
	if got := b.StartSec(); got != 0 {
		t.Fatalf("nil-safe StartSec=%v", got)
	}
	if got := b.EndSec(); got != 0 {
		t.Fatalf("nil-safe EndSec=%v", got)
	}
}
