package sqlite

import (
	"fmt"
	"testing"

	"github.com/mattn/go-sqlite3"
)

func TestRetryClassifierAcceptsSQLiteValueAndPointer(t *testing.T) {
	t.Parallel()
	value := sqlite3.Error{Code: sqlite3.ErrBusy}
	pointer := &sqlite3.Error{Code: sqlite3.ErrLocked}
	cases := []error{
		value,
		pointer,
		fmt.Errorf("wrapped value: %w", value),
		fmt.Errorf("wrapped pointer: %w", pointer),
	}
	for _, err := range cases {
		decision, ok := RetryClassifier(err)
		if !ok || !decision.Retryable {
			t.Fatalf("RetryClassifier(%T) = (%+v, %v), want retryable classification", err, decision, ok)
		}
	}
}
