package drive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestAdmin_GetOrCreateFolder_NoDuplicate_OnLookupError verifies the P0.4
// admin scope contract: when the seam-folder-lookup returns a transient
// error, GetOrCreateFolder MUST propagate the error WITHOUT falling
// through to Files.Create (which would produce a duplicate folder on Drive
// when a concurrent EnsureFolderPath call's Create branch succeeded).
//
// The test sets Uploader.Service to nil — a real Files.Create on this
// instance would fail with "drive service not configured". So if the err
// message contains "drive service not configured" (or any "create" word),
// the seam FAILED to fall through. The canonical pass signature:
// err contains "lookup" — the seam caught the lookup failure and propagated
// without touching the Create branch.
func TestAdmin_GetOrCreateFolder_NoDuplicate_OnLookupError(t *testing.T) {
	// Construct AdminAdapter with a Uploader whose Service is nil. Any
	// real SDK call would fail-fast with "drive service not configured".
	// The seam-injected lookup errors out before any SDK call.
	a := NewAdminAdapter(&Uploader{Service: nil}, zap.NewNop())
	if a == nil {
		t.Fatal("NewAdminAdapter returned nil for non-nil Uploader")
	}
	lookupErr := errors.New("simulated transient lookup failure (admin scope P0.4 test)")
	a.WithLookup(func(_ context.Context, _, _ string) (string, error) {
		return "", lookupErr
	})

	_, err := a.GetOrCreateFolder(context.Background(), "test-folder", "parent-id")
	if err == nil {
		t.Fatal("expected error from lookup, got nil — P0.4 contract violated")
	}

	// Pass criterion A: error message references the lookup branch (seam
	// intercepted correctly). The wrapper text in findOrCreateFolder
	// contains 'lookup'.
	if !strings.Contains(err.Error(), "lookup") {
		t.Fatalf("expected err msg to reference 'lookup' (proving no fallthrough to Create), got: %v", err)
	}

	// Pass criterion B: error message MUST NOT match the Create-branch
	// wrap prefix `findOrCreateFolder (admin): create ...`. The pre-fix
	// *Uploader.GetOrCreateFolder fell through to Files.Create on any
	// lookup soft-error, surfacing its fail via that exact wrap prefix.
	// (We use the precise substring rather than a coarse "create" check
	// because the lookup wrap's P0.4 contract annotation
	// "NO fallback-to-create" intentionally contains the word "create";
	// only the Create-branch wrap prefix is the no-fallthrough signal.)
	if strings.Contains(err.Error(), "findOrCreateFolder (admin): create") || strings.Contains(err.Error(), "drive service not configured") {
		t.Fatalf("err msg matches Create-branch wrap prefix or 'drive service not configured' — seam FAILED to short-circuit before Files.Create: %v", err)
	}

	// Pass criterion C: errors.Is resolves to the injected lookup err
	// (proves propagation: the P0.4 contract is that the SEAL error
	// is what callers see, NOT a derivative Create error).
	if !errors.Is(err, lookupErr) {
		t.Fatalf("expected errors.Is(err, lookupErr) = true (proving lookup err propagated verbatim), got: %v", err)
	}
}

// TestErrAdminAdapterUploaderNil_TypedErrorContract pins the godlike/07
// typed-error contract for the PR-ADAPTER-NIL-GUARD sentinel + the
// canonical wrap helper WrapDriveAdminError (admin.go:67).
//
// Why this matters: a future refactor that silently drops the %w wrap
// (e.g., switches to %s formatting) breaks the composition-root
// fail-closed pathway without any test catching the regression —
// callers would receive a string-only error and lose the errors.Is
// probe surface. The fix is to make the test invoke the canonical wrap
// helper (WrapDriveAdminError) rather than re-declare the wrap shape
// inline. Drift in WrapDriveAdminError's body surfaces as a test
// failure here.
//
// Four checks:
//
//  1. Sentinel is non-nil (compile-time guard already var-resolves the
//     declaration; this asserts runtime reachability).
//
//  2. Self errors.Is — sanity baseline (same pointer trivially probes).
//
//  3. Load-bearing probe: WrapDriveAdminError(ErrAdminAdapterUploaderNil)
//     MUST preserve the sentinel in its %w chain so callers can probe
//     via errors.Is. This is the SAME helper the production call site
//     (wire_assets.go) invokes — drift in either branch surfaces here.
//
//  4. Negative control: a %s (string-formatter) wrap on the sentinel
//     MUST NOT yield errors.Is=true — if it does, somebody broke
//     unwrapping semantics or the chain has been spuriously collapsed.
func TestDriveAdminSentinels_TypedErrorContract(t *testing.T) {
	sentinels := []struct {
		name     string
		sentinel error
	}{
		{"ErrAdminAdapterUploaderNil", error(ErrAdminAdapterUploaderNil)},
		{"ErrAdminUnknownType", error(ErrAdminUnknownType)},
	}

	for _, tc := range sentinels {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sentinel == nil {
				t.Fatalf("%s must be non-nil (declare sentinel via errors.New)", tc.name)
			}

			// Load-bearing probe: invoke the canonical wrap helper. Drift
			// in WrapDriveAdminError's body (%w -> %s, prefix change, etc.)
			// surfaces as a test failure here AND in the production call
			// site (wire_assets.go).
			productionWrap := WrapDriveAdminError(tc.sentinel)
			if !errors.Is(productionWrap, tc.sentinel) {
				t.Fatalf("WrapDriveAdminError %%w chain did NOT preserve sentinel probe (typed-error contract broken): wrap=%v, sentinel=%v",
					productionWrap, tc.sentinel)
			}

			// Negative control: a string-formatter wrap MUST NOT yield
			// errors.Is=true. Rephrased to avoid vet's %s-as-Printf flag.
			stringWrap := fmt.Errorf("drive.Admin: %s", tc.sentinel.Error())
			if errors.Is(stringWrap, tc.sentinel) {
				t.Fatal("string-formatter wrap falsely probed as sentinel — sentinel chain spurious")
			}
		})
	}
}
