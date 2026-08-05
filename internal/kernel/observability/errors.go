package observability

import (
	"errors"
	"fmt"
)

// ErrorCoder lets typed errors provide a stable machine-readable code that is
// persisted in reports (mirrors the processmetrics.ErrorCoder contract).
type ErrorCoder interface {
	ErrorCode() string
}

// errorCode derives the canonical persisted error code for an error. Typed
// errors carrying ErrorCoder win; everything else falls back to "error".
func errorCode(err error) string {
	if err == nil {
		return ""
	}
	var coded ErrorCoder
	if errors.As(err, &coded) && coded.ErrorCode() != "" {
		return coded.ErrorCode()
	}
	return "error"
}

// panicError normalises a recovered panic value into an error so a panicked
// stage/operation/run can still be closed with a machine-readable code.
func panicError(v any) error {
	if err, ok := v.(error); ok {
		return err
	}
	return fmt.Errorf("panic: %v", v)
}
