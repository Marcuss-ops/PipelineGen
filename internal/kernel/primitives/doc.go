// Package primitives defines canonical nominal types for the domain layer.
//
// godlike/06 SSOT (single-purpose doctrine): one file per nominal type,
// so the file *name* in this package == the type name it owns. This
// prevents "god files" as the package grows and matches the codebase
// convention (see sibling domain/*/job_types.go + application/ports/*.go).
//
// Why nominal types?
// ------------------
// Raw `string` for high-stakes identifiers (job ids, workspace ids,
// urls) is the textbook "Primitive Obsession" smell: callers freely
// swap them, validations are duplicated at every consumer, and the
// compiler can't catch an accidentally-passed argument.
//
// Substituting a nominal type (type JobID string) is zero-cost at
// runtime (still a string in memory, on the wire, in JSON), but turns
// every signature into a type-checked contract: the function that
// expects a JobID can no longer be called with a WorkspaceID by
// mistake.
//
// Constructor discipline
// ----------------------
// All constructors (`NewXxx`) are pure: they accept any input (no
// panic, no validation error). Validation belongs at the boundary
// layer (HTTP handler) where context (HTTP semantics, error mapping)
// is available. The `IsEmpty()` method is the boundary-friendly hook
// to fail closed on invalid input.
//
// JSON wire-identity
// ------------------
// No `MarshalJSON`/`UnmarshalJSON` is defined. Go's runtime guarantees
// byte-identical JSON output for `type X string` (the wrapper compiles
// to the inline string), so a primitive `JobID` serializes as a JSON
// string with no overhead and no edge cases.
//
// See architecture/canon.md and the godlike/SSOT docs for the broader
// nominal-types program across the domain layer.
package primitives
