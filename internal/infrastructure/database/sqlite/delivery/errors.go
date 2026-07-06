package delivery

import "errors"

// ErrNotFound is returned by FindByDestinationAndPath and Delete when
// no row matches the given (destination, path) pair.
var ErrNotFound = errors.New("drive_folder_catalog: entry not found")

// ErrInvalidEntry is returned by Upsert and query methods when required
// fields (destination, path) are empty.
var ErrInvalidEntry = errors.New("drive_folder_catalog: invalid entry (required field empty)")
