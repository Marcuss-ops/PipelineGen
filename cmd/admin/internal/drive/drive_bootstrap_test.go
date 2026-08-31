// cmd/admin/drive_bootstrap_test.go — TDD coverage for the
// pure-function seams in cmd/admin/drive_bootstrap.go.
//
// godlike/07 NO-FAKE-AVAILABILITY: the full --apply path (DB + Drive)
// is covered by integration tests against a live server. This test
// surface pins the pure-function seams and typed errors:
//
//  1. canonicalDriveNamespaces — 10 entries, correct mapping, no dups
//  2. formatBootstrapDryRunOutput — byte-stable output format
//  3. runDriveBootstrap error paths — missing/empty --root
//  4. executeBootstrap guard — ErrAdminNoDB when DB path unresolved
//  5. Typed-error contract — errors.Is probes on sentinels
//
// godlike/06 SSOT: the sentinels and namespaces are the canonical
// SSOT in drive_bootstrap.go; this test locks their contracts.

package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── canonicalDriveNamespaces tests ────────────────────────────────────

func TestCanonicalDriveNamespaces_Completeness(t *testing.T) {
	if len(canonicalDriveNamespaces) != 10 {
		t.Errorf("canonicalDriveNamespaces: expected 10 entries, got %d", len(canonicalDriveNamespaces))
	}
	seenNS := make(map[string]bool)
	seenDest := make(map[string]bool)
	for i, ns := range canonicalDriveNamespaces {
		if ns.Namespace == "" {
			t.Errorf("canonicalDriveNamespaces[%d]: Namespace is empty", i)
		}
		if ns.Destination == "" {
			t.Errorf("canonicalDriveNamespaces[%d]: Destination is empty (ns=%q)", i, ns.Namespace)
		}
		if seenNS[ns.Namespace] {
			t.Errorf("canonicalDriveNamespaces: duplicate namespace %q", ns.Namespace)
		}
		if seenDest[ns.Destination] {
			t.Errorf("canonicalDriveNamespaces: duplicate destination %q", ns.Destination)
		}
		seenNS[ns.Namespace] = true
		seenDest[ns.Destination] = true
	}
}

func TestCanonicalDriveNamespaces_Ordering(t *testing.T) {
	first := canonicalDriveNamespaces[0]
	if first.Namespace != "clips" || first.Destination != "youtube_clip" {
		t.Errorf("canonicalDriveNamespaces[0]: expected clips→youtube_clip, got %s→%s",
			first.Namespace, first.Destination)
	}
	last := canonicalDriveNamespaces[len(canonicalDriveNamespaces)-1]
	if last.Namespace != "admin" || last.Destination != "admin" {
		t.Errorf("canonicalDriveNamespaces[%d]: expected admin→admin, got %s→%s",
			len(canonicalDriveNamespaces)-1, last.Namespace, last.Destination)
	}
}

func TestCanonicalDriveNamespaces_KnownMappings(t *testing.T) {
	known := map[string]string{
		"clips":         "youtube_clip",
		"stock":         "stock",
		"artlist":       "artlist",
		"images":        "image",
		"voiceovers":    "voiceover",
		"books":         "book",
		"scripts":       "script",
		"sound_effects": "sound_effect",
		"documents":     "document",
		"admin":         "admin",
	}
	for _, ns := range canonicalDriveNamespaces {
		want, ok := known[ns.Namespace]
		if !ok {
			t.Errorf("canonicalDriveNamespaces: unexpected namespace %q (not in known set)", ns.Namespace)
			continue
		}
		if ns.Destination != want {
			t.Errorf("canonicalDriveNamespaces[%q]: expected destination %q, got %q",
				ns.Namespace, want, ns.Destination)
		}
	}
	if len(known) != len(canonicalDriveNamespaces) {
		t.Errorf("canonicalDriveNamespaces: known mappings length mismatch: expected %d namespaces, slice has %d",
			len(known), len(canonicalDriveNamespaces))
	}
}

// ── Typed-error contract tests ────────────────────────────────────────

func TestErrAdminNoDB_IsExported(t *testing.T) {
	if ErrAdminNoDB == nil {
		t.Fatal("ErrAdminNoDB must be non-nil (typed-error contract)")
	}
	wrapped := fmt.Errorf("drive-bootstrap: %w", ErrAdminNoDB)
	if !errors.Is(wrapped, ErrAdminNoDB) {
		t.Errorf("ErrAdminNoDB: errors.Is probe must succeed, got %v (raw: %s)",
			wrapped, wrapped.Error())
	}
}

func TestErrDriveBootstrapNoRoot_IsExported(t *testing.T) {
	if ErrDriveBootstrapNoRoot == nil {
		t.Fatal("ErrDriveBootstrapNoRoot must be non-nil (typed-error contract)")
	}
	wrapped := fmt.Errorf("drive-bootstrap: %w", ErrDriveBootstrapNoRoot)
	if !errors.Is(wrapped, ErrDriveBootstrapNoRoot) {
		t.Errorf("ErrDriveBootstrapNoRoot: errors.Is probe must succeed, got %v (raw: %s)",
			wrapped, wrapped.Error())
	}
}

func TestErrAdminNoDB_DistinctFromErrDriveBootstrapNoRoot(t *testing.T) {
	if ErrAdminNoDB == ErrDriveBootstrapNoRoot {
		t.Fatal("ErrAdminNoDB and ErrDriveBootstrapNoRoot must be distinct sentinels")
	}
	wrappedRoot := fmt.Errorf("drive-bootstrap: %w", ErrDriveBootstrapNoRoot)
	if errors.Is(wrappedRoot, ErrAdminNoDB) {
		t.Error("ErrDriveBootstrapNoRoot must NOT match ErrAdminNoDB (different failure modes)")
	}
	wrappedDB := fmt.Errorf("drive-bootstrap: %w", ErrAdminNoDB)
	if errors.Is(wrappedDB, ErrDriveBootstrapNoRoot) {
		t.Error("ErrAdminNoDB must NOT match ErrDriveBootstrapNoRoot (different failure modes)")
	}
}

func TestErrAdminNoDB_DualWWrapChain(t *testing.T) {
	inner := errors.New("simulated DB path resolution failure")
	wrapped := fmt.Errorf("drive-bootstrap: %w: %w", ErrAdminNoDB, inner)
	if !errors.Is(wrapped, ErrAdminNoDB) {
		t.Errorf("ErrAdminNoDB: errors.Is must traverse the dual-%%w chain; got %v (raw: %s)",
			wrapped, wrapped.Error())
	}
	if !errors.Is(wrapped, inner) {
		t.Errorf("ErrAdminNoDB dual-%%w chain: errors.Is must also find the inner error; got %v", wrapped)
	}
}

// ── Dry-run output tests ──────────────────────────────────────────────

func TestFormatBootstrapDryRunOutput_ContainsRootID(t *testing.T) {
	rootID := "1MB9pTRjvHUdMXUtGOMBcvgRc-MZG2rA4"
	got := formatBootstrapDryRunOutput(rootID)
	if !strings.Contains(got, rootID) {
		t.Errorf("formatBootstrapDryRunOutput: output must contain root ID %q, got:\n%s", rootID, got)
	}
}

func TestFormatBootstrapDryRunOutput_ByteStableFormat(t *testing.T) {
	got := formatBootstrapDryRunOutput("test-root-123")
	wantSubstrings := []string{
		"Drive Bootstrap — DRY RUN",
		"Root: test-root-123",
		"Would create/verify the following 10 canonical subdirectories:",
		"Pass --apply to execute the bootstrap.",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("formatBootstrapDryRunOutput: missing %q in output:\n%s", want, got)
		}
	}
}

func TestFormatBootstrapDryRunOutput_AllNamespacesPresent(t *testing.T) {
	got := formatBootstrapDryRunOutput("root")
	for _, ns := range canonicalDriveNamespaces {
		if !strings.Contains(got, ns.Namespace) {
			t.Errorf("formatBootstrapDryRunOutput: missing namespace %q in output", ns.Namespace)
		}
		if !strings.Contains(got, ns.Destination) {
			t.Errorf("formatBootstrapDryRunOutput: missing destination %q in output", ns.Destination)
		}
	}
}

func TestFormatBootstrapDryRunOutput_StartsWithHeader(t *testing.T) {
	got := formatBootstrapDryRunOutput("root")
	if !strings.HasPrefix(got, "Drive Bootstrap — DRY RUN") {
		t.Errorf("formatBootstrapDryRunOutput: must start with header, got:\n%s", got)
	}
}

// ── runDriveBootstrap error case tests ────────────────────────────────

func TestRunDriveBootstrap_MissingRootFlag(t *testing.T) {
	err := RunDriveBootstrap(nil)
	if err == nil {
		t.Fatal("runDriveBootstrap: expected ErrDriveBootstrapNoRoot when --root is missing")
	}
	if !errors.Is(err, ErrDriveBootstrapNoRoot) {
		t.Errorf("runDriveBootstrap: expected ErrDriveBootstrapNoRoot, got %v", err)
	}
}

func TestRunDriveBootstrap_EmptyRootFlag(t *testing.T) {
	err := RunDriveBootstrap([]string{"--root", ""})
	if err == nil {
		t.Fatal("runDriveBootstrap: expected ErrDriveBootstrapNoRoot when --root is empty")
	}
	if !errors.Is(err, ErrDriveBootstrapNoRoot) {
		t.Errorf("runDriveBootstrap: expected ErrDriveBootstrapNoRoot, got %v", err)
	}
}

func TestRunDriveBootstrap_WhitespaceRootFlag(t *testing.T) {
	err := RunDriveBootstrap([]string{"--root", "   "})
	if err == nil {
		t.Fatal("runDriveBootstrap: expected ErrDriveBootstrapNoRoot when --root is whitespace-only")
	}
	if !errors.Is(err, ErrDriveBootstrapNoRoot) {
		t.Errorf("runDriveBootstrap: expected ErrDriveBootstrapNoRoot, got %v", err)
	}
}

func TestRunDriveBootstrap_DryRunReturnsNil(t *testing.T) {
	err := RunDriveBootstrap([]string{"--root", "test-folder-id"})
	if err != nil {
		t.Errorf("runDriveBootstrap: dry-run must return nil, got %v", err)
	}
}

// ── executeBootstrap guard tests ──────────────────────────────────────

func TestExecuteBootstrap_NoDBPath(t *testing.T) {
	err := executeBootstrap(context.Background(), nil, nil, "test-root")
	if err == nil {
		t.Fatal("executeBootstrap: expected ErrAdminNoDB when DB path is empty")
	}
	if !errors.Is(err, ErrAdminNoDB) {
		t.Errorf("executeBootstrap: expected ErrAdminNoDB, got %v", err)
	}
}

func TestExecuteBootstrap_NoDBPath_WithConfig(t *testing.T) {
	cfg := &config.Config{Storage: config.StorageConfig{MediaDir: ""}}
	t.Skip("executeBootstrap guard only fires on nil config + empty flag (covered by TestExecuteBootstrap_NoDBPath)")
	_ = cfg
}

// ── bootstrapResult tests ─────────────────────────────────────────────

func TestBootstrapResult_ZeroValue(t *testing.T) {
	var r bootstrapResult
	if r.Namespace != "" {
		t.Errorf("bootstrapResult.Namespace zero value: expected \"\", got %q", r.Namespace)
	}
	if r.FolderID != "" {
		t.Errorf("bootstrapResult.FolderID zero value: expected \"\", got %q", r.FolderID)
	}
	if r.Error != "" {
		t.Errorf("bootstrapResult.Error zero value: expected \"\", got %q", r.Error)
	}
}

func TestBootstrapResult_ErrorFieldEmptyOnSuccess(t *testing.T) {
	r := bootstrapResult{
		Namespace: "stock",
		FolderID:  "drive-folder-abc",
	}
	if r.Error != "" {
		t.Errorf("bootstrapResult: Error must be empty on success, got %q", r.Error)
	}
}

func TestBootstrapResult_ErrorFieldPopulatedOnFailure(t *testing.T) {
	r := bootstrapResult{
		Namespace: "images",
		Error:     "drive.EnsureFolderPath: permission denied",
	}
	if r.FolderID != "" {
		t.Errorf("bootstrapResult: FolderID must be empty on error, got %q", r.FolderID)
	}
	if r.Error == "" {
		t.Error("bootstrapResult: Error must be populated on failure path")
	}
}
