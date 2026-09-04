// Package sqlite owns the canonical SQLite retry classifier.
package sqlite

import (
	"errors"
	"fmt"

	"github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// RetryClassifier is the single SQLite typed-error classifier injected at
// infrastructure boundaries. Capability packages must never inspect sqlite3
// errors directly.
var RetryClassifier = classifySQLiteError

func classifySQLiteError(err error) (retry.RetryDecision, bool) {
	code, message, ok := sqliteErrorCode(err)
	if !ok {
		return retry.RetryDecision{}, false
	}

	switch code {
	case sqlite3.ErrBusy, sqlite3.ErrLocked:
		return retry.RetryDecision{
			Class:       retry.ErrLockBusy,
			Retryable:   true,
			SafeMessage: fmt.Sprintf("sqlite: SQLITE_%s (%s)", sqliteCodeName(code), message),
		}, true
	case sqlite3.ErrFull, sqlite3.ErrIoErr:
		return retry.RetryDecision{
			Class:       retry.ErrUnknown,
			Retryable:   false,
			SafeMessage: fmt.Sprintf("sqlite: SQLITE_%s (operator-intervention required): %s", sqliteCodeName(code), message),
		}, true
	case sqlite3.ErrCorrupt, sqlite3.ErrSchema, sqlite3.ErrConstraint,
		sqlite3.ErrReadonly, sqlite3.ErrAuth:
		return retry.RetryDecision{
			Class:       retry.ErrValidation,
			Retryable:   false,
			SafeMessage: fmt.Sprintf("sqlite: SQLITE_%s (program/data condition, retry will not change outcome): %s", sqliteCodeName(code), message),
		}, true
	}
	return retry.RetryDecision{}, false
}

// sqliteErrorCode accepts both error carriers emitted by mattn/go-sqlite3:
// sqlite3.Error values and *sqlite3.Error pointers. Wrapping with %w is
// supported via errors.As. Keeping both shapes here preserves the behavior that
// previously leaked into jobs/worker_finalize_paths.go while restoring the
// platform boundary.
func sqliteErrorCode(err error) (sqlite3.ErrNo, string, bool) {
	if err == nil {
		return 0, "", false
	}
	var value sqlite3.Error
	if errors.As(err, &value) {
		return value.Code, value.Error(), true
	}
	var ptr *sqlite3.Error
	if errors.As(err, &ptr) && ptr != nil {
		return ptr.Code, ptr.Error(), true
	}
	return 0, "", false
}

func sqliteCodeName(code sqlite3.ErrNo) string {
	switch code {
	case sqlite3.ErrBusy:
		return "BUSY"
	case sqlite3.ErrLocked:
		return "LOCKED"
	case sqlite3.ErrIoErr:
		return "IOERR"
	case sqlite3.ErrCorrupt:
		return "CORRUPT"
	case sqlite3.ErrFull:
		return "FULL"
	case sqlite3.ErrSchema:
		return "SCHEMA"
	case sqlite3.ErrConstraint:
		return "CONSTRAINT"
	case sqlite3.ErrReadonly:
		return "READONLY"
	case sqlite3.ErrAuth:
		return "AUTH"
	}
	return fmt.Sprintf("code=%d", code)
}
