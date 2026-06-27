// Package manifest — typed errors (PR 6 / PR 7 cutover).
//
// Errors are exposed as sentinels so call sites can use errors.Is to
// branch on shape (e.g. "marker-not-found" vs "merge-conflict" vs
// "transport-failed") rather than string-match the wrapped error.
package manifest

import "errors"

var (
	// ErrInvalidPath is returned when the caller passes an empty
	// `path` (UpsertLocal) or empty `folderID` (UpsertRemote).
	// Indicates a programmer error, not a transient condition.
	ErrInvalidPath = errors.New("manifest: invalid path (must be non-empty)")

	// ErrInvalidEntry is returned when Entry.AssetID is empty.
	// The merge invariants depend on a non-empty AssetID; without
	// it, two anonymous entries would collide silently.
	ErrInvalidEntry = errors.New("manifest: invalid entry (AssetID required)")

	// ErrLocalWrite wraps any failure inside the atomic local
	// write (temp-file open, marshal, fsync, rename). Inspect
	// the underlying error for the specific failure mode.
	ErrLocalWrite = errors.New("manifest: local atomic write failed")

	// ErrRemoteWrite wraps any failure inside the Drive upsert
	// path (list-existing on the adapter, replace-or-create on
	// the adapter). Inspect the underlying error for the specific
	// failure mode.
	ErrRemoteWrite = errors.New("manifest: remote drive write failed")
)
