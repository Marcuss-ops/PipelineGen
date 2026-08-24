// Package asset — clip_key_test.go (Stock Pipeline Cutover P0-CLIP-IDEMP, July 2026).
//
// Locks in the canonical identity contract for ClipKey. Each
// test pins a single bullet from the user spec:
//
//  1. Determinism: same inputs → same 64-char lowercase hex.
//  2. Empty rejection: subject_id="" OR source_video_id=""
//     → ErrClipIdentityEmpty (godlike/07 fail-closed).
//  3. Bounds rejection: start_ms<0 OR end_ms<=start_ms
//     → ErrClipIdentityInvalid.
//  4. Output shape: returns exactly 64 lowercase hex chars,
//     prefix-free (unlike SHA256IdempotencyKey).
//  5. User-spec literal: hex of
//     "sugar-ray-robinson|dQw4w9WgXcQ|120000|124000" matches.
//
// These tests are the SSOT checkpoint — a future refactor that
// silently changes the canonical projection (e.g. adding a
// fifth segment) MUST update Test 1 AND Test 5 explicitly so
// the change is diff-visible at the test boundary.
package texttracks

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestClipKey_Deterministic locks the canonical projection:
// same inputs MAY produce a 64-char lowercase hex output AND
// outputs equal across multiple calls.
func TestClipKey_Deterministic(t *testing.T) {
	first, err := ClipKey("sugar-ray-robinson", "dQw4w9WgXcQ", 120000, 124000)
	if err != nil {
		t.Fatalf("first call returned error: %v", err)
	}
	second, err := ClipKey("sugar-ray-robinson", "dQw4w9WgXcQ", 120000, 124000)
	if err != nil {
		t.Fatalf("second call returned error: %v", err)
	}
	if first != second {
		t.Fatalf("ClipKey not deterministic: first=%q second=%q", first, second)
	}
	if got, want := len(first), 64; got != want {
		t.Fatalf("ClipKey length = %d, want %d (sha256 hex is always 64 lowercase hex chars)", got, want)
	}
	for i, c := range first {
		isLowerHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isLowerHex {
			t.Fatalf("ClipKey[%d] = %q is not lowercase hex (got %q)", i, string(c), first)
		}
	}
}

// TestClipKey_DifferentInvariants_DifferentKeys locks the
// "each input matters" property — varying ANY one of the 4
// inputs MUST produce a distinct hex (otherwise the repair
// path would silently merge clips that should be distinct).
func TestClipKey_DifferentInvariants_DifferentKeys(t *testing.T) {
	canonical, err := ClipKey("sugar-ray-robinson", "dQw4w9WgXcQ", 120000, 124000)
	if err != nil {
		t.Fatalf("canonical returned error: %v", err)
	}

	variants := []struct {
		name          string
		subjectID     string
		sourceVideoID string
		startMs       int64
		endMs         int64
	}{
		{"different subject", "mike-tyson", "dQw4w9WgXcQ", 120000, 124000},
		{"different video", "sugar-ray-robinson", "abc123xyzAB", 120000, 124000},
		{"different start_ms", "sugar-ray-robinson", "dQw4w9WgXcQ", 120001, 124000},
		{"different end_ms", "sugar-ray-robinson", "dQw4w9WgXcQ", 120000, 124001},
	}
	for _, v := range variants {
		got, err := ClipKey(v.subjectID, v.sourceVideoID, v.startMs, v.endMs)
		if err != nil {
			t.Fatalf("variant %q returned error: %v", v.name, err)
		}
		if got == canonical {
			t.Errorf("variant %q produced the SAME key as canonical (invariant drift): both = %q", v.name, got)
		}
	}
}

// TestClipKey_RejectsEmptySubjectID locks godlike/07: empty
// subjectID is one of the 4 "missing field" failure modes.
func TestClipKey_RejectsEmptySubjectID(t *testing.T) {
	_, err := ClipKey("", "dQw4w9WgXcQ", 0, 4000)
	if err == nil {
		t.Fatalf("ClipKey with empty subjectID returned nil error (godlike/07 violation)")
	}
	if !errors.Is(err, ErrClipIdentityEmpty) {
		t.Fatalf("err = %v, want errors.Is ErrClipIdentityEmpty", err)
	}
	// Operator diagnostic includes the offending field name.
	if got := err.Error(); !strings.Contains(got, "subject_id") {
		t.Fatalf("error message %q must name the offending field 'subject_id'", got)
	}
}

// TestClipKey_RejectsEmptySourceVideoID locks godlike/07:
// empty sourceVideoID is the second "missing field" mode.
func TestClipKey_RejectsEmptySourceVideoID(t *testing.T) {
	_, err := ClipKey("sugar-ray-robinson", "", 0, 4000)
	if err == nil {
		t.Fatalf("ClipKey with empty source_video_id returned nil error (godlike/07 violation)")
	}
	if !errors.Is(err, ErrClipIdentityEmpty) {
		t.Fatalf("err = %v, want errors.Is ErrClipIdentityEmpty", err)
	}
	if got := err.Error(); !strings.Contains(got, "source_video_id") {
		t.Fatalf("error message %q must name the offending field 'source_video_id'", got)
	}
}

// TestClipKey_RejectsNegativeStartMs locks godlike/07:
// negative start_ms is a "structurally wrong value" failure
// (ErrClipIdentityInvalid, distinct from ErrClipIdentityEmpty).
func TestClipKey_RejectsNegativeStartMs(t *testing.T) {
	_, err := ClipKey("sugar-ray-robinson", "dQw4w9WgXcQ", -1, 4000)
	if err == nil {
		t.Fatalf("ClipKey with negative start_ms returned nil error (godlike/07 violation)")
	}
	if !errors.Is(err, ErrClipIdentityInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrClipIdentityInvalid (negative bounds)", err)
	}
}

// TestClipKey_RejectsEndMsLEStartMs locks the canonical
// "end > start" invariant. Equal end_ms == start_ms is a
// zero-duration clip which is structurally impossible;
// end_ms < start_ms would invert the slice — both rejected.
func TestClipKey_RejectsEndMsLEStartMs(t *testing.T) {
	cases := []struct {
		name    string
		startMs int64
		endMs   int64
	}{
		{"equal", 4000, 4000},
		{"end_lt_start", 4000, 3999},
		{"end_zero_start", 4000, 0},
	}
	for _, c := range cases {
		_, err := ClipKey("sugar-ray-robinson", "dQw4w9WgXcQ", c.startMs, c.endMs)
		if err == nil {
			t.Errorf("case %q (start=%d end=%d) returned nil error — godlike/07 fail-closed violation",
				c.name, c.startMs, c.endMs)
			continue
		}
		if !errors.Is(err, ErrClipIdentityInvalid) {
			t.Errorf("case %q err = %v, want errors.Is ErrClipIdentityInvalid", c.name, err)
		}
	}
}

// TestClipKey_UserSpecLiteral locks the canonical projection's
// actual SHA-256 hex by computing it from the canonical
// projection string at test time:
//
//	hex(sha256("sugar-ray-robinson|dQw4w9WgXcQ|120000|124000"))
//
// Computing in-test (vs hardcoding the hex) avoids the
// godlike/07 "stale hash constant" antipattern: a future
// refactor that changes the projection (segment count,
// separator, ordering) will fail this assertion with a
// diff-visible hex mismatch the operator can re-verify
// offline via:
//
//	echo -n 'sugar-ray-robinson|dQw4w9WgXcQ|120000|124000' | sha256sum
func TestClipKey_UserSpecLiteral(t *testing.T) {
	const canonical = "sugar-ray-robinson|dQw4w9WgXcQ|120000|124000"
	sum := sha256.Sum256([]byte(canonical))
	want := fmt.Sprintf("%x", sum)

	got, err := ClipKey("sugar-ray-robinson", "dQw4w9WgXcQ", 120000, 124000)
	if err != nil {
		t.Fatalf("ClipKey returned error: %v", err)
	}
	if got != want {
		t.Fatalf("ClipKey produced %q, want %q (computed from canonical projection %q; reproduce via `echo -n '%s' | sha256sum`)", got, want, canonical, canonical)
	}
}
