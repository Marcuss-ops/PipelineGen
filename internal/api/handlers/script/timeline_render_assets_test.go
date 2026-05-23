package script

import (
	"strings"
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

func TestRenderSegmentPrimaryAssociationPrefersDriveFolder(t *testing.T) {
	seg := TimelineSegment{
		PreferredStockGroup: "stock_folder",
		PreferredStockPaths: []string{"/ignored/path", "https://drive.google.com/drive/folders/stock-folder-id"},
	}

	out := renderSegmentPrimaryAssociation(seg)
	if out == "" || !strings.Contains(out, "stock-folder-id") {
		t.Fatalf("expected drive folder association, got:\n%s", out)
	}
}

func TestRenderSegmentPrimaryAssociationPrefersArtlistFolder(t *testing.T) {
	seg := TimelineSegment{
		ArtlistMatches: []association.ScoredMatch{{
			Title:      "Artlist Clip",
			Link:       "https://drive.google.com/drive/folders/artlist-folder-id",
			FolderLink: "https://drive.google.com/drive/folders/artlist-folder-id",
			Score:      92,
			Source:     "artlist_live_run",
		}},
	}

	out := renderSegmentPrimaryAssociation(seg)
	if out == "" || !strings.Contains(out, "artlist-folder-id") {
		t.Fatalf("expected artlist folder association, got:\n%s", out)
	}
}

func TestRenderSegmentHeaderIncludesPrimaryAssociation(t *testing.T) {
	seg := TimelineSegment{
		Timestamp: "0-15",
		Subject:   "Mike Tyson",
		StockMatches: []association.ScoredMatch{{
			Title:      "Mike Tyson",
			Link:       "https://drive.google.com/drive/folders/stock-folder-id",
			FolderLink: "https://drive.google.com/drive/folders/stock-folder-id",
			Score:      100,
			Source:     "drive_stock",
		}},
	}

	out := renderSegmentHeader(seg)
	if !strings.Contains(out, "stock-folder-id") {
		t.Fatalf("expected primary association in segment header, got:\n%s", out)
	}
}
