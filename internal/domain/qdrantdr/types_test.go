package qdrantdr

import (
	"encoding/json"
	"testing"
	"time"
)

// TestSnapshotDescription_UnmarshalJSON_NaiveTime is the regression gate
// for the decode fix shipped 2026-08-22: real Qdrant returns creation_time
// WITHOUT a timezone suffix ("2006-01-02T15:04:05"), which Go's time.Time
// JSON decoder rejects ("cannot parse ... as Z07:00"). Every
// CreateSnapshot / ListSnapshots call failed against live Qdrant before
// this hook. Naive timestamps decode as UTC.
func TestSnapshotDescription_UnmarshalJSON_NaiveTime(t *testing.T) {
	raw := `{"name":"media_assets-1.snapshot","creation_time":"2026-08-22T10:55:49","size":4496896,"checksum":"abc"}`
	var s SnapshotDescription
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal naive creation_time: %v", err)
	}
	if s.Name != "media_assets-1.snapshot" || s.Size != 4496896 || s.Checksum != "abc" {
		t.Errorf("fields not decoded: %+v", s)
	}
	want := time.Date(2026, 8, 22, 10, 55, 49, 0, time.UTC)
	if !s.CreationTime.Equal(want) {
		t.Errorf("CreationTime = %v, want %v", s.CreationTime, want)
	}
}

// TestSnapshotDescription_UnmarshalJSON_RFC3339 pins that the hook still
// accepts the RFC3339 form (older fixtures / future Qdrant versions).
func TestSnapshotDescription_UnmarshalJSON_RFC3339(t *testing.T) {
	raw := `{"name":"x.snapshot","creation_time":"2026-06-01T12:00:00Z","size":1}`
	var s SnapshotDescription
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal RFC3339 creation_time: %v", err)
	}
	want := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if !s.CreationTime.Equal(want) {
		t.Errorf("CreationTime = %v, want %v", s.CreationTime, want)
	}
}

// TestSnapshotDescription_UnmarshalJSON_EmptyTime covers the mock shape
// that omits creation_time entirely (zero-value field stays zero).
func TestSnapshotDescription_UnmarshalJSON_EmptyTime(t *testing.T) {
	raw := `{"name":"x.snapshot","size":4096}`
	var s SnapshotDescription
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal without creation_time: %v", err)
	}
	if !s.CreationTime.IsZero() {
		t.Errorf("CreationTime = %v, want zero", s.CreationTime)
	}
}

// TestSnapshotDescription_UnmarshalJSON_BadTime fails closed on garbage.
func TestSnapshotDescription_UnmarshalJSON_BadTime(t *testing.T) {
	raw := `{"name":"x.snapshot","creation_time":"not-a-time","size":1}`
	var s SnapshotDescription
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		t.Fatalf("expected error for malformed creation_time, got nil")
	}
}
