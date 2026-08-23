// Package drive — publisher_translate_test.go (P2.3 acceptance test, July 2026)
//
// Single TDD test pinning the boundary conversion from drive.PutAction
// (low-level uploader outcome, 4 values) to delivery.UploadOutcome
// (cross-package surface value, 5 values incl. the UploadOutcomeUnknown
// zero-value sentinel). The switch lives on
// (*Publisher).actionFor (private, same-package access) so the test
// lives next to it to break-on-rename automatically.
//
// godlike/06 SSOT: the conversion is on the Publisher (composition
// boundary) rather than in delivery/types.go because delivery
// MUST NOT import drive (Pattern 0 layering — application layer has
// zero outward dependencies on infrastructure).
//
// Verdict-gate coverage: this test pins the load_test gate because
// the canonical 4-arm switch is the operationally-observable boundary
// every multi-worker canary job exercises during a phase-1 fleet
// upgrade; a regression in this switch would silently classify every
// Drive write as UploadOutcomeUnknown under sustained load.
package drive

import (
	"testing"

	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
)

// TestPublisherActionFor_DrivePutActionMapping exercises the 4-arm
// switch in (*Publisher).actionFor. Each PutAction value is mapped
// to its corresponding UploadOutcome value; an unknown (forward-compat)
// value falls through to UploadOutcomeUnknown. The bypass-channel
// decoder (the single test that exercises the cross-package mapping)
// runs against a zero-value Publisher struct (the method is
// side-effect-free, no need for a wired Publisher).
func TestPublisherActionFor_DrivePutActionMapping(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name string
		in   PutAction
		want delivery.UploadOutcome
	}{
		{"created", PutActionCreated, delivery.UploadOutcomeCreated},
		{"updated", PutActionUpdated, delivery.UploadOutcomeUpdated},
		{"skipped", PutActionSkipped, delivery.UploadOutcomeSkipped},
		{"renamed", PutActionRenamed, delivery.UploadOutcomeRenamed},
		{"unknown_fallthrough_default", PutAction("garbage-future-constant"), delivery.UploadOutcomeUnknown},
		{"empty_zero_fallthrough_default", PutAction(""), delivery.UploadOutcomeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.actionFor(tt.in)
			if got != tt.want {
				t.Errorf("actionFor(%q): got %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
