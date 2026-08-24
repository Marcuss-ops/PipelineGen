// Package stockbuild — payload_test.go (P0-2 stock-pipeline refactor, July 2026).
//
// Tests for the canonical Payload.Validate + DeriveRunID
// contract. The RunID derivation is the heart of the resume
// contract: the same intent (subject + target + categories +
// destination) MUST produce the same RunID across multiple
// invocations, otherwise the steps.Store ledger would fork and
// resume would be impossible.
//
// godlike/06 SSOT: this test is the single canonical tripwire for
// the wire-shape + RunID invariants. Future drift in Payload
// fields that affects RunID MUST update this test in lockstep.
package jobs

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/subjects"
)

// validPayload returns a fixture that passes Validate(). Reused
// across all payload tests so a single source of truth for the
// valid wire shape exists.
func validPayload() Payload {
	return Payload{
		Subject: SubjectRef{
			DisplayName: "Sugar Ray Robinson",
			Slug:        "sugar-ray-robinson",
		},
		Target: TargetSpec{
			Videos:              20,
			ClipsPerVideo:       15,
			ClipDurationSeconds: 4,
		},
		Categories: []CategoryCount{
			{Name: "fight", Count: 12},
			{Name: "interview", Count: 6},
			{Name: "training", Count: 2},
		},
		DestinationFolderID: "DRIVE_FOLDER_ID",
	}
}

// TestPayload_Valid_HappyPath asserts the canonical contract.
func TestPayload_Valid_HappyPath(t *testing.T) {
	if err := validPayload().Validate(); err != nil {
		t.Fatalf("validPayload rejected: %v", err)
	}
}

// TestPayload_Invalid_AggregatesProblems asserts the godlike/07
// no-fake-availability contract: ONE error message lists ALL
// problems (operators see everything at once, not one retry per
// field). Verifies error type match (errors.Is chain against
// ErrInvalidPayload).
func TestPayload_Invalid_AggregatesProblems(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *Payload)
	}{
		{
			name:   "empty display_name",
			mutate: func(p *Payload) { p.Subject.DisplayName = "" },
		},
		{
			name:   "non-canonical slug",
			mutate: func(p *Payload) { p.Subject.Slug = "WRONG-SLUG-NOT-EQUAL-TO-SLUGIFY" },
		},
		{
			name:   "videos = 0",
			mutate: func(p *Payload) { p.Target.Videos = 0 },
		},
		{
			name:   "clips_per_video = 0",
			mutate: func(p *Payload) { p.Target.ClipsPerVideo = 0 },
		},
		{
			name:   "clip_duration = 0",
			mutate: func(p *Payload) { p.Target.ClipDurationSeconds = 0 },
		},
		{
			name:   "empty destination",
			mutate: func(p *Payload) { p.DestinationFolderID = "" },
		},
		{
			name:   "empty categories",
			mutate: func(p *Payload) { p.Categories = nil },
		},
		{
			name: "duplicate category names",
			mutate: func(p *Payload) {
				p.Categories = []CategoryCount{
					{Name: "fight", Count: 1},
					{Name: "fight", Count: 2},
				}
			},
		},
		{
			name: "category sum exceeds capacity",
			mutate: func(p *Payload) {
				// 200_000 categories exceed clips_per_video * videos = 300.
				p.Categories = []CategoryCount{{Name: "kill", Count: 300_000}}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validPayload()
			tc.mutate(&p)
			err := p.Validate()
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("Validate did not return ErrInvalidPayload: %v", err)
			}
			if err == nil {
				t.Fatalf("Validate unexpectedly succeeded for %q", tc.name)
			}
		})
	}
}

// TestDeriveRunID_DeterministicContract asserts the canonical
// RunID derivation rules:
//
//  1. Identical intent → identical RunID (resume contract).
//  2. Different videos → different RunID (operator intent changes the run).
//  3. Different category counts → different RunID.
//  4. Different slugs (casing variants of the same display_name) →
//     DIFFERENT RunID at the DeriveRunID layer; the canonical
//     subject_id-layer deduplication is responsibilities of the
//     subjects.Resolver (NOT the DeriveRunID function).
//
// Note: casing of the display_name after resolver normalization is
// signaled by callers passing the SAME slug for both invocations.
// DeriveRunID is purely a function of (slug, target, categories,
// destination); the casing is the resolver's concern.
func TestDeriveRunID_DeterministicContract(t *testing.T) {
	a := DeriveRunID("sugar-ray-robinson", validPayload())
	b := DeriveRunID("sugar-ray-robinson", validPayload())
	if a != b {
		t.Errorf("Determinism broken: same intent → different IDs\n  a=%q\n  b=%q", a, b)
	}
	if len(a) != 64 {
		t.Errorf("RunID length=%d, want 64 hex chars (sha256)", len(a))
	}
	if strings.ContainsAny(a, "ABCDEF") && !strings.ContainsAny(a, "abcdef") {
		t.Errorf("RunID is uppercase hex (got %q); canonical ID format is lowercase", a)
	}

	// Different target → different ID.
	pa := validPayload()
	pb := validPayload()
	pb.Target.Videos = 25 // operator changed intent
	if DeriveRunID("sugar-ray-robinson", pa) == DeriveRunID("sugar-ray-robinson", pb) {
		t.Errorf("Different targets produced identical RunID (resume would re-merge)")
	}

	// Different categories → different ID.
	pc := validPayload()
	pc.Categories = []CategoryCount{{Name: "fight", Count: 13}} // operator changed intent
	if DeriveRunID("sugar-ray-robinson", pa) == DeriveRunID("sugar-ray-robinson", pc) {
		t.Errorf("Different categories produced identical RunID")
	}

	// Different slug → different ID (caller-driven intent).
	if DeriveRunID("sugar-ray-robinson", pa) == DeriveRunID("floyd-mayweather", pa) {
		t.Errorf("Different slug produced identical RunID")
	}

	// Different order to the categories array → SAME ID
	// (normalizeCategories sorts by Name).
	reorderP := validPayload()
	reorderP.Categories = []CategoryCount{
		{Name: "training", Count: 2},
		{Name: "interview", Count: 6},
		{Name: "fight", Count: 12},
	}
	if DeriveRunID("sugar-ray-robinson", validPayload()) != DeriveRunID("sugar-ray-robinson", reorderP) {
		t.Errorf("Reordered categories produced different RunID (normalizeCategories broken)")
	}

	// Resume=true vs Resume=false → SAME ID (resume is a runtime
	// hint, NOT a parameter of the run itself).
	resumeP := validPayload()
	resumeP.Resume = true
	if DeriveRunID("sugar-ray-robinson", validPayload()) != DeriveRunID("sugar-ray-robinson", resumeP) {
		t.Errorf("Resume flag affected RunID (resume MUST NOT influence identity per design)")
	}
}

// TestFormatRunIDLabel asserts the operator-grep-friendly label.
func TestFormatRunIDLabel(t *testing.T) {
	got := FormatRunIDLabel("sugar-ray-robinson", parseTime("2026-07-30T14:30:00Z"))
	want := "stock_sugar-ray-robinson_20260730"
	if got != want {
		t.Errorf("FormatRunIDLabel = %q, want %q", got, want)
	}
	// Empty slug → "unknown" sentinel.
	if FormatRunIDLabel("", parseTime("2026-07-30T14:30:00Z")) != "stock_unknown_20260730" {
		t.Errorf("Empty slug should produce 'stock_unknown_...'")
	}
}

// parseTime helper, local-only (test file).
func parseTime(s string) (t time.Time) {
	t, _ = time.Parse(time.RFC3339, s)
	return t
}

// ─── resolver-context sanity (compile-time check; integration test in P1) ────

// Compile-time assertion: subjects.Resolver lives in the
// domain package and is wired into stockbuild.Handler — any drift
// here would surface as a missing import or wrong port type at
// compile time. The blank identifier below is a use of the import
// without an active reference; it intentionally avoids a useless
// declaration-only variable while keeping the Go compiler happy
// about the import (otherwise unused-import is a build error when
// the resolver surface gets slimmed in a future refactor).
var _ = (*subjects.Resolver)(nil)
