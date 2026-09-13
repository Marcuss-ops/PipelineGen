package media

import (
	"fmt"
	"reflect"
	"testing"
)

func TestMediaAssetRecord_TitleOrName(t *testing.T) {
	if got := (&MediaAssetRecord{Name: "n", Title: "t"}).TitleOrName(); got != "t" {
		t.Fatalf("TitleOrName = %q, want t", got)
	}
	if got := (&MediaAssetRecord{Name: "n"}).TitleOrName(); got != "n" {
		t.Fatalf("TitleOrName fallback = %q, want n", got)
	}
	if got := (*MediaAssetRecord)(nil).TitleOrName(); got != "" {
		t.Fatalf("nil TitleOrName = %q, want empty", got)
	}
}

func TestMediaAssetRecord_MetadataAccessors(t *testing.T) {
	rec := &MediaAssetRecord{MetadataJSON: `{
		"clip_summary": "summary",
		"topics": ["a", "b"],
		"speakers": ["s1"],
		"start_sec": 1.25,
		"count": 7
	}`}
	if got := rec.MetadataString("clip_summary"); got != "summary" {
		t.Fatalf("MetadataString = %q", got)
	}
	if got := rec.MetadataString("missing"); got != "" {
		t.Fatalf("missing MetadataString = %q", got)
	}
	if got := rec.MetadataStringSlice("topics"); len(got) != 2 || got[0] != "a" {
		t.Fatalf("MetadataStringSlice topics = %v", got)
	}
	if got := rec.MetadataStringSlice("speakers"); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("MetadataStringSlice speakers = %v", got)
	}
	if got := rec.MetadataStringSlice("missing"); got != nil {
		t.Fatalf("missing MetadataStringSlice = %v", got)
	}
	if got := rec.MetadataFloat("start_sec"); got != 1.25 {
		t.Fatalf("MetadataFloat start_sec = %v", got)
	}
	if got := rec.MetadataFloat("count"); got != 7 {
		t.Fatalf("MetadataFloat count = %v", got)
	}
	if got := rec.MetadataFloat("missing"); got != 0 {
		t.Fatalf("missing MetadataFloat = %v", got)
	}
}

func TestMediaAssetRecord_MalformedMetadataDegrades(t *testing.T) {
	rec := &MediaAssetRecord{MetadataJSON: "{not json"}
	if got := rec.MetadataString("anything"); got != "" {
		t.Fatalf("malformed metadata must degrade to empty, got %q", got)
	}
	if got := rec.MetadataFloat("anything"); got != 0 {
		t.Fatalf("malformed metadata must degrade to 0, got %v", got)
	}
}

func TestMediaHashCandidates(t *testing.T) {
	if got := mediaHashCandidates(""); got != nil {
		t.Fatalf("empty hash candidates = %v, want nil", got)
	}
	raw := mediaHashCandidates("abc")
	if len(raw) != 2 || raw[0] != "abc" || raw[1] != "abc" {
		t.Fatalf("raw hash candidates = %v", raw)
	}
	prefixed := mediaHashCandidates("sha256:abc")
	if len(prefixed) != 2 || prefixed[0] != "sha256:abc" || prefixed[1] != "abc" {
		t.Fatalf("prefixed hash candidates = %v", prefixed)
	}
}

// fakeScanRow asserts the scan destination count matches the supplied values and
// assigns them through reflection. It pins the shared projection/scan alignment:
// if a future edit adds a column without updating scanMediaAssetRecord (or vice
// versa) this test fails instead of silently mis-reading every media row.
type fakeScanRow struct {
	values []any
}

func (f *fakeScanRow) Scan(dest ...any) error {
	if len(dest) != len(f.values) {
		return fmt.Errorf("scan destination count %d != value count %d", len(dest), len(f.values))
	}
	for i, d := range dest {
		target := reflect.ValueOf(d)
		if target.Kind() != reflect.Ptr {
			return fmt.Errorf("dest[%d] is not a pointer", i)
		}
		value := reflect.ValueOf(f.values[i])
		if !value.Type().AssignableTo(target.Elem().Type()) {
			return fmt.Errorf("dest[%d]: cannot assign %T to %s", i, f.values[i], target.Elem().Type())
		}
		target.Elem().Set(value)
	}
	return nil
}

func TestScanMediaAssetRecord_ColumnAlignment(t *testing.T) {
	row := &fakeScanRow{values: []any{
		"id-1", "name", "file.mp4", "Title", "youtube", "video", "training", `["t1"]`,
		"ACTIVE", "INDEXED", int64(1234),
		"/local", "drive-1", "https://drive/1", "https://dl/1",
		"sha256:deadbeef", "https://thumb/1",
		"https://src", "youtube", "vid-1", "vid-1",
		int64(1000), int64(2000), "folder-1", "parent-1", "/a/b", "2026-09-13T10:00:00Z",
		`{"clip_summary":"s"}`,
	}}
	rec, err := scanMediaAssetRecord(row)
	if err != nil {
		t.Fatalf("scanMediaAssetRecord: %v", err)
	}
	if rec.ID != "id-1" || rec.Title != "Title" || rec.SourceURL != "https://src" ||
		rec.SourceProvider != "youtube" || rec.SourceVideoID != "vid-1" ||
		rec.StartMS != 1000 || rec.EndMS != 2000 {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if len(rec.Tags) != 1 || rec.Tags[0] != "t1" {
		t.Fatalf("tags = %v", rec.Tags)
	}
	if rec.MetadataString("clip_summary") != "s" {
		t.Fatalf("metadata not retained: %q", rec.MetadataJSON)
	}
	if rec.FolderID != "folder-1" || rec.ParentFolderID != "parent-1" || rec.FolderPath != "/a/b" {
		t.Fatalf("folder projection = %q/%q/%q", rec.FolderID, rec.ParentFolderID, rec.FolderPath)
	}
	if rec.CreatedAtTime().IsZero() {
		t.Fatalf("created_at not parsed: %q", rec.CreatedAt)
	}
	meta := rec.MetadataMap()
	meta["mutated"] = true
	if _, leaked := rec.MetadataMap()["mutated"]; leaked {
		t.Fatal("MetadataMap must return a copy, not the internal map")
	}
}
