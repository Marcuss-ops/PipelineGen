package driveurl

import "testing"

func TestURLHelpersAreCanonical(t *testing.T) {
	if got, want := FileURLFromID("abc"), "https://drive.google.com/file/d/abc"; got != want {
		t.Fatalf("FileURLFromID = %q, want %q", got, want)
	}
	if got, want := FolderURLFromID("abc"), "https://drive.google.com/drive/folders/abc"; got != want {
		t.Fatalf("FolderURLFromID = %q, want %q", got, want)
	}
	if got := FileIDFromLink(FileURLFromID("abc")); got != "abc" {
		t.Fatalf("FileIDFromLink(FileURLFromID(abc)) = %q, want abc", got)
	}
	if got := FileIDFromLink("https://drive.google.com/uc?id=xyz&export=download"); got != "xyz" {
		t.Fatalf("FileIDFromLink(/uc?id=xyz) = %q, want xyz", got)
	}
}
