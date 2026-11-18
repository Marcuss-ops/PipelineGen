// Package mediaregistry is the SQLite adapter for the canonical media
// registry: source identity, content links, provenance and taxonomy. It
// stores only registry metadata; transcript and summary content remain in
// their canonical tables.
package mediaregistry

import "errors"

// ErrNotWired reports a registry adapter call made without a database.
var ErrNotWired = errors.New("media registry sqlite ledger: not wired")
