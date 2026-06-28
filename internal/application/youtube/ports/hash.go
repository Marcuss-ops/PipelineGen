// HashPort (June 2026, Step 4 split) — canonical narrow surface of MD5
// hashing operations used by the YouTube orchestrator. Service.MD5String /
// Service.MD5File implementations satisfy this port; the composition root
// wires a concrete adapter (e.g. *infrastructure/hashutil.HashAdapter) via
// ServiceDeps.HashSvc.
//
// HashPort is NEW; the pre-existing HashServicePort (declared in ports.go)
// has the same method set so any concrete *Service implementation satisfies
// BOTH interfaces implicitly (Go interface duck-typing). Existing code that
// types fields as youtubeports.HashServicePort keeps compiling unchanged.
package ports

// HashPort computes MD5 hex digests for strings and files.
type HashPort interface {
	// MD5String returns the MD5 hex digest of an in-memory string.
	// Always succeeds (no error return) so it fits a hot-path signature.
	MD5String(s string) string

	// MD5File returns the MD5 hex digest of the file at path, or an error
	// if the file cannot be opened / read.
	MD5File(path string) (string, error)
}
