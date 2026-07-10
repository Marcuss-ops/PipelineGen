package fullimages

import (
	"testing"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

func TestSafeFolderName_RemovesSpecialChars(t *testing.T) {
	got := textutil.SafeName("old stone house with a man!!!")
	if got != "old stone house with a man" {
		t.Fatalf("expected 'old stone house with a man', got %q", got)
	}
}

func TestSafeFolderName_CollapsesSpaces(t *testing.T) {
	got := textutil.SafeName("knight   on   horse")
	if got != "knight on horse" {
		t.Fatalf("expected 'knight on horse', got %q", got)
	}
}

func TestSafeFolderName_HyphensToSpaces(t *testing.T) {
	got := textutil.SafeName("medievale-castle")
	if got != "medievale castle" {
		t.Fatalf("expected 'medievale castle', got %q", got)
	}
}

func TestSafeFolderName_UnderscoresToSpaces(t *testing.T) {
	got := textutil.SafeName("stone_cottage")
	if got != "stone cottage" {
		t.Fatalf("expected 'stone cottage', got %q", got)
	}
}

func TestSafeFolderName_OnlyAlphanumericAndSpaces(t *testing.T) {
	got := textutil.SafeName("Cus D'Amato!")
	if got != "Cus D Amato" {
		t.Fatalf("expected 'Cus D Amato', got %q", got)
	}
}

func TestSafeFolderName_Empty(t *testing.T) {
	got := textutil.SafeName("")
	if got != "untitled" {
		t.Fatalf("expected 'untitled' for empty, got %q", got)
	}
}

func TestSafeFolderName_PreservesSpaces(t *testing.T) {
	got := textutil.SafeName("a knight on a white horse")
	if got != "a knight on a white horse" {
		t.Fatalf("expected spaces preserved, got %q", got)
	}
}

func TestSectionImage_StyleField(t *testing.T) {
	img := SectionImage{SectionIndex: 0, Title: "Castle", Style: "medievale", DriveLink: "https://drive.google.com/"}
	if img.Style != "medievale" {
		t.Fatalf("expected style 'medievale', got %q", img.Style)
	}
}

func TestSectionImage_Error(t *testing.T) {
	img := SectionImage{SectionIndex: 0, Title: "Castle", Style: "medievale", Error: "image generation failed"}
	if img.Error != "image generation failed" {
		t.Fatalf("expected error preserved, got %q", img.Error)
	}
}

func TestSection_StyleField(t *testing.T) {
	sec := Section{Title: "Knight", Style: "medievale"}
	if sec.Style != "medievale" {
		t.Fatalf("expected style 'medievale', got %q", sec.Style)
	}
}

func TestResult_Images(t *testing.T) {
	r := Result{Images: []SectionImage{
		{SectionIndex: 0, Title: "Castle", Style: "medievale"},
		{SectionIndex: 1, Title: "Knight", Style: "medievale"},
	}}
	if len(r.Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(r.Images))
	}
}

func TestSafeFolderName_SpecialCharsToSpaces(t *testing.T) {
	got := textutil.SafeName("test@#$%^&*()folder")
	if got != "test folder" {
		t.Fatalf("expected 'test folder', got %q", got)
	}
}
