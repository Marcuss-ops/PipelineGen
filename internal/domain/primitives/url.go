package primitives

// URL is the canonical nominal type for a URL string at the domain
// boundary.
//
// godlike/06 SSOT (narrow port doctrine): URL is the *only* typed
// URL passed/returned between layers. Boundary code (HTTP handlers,
// CLI) is responsible for syntactic validation (scheme, host,
// allow-listed schemes) — this nominal type carries NO validation
// logic because lexical/HTTP semantics belong at the boundary, not
// in the domain primitive.
//
// Why a nominal type for URLs?
//   - Prevents accidental swaps with raw strings that happen to be
//     URLs (clip source URLs vs callback URLs vs webhook URLs vs
//     asset URLs each have different semantics).
//   - Future schema/validation upgrades (e.g. require https-only,
//     length limits, denylist) become a single-file change with a
//     compile-time interface boundary that catches all consumers.
//
// Wire identity: JSON marshaling preserves the underlying string
// value with zero overhead because `type URL string` is a Go-defined
// named string type (no MarshalJSON/UnmarshalJSON needed).
type URL string

// NewURL wraps a raw string into a canonical URL. The constructor is
// pure: empty input and arbitrary input are allowed; lexical
// validation (scheme, host) happens at the HTTP handler boundary
// where it can be mapped to a 400 response with a typed error.
//
// Concurrency: the constructor is pure (no shared state) and safe
// for concurrent use.
func NewURL(s string) URL { return URL(s) }

// IsEmpty reports whether the URL is the zero value. Used to short-
// circuit URLs that are not set (e.g. optional callback URLs on a
// job).
func (u URL) IsEmpty() bool { return u == "" }

// String returns the underlying string form. Required by the fmt
// contract; deliberately NOT a `MarshalJSON`/`UnmarshalJSON` so the
// JSON wire format stays byte-identical with the raw-string version.
func (u URL) String() string { return string(u) }
