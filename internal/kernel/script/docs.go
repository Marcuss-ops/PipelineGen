package script

import (
	"errors"
	"strings"
)

// ErrScriptDocsFolderRequired is the typed sentinel surfaced when a
// docs.enabled=true generation has no resolvable script documents
// destination. It fires during resolution — BEFORE any Google Docs
// write — so the caller can fail closed instead of letting the
// publisher create a document in an unintended location
// (godlike/07 NO-FAKE-AVAILABILITY).
var ErrScriptDocsFolderRequired = errors.New("docs enabled but no script docs folder configured")

// ResolveScriptDocsFolderID is the SINGLE resolver for the script
// documents destination. It applies the canonical precedence chain:
//
//	explicit docs.folder_id override
//	    > PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID (configured default)
//	    > fail closed when docs.enabled=true and still empty
//
// Every entry surface (normalizer for the application plan builder,
// artifact routing resolution for the durable capability runner) must
// call this function and never re-derive the folder decision. The
// function is pure and deterministic: the same inputs always produce
// the same resolved folder ID and error.
//
// Returns (resolvedFolderID, error). The resolved folder ID is the
// caller override when present, otherwise the configured default. When
// enabled and neither value is present, the result is empty and the
// error is ErrScriptDocsFolderRequired.
func ResolveScriptDocsFolderID(enabled bool, callerFolderID, configuredDefault string) (string, error) {
	folderID := strings.TrimSpace(callerFolderID)
	if folderID == "" {
		folderID = strings.TrimSpace(configuredDefault)
	}
	if enabled && folderID == "" {
		return "", ErrScriptDocsFolderRequired
	}
	return folderID, nil
}
