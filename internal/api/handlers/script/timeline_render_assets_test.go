package script

import (
	"testing"

	"velox/go-master/internal/media/association"
)

func TestRenderSegmentAssetsIsDisabled(t *testing.T) {
	seg := TimelineSegment{
		StockMatches: []association.ScoredMatch{
			{Title: "Stock Clip", Link: "https://drive.google.com/file/d/stock/view", Score: 90, Source: "drive_stock"},
		},
		ArtlistMatches: []association.ScoredMatch{
			{Title: "Artlist Clip", Link: "https://drive.google.com/file/d/artlist/view", Score: 88, Source: "artlist_stock"},
		},
	}

	out := renderSegmentAssets(seg)
	if out != "" {
		t.Fatalf("expected no rendered asset block, got:\n%s", out)
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

func TestResolveTimelineDisplayLinkIgnoresFolderLinkWhenClipMissing(t *testing.T) {
	match := association.ScoredMatch{
		Title:      "Artlist Clip",
		FolderLink: "https://drive.google.com/drive/folders/drive-folder-id",
		Source:     "artlist_live_discovery",
	}

	link := resolveTimelineDisplayLink(match)
	if link != "" {
		t.Fatalf("expected folder link to be ignored, got %q", link)
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
