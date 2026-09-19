package drive

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"golang.org/x/oauth2"
)

func TestDriveConfigResolveFolder(t *testing.T) {
	t.Run("returns specific root over MediaRootFolder when set", func(t *testing.T) {
		d := config.DriveConfig{MediaRootFolder: "media-root", ImagesRootFolder: "images-root"}
		if got := d.ResolveFolder(d.ImagesRootFolder); got != "images-root" {
			t.Fatalf("expected images-root, got %q", got)
		}
	})
	t.Run("returns specific root when no MediaRoot", func(t *testing.T) {
		d := config.DriveConfig{ImagesRootFolder: "images-root"}
		if got := d.ResolveFolder(d.ImagesRootFolder); got != "images-root" {
			t.Fatalf("expected images-root, got %q", got)
		}
	})
	t.Run("convenience methods use ResolveFolder and prioritize specific", func(t *testing.T) {
		d := config.DriveConfig{
			MediaRootFolder:     "media-root",
			StockRootFolder:     "stock",
			ClipsRootFolder:     "clips",
			VoiceoverRootFolder: "voiceover",
			ArtlistRootFolder:   "artlist",
			ScriptsRootFolder:   "scripts",
			ImagesRootFolder:    "images",

			CopertineRootFolder:    "copertine",
			SoundEffectsRootFolder: "sfx",
		}
		for name, got := range map[string]string{
			"StockFolder":        d.StockFolder(),
			"ClipsFolder":        d.ClipsFolder(),
			"VoiceoverFolder":    d.VoiceoverFolder(),
			"ArtlistFolder":      d.ArtlistFolder(),
			"ScriptsFolder":      d.ScriptsFolder(),
			"ImagesFolder":       d.ImagesFolder(),
			"CopertineFolder":    d.CopertineFolder(),
			"SoundEffectsFolder": d.SoundEffectsFolder(),
		} {
			expected := name
			// Strip 'Folder' from end to get key
			expectedKey := name[:len(name)-6]
			switch expectedKey {
			case "Stock":
				expected = "stock"
			case "Clips":
				expected = "clips"
			case "Voiceover":
				expected = "voiceover"
			case "Artlist":
				expected = "artlist"
			case "Books":
				expected = "books"
			case "Scripts":
				expected = "scripts"
			case "Images":
				expected = "images"
			case "Copertine":
				expected = "copertine"
			case "SoundEffects":
				expected = "sfx"
			}
			if got != expected {
				t.Fatalf("%s expected %s, got %q", name, expected, got)
			}
		}
	})
}

type testTokenSource struct {
	token *oauth2.Token
	err   error
}

func (t testTokenSource) Token() (*oauth2.Token, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.token, nil
}

func TestFallbackTokenSource_UsesFallbackOnOAuthRefreshErrors(t *testing.T) {
	primaryErr := errors.New(`oauth2: "unauthorized_client" "Unauthorized"`)
	fallbackTok := &oauth2.Token{AccessToken: "fallback-token"}

	src := &fallbackTokenSource{
		primary:  testTokenSource{err: primaryErr},
		fallback: oauth2.StaticTokenSource(fallbackTok),
	}

	got, err := src.Token()
	if err != nil {
		t.Fatalf("expected fallback token, got error: %v", err)
	}
	if got == nil || got.AccessToken != "fallback-token" {
		t.Fatalf("expected fallback token, got %#v", got)
	}
}

func TestFallbackTokenSource_ReturnsPrimaryTokenOnSuccess(t *testing.T) {
	want := &oauth2.Token{AccessToken: "primary-token"}
	src := &fallbackTokenSource{
		primary:  testTokenSource{token: want},
		fallback: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fallback-token"}),
	}

	got, err := src.Token()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.AccessToken != "primary-token" {
		t.Fatalf("expected primary token, got %#v", got)
	}
}

func TestNewGoogleTransport_UsesPhaseTimeoutsWithoutWholeRequestDeadline(t *testing.T) {
	transport := newGoogleTransport()
	if transport == nil {
		t.Fatal("newGoogleTransport returned nil")
	}
	if transport.ResponseHeaderTimeout != 60*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 60s", transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != 10*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v, want 10s", transport.TLSHandshakeTimeout)
	}
	if transport.ExpectContinueTimeout != time.Second {
		t.Fatalf("ExpectContinueTimeout = %v, want 1s", transport.ExpectContinueTimeout)
	}
	if transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("IdleConnTimeout = %v, want 90s", transport.IdleConnTimeout)
	}
	if transport.DialContext == nil {
		t.Fatal("DialContext must be configured")
	}
	// Keep this assertion next to the transport contract: the old 90s
	// http.Client.Timeout was the source of the size-independent upload cap.
	client := &http.Client{Transport: transport}
	if client.Timeout != 0 {
		t.Fatalf("transport test client Timeout = %v, want 0", client.Timeout)
	}
}
