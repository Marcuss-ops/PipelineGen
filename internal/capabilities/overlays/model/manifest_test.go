package model

// manifest_test.go exercises the P5 overlay manifest identity model:
// per-entry retry identity, per-manifest composition identity, deterministic
// serialization and validation of the production content-identity rule.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOverlayManifestEntryValidate(t *testing.T) {
	t.Parallel()
	valid := OverlayManifestEntry{
		ItemID:      "scene-01-overlay-0",
		Track:       1,
		InUS:        1_000_000,
		OutUS:       3_500_000,
		ContentHash: "a3f1c2d4e5b60718293a4b5c6d7e8f90123456789abcdef0123456789abc0123",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
	for name, mutate := range map[string]func(*OverlayManifestEntry){
		"empty item_id":      func(e *OverlayManifestEntry) { e.ItemID = "  " },
		"negative track":     func(e *OverlayManifestEntry) { e.Track = -1 },
		"negative in_us":     func(e *OverlayManifestEntry) { e.InUS = -1 },
		"zero-length window": func(e *OverlayManifestEntry) { e.OutUS = e.InUS },
		"inverted window":    func(e *OverlayManifestEntry) { e.OutUS = e.InUS - 1 },
		"short content_hash": func(e *OverlayManifestEntry) { e.ContentHash = "abc123" },
		"uppercase hash":     func(e *OverlayManifestEntry) { e.ContentHash = strings.ToUpper(e.ContentHash) },
		"negative placement x": func(e *OverlayManifestEntry) {
			e.Placement = &ManifestPlacement{XPx: -1, YPx: 0, WPx: 800, HPx: 150}
		},
		"negative placement y": func(e *OverlayManifestEntry) {
			e.Placement = &ManifestPlacement{XPx: 0, YPx: -1, WPx: 800, HPx: 150}
		},
		"zero placement width": func(e *OverlayManifestEntry) {
			e.Placement = &ManifestPlacement{XPx: 0, YPx: 0, WPx: 0, HPx: 150}
		},
		"negative placement height": func(e *OverlayManifestEntry) {
			e.Placement = &ManifestPlacement{XPx: 0, YPx: 0, WPx: 800, HPx: -1}
		},
	} {
		entry := valid
		mutate(&entry)
		if err := entry.Validate(); err == nil {
			t.Errorf("%s: entry must be rejected", name)
		}
	}
}

func TestOverlayManifestEntryRetryKeyBindsContentNotPlacement(t *testing.T) {
	t.Parallel()
	base := OverlayManifestEntry{
		ItemID:      "scene-01-overlay-0",
		Track:       0,
		InUS:        0,
		OutUS:       2_000_000,
		ContentHash: "a3f1c2d4e5b60718293a4b5c6d7e8f90123456789abcdef0123456789abc0123",
	}
	moved := base
	moved.Track = 3
	moved.InUS = 5_000_000
	moved.OutUS = 7_000_000
	if base.RetryKey() != moved.RetryKey() {
		t.Fatalf("same item_id + content-hash must share one retry identity across placement changes")
	}
	rehashed := base
	rehashed.ContentHash = "b3f1c2d4e5b60718293a4b5c6d7e8f90123456789abcdef0123456789abc0123"
	if base.RetryKey() == rehashed.RetryKey() {
		t.Fatalf("changed content must change the retry identity")
	}
	reIDed := base
	reIDed.ItemID = "scene-01-overlay-1"
	if base.RetryKey() == reIDed.RetryKey() {
		t.Fatalf("changed item_id must change the retry identity")
	}
	if base.RetryKey() != base.RetryKey() {
		t.Fatalf("retry identity must be stable across calls")
	}
}

func TestOverlayManifestEntryRetryKeyIgnoresRectPlacement(t *testing.T) {
	t.Parallel()
	base := OverlayManifestEntry{
		ItemID:      "scene-01-overlay-0",
		Track:       0,
		InUS:        0,
		OutUS:       2_000_000,
		ContentHash: "a3f1c2d4e5b60718293a4b5c6d7e8f90123456789abcdef0123456789abc0123",
	}
	rect := base
	rect.Placement = &ManifestPlacement{XPx: 40, YPx: 800, WPx: 800, HPx: 150}
	if base.RetryKey() != rect.RetryKey() {
		t.Fatalf("the spatial rect is placement metadata: it must not change the entry work identity")
	}
	moved := rect
	moved.Placement = &ManifestPlacement{XPx: 640, YPx: 200, WPx: 800, HPx: 150}
	if rect.RetryKey() != moved.RetryKey() {
		t.Fatalf("moving the rect must not change the entry work identity")
	}
}

func TestOverlayManifestDigestSeparatesRectPlacements(t *testing.T) {
	t.Parallel()
	entry := OverlayManifestEntry{
		ItemID:      "scene-01-overlay-0",
		Track:       0,
		InUS:        0,
		OutUS:       2_000_000,
		ContentHash: "a3f1c2d4e5b60718293a4b5c6d7e8f90123456789abcdef0123456789abc0123",
	}
	base := OverlayManifest{
		SchemaVersion: SchemaVersionManifest,
		PlanID:        "plan-01",
		VideoID:       "video-01",
		Entries:       []OverlayManifestEntry{entry},
	}
	other := base
	moved := entry
	moved.Placement = &ManifestPlacement{XPx: 40, YPx: 800, WPx: 800, HPx: 150}
	other.Entries = []OverlayManifestEntry{moved}
	if base.Digest() == other.Digest() {
		t.Fatalf("full-canvas and rect-projected compositions draw different pixels: they must be distinct compositions")
	}
	reshaped := other
	reshapedEntry := moved
	reshapedEntry.Placement = &ManifestPlacement{XPx: 40, YPx: 800, WPx: 820, HPx: 150}
	reshaped.Entries = []OverlayManifestEntry{reshapedEntry}
	if other.Digest() == reshaped.Digest() {
		t.Fatalf("two different rects must name two different compositions")
	}
	raw, err := json.Marshal(other)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	again, err := json.Marshal(other)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(raw, again) {
		t.Fatalf("serialization must stay deterministic with placement present")
	}
}

func TestOverlayManifestValidate(t *testing.T) {
	t.Parallel()
	valid := OverlayManifest{
		SchemaVersion: SchemaVersionManifest,
		PlanID:        "plan-01",
		VideoID:       "video-01",
		Entries: []OverlayManifestEntry{
			{
				ItemID:      "scene-01-overlay-0",
				Track:       0,
				InUS:        0,
				OutUS:       2_000_000,
				ContentHash: "a3f1c2d4e5b60718293a4b5c6d7e8f90123456789abcdef0123456789abc0123",
			},
			{
				ItemID:      "scene-02-overlay-1",
				Track:       1,
				InUS:        2_000_000,
				OutUS:       4_000_000,
				ContentHash: "b3f1c2d4e5b60718293a4b5c6d7e8f90123456789abcdef0123456789abc0123",
			},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	for name, mutate := range map[string]func(*OverlayManifest){
		"wrong schema_version": func(m *OverlayManifest) { m.SchemaVersion = "pipelinegen.overlay-manifest.v2" },
		"missing plan_id":      func(m *OverlayManifest) { m.PlanID = " " },
		"missing video_id":     func(m *OverlayManifest) { m.VideoID = "" },
		"bad timeline_sha256":  func(m *OverlayManifest) { m.TimelineSHA256 = "nope" },
		"no entries":           func(m *OverlayManifest) { m.Entries = nil },
		"invalid entry":        func(m *OverlayManifest) { m.Entries[0].OutUS = 0 },
		"duplicate item_id":    func(m *OverlayManifest) { m.Entries[1].ItemID = m.Entries[0].ItemID },
		"duplicate content":    func(m *OverlayManifest) { m.Entries[1].ContentHash = m.Entries[0].ContentHash },
	} {
		doc := valid
		doc.Entries = append([]OverlayManifestEntry(nil), valid.Entries...)
		mutate(&doc)
		if err := doc.Validate(); err == nil {
			t.Errorf("%s: manifest must be rejected", name)
		}
	}
	var nilDoc *OverlayManifest
	if err := nilDoc.Validate(); err == nil {
		t.Errorf("nil manifest must be rejected")
	}
}

func TestOverlayManifestMarshalJSONIsDeterministic(t *testing.T) {
	t.Parallel()
	a := OverlayManifest{
		SchemaVersion: SchemaVersionManifest,
		PlanID:        "plan-01",
		VideoID:       "video-01",
		Entries: []OverlayManifestEntry{
			{ItemID: "b", Track: 1, InUS: 2_000_000, OutUS: 4_000_000, ContentHash: strings.Repeat("b", 64)},
			{ItemID: "a", Track: 0, InUS: 0, OutUS: 2_000_000, ContentHash: strings.Repeat("a", 64)},
		},
	}
	b := a
	b.Entries = []OverlayManifestEntry{a.Entries[1], a.Entries[0]}
	rawA, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rawB, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(rawA, rawB) {
		t.Fatalf("entry order must not change the wire bytes:\n%s\n%s", rawA, rawB)
	}
	var decoded OverlayManifest
	if err := json.Unmarshal(rawA, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("round-trip manifest rejected: %v", err)
	}
}

func TestOverlayManifestDigestNamesTheComposition(t *testing.T) {
	t.Parallel()
	base := OverlayManifest{
		SchemaVersion:  SchemaVersionManifest,
		PlanID:         "plan-01",
		VideoID:        "video-01",
		TimelineSHA256: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		Entries: []OverlayManifestEntry{
			{ItemID: "a", Track: 0, InUS: 0, OutUS: 2_000_000, ContentHash: strings.Repeat("a", 64)},
			{ItemID: "b", Track: 1, InUS: 2_000_000, OutUS: 4_000_000, ContentHash: strings.Repeat("b", 64)},
		},
	}
	if base.Digest() == "" {
		t.Fatalf("digest must not be empty")
	}
	reordered := base
	reordered.Entries = []OverlayManifestEntry{base.Entries[1], base.Entries[0]}
	if base.Digest() != reordered.Digest() {
		t.Fatalf("entry order must not change the composition identity")
	}
	recertified := base
	recertified.TimelineSHA256 = strings.Repeat("0", 64)
	if base.Digest() != recertified.Digest() {
		t.Fatalf("the artifact identity must not feed the request identity")
	}
	placed := base
	placed.Entries = append([]OverlayManifestEntry(nil), base.Entries...)
	placed.Entries[1].InUS = 3_000_000
	placed.Entries[1].OutUS = 5_000_000
	if base.Digest() == placed.Digest() {
		t.Fatalf("placement changes must change the composition identity")
	}
	recontented := base
	recontented.Entries = append([]OverlayManifestEntry(nil), base.Entries...)
	recontented.Entries[0].ContentHash = strings.Repeat("c", 64)
	if base.Digest() == recontented.Digest() {
		t.Fatalf("content changes must change the composition identity")
	}
}
