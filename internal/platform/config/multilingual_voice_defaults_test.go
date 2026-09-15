package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMultilingualVoiceDefaultsAreExplicitAndMale(t *testing.T) {
	want := map[string]string{
		"it":    "fr-FR-RemyMultilingualNeural",
		"en":    "en-US-ChristopherNeural",
		"pl":    "pl-PL-MarekNeural",
		"ru":    "ru-RU-DmitryNeural",
		"de":    "de-DE-FlorianMultilingualNeural",
		"es":    "es-ES-AlvaroNeural",
		"pt-BR": "pt-BR-AntonioNeural",
		"fr":    "fr-FR-RemyMultilingualNeural",
		"tr":    "tr-TR-AhmetNeural",
		"id":    "id-ID-ArdiNeural",
	}

	root := repoRoot(t)
	for _, rel := range []string{
		"config.yaml",
		"config/multilingual.yaml",
		"config.example.yaml",
		"config.production.example.yaml",
	} {
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatal(err)
			}
			var cfg Config
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("decode %s: %v", rel, err)
			}
			if got := len(cfg.Media.Multilingual.Languages); got != len(want) {
				t.Fatalf("languages=%d want=%d", got, len(want))
			}
			for _, spec := range cfg.Media.Multilingual.Languages {
				voice, ok := want[spec.Code]
				if !ok {
					t.Errorf("unexpected language %q", spec.Code)
					continue
				}
				if !spec.GenerateTTS {
					t.Errorf("%s: generate_tts=false", spec.Code)
				}
				if spec.EdgeTTSVoice != voice {
					t.Errorf("%s: voice=%q want=%q", spec.Code, spec.EdgeTTSVoice, voice)
				}
			}
		})
	}
}

// TestMultilingualSourcePriority_DefaultAndProductionValue pins the acquisition
// order knob (Sept 2026): an unset key MUST behave exactly like the historical
// chain (captions_first) so an old/partial config can never silently reorder
// transcription, while the shipped production config opts into whisper_first.
func TestMultilingualSourcePriority_DefaultAndProductionValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("media:\n  multilingual:\n    enabled: true\n    source_language: \"en\"\n"), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	loaded, err := GetFromPath(path)
	if err != nil {
		t.Fatalf("GetFromPath: %v", err)
	}
	if got := loaded.Media.Multilingual.SourcePriority; got != "captions_first" {
		t.Fatalf("default SourcePriority = %q, want %q (unset must keep the canonical captions-first chain)", got, "captions_first")
	}

	data, err := os.ReadFile(filepath.Join(repoRoot(t), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var prod Config
	if err := yaml.Unmarshal(data, &prod); err != nil {
		t.Fatalf("decode config.yaml: %v", err)
	}
	if got := prod.Media.Multilingual.SourcePriority; got != "whisper_first" {
		t.Fatalf("config.yaml SourcePriority = %q, want %q (production runs the local Whisper transcriber first)", got, "whisper_first")
	}
}
