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
