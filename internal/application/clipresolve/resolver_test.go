package clipresolve_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clipresolve"
)

// ── Test fixtures ─────────────────────────────────────────────────

// fakeSlotIndex is a map-backed SlotIndex fixture. The lookup table
// keys slotRef → []assetID. Every assetID returned by a successful
// LookupCandidate should be non-empty (godlike/07 contract).
type fakeSlotIndex struct {
	table      map[string][]string
	unknownErr error // injected failure for the "missing slot" probe
	oobErr     error // injected failure for the "out of bounds" probe
}

func (f *fakeSlotIndex) LookupCandidate(slotRef string, idx int) (string, error) {
	if f.unknownErr != nil {
		// Simulate the production SlotIndex returning ErrUnknownSlot
		// for an off-table slot even when the slotRef is non-empty.
		// We honour the injected error only when the slotRef is NOT
		// in the table (so happy paths still exercise real lookup).
		if _, ok := f.table[slotRef]; !ok {
			return f.unknownErr.Error(), f.unknownErr
		}
	}
	if row, ok := f.table[slotRef]; ok {
		if idx < 0 || idx >= len(row) {
			return "", f.oobErr
		}
		return row[idx], nil
	}
	// Off-table default
	return "", clipresolve.ErrUnknownSlot
}

// fakeHydrator is a map-backed AssetHydrator fixture. The lookup
// table keys asset_id → AssetMetadata. The hydrateErr field lets
// each test inject a failure mode per asset_id (deterministic via
// map presence check).
type fakeHydrator struct {
	table      map[string]clipresolve.AssetMetadata
	hydrateErr error
}

func (f *fakeHydrator) Hydrate(_ context.Context, assetID string) (clipresolve.AssetMetadata, error) {
	if f.hydrateErr != nil {
		return clipresolve.AssetMetadata{}, f.hydrateErr
	}
	meta, ok := f.table[assetID]
	if !ok {
		return clipresolve.AssetMetadata{}, clipresolve.ErrAssetNotHydrated
	}
	return meta, nil
}

// defaultFixture returns a (SlotIndex, AssetHydrator) pair that
// exercises the canonical happy path:
//   - 3 slots ("slot-1", "slot-2", "slot-3")
//   - 3 candidates per slot (different asset_ids)
//   - Each candidate fully hydrated with DriveLink + FolderPath +
//     NormalizedGroup
func defaultFixture() (*fakeSlotIndex, *fakeHydrator) {
	idx := &fakeSlotIndex{table: map[string][]string{
		"slot-1": {"A1", "A2", "A3"},
		"slot-2": {"B1", "B2", "B3"},
		"slot-3": {"C1", "C2", "C3"},
	}}
	hyd := &fakeHydrator{table: map[string]clipresolve.AssetMetadata{
		"A1": {DriveLink: "https://drive.google.com/file/d/A1/view", FolderPath: "/Boxe/PacquiaoBroner", NormalizedGroup: "boxe"},
		"A2": {DriveLink: "https://drive.google.com/file/d/A2/view", FolderPath: "/Boxe/PacquiaoBroner", NormalizedGroup: "boxe"},
		"A3": {DriveLink: "https://drive.google.com/file/d/A3/view", FolderPath: "/Boxe/PacquiaoBroner", NormalizedGroup: "boxe"},
		"B1": {DriveLink: "https://drive.google.com/file/d/B1/view", FolderPath: "/HipHop/Sample", NormalizedGroup: "hiphop"},
		"B2": {DriveLink: "https://drive.google.com/file/d/B2/view", FolderPath: "/HipHop/Sample", NormalizedGroup: "hiphop"},
		"B3": {DriveLink: "https://drive.google.com/file/d/B3/view", FolderPath: "/HipHop/Sample", NormalizedGroup: "hiphop"},
		"C1": {DriveLink: "https://drive.google.com/file/d/C1/view", FolderPath: "/General/Sample", NormalizedGroup: "general"},
		"C2": {DriveLink: "https://drive.google.com/file/d/C2/view", FolderPath: "/General/Sample", NormalizedGroup: "general"},
		"C3": {DriveLink: "https://drive.google.com/file/d/C3/view", FolderPath: "/General/Sample", NormalizedGroup: "general"},
	}}
	return idx, hyd
}

// ── parseCandidateRef (exercised via Resolve) ─────────────────────

// TestResolver_HappyPath_ReturnsComposedMapping pins the canonical
// 3-step pipeline: parse → lookup → hydrate → compose → validate.
// Every AssetMapping field is asserted against the fixture so a
// future drift in the SlotIndex / AssetHydrator surface surfaces as
// a test failure, not a silent zero-value.
func TestResolver_HappyPath_ReturnsComposedMapping(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	got, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.AssetID != "A2" {
		t.Errorf("AssetID = %q, want A2", got.AssetID)
	}
	if !strings.Contains(got.DriveLink, "A2") {
		t.Errorf("DriveLink = %q, want to contain A2", got.DriveLink)
	}
	if got.FolderPath != "/Boxe/PacquiaoBroner" {
		t.Errorf("FolderPath = %q, want /Boxe/PacquiaoBroner", got.FolderPath)
	}
	if got.NormalizedGroup != "boxe" {
		t.Errorf("NormalizedGroup = %q, want boxe", got.NormalizedGroup)
	}
}

// TestResolver_EmptySlotRefMaybe_FallsBackToParsedSlot pins the
// permissive branch: when slotRefMaybe is "", the resolver trusts
// the embedded slotRef in candidate_ref (no mismatch error).
func TestResolver_EmptySlotRefMaybe_FallsBackToParsedSlot(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	got, err := r.Resolve(context.Background(), "", "slot-3:candidate-2")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.AssetID != "C3" {
		t.Errorf("AssetID = %q, want C3 (slot-3 idx 2)", got.AssetID)
	}
	if got.NormalizedGroup != "general" {
		t.Errorf("NormalizedGroup = %q, want general", got.NormalizedGroup)
	}
}

// TestResolver_SlotRefMismatch_FailClosed pins the strict-mismatch
// branch: when slotRefMaybe is non-empty AND does not match the
// embedded slot in candidate_ref, the resolver surfaces
// ErrInvalidCandidateRef. godlike/07: no silent override, no
// clamping, no trusting whichever side arrived first.
func TestResolver_SlotRefMismatch_FailClosed(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "slot-2", "slot-1:candidate-0")
	if err == nil {
		t.Fatal("mismatch must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
		t.Errorf("err = %v, want errors.Is(ErrInvalidCandidateRef)", err)
	}
	// Negative pin: unmask the cross-check, never any "fix it
	// silently" path.
	if !strings.Contains(err.Error(), "cross-check failed") {
		t.Errorf("err %q must mention cross-check failure for audit", err)
	}
}

// TestResolver_UnknownSlot_FailClosed pins the LookupCandidate
// miss path: when the slot is off the in-memory Index, the
// resolver surfaces a wrapped ErrUnknownSlot.
func TestResolver_UnknownSlot_FailClosed(t *testing.T) {
	idx := &fakeSlotIndex{table: map[string][]string{
		"slot-1": {"A1"},
	}}
	hyd := &fakeHydrator{table: map[string]clipresolve.AssetMetadata{
		"A1": {DriveLink: "https://drive/x"},
	}}
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "missing-slot", "missing-slot:candidate-0")
	if err == nil {
		t.Fatal("unknown slot must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrUnknownSlot) {
		t.Errorf("err = %v, want errors.Is(ErrUnknownSlot)", err)
	}
}

// TestResolver_OutOfBoundsIndex_FailClosed pins the LookupCandidate
// out-of-bounds branch: when idx exceeds the slot's candidate
// length, the resolver surfaces ErrUnknownIndex. godlike/07: no
// clamping the idx into the table, no returning the last
// candidate as a fallback.
func TestResolver_OutOfBoundsIndex_FailClosed(t *testing.T) {
	idx := &fakeSlotIndex{table: map[string][]string{
		"slot-1": {"A1", "A2"}, // length 2, valid idx 0..1
	}, oobErr: clipresolve.ErrUnknownIndex}
	hyd := &fakeHydrator{table: map[string]clipresolve.AssetMetadata{
		"A1": {DriveLink: "https://drive/x"},
		"A2": {DriveLink: "https://drive/y"},
	}}
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate-99")
	if err == nil {
		t.Fatal("out-of-bounds idx must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrUnknownIndex) {
		t.Errorf("err = %v, want errors.Is(ErrUnknownIndex)", err)
	}
}

// TestResolver_HydrateFailure_FailClosed pins the AssetHydrator
// error branch: when Hydrate returns a typed error (DB miss / IO),
// the resolver surfaces a wrapped ErrAssetNotHydrated.
func TestResolver_HydrateFailure_FailClosed(t *testing.T) {
	idx := &fakeSlotIndex{table: map[string][]string{
		"slot-1": {"A1"},
	}}
	hyd := &fakeHydrator{table: map[string]clipresolve.AssetMetadata{
		"A1": {DriveLink: "https://drive/x"},
	}, hydrateErr: clipresolve.ErrAssetNotHydrated}
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate-0")
	if err == nil {
		t.Fatal("hydrate failure must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrAssetNotHydrated) {
		t.Errorf("err = %v, want errors.Is(ErrAssetNotHydrated)", err)
	}
}

// TestResolver_EmptyDriveLink_FailClosed pins godlike/07
// NO-FAKE-AVAILABILITY at the binding boundary: when Hydrate
// succeeds but DriveLink="" (asset is missing on Drive), the
// resolver surfaces ErrAssetMappingIncomplete — NEVER a placeholder
// URL, NEVER a clamped empty string that the worker UI cannot
// distinguish from "no asset".
func TestResolver_EmptyDriveLink_FailClosed(t *testing.T) {
	idx := &fakeSlotIndex{table: map[string][]string{
		"slot-1": {"A1"},
	}}
	hyd := &fakeHydrator{table: map[string]clipresolve.AssetMetadata{
		"A1": {DriveLink: "", FolderPath: "/X/Y", NormalizedGroup: "general"},
	}}
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate-0")
	if err == nil {
		t.Fatal("missing drive_link must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrAssetMappingIncomplete) {
		t.Errorf("err = %v, want errors.Is(ErrAssetMappingIncomplete)", err)
	}
}

// ── parseCandidateRef shape (exercised via Resolve) ──────────────

// TestResolver_EmptyCandidateRef_FailClosed pins the parser's
// empty-ref branch.
func TestResolver_EmptyCandidateRef_FailClosed(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "slot-1", "")
	if err == nil {
		t.Fatal("empty ref must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
		t.Errorf("err = %v, want errors.Is(ErrInvalidCandidateRef)", err)
	}
}

// TestResolver_MissingCandidatePrefix_FailClosed pins the parser's
// missing-delimiter branch: a ref that does not contain ":candidate-"
// is uniformly fail-closed.
func TestResolver_MissingCandidatePrefix_FailClosed(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	for _, bad := range []string{"slot-1:0", "candidate-3", "just-slot-1"} {
		_, err := r.Resolve(context.Background(), "", bad)
		if err == nil {
			t.Errorf("malformed ref %q must surface typed error", bad)
			continue
		}
		if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
			t.Errorf("ref %q err = %v, want errors.Is(ErrInvalidCandidateRef)", bad, err)
		}
	}
}

// TestResolver_EmptySlotPart_FailClosed pins the parser's
// empty-slotRef branch (":candidate-3" → invalid slotRef).
func TestResolver_EmptySlotPart_FailClosed(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "", ":candidate-3")
	if err == nil {
		t.Fatal("empty slot-embedded section must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
		t.Errorf("err = %v, want errors.Is(ErrInvalidCandidateRef)", err)
	}
}

// TestResolver_EmptyIndex_FailClosed pins the parser's empty-index
// branch ("slot-1:candidate-" → invalid index).
func TestResolver_EmptyIndex_FailClosed(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate-")
	if err == nil {
		t.Fatal("empty index must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
		t.Errorf("err = %v, want errors.Is(ErrInvalidCandidateRef)", err)
	}
}

// TestResolver_NonNumericIndex_FailClosed pins the parser's
// strconv.Atoi-fail branch ("slot-1:candidate-abc" → invalid).
func TestResolver_NonNumericIndex_FailClosed(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate-abc")
	if err == nil {
		t.Fatal("non-numeric index must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
		t.Errorf("err = %v, want errors.Is(ErrInvalidCandidateRef)", err)
	}
}

// TestResolver_NegativeIndex_FailClosed pins the parser's
// negative-int branch (explicit rejection so the typed envelope is
// surfaced before slot-search tries to bind it).
func TestResolver_NegativeIndex_FailClosed(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	_, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate--1")
	if err == nil {
		t.Fatal("negative index must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
		t.Errorf("err = %v, want errors.Is(ErrInvalidCandidateRef)", err)
	}
}

// ── Configuration/discipline pins ─────────────────────────────────

// TestNewResolver_NilIndex_FailClosed pins the constructor's
// nil-component branch: resolvers must refuse to bind against nil
// SlotIndex or nil AssetHydrator (godlike/07 NO-FAKE-AVAILABILITY
// at the wire boundary). The resolver MUST surface the typed
// envelope rather than panic on first use.
func TestNewResolver_NilIndex_FailClosed(t *testing.T) {
	_, hyd := defaultFixture()

	if r := clipresolve.NewResolver(nil, hyd); r != nil {
		_, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate-0")
		if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
			t.Errorf("nil index, err = %v, want errors.Is(ErrInvalidCandidateRef)", err)
		}
	}

	idx, _ := defaultFixture()
	if r := clipresolve.NewResolver(idx, nil); r != nil {
		_, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate-0")
		if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
			t.Errorf("nil hydrator, err = %v, want errors.Is(ErrInvalidCandidateRef)", err)
		}
	}
}

// TestResolver_NilReceiver_DoesNotPanic pins the (*Resolver)(nil)
// branch: a method call on a nil receiver MUST return a typed
// error (not panic). godlike/07 NO-FAKE-AVAILABILITY extends to
// nil-receiver probe — call sites can defensively check for
// constructed-but-not-wired resolvers without a try-recover.
func TestResolver_NilReceiver_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil receiver panicked: %v", r)
		}
	}()
	var r *clipresolve.Resolver
	_, err := r.Resolve(context.Background(), "slot-1", "slot-1:candidate-0")
	if err == nil {
		t.Fatal("nil receiver must surface typed error")
	}
	if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
		t.Errorf("err = %v, want errors.Is(ErrInvalidCandidateRef)", err)
	}
}

// TestResolver_GladTolerantAcceptsTrimmableWhitespace pins the
// parser's boundary whitespace tolerance: the ref may carry
// leading/trailing whitespace (some upstream map<string, candidate>
// stores round-trip via fmt.Sprintf with stray padding) and the
// parser TRIMS only the parse-boundary whitespace. The canonical
// `":candidate-"` delimiter itself MUST remain intact — a ref with
// stray whitespace inside the delimiter is treated as malformed
// (the delimiter is part of the SSOT format, not a free-text zone).
func TestResolver_GladTolerantAcceptsTrimmableWhitespace(t *testing.T) {
	idx, hyd := defaultFixture()
	r := clipresolve.NewResolver(idx, hyd)

	// Acceptable: outer whitespace only — delimiter intact.
	for _, ok := range []string{
		"slot-1:candidate-0",     // canonical
		"  slot-1:candidate-0",   // leading
		"slot-1:candidate-0  ",   // trailing
		"  slot-1:candidate-0  ", // both
		"\tslot-1:candidate-0\n", // tab+newline
	} {
		got, err := r.Resolve(context.Background(), "slot-1", ok)
		if err != nil {
			t.Errorf("ref %q must parse cleanly, got: %v", ok, err)
			continue
		}
		if got.AssetID != "A1" {
			t.Errorf("ref %q: AssetID = %q, want A1", ok, got.AssetID)
		}
	}

	// Negative pin: internal whitespace inside the delimiter is
	// NOT acceptable — the canonical ":candidate-" must remain
	// intact. This pins the SSOT format itself, not just the
	// happy-path trim.
	for _, bad := range []string{
		"slot-1 :candidate-0",   // space before ':'
		"slot-1: candidate-0",   // space after ':'
		"slot-1: candidate -0",  // space before '-'
		"slot-1 : candidate -0", // several stray spaces
	} {
		_, err := r.Resolve(context.Background(), "slot-1", bad)
		if err == nil {
			t.Errorf("internal-whitespace ref %q must surface typed error", bad)
		} else if !errors.Is(err, clipresolve.ErrInvalidCandidateRef) {
			t.Errorf("internal-whitespace ref %q err = %v, want errors.Is(ErrInvalidCandidateRef)", bad, err)
		}
	}
}

// TestCandidateRefPrefix_StaysInSyncWithClipviewFormat is a
// forward-pointer gate: the test pins that the canonical delimiter
// is exactly ":candidate-" (matches the literal in
// clipview/builder.go). A future refactor that flips the
// delimiter in either package surfaces here as a docstring-only
// drift — the literal in clipview/builder.go has to be updated
// alongside this constant, OR clipview's roundtrip test fails
// first.
func TestCandidateRefPrefix_StaysInSyncWithClipviewFormat(t *testing.T) {
	if clipresolve.CandidateRefPrefix != ":candidate-" {
		t.Fatalf("CandidateRefPrefix = %q, want %q (drift from clipview/builder.go)",
			clipresolve.CandidateRefPrefix,
			":candidate-")
	}
}
