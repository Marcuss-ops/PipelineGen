package script

import (
	"strings"
	"testing"

	"velox/go-master/internal/media/association"
)

func TestRenderSegmentAssetsRendersBothStockAndArtlistBlocks(t *testing.T) {
	seg := TimelineSegment{
		StockMatches: []association.ScoredMatch{
			{Title: "Stock Clip", Link: "https://drive.google.com/file/d/stock/view", Score: 90, Source: "drive_stock"},
		},
		ArtlistMatches: []association.ScoredMatch{
			{Title: "Artlist Clip", Link: "https://drive.google.com/file/d/artlist/view", Score: 88, Source: "artlist_stock"},
		},
	}

	out := renderSegmentAssets(seg)

	if !strings.Contains(out, "📦 Stock Drive Association") {
		t.Fatalf("expected stock association block, got:\n%s", out)
	}
	if !strings.Contains(out, "📦 Artlist Drive Association") {
		t.Fatalf("expected artlist association block, got:\n%s", out)
	}
	if !strings.Contains(out, "Stock Clip") || !strings.Contains(out, "Artlist Clip") {
		t.Fatalf("expected both clip titles in output, got:\n%s", out)
	}
}

func TestResolveTimelineDisplayLinkPrefersClipLinkOverFolderLink(t *testing.T) {
	match := association.ScoredMatch{
		Title:      "Artlist Clip",
		Link:       "https://drive.google.com/file/d/clip-id/view",
		FolderLink: "https://drive.google.com/drive/folders/drive-folder-id",
		Source:     "artlist_live_discovery",
	}

	link := resolveTimelineDisplayLink(match)
	if link != match.Link {
		t.Fatalf("expected clip link, got %q", link)
	}
}

func TestResolveTimelineDisplayLinkSuppressesDirectArtlistURL(t *testing.T) {
	match := association.ScoredMatch{
		Title:  "Artlist Clip",
		Link:   "https://cms-public-artifacts.artlist.io/content/artgrid/footage-hls/song-123.m3u8",
		Source: "artlist_live_discovery",
	}

	link := resolveTimelineDisplayLink(match)
	if link != "" {
		t.Fatalf("expected direct artlist URL to be suppressed, got %q", link)
	}
}

func TestResolveAssociatedDisplayLinkPrefersClipLink(t *testing.T) {
	match := association.ScoredMatch{
		Title:      "Artlist Clip",
		Link:       "https://drive.google.com/file/d/clip-id/view",
		FolderLink: "https://drive.google.com/drive/folders/drive-folder-id",
		Source:     "artlist_live_discovery",
	}

	link := resolveAssociatedDisplayLink(match)
	if link != match.Link {
		t.Fatalf("expected clip link, got %q", link)
	}
}

func TestBuildClipsAssociatedSectionUsesClipLinkAndSkipsDirectArtlistURL(t *testing.T) {
	plan := &TimelinePlan{
		Segments: []TimelineSegment{
			{
				ArtlistMatches: []association.ScoredMatch{
					{
						Title:      "Artlist Clip",
						Link:       "https://drive.google.com/file/d/clip-id/view",
						FolderLink: "https://drive.google.com/drive/folders/drive-folder-id",
						Source:     "artlist_live_discovery",
					},
				},
			},
		},
	}

	section := buildClipsAssociatedSection(plan)
	if section.Body == "" {
		t.Fatalf("expected associated section to be rendered")
	}
	if strings.Contains(section.Body, "cms-public-artifacts.artlist.io") {
		t.Fatalf("expected direct artlist URL to be suppressed, got:\n%s", section.Body)
	}
	if !strings.Contains(section.Body, "drive.google.com/file/d/clip-id/view") {
		t.Fatalf("expected clip link in output, got:\n%s", section.Body)
	}
}
