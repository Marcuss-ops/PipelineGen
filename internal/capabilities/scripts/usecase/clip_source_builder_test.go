// Package usecase_test — clip_source_builder_test.go (June 2026).
//
// Stub-based unit tests for the canonical-ID contract introduced in PR 6.
// These tests pin the invariant that the resolved IDs in the evidence and the
// keys of DriveLinks are the canonical = REQUESTED IDs, not the
// asset's internal ID. Production *assets.ClipsRepository is intentionally
// NOT wired here — the typedClipResolverPort interface and
// NewClipSourceBuilder constructor let unit tests inject a stub
// (passing nil for ollamaClient) that returns clips with deliberate
// clip.ID != DriveFileID mismatches.
//
// P1 #6 (June 2026): BuildClipEvidence removed; BuildClipContext
// returns *scriptpkg.ClipEvidence directly. Tests read evidence fields
// instead of map[string]any pack keys.
// P1 #7 (June 2026): NewClipSourceBuilderForTest removed; tests use
// the canonical NewClipSourceBuilder with nil ollamaClient.
//
// The semantic distinction is:
//
//   - When the caller supplies an asset ID ("my-asset-42"), and the DB
//     returns a clip with ID == "my-asset-42", the canonical ID IS the
//     asset's internal ID (backwards-compat).
//   - When the caller supplies a Drive file ID ("1ABC_XYZ"), and the DB
//     returns a clip with ID == "internal-789" + DriveFileID == "1ABC_XYZ",
//     the canonical ID IS the supplied Drive file ID — NOT "internal-789".
//
// Pre-PR-6 the second case silently broke every DriveLinks[xxx] lookup,
// because clip_drive_links was keyed by clip.ID (the internal ID the user
// never typed).
package usecase_test

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// stubClipsResolver is a hand-rolled typedClipResolverPort stub that
// independently maps two lookup tables: byID (primary, asset.ID) and
// byDrive (fallback, DriveFileID). Unknown IDs return (nil, err) or
// (nil, err) for ResolveByMediaAssetID/ResolveByDriveFileID respectively.
// Tests can populate only one side to exercise the canonical-ID matrix.
type stubClipsResolver struct {
	byID    map[string]*asset.Asset
	byDrive map[string]*asset.Asset
}

func (s *stubClipsResolver) ResolveByMediaAssetID(_ context.Context, id string) (*asset.Asset, error) {
	if clip, ok := s.byID[id]; ok {
		return clip, nil
	}
	return nil, errors.New("stubClipsResolver.ResolveByMediaAssetID: not found")
}

func (s *stubClipsResolver) ResolveByDriveFileID(_ context.Context, fileID string) ([]*asset.Asset, error) {
	if clip, ok := s.byDrive[fileID]; ok {
		return []*asset.Asset{clip}, nil
	}
	return nil, errors.New("stubClipsResolver.ResolveByDriveFileID: not found")
}

// newAssetWithDriveLink constructs a clip whose internal ID differs from
// its DriveFileID. The returned *Asset satisfies the canonical-ID
// mismatch case PR 6 is meant to pin.
func newAssetWithDriveLink(internalID, driveID, name, driveLink string) *asset.Asset {
	a := &asset.Asset{
		ID:   internalID,
		Name: name,
	}
	if driveID != "" {
		a.SetDriveFileID(driveID)
	}
	if driveLink != "" {
		a.SetDriveLink(driveLink)
	}
	return a
}

// TestClipSourceBuilder_Canonical_DriveFileIDRequested_PR6 is the
// load-bearing test of the PR 6 invariant: when the caller supplies a
// Drive file ID and the stub returns a clip whose internal asset.ID
// differs from that Drive file ID, ClipEvidence.AcceptedClipIDs and
// DriveLinks keys MUST be the supplied Drive file ID, NOT the
// internal asset.ID. The previous (pre-PR-6) bug was that DriveLinks
// were keyed by clip.ID — so a caller that typed "1ABC_XYZ" got back a
// map keyed by "internal-789", and every DriveLinks["1ABC_XYZ"] lookup
// silently missed.
func TestClipSourceBuilder_Canonical_DriveFileIDRequested_PR6(t *testing.T) {
	const (
		driveFileID = "1ABC_XYZ"     // what the user typed
		internalID  = "internal-789" // what the DB knows it as
		driveLink   = "https://drive.google.com/file/d/1ABC_XYZ/view"
	)

	// Stub only on byDrive — the user supplied a Drive file ID, NOT an
	// internal ID. GetClip returns (nil, err); GetByDriveFileID returns
	// the clip with internal-ID != Drive-file-ID.
	stub := &stubClipsResolver{
		byDrive: map[string]*asset.Asset{
			driveFileID: newAssetWithDriveLink(internalID, driveFileID, "Cinematic Drone", driveLink),
		},
	}

	b := scripts.NewClipSourceBuilder(stub, nil, zap.NewNop())
	b.ConfigureTextTrackReader(&a4StubTextTrackReader{transcripts: map[string]string{
		internalID: "fixture transcript for " + internalID,
	}})

	ev, _, _, err := b.BuildClipContext(context.Background(), []string{driveFileID}, &scripts.ClipGenerationOptions{Language: "en"})
	if err != nil {
		t.Fatalf("BuildClipContext returned error: %v", err)
	}
	if ev == nil {
		t.Fatal("evidence is nil")
	}

	if len(ev.AcceptedClipIDs) != 1 || ev.AcceptedClipIDs[0] != driveFileID {
		t.Fatalf("ev.AcceptedClipIDs = %v, want [%q]", ev.AcceptedClipIDs, driveFileID)
	}

	if got, found := ev.DriveLinks[driveFileID]; !found {
		t.Fatalf("DriveLinks DOES NOT key by canonical Drive file ID %q; keys = %v", driveFileID, keysOf(ev.DriveLinks))
	} else if got != driveLink {
		t.Fatalf("DriveLinks[%q] = %q, want %q", driveFileID, got, driveLink)
	}
	if _, leaked := ev.DriveLinks[internalID]; leaked {
		t.Fatalf("DriveLinks MUST NOT key by internal asset.ID %q; pre-PR-6 leak detected", internalID)
	}

	if len(ev.MissingClipIDs) != 0 {
		t.Fatalf("MissingClipIDs = %v, want nil (the only requested ID resolved via byDrive fallback)", ev.MissingClipIDs)
	}
}

// TestClipSourceBuilder_Canonical_AssetIDRequested_PR6 covers the
// backwards-compatible case: caller supplied an asset ID, AND the DB
// knows it by the same ID. Both sides agree, so canonical == internal.
// This guards against refactors that accidentally flip the canonical
// selection rule to "always use clip.ID".
func TestClipSourceBuilder_Canonical_AssetIDRequested_PR6(t *testing.T) {
	const (
		assetID   = "my-asset-42"
		driveLink = "https://drive.google.com/file/d/my-asset-42/view"
	)

	// Stub on byID — caller supplied the internal ID. Drive file ID
	// also equals assetID here (they coincide for legacy clips where
	// the DB doesn't distinguish). The assertion is that we stay on the
	// asset.ID key — not regressively key by something else.
	stub := &stubClipsResolver{
		byID: map[string]*asset.Asset{
			assetID: newAssetWithDriveLink(assetID, "", "Cinematic Drone", driveLink),
		},
	}

	b := scripts.NewClipSourceBuilder(stub, nil, zap.NewNop())
	b.ConfigureTextTrackReader(&a4StubTextTrackReader{transcripts: map[string]string{
		assetID: "fixture transcript for " + assetID,
	}})
	ev, _, _, err := b.BuildClipContext(context.Background(), []string{assetID}, &scripts.ClipGenerationOptions{Language: "en"})
	if err != nil {
		t.Fatalf("BuildClipContext returned error: %v", err)
	}
	if ev == nil {
		t.Fatal("evidence is nil")
	}

	if len(ev.AcceptedClipIDs) != 1 || ev.AcceptedClipIDs[0] != assetID {
		t.Fatalf("ev.AcceptedClipIDs = %v, want [%q]", ev.AcceptedClipIDs, assetID)
	}
	links := ev.DriveLinks
	if got, ok := links[assetID]; !ok || got != driveLink {
		t.Fatalf("DriveLinks[%q] = (%q, %v), want (%q, true)", assetID, got, ok, driveLink)
	}
}

// TestClipSourceBuilder_Missing_NotFound_PR6 pins PR 5 + PR 6
// interaction: an ID that both lookups miss ends up in
// MissingClipIDs with reason "not_found", and is dropped from
// resolved set. The resolved-only discipline from PR 5 must survive
// canonical-keying from PR 6.
func TestClipSourceBuilder_Missing_NotFound_PR6(t *testing.T) {
	stub := &stubClipsResolver{} // empty: any lookup returns (nil, err)

	b := scripts.NewClipSourceBuilder(stub, nil, zap.NewNop())
	ev, _, _, err := b.BuildClipContext(context.Background(), []string{"ghost"}, nil)
	if err == nil {
		t.Fatalf("BuildClipContext returned nil error for an all-missing request; want err")
	}
	// All requested IDs dropped → caller returns typed error before
	// reaching evidence construction. The evidence is nil in this branch.
	if ev != nil {
		t.Fatalf("ev = %v, want nil when zero clips resolved", ev)
	}
}

// TestClipSourceBuilder_Missing_DriveNotFound_PR6 pins the second
// reason code: a clip row exists and resolves, but its DriveLink
// metadata is empty. The resolver must drop it from resolved and
// record reason "drivenotfound".
func TestClipSourceBuilder_Missing_DriveNotFound_PR6(t *testing.T) {
	const orphanID = "orphan-1"
	stub := &stubClipsResolver{
		byID: map[string]*asset.Asset{
			orphanID: newAssetWithDriveLink(orphanID, "", "Orphan", "" /* driveLink empty */),
		},
	}

	b := scripts.NewClipSourceBuilder(stub, nil, zap.NewNop())
	ev, _, _, err := b.BuildClipContext(context.Background(), []string{orphanID}, nil)
	if err == nil {
		t.Fatalf("expected error for all-missing-after-DriveLink-check; got nil")
	}
	if ev != nil {
		t.Fatalf("ev = %v, want nil when zero clips resolved (drivenotfound)", ev)
	}
}

// TestClipSourceBuilder_Missing_MixedResolutions_PR6 covers the
// realistic mixed batch: one Drive-file-ID clip (canonical mismatch),
// one missing-from-DB clip, and one well-resolved clip. Confirms:
//   - resolved ClipIDs uses canonical IDs only
//   - MissingClipIDs carries both reasons with correct IDs
//   - DriveLinks keys by canonical
//   - ClipCount == len(ClipIDs)
func TestClipSourceBuilder_Missing_MixedResolutions_PR6(t *testing.T) {
	// Drive-file-ID mismatch case.
	canonicalA := "driveFile-AAA"
	a := newAssetWithDriveLink("internal-AAA", canonicalA, "Clip A", "https://drive/a")

	// Resolved trivially (no mismatch).
	b := newAssetWithDriveLink("clipB", "", "Clip B", "https://drive/b")

	stub := &stubClipsResolver{
		byID:    map[string]*asset.Asset{"clipB": b},
		byDrive: map[string]*asset.Asset{canonicalA: a},
	}

	builder := scripts.NewClipSourceBuilder(stub, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(&a4StubTextTrackReader{transcripts: map[string]string{
		"internal-AAA": "fixture transcript for internal-AAA",
		"clipB":        "fixture transcript for clipB",
	}})
	ev, _, _, err := builder.BuildClipContext(
		context.Background(),
		[]string{canonicalA, "missing-X", "clipB"},
		&scripts.ClipGenerationOptions{Language: "en"},
	)
	if err != nil {
		t.Fatalf("BuildClipContext returned error: %v", err)
	}
	if ev == nil {
		t.Fatal("evidence is nil")
	}

	wantClipIDs := []string{canonicalA, "clipB"} // resolved-only, canonical-keyed
	if !equalStringSlices(ev.AcceptedClipIDs, wantClipIDs) {
		t.Fatalf("ev.AcceptedClipIDs = %v, want %v (resolved-only, driveFile ID preserved as canonical)", ev.AcceptedClipIDs, wantClipIDs)
	}

	if got, ok := ev.DriveLinks[canonicalA]; !ok || got != "https://drive/a" {
		t.Fatalf("DriveLinks[%q] = (%q, %v), want (https://drive/a, true)", canonicalA, got, ok)
	}
	if got, ok := ev.DriveLinks["clipB"]; !ok || got != "https://drive/b" {
		t.Fatalf("DriveLinks[%q] = (%q, %v), want (https://drive/b, true)", "clipB", got, ok)
	}
	if _, leaked := ev.DriveLinks["internal-AAA"]; leaked {
		t.Fatalf("DriveLinks MUST NOT key by internal-AAA; PR 6 leak detected")
	}

	if len(ev.MissingClipIDs) != 1 {
		t.Fatalf("MissingClipIDs has %d entries, want 1 (missing-X only); got %v", len(ev.MissingClipIDs), ev.MissingClipIDs)
	}
	if ev.MissingClipIDs[0].ClipID != "missing-X" || ev.MissingClipIDs[0].Reason != scriptpkg.MissingClipReasonNotFound {
		t.Fatalf("missing entry = %+v, want {ClipID: missing-X, Reason: %q}", ev.MissingClipIDs[0], scriptpkg.MissingClipReasonNotFound)
	}

	if ev.ClipCount != 2 {
		t.Fatalf("ClipCount = %d, want 2", ev.ClipCount)
	}
}

// keysOf returns the sorted key list of a map[string]string. Used in
// failure messages to show the actual key set.
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// equalStringSlices returns true iff a and b have identical contents in
// the same order. Strict ordered equality is the right test here: PR 5
// + PR 6 preserve the requested-ID order in the resolved set, not a
// canonical re-sort.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestClipSourceBuilder_TranscriptRuneSafeExcerpt_A4 pins the
// rune-safe truncation contract introduced in PR A4 (June 2026). The
// previous implementation byte-truncated `excerpt[:500]`, which
// silently split multi-byte UTF-8 codepoints (CJK ideographs,
// supplementary-plane emoji, accented Latin) and produced invalid
// downstream bytes. The new helper truncates by RUNES and appends
// U+2026 HORIZONTAL ELLIPSIS, pinning four properties per case:
//
//  1. ev.AssembledText is well-formed UTF-8 (utf8.ValidString).
//  2. The bytes contain no U+FFFD replacement character — the
//     canonical fingerprint of a forced byte-cut mid-codepoint.
//  3. The `Transcript: ...` line has the expected rune count.
//  4. The line ends with U+2026 iff the input exceeded 500 runes.
//
// Inputs deliberately cover the failure modes the old code hit: pure
// CJK (3-byte runes), pure supplementary-plane emoji (4-byte runes),
// and an ASCII / CJK / emoji mixture where the byte-budget would have
// landed inside a CJK character.
//
// Note on AssembledText: the call site runs
// `strings.TrimSpace(sourceTextBuilder.String())` (BuildClipContext
// ~L294-296), so the transcript line is the LAST non-whitespace
// segment — there is no trailing "\n" terminator to find. The test
// slices from the `  Transcript: ` prefix to end-of-string.
func TestClipSourceBuilder_TranscriptRuneSafeExcerpt_A4(t *testing.T) {
	ellipsis := "\u2026"

	cases := []struct {
		label                string
		transcript           string
		wantExcerptRunes     int
		wantEndsWithEllipsis bool
	}{
		// Below the budget: untouched.
		{"short ASCII, no truncation", "Hello world", 11, false},
		{"exactly 500 ASCII runes, no truncation", strings.Repeat("a", 500), 500, false},

		// One above the budget: truncated to 500 + U+2026 = 501 runes.
		{"501 ASCII runes, truncated", strings.Repeat("a", 501), 501, true},

		// CJK ideographs (3 bytes / rune in UTF-8): byte-truncating at
		// 500 would split a codepoint; rune-truncation must not.
		{"600 CJK ideographs `世`, truncated", strings.Repeat("世", 600), 501, true},
		{"500 `界` + 100 `世`, boundary at CJK", strings.Repeat("界", 500) + strings.Repeat("世", 100), 501, true},

		// 4-byte supplementary-plane emoji: each emoji is a single rune
		// but 4 bytes. Byte-cut at 500 would fall mid-emoji.
		{"600 emoji `😀`, truncated", strings.Repeat("😀", 600), 501, true},

		// Boundary probe: 498 ASCII + 1 CJK + 100 emoji = 599 runes.
		// The byte-budget would have landed inside either the CJK
		// rune (splitting its 3 bytes) or one of the emoji (splitting
		// its 4 bytes). Rune truncation must cleanly stop after the
		// single '😀' at rune index 500.
		{"mixed ASCII/CJK/emoji, boundary at emoji",
			strings.Repeat("a", 498) + "界" + strings.Repeat("😀", 100), 501, true},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			const clipID = "clip-A4"
			clip := &asset.Asset{ID: clipID, Name: "A4 clip"}
			clip.SetMetadataString("transcript", tc.transcript)
			clip.SetDriveFileID(clipID + "-drive")
			clip.SetDriveLink("https://drive/" + clipID)

			stub := &stubClipsResolver{
				byID: map[string]*asset.Asset{clipID: clip},
			}
			b := scripts.NewClipSourceBuilder(stub, nil, zap.NewNop())
			b.ConfigureTextTrackReader(&a4StubTextTrackReader{transcripts: map[string]string{clipID: tc.transcript}})
			ev, _, _, err := b.BuildClipContext(context.Background(), []string{clipID}, &scripts.ClipGenerationOptions{Language: "en"})
			if err != nil {
				t.Fatalf("BuildClipContext: %v", err)
			}
			if ev == nil {
				t.Fatal("evidence is nil")
			}

			// Property 1: assembled text must be valid UTF-8.
			if !utf8.ValidString(ev.AssembledText) {
				t.Fatalf("ev.AssembledText is NOT valid UTF-8 — codepoint got split: %q", ev.AssembledText)
			}

			// Property 2: no replacement character (the canonical
			// fingerprint of a forced byte-cut mid-codepoint).
			if strings.ContainsRune(ev.AssembledText, '\uFFFD') {
				t.Fatalf("ev.AssembledText contains U+FFFD — multibyte codepoint was split: %q", ev.AssembledText)
			}

			// Extract the transcript line. TrimSpace at the call site
			// means we slice prefix → EOF, not prefix → "\n".
			const prefix = "  Transcript: "
			i := strings.Index(ev.AssembledText, prefix)
			if i < 0 {
				t.Fatalf("Transcript prefix not found in: %q", ev.AssembledText)
			}
			start := i + len(prefix)
			excerpt := ev.AssembledText[start:]

			// Property 3: rune budget.
			if got := utf8.RuneCountInString(excerpt); got != tc.wantExcerptRunes {
				t.Fatalf("excerpt rune count = %d, want %d (excerpt=%q)", got, tc.wantExcerptRunes, excerpt)
			}

			// Property 4: ellipsis iff truncated.
			gotEllipsis := strings.HasSuffix(excerpt, ellipsis)
			if gotEllipsis != tc.wantEndsWithEllipsis {
				t.Fatalf("excerpt ends with U+2026 = %v, want %v (excerpt=%q)", gotEllipsis, tc.wantEndsWithEllipsis, excerpt)
			}
		})
	}
}

// TestClipSourceBuilder_ModelSourceText_IsNarrativeOnly pins the
// model-facing projection contract. The builder still produces a
// technical AssembledText for compatibility, but ModelSourceText
// must be free of technical locators and include only the narrative
// clip view.
func TestClipSourceBuilder_ModelSourceText_IsNarrativeOnly(t *testing.T) {
	const clipID = "clip-model-view"

	clip := newAssetWithDriveLink(clipID, "", "Pacquiao vs Broner", "https://drive.google.com/file/d/clip-model-view/view")
	clip.SearchText = "Pacquiao controls the distance with his jab."
	clip.SetMetadataString("description", "Visual recap of Pacquiao and Broner at center ring.")
	clip.Tags = []string{"commentator", "boxing"}

	reader := &stubTextTrackReader{
		tracks: map[string]*detail.TextTrack{
			clipID + ":en": makeTrack(clipID, "en", "Pacquiao appears faster and lighter on his feet.", detail.TextTrackReady),
		},
		readyLanguages: map[string][]string{clipID: {"en"}},
	}
	resolver := &stubClipsResolver{byID: map[string]*asset.Asset{clipID: clip}}

	builder := scripts.NewClipSourceBuilder(resolver, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(reader)

	ev, _, sourceText, err := builder.BuildClipContext(context.Background(), []string{clipID}, &scripts.ClipGenerationOptions{Language: "en"})
	require.NoError(t, err)
	require.NotNil(t, ev)
	require.NotEmpty(t, sourceText)

	modelText := ev.ModelSourceText()
	require.NotEmpty(t, modelText)
	assert.Contains(t, modelText, "NARRATIVE EVIDENCE 1")
	assert.Contains(t, modelText, "Ref: clip_1")
	// The grounding Description is the canonical EvidenceResolver winner:
	// with a READY transcript present, the transcript tier wins over
	// search_text / description (strict 5-source precedence).
	assert.Contains(t, modelText, "Description: Pacquiao appears faster and lighter on his feet.")
	assert.Contains(t, modelText, "Transcript: Pacquiao appears faster and lighter on his feet.")
	assert.Contains(t, modelText, "DurationMs: 0")
	assert.NotContains(t, modelText, "CLIP "+clipID+":")
	assert.NotContains(t, modelText, "drive.google.com")
	assert.NotContains(t, modelText, "youtube.com")
	assert.NotContains(t, modelText, "Tags:")
	assert.NotContains(t, modelText, "commentator")
	assert.NotContains(t, modelText, "announcer")
}

// a4StubTextTrackReader is a per-subtest TextTrackReader stub used by
// TestClipSourceBuilder_TranscriptRuneSafeExcerpt_A4. It returns the
// test-fixture transcript (parameterized in tc.transcript) for the
// canonical clip ID at language "en" with kind=Transcript. All other
// (assetID, language, kind) triples return (nil, nil, nil) so the
// test surface is fully hermetic — no real DB / Whisper path.
type a4StubTextTrackReader struct {
	transcripts map[string]string
}

func (s *a4StubTextTrackReader) FindReady(_ context.Context, assetID, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	if text, ok := s.transcripts[assetID]; ok && kind == detail.TextTrackTranscript {
		return &detail.TextTrack{
			AssetID:       assetID,
			LanguageCode:  languageCode,
			TextKind:      detail.TextTrackTranscript,
			TextContent:   text,
			TextHash:      "a4-" + assetID,
			SourceVersion: "v1",
			Status:        detail.TextTrackReady,
		}, nil, nil
	}
	return nil, nil, nil
}

func (s *a4StubTextTrackReader) ListReadyLanguages(_ context.Context, assetID string, _ detail.TextTrackKind) ([]string, error) {
	if _, ok := s.transcripts[assetID]; ok {
		return []string{"en"}, nil
	}
	return nil, nil
}

// Compile-time pin: a4StubTextTrackReader satisfies the canonical
// scriptports.TextTrackReader surface (Fase 4 strict cutover).
var _ scriptports.TextTrackReader = (*a4StubTextTrackReader)(nil)
