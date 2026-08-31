package indexing

import (
	"errors"
	"fmt"
)

// ErrIndexClipDisabledButEventRequested signals that an indexing request was
// received while the indexer is disabled. Consumers should keep the event
// retryable rather than acknowledging it as successfully indexed.
var ErrIndexClipDisabledButEventRequested = errors.New("clipindexer disabled but asset.index.requested event arrived")

// ErrIndexSuperseded carries the identity of an indexing request that lost
// its optimistic-concurrency fence to a newer asset revision.
type ErrIndexSuperseded struct {
	ClipID        string
	SourceVersion string
}

func (e *ErrIndexSuperseded) Error() string {
	return fmt.Sprintf("clipindexer: CAS miss for %s (source_version=%q) — index event superseded by newer version", e.ClipID, e.SourceVersion)
}
