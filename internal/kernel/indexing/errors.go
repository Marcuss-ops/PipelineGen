package indexing

import (
	"errors"
)

// ErrIndexClipDisabledButEventRequested signals that an indexing request was
// received while the indexer is disabled. Consumers should keep the event
// retryable rather than acknowledging it as successfully indexed.
var ErrIndexClipDisabledButEventRequested = errors.New("clipindexer disabled but asset.index.requested event arrived")

// ErrIndexSuperseded was DELETED here on 2026-09-20. It carried the identity of
// an indexing request that lost its optimistic-concurrency fence to a newer
// asset revision, but nothing in the tree ever constructed or matched one: its
// only references were its own declaration and its Error method. The CAS-miss
// signal it described belonged to the retired clipindexer write plane.
