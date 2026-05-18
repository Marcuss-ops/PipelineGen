package cas

import "time"

type PutResult struct {
	ContentHash   string
	Path          string
	AlreadyExists bool
	Timestamp     time.Time
}
