package storage

import (
	"path/filepath"
	"testing"
)

func TestResolver_ClipYoutube(t *testing.T) {
	r := NewResolver("data/media", "drive-root-id")

	dest, err := r.Resolve(AssetDestinationRequest{
		Source:    "youtube",
		MediaType: "clip",
		Group:     "travel",
		Hash:      "abc123",
		Ext:       ".mp4",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantRel := filepath.Join("clips", "youtube", "travel", "abc123.mp4")
	if dest.RelativePath != wantRel {
		t.Errorf("RelativePath = %q, want %q", dest.RelativePath, wantRel)
	}
	if dest.LocalPath != filepath.Join("data/media", wantRel) {
		t.Errorf("LocalPath = %q", dest.LocalPath)
	}
	if dest.DriveFolderPath != filepath.Join("clips", "youtube", "travel") {
		t.Errorf("DriveFolderPath = %q", dest.DriveFolderPath)
	}
	if dest.DriveFileName != "abc123.mp4" {
		t.Errorf("DriveFileName = %q", dest.DriveFileName)
	}
}

func TestResolver_ClipArtlist(t *testing.T) {
	r := NewResolver("data/media", "drive-root-id")

	dest, err := r.Resolve(AssetDestinationRequest{
		Source:    "artlist",
		MediaType: "clip",
		Group:     "medieval",
		Hash:      "def456",
		Ext:       ".mp4",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantRel := filepath.Join("clips", "artlist", "medieval", "def456.mp4")
	if dest.RelativePath != wantRel {
		t.Errorf("RelativePath = %q, want %q", dest.RelativePath, wantRel)
	}
}

func TestResolver_ClipStock(t *testing.T) {
	r := NewResolver("data/media", "drive-root-id")

	dest, err := r.Resolve(AssetDestinationRequest{
		Source:    "stock",
		MediaType: "clip",
		Group:     "nature",
		Provider:  "pexels",
		Hash:      "ghi789",
		Ext:       ".mp4",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantRel := filepath.Join("clips", "stock", "pexels", "nature", "ghi789.mp4")
	if dest.RelativePath != wantRel {
		t.Errorf("RelativePath = %q, want %q", dest.RelativePath, wantRel)
	}
}

func TestResolver_ImageGenerated(t *testing.T) {
	r := NewResolver("data/media", "drive-root-id")

	dest, err := r.Resolve(AssetDestinationRequest{
		Source:    "image",
		MediaType: "image",
		Style:     "medievale",
		Subject:   "Castello Medievale",
		Hash:      "hash123",
		Ext:       ".png",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantRel := filepath.Join("images", "generated", "medievale", "castello-medievale", "hash123.png")
	if dest.RelativePath != wantRel {
		t.Errorf("RelativePath = %q, want %q", dest.RelativePath, wantRel)
	}
}

func TestResolver_ImageDownloaded(t *testing.T) {
	r := NewResolver("data/media", "drive-root-id")

	dest, err := r.Resolve(AssetDestinationRequest{
		Source:    "wikimedia",
		MediaType: "image",
		Subject:   "Mona Lisa",
		Hash:      "hash456",
		Ext:       ".jpg",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantRel := filepath.Join("images", "downloaded", "wikimedia", "mona-lisa", "hash456.jpg")
	if dest.RelativePath != wantRel {
		t.Errorf("RelativePath = %q, want %q", dest.RelativePath, wantRel)
	}
}

func TestResolver_ImageVideo(t *testing.T) {
	r := NewResolver("data/media", "drive-root-id")

	dest, err := r.Resolve(AssetDestinationRequest{
		Source:    "image",
		MediaType: "image_video",
		Style:     "gothic",
		Subject:   "Cattedrale",
		Hash:      "vid123",
		Ext:       ".mp4",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantRel := filepath.Join("image_videos", "fullscreen", "gothic", "cattedrale", "vid123.mp4")
	if dest.RelativePath != wantRel {
		t.Errorf("RelativePath = %q, want %q", dest.RelativePath, wantRel)
	}
}

func TestResolver_Voiceover(t *testing.T) {
	r := NewResolver("data/media", "drive-root-id")

	dest, err := r.Resolve(AssetDestinationRequest{
		Source:    "voiceover",
		MediaType: "voiceover",
		Provider:  "elevenlabs",
		Voice:     "chiara",
		Hash:      "vo123",
		Ext:       ".mp3",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantRel := filepath.Join("voiceovers", "elevenlabs", "chiara", "vo123.mp3")
	if dest.RelativePath != wantRel {
		t.Errorf("RelativePath = %q, want %q", dest.RelativePath, wantRel)
	}
}

func TestResolver_ExportVideo(t *testing.T) {
	r := NewResolver("data/media", "drive-root-id")

	dest, err := r.Resolve(AssetDestinationRequest{
		Source:    "export",
		MediaType: "export_video",
		Project:   "my-documentary",
		Date:      "2026-05-25",
		Hash:      "exp123",
		Ext:       ".mp4",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantRel := filepath.Join("exports", "videos", "my-documentary", "2026-05-25", "exp123.mp4")
	if dest.RelativePath != wantRel {
		t.Errorf("RelativePath = %q, want %q", dest.RelativePath, wantRel)
	}
}

func TestResolver_Validation(t *testing.T) {
	r := NewResolver("data/media", "drive-root-id")

	_, err := r.Resolve(AssetDestinationRequest{})
	if err == nil {
		t.Fatal("expected validation error for empty request")
	}

	_, err = r.Resolve(AssetDestinationRequest{Source: "youtube", MediaType: "clip"})
	if err == nil {
		t.Fatal("expected validation error for missing hash")
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Castello Medievale", "castello-medievale"},
		{"  Hello   World  ", "hello-world"},
		{"", "untitled"},
		{"Caffè", "caffè"},
	}
	for _, tt := range tests {
		got := slugify(tt.in)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
