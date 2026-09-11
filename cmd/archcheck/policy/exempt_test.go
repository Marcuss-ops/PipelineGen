package policy

import "testing"

// TestCanonicalMediaWriterPrimaryIsShared pins the SSOT invariant behind the
// consolidation: the single canonical owner path used by the finalizer gate
// MUST be one of the files exempted by the codebase-wide media-writer gate.
// Drift between those two lists was the duplicated-allowlist failure this
// file removes.
func TestCanonicalMediaWriterPrimaryIsShared(t *testing.T) {
	const want = "internal/platform/postgres/media/media_committer.go"
	if CanonicalMediaWriterPrimary != want {
		t.Fatalf("CanonicalMediaWriterPrimary = %q, want %q", CanonicalMediaWriterPrimary, want)
	}
	if !IsCanonicalMediaWriter(CanonicalMediaWriterPrimary) {
		t.Fatalf("CanonicalMediaWriterPrimary %q is not a canonical media writer", CanonicalMediaWriterPrimary)
	}
	// Package ownership, not a filename list: any file inside the canonical
	// PostgreSQL media package is a canonical writer by construction. The
	// filename allowlist used to drift the moment such a file landed
	// (delete_saga.go) and then reported the SSOT owner as a violation.
	for _, packageFile := range []string{
		"internal/platform/postgres/media/delete_saga.go",
		"internal/platform/postgres/media/outbox.go",
	} {
		if !IsCanonicalMediaWriter(packageFile) {
			t.Fatalf("file %q inside the canonical media package must be a canonical writer", packageFile)
		}
	}
	// The demolished SQLite media writer family must never be exempted:
	// its reappearance is a violation, not an exemption.
	for _, retired := range []string{
		"internal/platform/sqlite/assets/asset_committer.go",
		"internal/platform/sqlite/assets/imagesregistry/canonical_clip_writer.go",
		"internal/platform/sqlite/assets/imagesregistry/media_committer.go",
	} {
		if IsCanonicalMediaWriter(retired) {
			t.Fatalf("retired SQLite media writer %q must not be canonical", retired)
		}
	}
}

// TestSkipDirsDoesNotMutateStandardSet verifies SkipDirs clones the shared
// set instead of aliasing it, so one scanner cannot widen another's surface.
func TestSkipDirsDoesNotMutateStandardSet(t *testing.T) {
	extended := SkipDirs("testdata", "test")
	for _, dir := range []string{"testdata", "test"} {
		if !extended[dir] {
			t.Fatalf("extra dir %q missing from SkipDirs result", dir)
		}
		if StandardSkipDirs[dir] {
			t.Fatalf("SkipDirs mutated the shared set with %q", dir)
		}
	}
	for dir := range StandardSkipDirs {
		if !extended[dir] {
			t.Fatalf("standard dir %q missing from SkipDirs result", dir)
		}
	}
}

// TestPrefixesPreservesOrderAndCopies verifies Prefixes concatenates groups
// in order into a fresh slice with no aliasing of the source groups.
func TestPrefixesPreservesOrderAndCopies(t *testing.T) {
	first := []string{"a"}
	second := []string{"b", "c"}
	got := Prefixes(first, second)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("Prefixes len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Prefixes[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	got[0] = "mutated"
	if first[0] != "a" {
		t.Fatalf("Prefixes aliased the source group: first[0] = %q", first[0])
	}
}
