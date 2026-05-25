package fullimages

import (
	"testing"
)

func TestSafeFolderName_RemovesSpecialChars(t *testing.T) {
	got := safeFolderName("old stone house with a man!!!")
	if got != "old stone house with a man" {
		t.Fatalf("expected 'old stone house with a man', got %q", got)
	}
}

func TestSafeFolderName_CollapsesSpaces(t *testing.T) {
	got := safeFolderName("knight   on   horse")
	if got != "knight on horse" {
		t.Fatalf("expected 'knight on horse', got %q", got)
	}
}

func TestSafeFolderName_NoHyphens(t *testing.T) {
	got := safeFolderName("medievale-castle")
	if got != "medievalecastle" {
		t.Fatalf("expected no hyphens, got %q", got)
	}
}

func TestSafeFolderName_NoUnderscores(t *testing.T) {
	got := safeFolderName("stone_cottage")
	if got != "stonecottage" {
		t.Fatalf("expected no underscores, got %q", got)
	}
}

func TestSafeFolderName_OnlyAlphanumeric(t *testing.T) {
	got := safeFolderName("Cus D'Amato!")
	if got != "Cus DAmato" {
		t.Fatalf("expected 'Cus DAmato', got %q", got)
	}
}

func TestSafeFolderName_Empty(t *testing.T) {
	got := safeFolderName("")
	if got != "untitled" {
		t.Fatalf("expected 'untitled' for empty, got %q", got)
	}
}

func TestSafeFolderName_PreservesSpaces(t *testing.T) {
	got := safeFolderName("a knight on a white horse")
	if got != "a knight on a white horse" {
		t.Fatalf("expected spaces preserved, got %q", got)
	}
}

func TestSectionVideo_StyleField(t *testing.T) {
	v := SectionVideo{SectionIndex: 0, Title: "Castle", Style: "medievale", DriveLink: "https://drive.google.com/"}
	if v.Style != "medievale" {
		t.Fatalf("expected style 'medievale', got %q", v.Style)
	}
}

func TestSectionVideo_Error(t *testing.T) {
	v := SectionVideo{SectionIndex: 0, Title: "Castle", Style: "medievale", Error: "NVIDIA API failed"}
	if v.Error != "NVIDIA API failed" {
		t.Fatalf("expected error preserved, got %q", v.Error)
	}
}

func TestSection_StyleField(t *testing.T) {
	sec := Section{Title: "Knight", Style: "medievale"}
	if sec.Style != "medievale" {
		t.Fatalf("expected style 'medievale', got %q", sec.Style)
	}
}

func TestResult_Videos(t *testing.T) {
	r := Result{Videos: []SectionVideo{
		{SectionIndex: 0, Title: "Castle", Style: "medievale"},
		{SectionIndex: 1, Title: "Knight", Style: "medievale"},
	}}
	if len(r.Videos) != 2 {
		t.Fatalf("expected 2 videos, got %d", len(r.Videos))
	}
}

func TestSafeFolderName_DropsNonAlphanumericCompletely(t *testing.T) {
	got := safeFolderName("test@#$%^&*()folder")
	if got != "testfolder" {
		t.Fatalf("expected 'testfolder', got %q", got)
	}
}
