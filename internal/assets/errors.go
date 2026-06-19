package assets

import "errors"

// Canonical domain errors for asset operations.
var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("asset already exists")
	ErrInvalidID     = errors.New("invalid asset ID")
	ErrSoftDeleted   = errors.New("asset is soft-deleted")
)
