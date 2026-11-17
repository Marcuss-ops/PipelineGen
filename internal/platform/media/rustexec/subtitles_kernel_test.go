package rustexec

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizeSubtitleCuesUsesRustKernel(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":true,"cues":[{"start_ms":0,"end_ms":1000,"text":"hello"}]}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	cues, err := adapter.NormalizeSubtitleCues(context.Background(), []SubtitleCue{
		{StartMs: 0, EndMs: 5000, Text: "a long whisper segment that must be split into readable captions by the rust kernel"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 1 || cues[0].Text != "hello" {
		t.Fatalf("cues = %+v", cues)
	}
	if !strings.Contains(string(runner.input), `"operation":"subtitles_kernel"`) {
		t.Fatalf("request did not carry the subtitles_kernel op: %s", runner.input)
	}
	if !strings.Contains(string(runner.input), `"mode":"normalize"`) {
		t.Fatalf("request did not carry the normalize mode: %s", runner.input)
	}
}

func TestNormalizeSubtitleCuesEmptyInputSkipsWorker(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":false,"error":"must not be called"}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	cues, err := adapter.NormalizeSubtitleCues(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 0 {
		t.Fatalf("cues = %+v", cues)
	}
	if len(runner.input) != 0 {
		t.Fatalf("empty input must not reach the worker: %s", runner.input)
	}
}

func TestCompileSubtitleASSRejectsEmptyCuesLocally(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":true}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.CompileSubtitleASS(context.Background(), nil, "shorts-v1"); err == nil {
		t.Fatal("expected empty cues to fail closed")
	}
}

func TestCompileSubtitleASSVerifiesCueInvariants(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":true,"ass_content":"[Script Info]","cue_count":1,"last_cue_end_ms":9999}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.CompileSubtitleASS(context.Background(), []SubtitleCue{{StartMs: 0, EndMs: 1000, Text: "hi"}}, "shorts-v1")
	if err == nil || !strings.Contains(err.Error(), "last cue end") {
		t.Fatalf("expected last cue end mismatch, got %v", err)
	}
}

func TestDistributeSubtitleCuesPreservesWindows(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":true,"cues":[{"start_ms":0,"end_ms":1000,"text":"one"},{"start_ms":990,"end_ms":2000,"text":"two"}]}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.DistributeSubtitleCues(context.Background(), []SubtitleCue{
		{StartMs: 0, EndMs: 1000, Text: ""},
		{StartMs: 1000, EndMs: 2000, Text: ""},
	}, "one two")
	if err == nil || !strings.Contains(err.Error(), "moved the window") {
		t.Fatalf("expected window drift to fail closed, got %v", err)
	}
}

func TestParseSubtitleTextRejectsEmptyCueList(t *testing.T) {
	runner := &semanticRunner{output: []byte(`{"ok":true,"plain_text":"","cues":[]}`)}
	adapter, err := NewSemanticEngineAdapter(executorForSemantic(runner))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := adapter.ParseSubtitleText(context.Background(), "subs.vtt", "WEBVTT"); err == nil {
		t.Fatal("expected an empty cue list to fail closed")
	}
}
