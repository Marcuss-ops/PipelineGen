package local

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type structuredProgressCapture struct {
	progress int
	message  string
	data     map[string]any
	calls    int
}

func (s *structuredProgressCapture) SetProgress(_ context.Context, _ string, progress int, message string) error {
	s.progress = progress
	s.message = message
	s.data = nil
	s.calls++
	return nil
}

func (s *structuredProgressCapture) SetProgressData(_ context.Context, _ string, progress int, message string, data map[string]any) error {
	s.progress = progress
	s.message = message
	s.data = data
	s.calls++
	return nil
}

func TestProgressCoalescerImmediateWritePreservesActivityData(t *testing.T) {
	sink := &structuredProgressCapture{}
	coalescer := NewProgressCoalescer(sink, ProgressCoalesceConfig{}, nil)
	data, err := json.Marshal(map[string]any{
		"kind": "script.generate", "sub_kind": "script.postprocess.translation",
		"status": "running", "detail": "Translating script",
		"payload": map[string]any{"processor": "translation"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := coalescer.TakeData(context.Background(), "job-1", 73, "Translating script", data); err != nil {
		t.Fatalf("TakeData: %v", err)
	}
	if sink.calls != 1 || sink.progress != 73 || sink.message != "Translating script" {
		t.Fatalf("sink update = %+v, want one update preserving progress and message", sink)
	}
	if sink.data["sub_kind"] != "script.postprocess.translation" || sink.data["status"] != "running" {
		t.Fatalf("structured data = %#v", sink.data)
	}
}

func TestProgressCoalescerBufferedWritePreservesLatestActivityData(t *testing.T) {
	sink := &structuredProgressCapture{}
	coalescer := NewProgressCoalescer(sink, ProgressCoalesceConfig{Window: time.Second}, nil)
	first, _ := json.Marshal(map[string]any{"sub_kind": "script.prepare.validate", "status": "running"})
	latest, _ := json.Marshal(map[string]any{
		"sub_kind": "script.postprocess.voiceover", "status": "running",
		"payload": map[string]any{"processor": "voiceover"},
	})

	if err := coalescer.TakeData(context.Background(), "job-1", 20, "Validating", first); err != nil {
		t.Fatalf("TakeData first: %v", err)
	}
	if err := coalescer.TakeData(context.Background(), "job-1", 81, "Running voiceover", latest); err != nil {
		t.Fatalf("TakeData latest: %v", err)
	}
	if sink.calls != 0 {
		t.Fatalf("buffered updates wrote immediately %d times, want 0", sink.calls)
	}

	update, err := coalescer.FlushJob("job-1")
	if err != nil || update == nil {
		t.Fatalf("FlushJob = (%+v, %v), want buffered update", update, err)
	}
	if err := coalescer.setProgress(context.Background(), "job-1", *update); err != nil {
		t.Fatalf("setProgress: %v", err)
	}
	if sink.calls != 1 || sink.progress != 81 || sink.message != "Running voiceover" {
		t.Fatalf("latest sink update = %+v, want latest progress and message", sink)
	}
	if sink.data["sub_kind"] != "script.postprocess.voiceover" {
		t.Fatalf("latest structured data = %#v, want voiceover sub_kind", sink.data)
	}
	payload, ok := sink.data["payload"].(map[string]any)
	if !ok || payload["processor"] != "voiceover" {
		t.Fatalf("latest nested payload = %#v, want processor=voiceover", sink.data["payload"])
	}
}
