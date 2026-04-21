package youtube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildYtDlpAuthArgs_UsesDetectedCookiesFile(t *testing.T) {
	home := t.TempDir()
	downloads := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(downloads, 0755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	cookiesPath := filepath.Join(downloads, "cookies.txt")
	if err := os.WriteFile(cookiesPath, []byte("# Netscape HTTP Cookie File\n"), 0644); err != nil {
		t.Fatalf("write cookies: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("VELOX_YTDLP_COOKIES_FILE", "")
	t.Setenv("YTDLP_COOKIES_FILE", "")
	t.Setenv("VELOX_YTDLP_COOKIES_FROM_BROWSER", "")
	t.Setenv("YTDLP_COOKIES_FROM_BROWSER", "")
	t.Setenv("VELOX_YTDLP_JS_RUNTIMES", "")
	t.Setenv("YTDLP_JS_RUNTIMES", "")
	t.Setenv("VELOX_YTDLP_REMOTE_COMPONENTS", "")
	t.Setenv("YTDLP_REMOTE_COMPONENTS", "")

	args := BuildYtDlpAuthArgs("", "")

	if !containsPair(args, "--cookies", cookiesPath) {
		t.Fatalf("expected cookies file %q in args, got %v", cookiesPath, args)
	}
	if !containsPair(args, "--remote-components", "ejs:github") {
		t.Fatalf("expected remote components fallback, got %v", args)
	}
	if hasCommand("node") && !containsPair(args, "--js-runtimes", "node") {
		t.Fatalf("expected node js runtime fallback, got %v", args)
	}
}

func TestYouTubeExtractorArgsVariantsIncludeAndroidFallbacks(t *testing.T) {
	variants := YouTubeExtractorArgsVariants()
	if len(variants) == 0 {
		t.Fatal("expected extractor args variants")
	}

	var hasAndroid, hasMweb bool
	for _, v := range variants {
		if strings.Contains(v, "android") {
			hasAndroid = true
		}
		if strings.Contains(v, "mweb") {
			hasMweb = true
		}
	}

	if !hasAndroid {
		t.Fatalf("expected android fallback in variants, got %v", variants)
	}
	if !hasMweb {
		t.Fatalf("expected mweb fallback in variants, got %v", variants)
	}
}

func containsPair(args []string, key, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
