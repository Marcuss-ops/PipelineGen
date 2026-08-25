// Package completion — strict_destination_projection_test.go (FASE 3,
// July 2026).
//
// White-box test that pins the NEW strict-mode contract on the two
// destination-projection helpers introduced by FASE 3:
//
//   - completion.KindFromDestination(dest) → (finalization.ArtifactKind, error)
//   - completion.SourceFromDestination(dest) → (string, error)
//
// FASE 3 (Piano d'Azione §3, July 2026) REMOVED the silent auto-cast to
// KindDocument (the pre-FASE-3 code mapped ANY unknown destination to
// KindDocument, producing mis-routed media_assets media_type=document
// rows for YouTube clips, voiceover audio, stock videos, and artlist
// clips). The user-spec line — "rimuovi la conversione
// VerifiedArtifact→stato guidata da dest sconosciute (non più
// auto-cast a 'document')" — is the canonical fix codified here as a
// per-destination typed mapping + a typed error on unknown destinations.
//
// Honest scope-lock (godlike/07): the helpers here are the SECOND layer
// of defense. The FIRST layer is `remote.StagedArtifacts.Validate()`
// (internal/domain/remote/staged_artifact_reference.go), which short-
// circuits ANY non-canonical destination with
// ErrStagedArtifactReferenceInvalidDestination BEFORE this package's
// code runs. Therefore the FASE 3 typed error
// (completion.ErrUnmappedCompletionDestination) is unreachable from the canonical
// wire path — it is defense-in-depth for internal callers that bypass
// StagedArtifacts.Validate, plus an anti-regression guard for the
// strict-mode contract itself. This file locks the strict-mode
// semantics directly, in white-box, so the contract cannot drift even
// if the Validate upstream signature changes.
package jobs

import (
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	completion "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/completion"
)

// TestKindFromDestination_StrictMode_LocksCutoverFromKindDocument pins the
// per-destination mapping in a single tabular test: every canonical
// 9-key destination MUST map to its OWN declared Kind — NOT a uniform
// KindDocument fallback (the pre-FASE-3 anti-pattern that mis-routed
// YouTube clips, voiceover audio, stock videos, and artlist clips).
//
// The 9 mappings canonically declared in the production switch:
//
//	image         → KindImage
//	youtube_clip  → KindVideo         (cutover away from KindDocument)
//	voiceover     → KindAudio         (cutover away from KindDocument)
//	script        → KindScript
//	document      → KindDocument      (only one true document destination)
//	book          → KindDocument      (text content; canonical mapping per FASE 3)
//	stock         → KindVideo         (cutover away from KindDocument)
//	artlist       → KindVideo         (cutover away from KindDocument)
//	sound_effect  → KindSoundEffect   (cutover away from KindDocument)
//
// The table also asserts errors.Is compatibility for all 9 (no error
// returned for any canonical key — so the FASE 3 strict-mode fallback
// is the SOLE source of completion.ErrUnmappedCompletionDestination).
func TestKindFromDestination_StrictMode_LocksCutoverFromKindDocument(t *testing.T) {
	cases := []struct {
		dest    string
		want    finalization.ArtifactKind
		cutover bool // true = the FASE 3 cutover away from KindDocument
	}{
		{dest: "image", want: finalization.KindImage, cutover: false},             // already KindImage in pre-FASE-3
		{dest: "youtube_clip", want: finalization.KindVideo, cutover: true},       // was KindDocument
		{dest: "voiceover", want: finalization.KindAudio, cutover: true},          // was KindDocument
		{dest: "script", want: finalization.KindScript, cutover: false},           // already KindScript
		{dest: "document", want: finalization.KindDocument, cutover: false},       // already KindDocument
		{dest: "book", want: finalization.KindDocument, cutover: false},           // canonical Document
		{dest: "stock", want: finalization.KindVideo, cutover: true},              // was KindDocument
		{dest: "artlist", want: finalization.KindVideo, cutover: true},            // was KindDocument
		{dest: "sound_effect", want: finalization.KindSoundEffect, cutover: true}, // was KindDocument
	}
	if got, want := len(cases), 9; got != want {
		t.Fatalf("canonical 9-key table drift: got %d cases, want %d (the FASE 3 strict-mode surface)", got, want)
	}
	cutoverCount := 0
	for _, c := range cases {
		t.Run(c.dest, func(t *testing.T) {
			got, err := completion.KindFromDestination(c.dest)
			if err != nil {
				t.Fatalf("completion.KindFromDestination(%q): unexpected err=%v (canonical destination MUST succeed)", c.dest, err)
			}
			if got != c.want {
				t.Errorf("completion.KindFromDestination(%q): got %v want %v (FASE 3 cutover LOCK; pre-FASE-3 silently cast to KindDocument)",
					c.dest, got, c.want)
			}
			if c.cutover {
				cutoverCount++
				// sanity: the declared Kind MUST NOT be KindDocument for
				// any "cutover" row — the regression guard is
				// specifically "stop mapping to KindDocument".
				if got == finalization.KindDocument {
					t.Errorf("completion.KindFromDestination(%q) MUST NOT return KindDocument (FASE 3 cutover regression)", c.dest)
				}
			}
		})
	}
	if cutoverCount != 5 {
		t.Errorf("FASE 3 locks the cutover of 5 destinations away from KindDocument; got %d", cutoverCount)
	}
}

// TestKindFromDestination_StrictMode_RejectsUnknownDestination is the
// canonical anti-regression lock for the FASE 3 NO-FAKE-AVAILABILITY
// contract: unknown destinations MUST surface as
// completion.ErrUnmappedCompletionDestination (typed sentinel, errors.Is
// recoverable). The wrapped message MUST name the offending
// destination so the operator can fix the producer without
// grep-arounds. Three failure modes are exercised:
//
//  1. Typo'd destination ("drives" — extra 's') — the canonical
//     "drives" vs "drive" accident.
//  2. Empty destination string — upstream StagedArtifacts.Validate
//     catches empty strings at layer 1 with
//     ErrStagedArtifactReferenceMissingFields; here the helper is
//     STILL fail-closed as defense-in-depth (so internal callers
//     that bypass Validate cannot regress to a CastDocument silent).
//  3. Random alphanumeric suffix ("documentx") — the canonical
//     "almost-canonical" typo.
func TestKindFromDestination_StrictMode_RejectsUnknownDestination(t *testing.T) {
	cases := []string{
		"drives",    // canonical typo (extra 's')
		"",          // empty / unset
		"documentx", // almost-canonical
		"video",     // plausible-but-not-canonical (no "video" in 9-key)
	}
	for _, dest := range cases {
		t.Run("dest="+dest, func(t *testing.T) {
			got, err := completion.KindFromDestination(dest)
			if err == nil {
				t.Fatalf("completion.KindFromDestination(%q): expected wrapped completion.ErrUnmappedCompletionDestination, got nil (silent auto-cast FASE 3 regression)", dest)
			}
			if !errors.Is(err, completion.ErrUnmappedCompletionDestination) {
				t.Errorf("completion.KindFromDestination(%q): err=%v; want wraps completion.ErrUnmappedCompletionDestination", dest, err)
			}
			// The wrapped message MUST name the offending destination
			// so the operator can fix the producer without
			// grep-arounds (godlike/07 operator-friendly diagnostics).
			if !strings.Contains(err.Error(), `"`+dest+`"`) {
				t.Errorf("completion.KindFromDestination(%q): err message=%q; want quoted destination in the message", dest, err.Error())
			}
			if got != "" {
				t.Errorf("completion.KindFromDestination(%q): on unknown destination want zero ArtifactKind, got %q", dest, got)
			}
		})
	}
}

// TestSourceFromDestination_StrictMode_LocksCanonicalSourceEnum pins the
// per-destination source identity for media_assets.source. FASE 3
// removes the pre-FASE-3 silent stash (`default: return dest`) which
// propagated arbitrary strings into the canonical source enum and
// produced off-spec downstream behavior in IndexingHandler's Qdrant
// payload builder (which reads media_assets.source as the semantic
// `origin` slot).
//
// The 9 mappings declare a CLOSED enum of source strings — every
// canonical destination's source MUST match its declared value, and
// no destination may propagate the destination string verbatim.
func TestSourceFromDestination_StrictMode_LocksCanonicalSourceEnum(t *testing.T) {
	cases := []struct {
		dest string
		want string
	}{
		{dest: "image", want: "image"},
		{dest: "youtube_clip", want: "youtube"},
		{dest: "voiceover", want: "voiceover"},
		{dest: "script", want: "script"},
		{dest: "document", want: "document"},
		{dest: "book", want: "book"},
		{dest: "stock", want: "stock"},
		{dest: "artlist", want: "artlist"},
		{dest: "sound_effect", want: "sound_effect"},
	}
	if got, want := len(cases), 9; got != want {
		t.Fatalf("canonical 9-key source table drift: got %d cases, want %d", got, want)
	}
	for _, c := range cases {
		t.Run(c.dest, func(t *testing.T) {
			got, err := completion.SourceFromDestination(c.dest)
			if err != nil {
				t.Fatalf("completion.SourceFromDestination(%q): unexpected err=%v", c.dest, err)
			}
			if got != c.want {
				t.Errorf("completion.SourceFromDestination(%q): got %q want %q (FASE 3 strict-mode LOCK; pre-FASE-3 used `default: return dest` which propagated unknown values verbatim)",
					c.dest, got, c.want)
			}
		})
	}
}

// TestSourceFromDestination_StrictMode_RejectsUnknownDestination locks
// the strict-mode fallback for the source identity (paired with the
// kind mapping). Unknown destinations MUST surface
// completion.ErrUnmappedCompletionDestination AND return an empty source string
// (NOT the destination string verbatim — that was the pre-FASE-3
// silent stash).
func TestSourceFromDestination_StrictMode_RejectsUnknownDestination(t *testing.T) {
	cases := []string{
		"drives",    // typo
		"",          // empty
		"documentx", // almost-canonical
	}
	for _, dest := range cases {
		t.Run("dest="+dest, func(t *testing.T) {
			got, err := completion.SourceFromDestination(dest)
			if err == nil {
				t.Fatalf("completion.SourceFromDestination(%q): expected wrapped completion.ErrUnmappedCompletionDestination, got nil", dest)
			}
			if !errors.Is(err, completion.ErrUnmappedCompletionDestination) {
				t.Errorf("completion.SourceFromDestination(%q): err=%v; want wraps completion.ErrUnmappedCompletionDestination", dest, err)
			}
			if got != "" {
				t.Errorf("completion.SourceFromDestination(%q): on unknown destination want empty source, got %q (pre-FASE-3 silently stashed the destination string)", dest, got)
			}
		})
	}
}
