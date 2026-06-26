package qdrant

import "fmt"

// ── Sentinel errors ──────────────────────────────────────────────────

// ErrSchemaIncompatible is returned when the collection schema doesn't match expectations.
type ErrSchemaIncompatible struct {
	Diff *SchemaDiff
}

func (e *ErrSchemaIncompatible) Error() string {
	return fmt.Sprintf("qdrant schema incompatible: %d missing vectors, %d dimension mismatches",
		len(e.Diff.MissingVectors), len(e.Diff.DimensionMismatches))
}

// ErrCollectionNotFound is returned when a collection doesn't exist.
type ErrCollectionNotFound struct {
	Name string
}

func (e *ErrCollectionNotFound) Error() string {
	return fmt.Sprintf("qdrant collection %q not found", e.Name)
}

// ErrAliasNotFound is returned when an alias doesn't exist.
type ErrAliasNotFound struct {
	Alias string
}

func (e *ErrAliasNotFound) Error() string {
	return fmt.Sprintf("qdrant alias %q not found", e.Alias)
}

// ErrVectorDimensionMismatch is returned when a vector has wrong dimensions.
type ErrVectorDimensionMismatch struct {
	Channel  string
	Expected int
	Actual   int
	AssetID  string
}

func (e *ErrVectorDimensionMismatch) Error() string {
	return fmt.Sprintf("qdrant vector %q dimension mismatch for asset %q: expected %d, got %d",
		e.Channel, e.AssetID, e.Expected, e.Actual)
}

// ErrNaNOrInf is returned when a vector contains NaN or Inf values.
type ErrNaNOrInf struct {
	Channel string
	AssetID string
}

func (e *ErrNaNOrInf) Error() string {
	return fmt.Sprintf("qdrant vector %q contains NaN or Inf for asset %q", e.Channel, e.AssetID)
}

// ErrEmptyVector is returned when a required vector is empty.
type ErrEmptyVector struct {
	Channel string
	AssetID string
}

func (e *ErrEmptyVector) Error() string {
	return fmt.Sprintf("qdrant vector %q is empty for asset %q", e.Channel, e.AssetID)
}

// ErrChannelUnavailable is returned when a vector channel is requested but
// the model is not yet available (e.g. audio without CLAP).
type ErrChannelUnavailable struct {
	Channel string
}

func (e *ErrChannelUnavailable) Error() string {
	return fmt.Sprintf("qdrant vector channel %q is unavailable: no real model configured", e.Channel)
}

// ErrAliasSwitchNotReady is returned when pre-switch verification hasn't passed.
type ErrAliasSwitchNotReady struct {
	Report *SwitchReport
}

func (e *ErrAliasSwitchNotReady) Error() string {
	return "qdrant alias switch not ready: pre-switch verification failed"
}

// ErrSparseRequired is returned when the schema has a sparse BM25 channel
// configured but the caller did not supply a SparseQueryVector for the
// hybrid search request. Per QDRANT-004 closure: hybrid search is a
// HARD promise — when the schema has a BM25 channel, the caller must
// send a sparse query vector. Falling back to dense-only is a regression
// and the caller must surface this as a 4xx to the client (handler maps
// it to 400 Bad Request).
type ErrSparseRequired struct {
	Channel string // sparse vector channel that should have been supplied
}

func (e *ErrSparseRequired) Error() string {
	ch := e.Channel
	if ch == "" {
		ch = "bm25_text"
	}
	return "qdrant hybrid search: sparse query vector required for channel " + ch +
		" — schema has sparse BM25 configured; dense-only is a regression"
}

// ── Helpers ──────────────────────────────────────────────────────────

// IsRetryable returns true for errors that should be retried (HTTP timeout, 5xx, etc.).
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Schema and validation errors are permanent.
	if isPermanent(err) {
		return false
	}
	return true
}

func isPermanent(err error) bool {
	switch err.(type) {
	case *ErrSchemaIncompatible, *ErrVectorDimensionMismatch, *ErrNaNOrInf,
		*ErrEmptyVector, *ErrChannelUnavailable:
		return true
	}
	return false
}
