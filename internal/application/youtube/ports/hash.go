// HashPort (June 2026, Step 4 split) — canonical hash surface consumed by
// the YouTube orchestrator. Concrete adapters satisfy both the legacy MD5
// methods (for backward compatibility) and the canonical SHA-256 methods.
//
// godlike/06 hash-identity convention:
//
//	content_sha256  = canonical byte identity (SHA256File, always 64-hex SHA-256)
//	drive_md5       = Google Drive provider receipt only (MD5File, 32-hex MD5)
//	source_version  = source revision/fingerprint
//
// New callers MUST use SHA256File for content identity. MD5File is retained
// only for legacy Drive receipt compatibility — never for identity.
package ports

type HashPort interface {
	// SHA256File returns the canonical SHA-256 hex digest (64 chars) of the
	// file at path. This is the content identity — two files with identical
	// bytes yield the same digest.
	SHA256File(path string) (string, error)

	// SHA256String returns the canonical SHA-256 hex digest of a string.
	SHA256String(s string) string

	// MD5String returns the MD5 hex digest of an in-memory string.
	//
	// Deprecated: use SHA256String for content identity. MD5String remains
	// only for Drive upload receipt compatibility.
	MD5String(data string) string

	// MD5File returns the MD5 hex digest of the file at path, or an error
	// if the file cannot be opened / read.
	//
	// Deprecated: use SHA256File for content identity. MD5File remains only
	// for Drive upload receipt compatibility.
	MD5File(path string) (string, error)
}