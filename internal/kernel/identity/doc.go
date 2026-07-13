// Package identity is one of the kernel subzones declared in
// docs/architecture/godlike/02_TARGET_STRUCTURE.md §"internal/kernel".
// It is reserved for stable identity primitives (correlation IDs, asset
// IDs, request IDs, job IDs) used by at least two capabilities.
//
// EXPAND-phase placeholder (Wave 19, June 2026). Existing in-tree
// identity helpers currently live in pkg/corid/ and remain authoritative
// there. Kernel extraction requires the §"internal/kernel" criterion
// ("≥2 real capabilities need the same semantic contract") to be met
// for each primitive we move in.
package identity
