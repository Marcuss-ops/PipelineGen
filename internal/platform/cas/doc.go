// Package cas provides the platform Content-Addressable Storage (CAS)
// adapter: an immutable, content-addressed blob store on the local
// filesystem.
//
// Layout (per the CAS design, August 2026): <root>/<ab>/<cd>/<sha256> where
// ab is the first two hex characters of the SHA-256 digest and cd the next
// two — two-level sharding mirroring the canonical
// `cas://a1/f8/a1f8c72e...` shape. The filename IS the address: byte
// identity is the storage key, never the filename/URL/provider.
//
// Guarantees (CAS DoD):
//
//   - Atomic writes: content is streamed through the staging.Stager port —
//     the existing LocalStore's canonical atomic write path (.partial tmp +
//     O_EXCL + stream-through-hash + fsync + atomic rename) — and then
//     linked into the sharded layout with os.Link, which fails with EEXIST
//     when the address is already present. That is a genuine no-overwrite
//     primitive: two concurrent Puts of the same bytes never race into
//     corruption (unlike os.Rename, which replaces).
//   - SHA-256 verification after write: once the object lands at its
//     canonical address the on-disk bytes are re-hashed and MUST equal the
//     address; a mismatch is treated as corruption (fail-closed: the object
//     is removed and ErrCorruption is returned).
//   - Immutability: an existing address is never overwritten. Put against
//     an existing address is a verified dedup hit (identical bytes) or a
//     corruption error (different bytes at the same address). Delete is the
//     only destructive operation and is explicit.
//
// The package owns NO business semantics (godlike/02 §internal/platform):
// the owning capability decides when and why content is stored; this
// package decides how bytes are safely addressed, written, and retrieved.
// Every failure path returns a typed sentinel (godlike/07 fail-closed);
// an unavailable backend is never represented as a successful no-op.
package cas
