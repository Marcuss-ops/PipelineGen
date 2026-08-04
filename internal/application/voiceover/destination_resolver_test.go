// Package voiceover — destination_resolver_test.go (PR-VO-AUDIT-P02-P03,
// June 2026).
//
// Unit tests for the canonical destination resolver introduced in P0.2
// (centralised fallback) + P0.3 (typed DestinationKind routing). Each
// case pins a precedence edge in the audit's mandatory hierarchy:
//
//  1. explicit         → FolderID verbatim, no resolver call.
//  2. group            → asset.Resolver call, Group required.
//  3. auto / empty     → Group → FolderID → defaultFolderID.
//  4. nil dest         → defaultFolderID (the inline-fallback
//     replacement that used to live in
//     `Service.Generate`).
//
// `recordingResolver` (defined in `process_metadata_test.go`) is
// reused as the asset.Resolver stub so the test body stays minimal.
// `recordingResolver` was originally declared for the PR-VO-B2
// forwarding/mirroring tests; it satisfies the same signature here.
package voiceover

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestResolveDestination_ExplicitKindUsesFolderID: when Kind=explicit
// and FolderID is set, the resolver uses FolderID verbatim and NEVER
// calls the asset.Resolver. Passing nil resolver to this branch must
// succeed (zero resolver calls in the explicit branch).
func TestResolveDestination_ExplicitKindUsesFolderID(t *testing.T) {
	// A raw non-empty folder ID is forwarded verbatim; validation of
	// Drive existence belongs to the Drive resolver/publisher seam.
	dest := &DestinationRequest{
		Kind:          string(KindExplicit),
		FolderID:      "explicit-folder-id",
		FolderPath:    "/explicit/path",
		SubfolderName: "intro",
		StyleGroup:    "cinematic",
	}

	// nil resolver must NOT be consulted in the explicit branch.
	rd, err := ResolveVoiceoverDestination(context.Background(), dest, nil, "default-folder")
	if err != nil {
		t.Fatalf("ResolveVoiceoverDestination: %v", err)
	}
	if rd.FolderID != "explicit-folder-id" {
		t.Errorf("FolderID = %q; want explicit-folder-id", rd.FolderID)
	}
	if rd.FolderPath != "/explicit/path" {
		t.Errorf("FolderPath = %q; want /explicit/path", rd.FolderPath)
	}
	if rd.SubfolderName != "intro" {
		t.Errorf("SubfolderName = %q; want intro", rd.SubfolderName)
	}
	if rd.StyleGroup != "cinematic" {
		t.Errorf("StyleGroup = %q; want cinematic", rd.StyleGroup)
	}
	// DriveLink must NOT be populated by the explicit branch (resolver
	// was not consulted).
	if rd.DriveLink != "" {
		t.Errorf("DriveLink = %q; want empty (explicit branch does not consult resolver)", rd.DriveLink)
	}
}

func TestResolveDestination_ExplicitKindMissingFolderIsUnavailable(t *testing.T) {
	rd, err := ResolveVoiceoverDestination(context.Background(), &DestinationRequest{
		Kind: string(KindExplicit),
	}, &recordingResolver{}, "historical-root")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrExplicitKindRequiresFolderID)
	assert.ErrorIs(t, err, ErrVoiceoverDestinationUnavailable)
	assert.Nil(t, rd)
}

// TestResolveDestination_GroupKindFallsThroughOnMissingGroup: when
// Kind=group but Group is empty, the resolver MUST error (NOT fall
// through to auto or use the default folder). The validator pattern
// is mirrored at the API layer (handlers return 400) but the resolver
// reasserts for internal callers that bypass the handler. Test name
// preserves the audit-mandated spec wording (Micro-commit #2 brief)
// — the "FallsThrough" semantically describes the resolution branch
// order: the resolver does NOT silently fall through the Group
// branch to the auto/default branch on missing Group.
func TestResolveDestination_GroupKindFallsThroughOnMissingGroup(t *testing.T) {
	dest := &DestinationRequest{
		Kind: string(KindGroup),
		// Group intentionally empty.
	}

	rd, err := ResolveVoiceoverDestination(context.Background(), dest, &recordingResolver{}, "default-folder")
	if err == nil {
		t.Fatalf("expected error for kind=group with empty Group; got %#v", rd)
	}
	if !errors.Is(err, ErrGroupKindRequiresGroup) {
		t.Errorf("err = %v; want ErrGroupKindRequiresGroup", err)
	}
	if rd != nil {
		t.Errorf("rd must be nil on error; got %#v", rd)
	}
}

// TestResolveDestination_NoKindFallsBackToConfiguredFolder: when Kind
// is empty (legacy auto-detect), Group and FolderID are both empty,
// but the configured defaultFolderID is non-empty. Resolver uses
// defaultFolderID rather than erroring out.
func TestResolveDestination_NoKindFallsBackToConfiguredFolder(t *testing.T) {
	dest := &DestinationRequest{
		// Kind intentionally empty (legacy auto-detect).
	}

	rd, err := ResolveVoiceoverDestination(context.Background(), dest, &recordingResolver{}, "default-folder-id")
	if err != nil {
		t.Fatalf("ResolveVoiceoverDestination: %v", err)
	}
	if rd.FolderID != "default-folder-id" {
		t.Errorf("FolderID = %q; want default-folder-id", rd.FolderID)
	}
	if rd.FolderPath != "" {
		t.Errorf("FolderPath = %q; want empty (auto branch does not populate FolderPath)", rd.FolderPath)
	}
}

// TestResolveDestination_AllMissingReturnsMissingFolder: when Kind is
// empty, Group and FolderID are empty, and defaultFolderID is also
// empty, the resolver surfaces ErrMissingFolder. The legacy log-marker
// "missing_folder_id" is preserved through the wrapped error so
// existing telemetry keeps inheriting the surface.
func TestResolveDestination_AllMissingReturnsMissingFolder(t *testing.T) {
	dest := &DestinationRequest{}

	rd, err := ResolveVoiceoverDestination(context.Background(), dest, &recordingResolver{}, "")
	if err == nil {
		t.Fatalf("expected missing_folder_id error; got %#v", rd)
	}
	if !errors.Is(err, ErrMissingFolder) {
		t.Errorf("err = %v; want ErrMissingFolder", err)
	}
	if rd != nil {
		t.Errorf("rd must be nil on error; got %#v", rd)
	}
}

// TestResolveDestination_GroupKindCallsResolver: positive regression —
// when Kind=group + Group="boxe" + StyleGroup="anime", the resolver
// is invoked with Source=voiceover + Group=boxe + StyleGroup=anime,
// and ResolvedDestination receives FolderID from the resolver +
// StyleGroup mirrored verbatim from the input.
func TestResolveDestination_GroupKindCallsResolver(t *testing.T) {
	stub := &recordingResolver{result: &asset.ResolveResult{FolderID: "resolved-folder-id", FolderPath: "/resolved/boxe"}}
	dest := &DestinationRequest{
		Kind:       string(KindGroup),
		Group:      "boxe",
		StyleGroup: "anime",
		FolderID:   "<ignored>", // explicit FolderID is ignored in KindGroup branch
	}

	rd, err := ResolveVoiceoverDestination(context.Background(), dest, stub, "default-folder-id")
	if err != nil {
		t.Fatalf("ResolveVoiceoverDestination: %v", err)
	}
	if stub.got == nil {
		t.Fatal("resolver not called (Kind=group should always invoke the resolver)")
	}
	if stub.got.Group != "boxe" {
		t.Errorf("resolver got Group = %q; want boxe", stub.got.Group)
	}
	if stub.got.Source != "voiceover" {
		t.Errorf("resolver got Source = %q; want voiceover", stub.got.Source)
	}
	if stub.got.StyleGroup != "anime" {
		t.Errorf("resolver got StyleGroup = %q; want anime", stub.got.StyleGroup)
	}
	if rd.StyleGroup != "anime" {
		t.Errorf("ResolvedDestination.StyleGroup = %q; want anime (mirrored)", rd.StyleGroup)
	}
	if rd.Group != "boxe" {
		t.Errorf("ResolvedDestination.Group = %q; want boxe", rd.Group)
	}
}

// TestResolveDestination_NilDestFallsBackToDefault: when dest is nil,
// the resolver treats it as KindAuto with no caller-supplied info and
// uses the defaultFolderID. This is the audit's P0.2 closure of the
// inline fallback that used to live in `Service.Generate` — the
// canonical resolver is the ONLY place where the cfg default is
// consulted.
func TestResolveDestination_NilDestFallsBackToDefault(t *testing.T) {
	rd, err := ResolveVoiceoverDestination(context.Background(), nil, &recordingResolver{}, "default-folder-id")
	if err != nil {
		t.Fatalf("ResolveVoiceoverDestination: %v", err)
	}
	if rd.FolderID != "default-folder-id" {
		t.Errorf("FolderID = %q; want default-folder-id", rd.FolderID)
	}
	if rd.SubfolderName != "" {
		t.Errorf("SubfolderName = %q; want empty (nil dest branch)", rd.SubfolderName)
	}
	if rd.StyleGroup != "" {
		t.Errorf("StyleGroup = %q; want empty (nil dest branch)", rd.StyleGroup)
	}
}

// TestResolveDestination_NilDestAndNoDefaultReturnsMissingFolder:
// nil-dest + empty defaultFolderID is the canonical P0.2 audit
// missing_folder_id short-circuit. The wrapped error keeps the
// "missing_folder_id" log-marker so existing telemetry stays intact.
func TestResolveDestination_NilDestAndNoDefaultReturnsMissingFolder(t *testing.T) {
	rd, err := ResolveVoiceoverDestination(context.Background(), nil, &recordingResolver{}, "")
	if err == nil {
		t.Fatalf("expected missing_folder_id error; got %#v", rd)
	}
	if !errors.Is(err, ErrMissingFolder) {
		t.Errorf("err = %v; want ErrMissingFolder", err)
	}
	if rd != nil {
		t.Errorf("rd must be nil on error; got %#v", rd)
	}
}

// TestResolveDestination_UnknownKindError: when Kind is non-empty
// and not in {explicit, group, auto}, the resolver errors with
// ErrInvalidDestinationKind. Defensive — catches future API drift
// before the resolver silently degrades to a wrong routing decision.
func TestResolveDestination_UnknownKindError(t *testing.T) {
	dest := &DestinationRequest{
		Kind: "bogus",
	}

	rd, err := ResolveVoiceoverDestination(context.Background(), dest, &recordingResolver{}, "default-folder")
	if err == nil {
		t.Fatalf("expected error for unknown Kind; got %#v", rd)
	}
	if !errors.Is(err, ErrInvalidDestinationKind) {
		t.Errorf("err = %v; want ErrInvalidDestinationKind", err)
	}
	if rd != nil {
		t.Errorf("rd must be nil on error; got %#v", rd)
	}
}

// TestResolveDestination_EmptyKindMapsToAuto: the legacy
// "`Kind=\"\"` means auto-detect" contract is preserved verbatim so
// pre-PR-VO-C1 callers continue to be handled identically.
func TestResolveDestination_EmptyKindMapsToAuto(t *testing.T) {
	// Group set, no FolderID, no Kind → auto-detect should consult
	// resolver (Group has precedence over FolderID per back-compat
	// precedence).
	dest := &DestinationRequest{
		Group: "boxe",
		// Kind intentionally empty.
	}
	stub := &recordingResolver{result: &asset.ResolveResult{FolderID: "resolved-folder-id", FolderPath: "/resolved/boxe"}}

	rd, err := ResolveVoiceoverDestination(context.Background(), dest, stub, "default-folder-id")
	if err != nil {
		t.Fatalf("ResolveVoiceoverDestination: %v", err)
	}
	if stub.got == nil {
		t.Fatal("resolver not called for legacy Kind=empty with Group set")
	}
	if stub.got.Group != "boxe" {
		t.Errorf("resolver got Group = %q; want boxe", stub.got.Group)
	}
	// Even though Group is set, defaultFolderID is irrelevant here —
	// the resolver is the source of FolderID.
	if rd.FolderID == "default-folder-id" {
		t.Errorf("FolderID = %q; want stub value, NOT defaultFolderID (Group takes precedence)", rd.FolderID)
	}
}
