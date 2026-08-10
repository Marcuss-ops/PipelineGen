package main

import (
	"errors"
	"testing"
)

func TestRecordStockResetErrorPreservesFirstFailure(t *testing.T) {
	first := errors.New("drive unavailable")
	second := errors.New("database unavailable")
	var got error

	recordStockResetError(&got, "delete Drive folder folder-1", first)
	recordStockResetError(&got, "clean asset index", second)

	if got == nil {
		t.Fatal("expected the first stock reset error to be recorded")
	}
	if !errors.Is(got, first) {
		t.Fatalf("recorded error = %v, want it to wrap first error %v", got, first)
	}
	if errors.Is(got, second) {
		t.Fatalf("recorded error = %v, must not replace first error with second error", got)
	}
	if got.Error() != "reset-stock-drive: delete Drive folder folder-1: drive unavailable" {
		t.Fatalf("recorded error = %q, want operation context", got.Error())
	}
}

func TestRecordStockResetErrorIgnoresNilInputs(t *testing.T) {
	var got error
	recordStockResetError(&got, "noop", nil)
	recordStockResetError(nil, "noop", errors.New("ignored"))
	if got != nil {
		t.Fatalf("recorded error = %v, want nil for nil inputs", got)
	}
}
