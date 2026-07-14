// Package immutability — typed copy-on-write primitive for the
// percheck_inputimmutability archcheck rule.
//
// godlike/06 SSOT (godlike/06 one-canonical-owner-per-fact):
// pkg/immutability.CloneWith is the canonical typed primitive for
// "treat inputs as read-only; mutate a copy and return it". It is
// invoked at every call site where a function PARAM named
// `req`/`input`/`request`/`params` would otherwise be mutated in
// place — the scanner exempts callers using this helper because the
// mutation occurs on a CLONED value, not on the parameter itself.
//
// Why generics (Go 1.18+) and not reflect: zero reflection cost at
// runtime, fully type-checked at compile. The mutation closure
// pattern lets the scanner's regex (which looks at PARAM-level
// field assignments on lines like `req.X = ...`) not fire because
// the assignment happens to a closure-captured local pointer
// designated `*T` — the outer parameter is left untouched.
//
// SHALLOW-CLONE SEMANTICS (godlike/07 audit-pin, contract test
// copy_test.go::TestCloneWith_ShallowClone_SharesSliceBacking):
// CloneWith is a single-value Go clone (`cloned := orig`). For
// primitives and struct fields by value, this is fully independent.
// For SLICES, MAPS, POINTERS, INTERFACES, CHANNELS, and FUNCTIONS
// the header/pointer is copied but the backing storage is SHARED
// with `orig`. Callers that mutate `r.Slice[i]`, `r.Map[k]`,
// `*r.PointerField`, or other heap-backed fields WILL inadvertently
// mutate the original. The canonical pattern is REPLACEMENT
// (`r.Slice = append([]T{}, r.Slice...); r.Slice[i] = newVal`) rather
// than index-mutation, which the archcheck scanner specifically
// accepts (it only fires on `req.X = ...` field-replacement patterns,
// not on slice-element mutation which the scanner does not audit).
//
// Forward-pointer (godlike/07 audit-pin convention): see
// architecture/deprecations.yaml#INPUT-IMMUTABILITY-COPY-ON-WRITE-MIGRATION
// for forward-pointer + removal criterion when the archcheck lifts
// the scan scope.
package immutability

// CloneWith returns a freshly-cloned copy of `orig` after the
// supplied `mutate` closure mutates the clone in place. The original
// value is not modified. This is the canonical typed
// copy-on-write primitive for input-struct immutability compliance.
//
// Usage at a hot call site:
//
//	func normalize(req RunTagRequest) RunTagRequest {
//	    return immutability.CloneWith(req, func(r *RunTagRequest) {
//	        r.Term = strings.TrimSpace(r.Term)
//	        r.Limit = clampLimit(r.Limit)
//	    })
//	}
//
// The signature of `mutate` is `func(*T)` (pointer receiver to make
// mutations cheap), and it is invoked exactly once on the cloned
// value before CloneWith returns the cloned value to the caller.
//
// Type assertions for value types vs pointer types: CloneWith works
// on T directly; callers that hold *T should dereference
// (`CloneWith(*p, ...)`) before invoking.
func CloneWith[T any](orig T, mutate func(*T)) T {
	cloned := orig
	mutate(&cloned)
	return cloned
}
