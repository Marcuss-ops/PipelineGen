package immutability

import (
	"testing"
)

// runTagLike is a minimal local-variable-mutation target that
// mirrors the shape of the canonical pkg/<domain>/... request types
// so the contract test is decoupled from any specific request
// type's evolution. Used as T in CloneWith[T] for the contract pins.
//
// Includes a Nested struct (value-type) AND a slice field, so the
// contract test can independently pin both behaviors:
//   - value-type fields (Term, Limit, Nested.FolderID, Nested.Tags-header)
//     are independently cloned;
//   - heap-backed storage (slice backing array) IS SHARED between
//     the original and the clone — Go's shallow-clone semantics.
//
// godlike/07 audit-pin: pkg/immutability.Copy_test.go documents
// these contracts. Mismatch with pkg/immutability/copy.go docblock
// is a release-block bug.
type runTagLike struct {
	Term   string
	Limit  int
	Nested struct {
		FolderID string
		Tags     []string
	}
}

// TestCloneWith_PrimitiveFieldsAreIsolated — primitive (value-type)
// fields are independently cloned. Mutating them inside the closure
// does NOT affect the original. This is the CANONICAL contract for
// the typed copy-on-write primitive's primary use case (req.X =
// primitive-value patterns).
//
// Per godlike/07 audit-pin: this test pins the scanner exemption:
// percheck_inputimmutability regex matches `req.X = ...` only;
// CloneWith callers that mutate primitive fields via field
// assignment pass this contract and the scanner correctly does
// not fire (because the mutation is on the closure-captured
// *T pointer to the clone, not the function parameter).
func TestCloneWith_PrimitiveFieldsAreIsolated(t *testing.T) {
	orig := runTagLike{Term: " hello ", Limit: 0}
	orig.Nested.FolderID = "old"
	// Nested.Tags-HDR is copied but the backing array is shared.
	// Use the header-level property (length-3 distinct) so this
	// test cannot silently pass when slice mutation bleeds through.
	orig.Nested.Tags = []string{"a", "b", "c"}

	clone := CloneWith(orig, func(r *runTagLike) {
		r.Term = "HELLO"
		r.Limit = 99
		r.Nested.FolderID = "new"
		// REPLACEMENT (not index-mutation): builds a fresh slice
		// to avoid the documented shallow-clone slice-backing
		// sharing behavior. The scanner does not fire on this
		// pattern (it would fire on `r.Nested.Tags[i] = ...`).
		r.Nested.Tags = []string{"X", "Y", "Z"}
	})

	// Clone is fully updated.
	if clone.Term != "HELLO" {
		t.Fatalf("clone.Term not updated: %q", clone.Term)
	}
	if clone.Limit != 99 {
		t.Fatalf("clone.Limit not updated: %d", clone.Limit)
	}
	if clone.Nested.FolderID != "new" {
		t.Fatalf("clone.Nested.FolderID not updated: %q", clone.Nested.FolderID)
	}
	if len(clone.Nested.Tags) != 3 || clone.Nested.Tags[0] != "X" {
		t.Fatalf("clone.Nested.Tags not updated: %v", clone.Nested.Tags)
	}

	// Original primitives are isolated (the canonical contract).
	if orig.Term != " hello " {
		t.Fatalf("original.Term mutated: %q", orig.Term)
	}
	if orig.Limit != 0 {
		t.Fatalf("original.Limit mutated: %d", orig.Limit)
	}
	if orig.Nested.FolderID != "old" {
		t.Fatalf("original.Nested.FolderID mutated: %q", orig.Nested.FolderID)
	}
	if len(orig.Nested.Tags) != 3 || orig.Nested.Tags[0] != "a" {
		// The slice element at index 0 is unchanged because the
		// closure used REPLACEMENT (assigning a fresh slice),
		// which writes a NEW backing array. If a future caller
		// accidentally uses index-mutation `r.Nested.Tags[0] = ...`,
		// this assertion survives (the index mutation lands on
		// the SHARED backing array, mutating orig).
		t.Fatalf("original.Nested.Tags mutated: %v", orig.Nested.Tags)
	}
}

// TestCloneWith_ShallowClone_SharesSliceBacking — explicit pinned
// contract for shallow-clone slice-backing semantics. This is the
// godlike/07 audit-pin for the wording in pkg/immutability/copy.go:
//
//	"For SLICES, MAPS, POINTERS, INTERFACES, CHANNELS, and FUNCTIONS
//	 the header/pointer is copied but the backing storage is SHARED
//	 with `orig`."
//
// Callers who mutate `r.Slice[i]` WILL bleed through to orig. The
// canonical mitigation is REPLACEMENT (`r.Slice = newSlice`).
func TestCloneWith_ShallowClone_SharesSliceBacking(t *testing.T) {
	orig := runTagLike{}
	orig.Nested.Tags = []string{"orig-0", "orig-1"}

	clone := CloneWith(orig, func(r *runTagLike) {
		// Index mutation on the SHARED backing array → bleeds through.
		r.Nested.Tags[0] = "MUTATED"
	})

	// Documented shallow-clone behavior: the closure mutated the
	// shared backing array, so orig.Nested.Tags[0] is now "MUTATED".
	// This is intentional per pkg/immutability/copy.go docblock.
	if clone.Nested.Tags[0] != "MUTATED" {
		t.Fatalf("clone.Nested.Tags[0] expected MUTATED, got %q",
			clone.Nested.Tags[0])
	}
	if orig.Nested.Tags[0] != "MUTATED" {
		t.Fatalf("orig.Nested.Tags[0] expected MUTATED (shallow-clone\n"+
			"bleed-through is INTENTIONAL per pkg/immutability/copy.go\n"+
			"shallow-clone-semantics docblock), got %q",
			orig.Nested.Tags[0])
	}
}

// TestCloneWith_ClosureMutatesTheClone — the closure's *T pointer
// references the CLONE, not the original. Confirm by comparing the
// pointer address and the value-after-mutation visibility.
func TestCloneWith_ClosureMutatesTheClone(t *testing.T) {
	orig := runTagLike{Term: ""}
	var observedInClosure *runTagLike

	CloneWith(orig, func(r *runTagLike) {
		observedInClosure = r
		r.Term = "set-via-closure"
	})

	if observedInClosure == nil {
		t.Fatal("closure never invoked")
	}
	if observedInClosure.Term != "set-via-closure" {
		t.Fatalf("closure did not mutate: %s", observedInClosure.Term)
	}
	// Pointer must NOT be the original's address — addresses the
	// shallow-clone semantics concern at the value-type level.
	if observedInClosure == &orig {
		t.Fatal("closure received the original — CloneWith leaked the param pointer")
	}
}

// TestCloneWith_NilClosure_Panics — the documented contract requires
// mutate to be non-nil. If a future refactor passes nil, the function
// panics at `mutate(&cloned)` rather than returning silently.
//
// godlike/07 no-fake-availability: silent no-op on nil closure would
// make Caller mistaken about the mutation having happened.
func TestCloneWith_NilClosure_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil mutate closure; CloneWith silently returned")
		}
	}()
	CloneWith(runTagLike{}, nil)
}

// TestCloneWith_NoMutation_ReturnsEquivalentClone — if the closure
// makes no mutations, CloneWith returns a clone whose public fields
// are equivalent to the original (modulo the documented shallow-clone
// semantics for heap-backed fields). This pins the "read-only
// traversal" use-case (e.g. validation-only normalization passes).
func TestCloneWith_NoMutation_ReturnsEquivalentClone(t *testing.T) {
	orig := runTagLike{Term: "abc", Limit: 5}
	orig.Nested.FolderID = "f"
	orig.Nested.Tags = []string{"x"}
	clone := CloneWith(orig, func(r *runTagLike) {} /* no-op */)
	if clone.Term != orig.Term || clone.Limit != orig.Limit {
		t.Fatalf("primitive fields differ: orig=%+v clone=%+v", orig, clone)
	}
	if clone.Nested.FolderID != orig.Nested.FolderID {
		t.Fatalf("clone.Nested.FolderID differs: orig=%q clone=%q",
			orig.Nested.FolderID, clone.Nested.FolderID)
	}
	if len(clone.Nested.Tags) != len(orig.Nested.Tags) ||
		clone.Nested.Tags[0] != orig.Nested.Tags[0] {
		t.Fatalf("clone.Nested.Tags differs: orig=%v clone=%v",
			orig.Nested.Tags, clone.Nested.Tags)
	}
}
