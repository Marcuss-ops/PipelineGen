package asset

import "errors"

var (
	ErrNotFound      = errors.New("asset not found")
	ErrAlreadyExists = errors.New("asset already exists")
	ErrInvalidID     = errors.New("invalid asset ID")
	ErrSoftDeleted   = errors.New("asset is soft-deleted")
)
