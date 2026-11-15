package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func jobDocument(t *testing.T, phrases []map[string]any, speech []map[string]any, style map[string]any) []byte {
	t.Helper()
	doc := map[string]any{
		"job": map[string]any{
			"payload": map[string]any{
				"items": []any{
					map[string]any{
						"output": map[string]any{
							"render": map[string]any{
								"subtitles": map[string]any{
									"enabled": true,
									"mode":    "burn",
									"style":   style,
								},
							},
						},
					},
				},
			},
		},
		"result": map[string]any{
			"result": map[string]any{
				"phrase_timings":       phrases,
				"scene_speech_timings": speech,
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal job document: %v", err)
	}
	return raw
}

func TestBuildSubtitlesUsesCertifiedOffsetsAndStyle(t *testing.T) {
	phrases := []map[string]any{
		{"scene_index": 1, "timeline_start_us": 15_000_000},
		{"scene_index": 2, "timeline_start_us": 29_664_000},
	}
	speech := []map[string]any{
		{
			"scene_id": "scene-01",
			"words": []any{
				map[string]any{"text": "Barack", "start_us": 50_000, "end_us": 465_667},
				map[string]any{"text": "Obama", "start_us": 465_667, "end_us": 1_028_167},
			},
		},
		{
			"scene_id": "scene-02",
			"words": []any{
				map[string]any{"text": "Nato", "start_us": 50_000, "end_us": 564_500},
			},
		},
	}
	style := map[string]any{
		"font":         "Poppins",
		"position":     "bottom_center",
		"color":        "#FFD400",
		"font_size_px": 64,
		"stroke":       map[string]any{"color": "#000000", "width": 6},
		"shadow":       map[string]any{"color": "#000000", "opacity": 0.9, "offset_y": 6},
	}
	cues, gotStyle, err := buildSubtitles(jobDocument(t, phrases, speech, style))
	if err != nil {
		t.Fatalf("buildSubtitles: %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("cues = %d, want 2", len(cues))
	}
	if cues[0].SceneID != "scene-01" || cues[0].Start != 15.05 {
		t.Fatalf("first cue = %+v, want scene-01 at 15.05s", cues[0])
	}
	if cues[1].SceneID != "scene-02" || cues[1].Start != 29.714 {
		t.Fatalf("second cue = %+v, want scene-02 at 29.714s", cues[1])
	}
	if gotStyle.Font != "Poppins" || gotStyle.FontSize != 64 || gotStyle.Primary != "#FFD400" {
		t.Fatalf("style = %+v", gotStyle)
	}
	if gotStyle.OutlineW != 6 || gotStyle.Shadow != 6 || gotStyle.Alignment != 2 {
		t.Fatalf("stroke/shadow/alignment = %+v", gotStyle)
	}
}

func TestBuildSubtitlesRejectsMissingPhraseTimings(t *testing.T) {
	speech := []map[string]any{
		{"scene_id": "scene-01", "words": []any{map[string]any{"text": "x", "start_us": 0, "end_us": 1}}},
	}
	_, _, err := buildSubtitles(jobDocument(t, nil, speech, map[string]any{}))
	if err == nil || !strings.Contains(err.Error(), "phrase timings") {
		t.Fatalf("err = %v, want a missing-phrase-timings error", err)
	}
}

func TestBuildSceneCuesWrapsLongText(t *testing.T) {
	scene := assSceneSpeech{SceneID: "scene-01", Words: []assWord{
		{Text: "Barack", StartUS: 0, EndUS: 400_000},
		{Text: "Obama", StartUS: 400_000, EndUS: 800_000},
		{Text: "è", StartUS: 800_000, EndUS: 900_000},
		{Text: "entrato", StartUS: 900_000, EndUS: 1_300_000},
		{Text: "in", StartUS: 1_300_000, EndUS: 1_400_000},
		{Text: "carica", StartUS: 1_400_000, EndUS: 1_800_000},
		{Text: "il", StartUS: 1_800_000, EndUS: 1_900_000},
		{Text: "20", StartUS: 1_900_000, EndUS: 2_000_000},
		{Text: "gennaio", StartUS: 2_000_000, EndUS: 2_400_000},
		{Text: "2009.", StartUS: 2_400_000, EndUS: 3_000_000},
	}}
	cues := buildSceneCues(scene, 0)
	if len(cues) != 2 {
		t.Fatalf("cues = %d, want 2 (the 42-char break before gennaio)", len(cues))
	}
	if !strings.Contains(cues[len(cues)-1].Text, "2009.") {
		t.Fatalf("last cue text = %q", cues[len(cues)-1].Text)
	}
	if cues[len(cues)-1].End < 3.2 {
		t.Fatalf("cue end = %.2f, want the 0.25s tail", cues[len(cues)-1].End)
	}
}

func TestWriteASSContainsStyleAndDialogue(t *testing.T) {
	cues := []subtitleCue{{SceneID: "scene-01", Start: 15.05, End: 17.2, Text: "Barack Obama è entrato in carica il 20 gennaio 2009 come 44º presidente"}}
	var buf bytes.Buffer
	if err := writeASS(&buf, cues, defaultASSstyle()); err != nil {
		t.Fatalf("writeASS: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"[Script Info]",
		"PlayResX: 1920",
		"Style: Default,Poppins,64,&H0000D4FF",
		"Dialogue: 0,0:00:15.05,0:00:17.20,Default,,0,0,0,,",
		`\N`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ASS missing %q:\n%s", want, out)
		}
	}
}

func TestASSColour(t *testing.T) {
	got, err := assColour("#FFD400", 0)
	if err != nil {
		t.Fatalf("assColour: %v", err)
	}
	if got != "&H0000D4FF" {
		t.Fatalf("assColour = %q, want &H0000D4FF", got)
	}
	if _, err := assColour("nope", 0); err == nil {
		t.Fatal("assColour accepted an invalid colour")
	}
}

func TestASSAlignment(t *testing.T) {
	cases := map[string]int{
		"bottom_center": 2,
		"top_center":    8,
		"center":        5,
		"top_left":      7,
		"bottom_right":  3,
		"":              2,
	}
	for in, want := range cases {
		if got := assAlignment(in); got != want {
			t.Fatalf("assAlignment(%q) = %d, want %d", in, got, want)
		}
	}
}
