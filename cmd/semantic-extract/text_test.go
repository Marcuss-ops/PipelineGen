package main

import (
	"os"
	"strings"
	"testing"
)

func TestVisualEventsFixturePreservesSentenceAndParagraphContext(t *testing.T) {
	data, err := os.ReadFile("testdata/visual_events.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(data))
	scenes := splitIntoScenes(text, 320)
	if len(scenes) != 1 {
		t.Fatalf("fixture should fit one semantic context window, got %d", len(scenes))
	}
	for _, sentence := range []string{
		"treatment plant at Collins Park in East Toledo",
		"factory is not part of the water emergency",
		"Tracy Rhodess describes buying supplies in Taylor, Michigan",
		"Construction has not started",
		"140ome",
		"Lake Mami",
	} {
		if !strings.Contains(scenes[0], sentence) {
			t.Errorf("fixture lost test context %q", sentence)
		}
	}
}

func TestVisualEventsFixtureCanBeChunkedWithoutSplittingSentences(t *testing.T) {
	data, err := os.ReadFile("testdata/visual_events.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(data))
	scenes := splitIntoScenes(text, 24)
	if len(scenes) < 2 {
		t.Fatalf("small target should create several scenes, got %d", len(scenes))
	}
	joined := strings.Join(scenes, " ")
	for _, sentence := range splitSentences(text) {
		if !strings.Contains(joined, sentence) {
			t.Errorf("split dropped sentence %q", sentence)
		}
	}
	for _, scene := range scenes {
		if strings.HasSuffix(scene, "Collins Park") || strings.HasSuffix(scene, "Lake") {
			t.Errorf("scene ends in a fragment: %q", scene)
		}
	}
}
