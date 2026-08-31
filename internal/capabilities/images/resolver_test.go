package images

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ── Test helpers ──────────────────────────────────────────────────────

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "image_destinations.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test yaml: %v", err)
	}
	return path
}

const fallback = "FALLBACK_FOLDER_ID"

// ── TestResolve_Table covers routing + soft-fallback semantics ─────────

func TestResolve_Table(t *testing.T) {
	type tc struct {
		name string
		key  string
		want string
	}

	r := NewYamlResolverFromMap(map[string]string{
		"ai-images/cinematic": "FOLDER_CINEMATIC",
		"ai-images/anime":     "FOLDER_ANIME",
		"ai-images/default":   "",
		"ai-images/empty":     "",
	}, fallback)

	cases := []tc{
		{name: "route_cinematic", key: "ai-images/cinematic", want: "FOLDER_CINEMATIC"},
		{name: "route_anime", key: "ai-images/anime", want: "FOLDER_ANIME"},
		{name: "case_insensitive_upper", key: "AI-IMAGES/CINEMATIC", want: "FOLDER_CINEMATIC"},
		{name: "case_insensitive_mixed", key: "Ai-Images/Anime", want: "FOLDER_ANIME"},
		{name: "ai_images_default_key", key: "ai-images/default", want: fallback},
		{name: "empty_mapped_value_falls_back", key: "ai-images/empty", want: fallback},
		{name: "unknown_key_falls_back", key: "ai-images/medieval", want: fallback},
		{name: "empty_key_falls_back", key: "", want: fallback},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dest, err := r.Resolve(c.key)
			if err != nil {
				t.Fatalf("Resolve(%q) err=%v, want nil", c.key, err)
			}
			if dest.DriveFolderID != c.want {
				t.Fatalf("Resolve(%q).DriveFolderID = %q, want %q",
					c.key, dest.DriveFolderID, c.want)
			}
		})
	}
}

// ── Fail-closed path: no fallback returns ErrDestinationNotFound ────────

func TestResolve_NoFallback_ReturnsError(t *testing.T) {
	r := NewYamlResolverFromMap(map[string]string{
		"ai-images/cinematic": "FOLDER_CINEMATIC",
	}, "" /* NO fallback */)

	dest, err := r.Resolve("ai-images/cinematic")
	if err != nil {
		t.Fatalf("known key should not error: %v", err)
	}
	if dest.DriveFolderID != "FOLDER_CINEMATIC" {
		t.Fatalf("known key DriveFolderID = %q", dest.DriveFolderID)
	}

	_, err = r.Resolve("ai-images/unknown")
	if err == nil {
		t.Fatal("expected ErrDestinationNotFound for unknown key with no fallback")
	}
	if !errors.Is(err, ErrDestinationNotFound) {
		t.Fatalf("err = %v, want errors.Is(_, ErrDestinationNotFound)=true", err)
	}
}

// ── Constructor: from YAML file ───────────────────────────────────────

func TestNewYamlResolver_FromFile(t *testing.T) {
	path := writeYAML(t, `
destinations:
  ai-images/cinematic: "FOLDER_FROM_FILE"
  ai-images/default: ""
`)

	r, err := NewYamlResolver(path, fallback)
	if err != nil {
		t.Fatalf("NewYamlResolver: %v", err)
	}

	dest, err := r.Resolve("ai-images/cinematic")
	if err != nil {
		t.Fatalf("Resolve(cinematic): %v", err)
	}
	if dest.DriveFolderID != "FOLDER_FROM_FILE" {
		t.Fatalf("Resolve(cinematic) = %q, want FOLDER_FROM_FILE", dest.DriveFolderID)
	}

	dest, err = r.Resolve("unknown")
	if err != nil {
		t.Fatalf("Resolve(unknown): %v", err)
	}
	if dest.DriveFolderID != fallback {
		t.Fatalf("Resolve(unknown) = %q, want fallback %q", dest.DriveFolderID, fallback)
	}
}

// ── Constructor: empty path fails fast ─────────────────────────────────

func TestNewYamlResolver_EmptyPath_Error(t *testing.T) {
	_, err := NewYamlResolver("", fallback)
	if err == nil {
		t.Fatal("expected error for empty yamlPath")
	}
}

// ── Constructor: missing file ──────────────────────────────────────────

func TestNewYamlResolver_MissingFile_Error(t *testing.T) {
	_, err := NewYamlResolver("/does/not/exist/image_destinations.yaml", fallback)
	if err == nil {
		t.Fatal("expected error for missing yamlPath")
	}
}

// ── Constructor: malformed YAML ────────────────────────────────────────

func TestNewYamlResolver_MalformedYAML_Error(t *testing.T) {
	path := writeYAML(t, `destinations:
  this is not: [valid yaml
   - broken
`)
	_, err := NewYamlResolver(path, fallback)
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

// ── Constructor: empty YAML all routes fall back ────────────────────────

func TestNewYamlResolver_EmptyYAML_AllRoutesGoToFallback(t *testing.T) {
	path := writeYAML(t, `destinations: {}`)

	r, err := NewYamlResolver(path, fallback)
	if err != nil {
		t.Fatalf("NewYamlResolver: %v", err)
	}

	dest, err := r.Resolve("ai-images/anything")
	if err != nil {
		t.Fatalf("Resolve on empty map should fall back: %v", err)
	}
	if dest.DriveFolderID != fallback {
		t.Fatalf("DriveFolderID = %q, want fallback %q", dest.DriveFolderID, fallback)
	}
}

// ── Compile-time interface assertion (Pattern 0) ──────────────────────

func TestYamlResolver_ImplementsInterface(t *testing.T) {
	var _ DestinationResolver = (*YamlResolver)(nil)
	var _ DestinationResolver = NewYamlResolverFromMap(map[string]string{}, fallback)
}
