package job

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"
)

func TestActivityDataWithTracePreservesLegacyFieldsAndPayload(t *testing.T) {
	payload := map[string]any{"item_id": "item-7", "stage": "translation", "payload": map[string]any{"language": "pt-BR", "count": 3}}
	data := ActivityDataWithTrace("script.generate", "script.translate", "in_progress", "Translating", payload, ActivityTrace{
		RunID: "run-1", AttemptID: "attempt-1", ParentRunID: "parent-run", CorrelationID: "corr-1", Sequence: 4,
	})
	if data["kind"] != "script.generate" || data["sub_kind"] != "script.translate" || data["micro_kind"] != data["sub_kind"] {
		t.Fatalf("activity aliases = %#v", data)
	}
	if data["status"] != "running" || data["detail"] != "Translating" || data["item_id"] != "item-7" || data["stage"] != "translation" {
		t.Fatalf("activity root fields = %#v", data)
	}
	inner, ok := data["payload"].(map[string]any)
	if !ok || inner["language"] != "pt-BR" || inner["count"] != 3 {
		t.Fatalf("nested payload = %#v", data["payload"])
	}
	for key, want := range map[string]any{"run_id": "run-1", "attempt_id": "attempt-1", "parent_run_id": "parent-run", "correlation_id": "corr-1", "sequence": uint64(4)} {
		if data[key] != want {
			t.Errorf("%s = %#v, want %#v", key, data[key], want)
		}
	}
}

func TestActivityTraceSequenceSurvivesSerializationAndDetachedContext(t *testing.T) {
	ctx := WithActivityTrace(context.Background(), ActivityTrace{RunID: "run-1", AttemptID: "attempt-1", ParentRunID: "parent-run", CorrelationID: "corr-1"})
	first := ActivityTraceFromContext(ctx)
	data := ActivityDataWithTrace("script.generate", "script.prepare.validate", "running", "Validating", nil, first)
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal activity: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal activity: %v", err)
	}
	crossedBoundary := ActivityTraceFromData(decoded, ctx)
	if crossedBoundary.Sequence != first.Sequence || crossedBoundary.RunID != first.RunID || crossedBoundary.AttemptID != first.AttemptID {
		t.Fatalf("decoded trace = %+v, want producer trace %+v", crossedBoundary, first)
	}
	second := ActivityTraceFromContext(ctx)
	if second.Sequence != first.Sequence+1 {
		t.Fatalf("next sequence = %d, want %d", second.Sequence, first.Sequence+1)
	}
	detached := WithActivityTraceFrom(context.Background(), ctx)
	third := ActivityTraceFromContext(detached)
	if third.Sequence != second.Sequence+1 || third.RunID != first.RunID || third.ParentRunID != first.ParentRunID {
		t.Fatalf("detached trace = %+v, want shared next sequence and identity", third)
	}
}

func TestActivityTraceSequenceIsMonotonicUnderConcurrency(t *testing.T) {
	const count = 128
	ctx := WithActivityTrace(context.Background(), ActivityTrace{AttemptID: "attempt-1"})
	sequences := make([]uint64, count)
	var group sync.WaitGroup
	for index := range sequences {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			sequences[index] = ActivityTraceFromContext(ctx).Sequence
		}(index)
	}
	group.Wait()
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
	for index, sequence := range sequences {
		if sequence != uint64(index+1) {
			t.Fatalf("sequences = %v; expected contiguous 1..%d", sequences, count)
		}
	}
}

func TestActivityTraceFromDataAdvancesCounterPastProducerSequence(t *testing.T) {
	ctx := WithActivityTrace(context.Background(), ActivityTrace{AttemptID: "attempt-1"})
	trace := ActivityTraceFromData(map[string]any{"trace": map[string]any{"attempt_id": "attempt-1", "sequence": float64(9)}}, ctx)
	if trace.Sequence != 9 {
		t.Fatalf("producer sequence = %d, want 9", trace.Sequence)
	}
	if next := ActivityTraceFromContext(ctx).Sequence; next != 10 {
		t.Fatalf("next sequence = %d, want 10", next)
	}
}
