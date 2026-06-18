package drive

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestDriveConfigResolveFolder(t *testing.T) {
	t.Run("returns MediaRootFolder when set", func(t *testing.T) {
		d := config.DriveConfig{MediaRootFolder: "media-root", ImagesRootFolder: "images-root"}
		if got := d.ResolveFolder(d.ImagesRootFolder); got != "media-root" {
			t.Fatalf("expected media-root, got %q", got)
		}
	})
	t.Run("returns specific root when no MediaRoot", func(t *testing.T) {
		d := config.DriveConfig{ImagesRootFolder: "images-root"}
		if got := d.ResolveFolder(d.ImagesRootFolder); got != "images-root" {
			t.Fatalf("expected images-root, got %q", got)
		}
	})
	t.Run("convenience methods use ResolveFolder", func(t *testing.T) {
		d := config.DriveConfig{
			MediaRootFolder:        "media-root",
			StockRootFolder:        "stock",
			ClipsRootFolder:        "clips",
			VoiceoverRootFolder:    "voiceover",
			ArtlistRootFolder:      "artlist",
			BooksRootFolder:        "books",
			ScriptsRootFolder:      "scripts",
			ImagesRootFolder:       "images",
			VideoAIRootFolder:      "video-ai",
			CopertineRootFolder:    "copertine",
			SoundEffectsRootFolder: "sfx",
		}
		for name, got := range map[string]string{
			"StockFolder":        d.StockFolder(),
			"ClipsFolder":        d.ClipsFolder(),
			"VoiceoverFolder":    d.VoiceoverFolder(),
			"ArtlistFolder":      d.ArtlistFolder(),
			"BooksFolder":        d.BooksFolder(),
			"ScriptsFolder":      d.ScriptsFolder(),
			"ImagesFolder":       d.ImagesFolder(),
			"VideoAIFolder":      d.VideoAIFolder(),
			"CopertineFolder":    d.CopertineFolder(),
			"SoundEffectsFolder": d.SoundEffectsFolder(),
		} {
			if got != "media-root" {
				t.Fatalf("%s expected media-root with MediaRoot set, got %q", name, got)
			}
		}
	})
}
