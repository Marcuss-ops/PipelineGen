// Package semantic — nop_test.go: tests for the canonical nop
// implementation of MetadataWriterPort (P0-#2, July 2026).
//
// These tests pin the contract of the EXPLICIT nop
// (semantic.NewNopMetadataWriter) that replaces the Phase 1.2
// "DISABLED stub" MetadataWriter fake concrete. The previous
// semantic_stub_test.go (which tested `*MetadataWriter` and
// `NewMetadataWriter`) is RETIRED — those types no longer exist.
//
// Per godlike/07 NO-FAKE-AVAILABILITY: every test here asserts
// the canonical typed-sentinel return
// (ErrSemanticMetadataWriterDisabled) on BOTH port methods, so a
// future refactor that accidentally fabricates a synthetic Payload
// (the pre-Phase-1.2 anti-pattern) fails the test.
package semantic

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestNopMetadataWriter_GeneratePayload_ReturnsDisabledSentinel pins
// the canonical contract: nopWriter.GeneratePayload returns
// (nil, "", ErrSemanticMetadataWriterDisabled) for ANY WriteRequest.
// Pre-fix the stub returned a synthetic Payload shell (godlike/07
// fake-availability violation); the nop now returns the typed
// sentinel so callers branch via errors.Is.
func TestNopMetadataWriter_GeneratePayload_ReturnsDisabledSentinel(t *testing.T) {
	w := NewNopMetadataWriter(zap.NewNop())
	payload, status, err := w.GeneratePayload(context.Background(), WriteRequest{
		AssetID:   "asset-abc-123",
		AssetType: "image",
		MediaType: "photo",
	})
	require.Error(t, err, "nop GeneratePayload MUST return an error (no synthetic Payload)")
	assert.True(t, errors.Is(err, ErrSemanticMetadataWriterDisabled),
		"GeneratePayload MUST wrap ErrSemanticMetadataWriterDisabled so callers can errors.Is() the canonical typed sentinel")
	assert.Nil(t, payload, "nop GeneratePayload MUST return nil Payload (no synthetic shell)")
	assert.Equal(t, "", status, "nop GeneratePayload MUST return empty status string")
}

// TestNopMetadataWriter_Write_ReturnsDisabledSentinel pins the
// canonical contract: nopWriter.Write returns
// (nil, ErrSemanticMetadataWriterDisabled) for ANY WriteRequest.
// Pre-fix the method returned a synthetic WriteResult with a
// LocalPath echo (godlike/07 fake-availability violation); the
// nop now returns the typed sentinel.
func TestNopMetadataWriter_Write_ReturnsDisabledSentinel(t *testing.T) {
	w := NewNopMetadataWriter(zap.NewNop())
	res, err := w.Write(context.Background(), WriteRequest{
		AssetID:   "asset-abc-123",
		AssetType: "image",
		MediaType: "photo",
	})
	require.Error(t, err, "nop Write MUST return an error (no synthetic WriteResult)")
	assert.True(t, errors.Is(err, ErrSemanticMetadataWriterDisabled),
		"Write MUST wrap ErrSemanticMetadataWriterDisabled so callers can errors.Is()")
	assert.Nil(t, res, "nop Write MUST return nil WriteResult (no synthetic LocalPath echo)")
}

// TestNewNopMetadataWriter_ReturnsPortImplementation pins the
// constructor contract: NewNopMetadataWriter returns a value
// implementing the MetadataWriterPort interface (the canonical
// narrow typed surface). A future refactor that returns a concrete
// pointer (not the port) would break the AGENTS.md Pattern 0
// compile-time assertion in types.go and fail the build.
func TestNewNopMetadataWriter_ReturnsPortImplementation(t *testing.T) {
	w := NewNopMetadataWriter(zap.NewNop())
	require.NotNil(t, w, "NewNopMetadataWriter MUST return a non-nil port (composition-root wiring depends on this)")
	// The compile-time assertion `var _ MetadataWriterPort = (*nopWriter)(nil)`
	// in types.go already pins the structural conformance; this test
	// pins the runtime behaviour: the returned value satisfies the
	// port interface and the methods return the typed sentinel.
	_, _, err := w.GeneratePayload(context.Background(), WriteRequest{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSemanticMetadataWriterDisabled))
	_, err = w.Write(context.Background(), WriteRequest{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSemanticMetadataWriterDisabled))
}

// TestNewNopMetadataWriter_NilLoggerTolerant pins the nil-logger
// contract: NewNopMetadataWriter accepts a nil log (falls back to
// zap.NewNop()) so test fixtures and composition-root paths that
// pass a nil log don't panic at construction. The Warn marker is
// still emitted (to zap.NewNop()).
func TestNewNopMetadataWriter_NilLoggerTolerant(t *testing.T) {
	w := NewNopMetadataWriter(nil)
	require.NotNil(t, w, "nil logger MUST NOT cause NewNopMetadataWriter to panic (pre-fix contract preserved)")
	_, err := w.Write(context.Background(), WriteRequest{})
	require.Error(t, err, "nil-logger construction path MUST still return the typed sentinel on every semantic op")
	assert.True(t, errors.Is(err, ErrSemanticMetadataWriterDisabled))
}

// TestErrSemanticMetadataWriterDisabled_AuditPinString pins the
// canonical error message substring so operators can grep for the
// "real semantic tagger has not been reintroduced yet" condition in
// observability dashboards and audit logs. A future refactor that
// shortens or rewords the message would break this test — the
// canonical string is part of the godlike/07 contract.
func TestErrSemanticMetadataWriterDisabled_AuditPinString(t *testing.T) {
	require.NotNil(t, ErrSemanticMetadataWriterDisabled)
	msg := ErrSemanticMetadataWriterDisabled.Error()
	assert.Contains(t, msg, "DISABLED",
		"ErrSemanticMetadataWriterDisabled MUST contain the canonical audit-pin substring %q for operator grep + observability dashboards; got: %q",
		"DISABLED", msg)
	assert.Contains(t, msg, "P0-#2",
		"ErrSemanticMetadataWriterDisabled MUST contain the canonical %q substring so dashboards can correlate the failure to the P0-#2 ticket; got: %q",
		"P0-#2", msg)
}
