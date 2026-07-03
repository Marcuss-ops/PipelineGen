// Package semantic — semantic_stub_test.go: audit-pin tests for the
// Phase 1.2 disabled stub surface.
//
// Per godlike/07 no-fake-availability, the pre-fix MetadataWriter
// returned synthetic Payload shells that callers accepted as if a
// real Ollama/Python semantic tagger had run. Phase 1.2 closure
// replaces that with a strict no-op that returns the typed sentinel
// ErrSemanticMetadataWriterDisabled for every semantic operation;
// these tests pin the new contract so future drift back into the
// synthetic-payload shape fails at build/test time rather than
// silently regressing audit confidence.
package semantic

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMetadataWriter_GeneratePayload_ReturnsDisabledSentinel pins the
// canonical (nil, "", typedErr) shape + the errors.Is-able sentinel.
// This is the load-bearing assertion: any future refactor that returns
// a non-nil Payload OR a nil error MUST fail this test, surfacing
// the fake-availability regression before it reaches production.
func TestMetadataWriter_GeneratePayload_ReturnsDisabledSentinel(t *testing.T) {
	t.Parallel()

	w := &MetadataWriter{}
	payload, strOut, err := w.GeneratePayload(context.Background(), WriteRequest{
		AssetID:   "asset-abc-123",
		AssetType: "image",
		MediaType: "photo",
		Source:    "test-source",
		Style:     "test-style",
		Prompt:    "test-prompt",
		LocalPath: "/tmp/test-image.jpg",
	})

	// godlike/07 no-fake-availability: the synthetic Payload shell is
	// retired. The disabled stub MUST return a nil payload rather than
	// fabricate one, so callers that ignore err and dereference the
	// payload fail loud at the operator surface instead of accepting
	// fabricated metadata as real.
	assert.Nil(t, payload, "Phase 1.2 closure: GeneratePayload MUST return nil payload (godlike/07 no-fake-availability); the pre-fix synthetic Payload shell is retired")
	assert.Equal(t, "", strOut, "Phase 1.2 closure: GeneratePayload string return is ALWAYS \"\" (no fabricated token)")

	require.Error(t, err, "Phase 1.2 closure: GeneratePayload MUST return the typed sentinel (nil err is a regression to silent fake-availability)")
	assert.True(t, errors.Is(err, ErrSemanticMetadataWriterDisabled),
		"GeneratePayload MUST wrap ErrSemanticMetadataWriterDisabled so callers can errors.Is() the canonical typed sentinel")
}

// TestMetadataWriter_Write_ReturnsDisabledSentinel pins the canonical
// (nil, typedErr) shape. Same load-bearing contract as the
// GeneratePayload test, but for the file-persisting variant.
func TestMetadataWriter_Write_ReturnsDisabledSentinel(t *testing.T) {
	t.Parallel()

	w := &MetadataWriter{}
	result, err := w.Write(context.Background(), WriteRequest{
		AssetID:   "asset-abc-123",
		AssetType: "image",
		MediaType: "photo",
		Source:    "test-source",
		Style:     "test-style",
		Prompt:    "test-prompt",
		LocalPath: "/tmp/test-image.jpg",
	})

	assert.Nil(t, result, "Phase 1.2 closure: Write MUST return nil *WriteResult (godlike/07 no-fake-availability); the pre-fix LocalPath echo is retired")
	require.Error(t, err, "Phase 1.2 closure: Write MUST return the typed sentinel")
	assert.True(t, errors.Is(err, ErrSemanticMetadataWriterDisabled),
		"Write MUST wrap ErrSemanticMetadataWriterDisabled so callers can errors.Is()")
}

// TestNewMetadataWriter_ReturnsDisabledStub pins the constructor
// shape: it accepts the 5-arg signature and returns a non-nil
// *MetadataWriter pointer. Production callers (22+ sites across
// `internal/application/`, `internal/app/`, `internal/api/`)
// construct this writer at composition time; the audit pin locks
// the constructor signature so future wiring changes stop at
// compile time.
func TestNewMetadataWriter_ReturnsDisabledStub(t *testing.T) {
	t.Parallel()

	w := NewMetadataWriter(
		"/tmp/python-scripts",
		"/tmp/temp",
		"http://127.0.0.1:11434",
		"gemma4:e4b",
		zap.NewNop(),
	)

	require.NotNil(t, w, "NewMetadataWriter MUST return a non-nil *MetadataWriter (composition-root wiring depends on this)")
	// Verify the returned writer's methods honour the disabled-stub
	// contract: GeneratePayload returns the typed sentinel.
	_, _, err := w.GeneratePayload(context.Background(), WriteRequest{})
	require.Error(t, err, "writer from NewMetadataWriter MUST honour the disabled stub contract on every semantic operation")
	assert.True(t, errors.Is(err, ErrSemanticMetadataWriterDisabled))
}

// TestNewMetadataWriter_NilLoggerTolerant pins the nil-logger
// fallback behaviour preserved from the pre-fix contract: a nil
// *zap.Logger is replaced with zap.NewNop() so composition roots
// in unit-test contexts (where logger isn't wired) don't panic.
// Per godlike/07, the disabled-stub log marker is therefore NOT
// emitted in tests (zap.NewNop swallows it) — that's INTENTIONAL
// (test silence); production wires a real logger and the marker
// surfaces for operator auditing.
func TestNewMetadataWriter_NilLoggerTolerant(t *testing.T) {
	t.Parallel()

	w := NewMetadataWriter(
		"/tmp/python-scripts",
		"/tmp/temp",
		"http://127.0.0.1:11434",
		"gemma4:e4b",
		nil, // deliberately nil
	)

	require.NotNil(t, w, "nil logger MUST NOT cause NewMetadataWriter to panic (pre-fix contract preserved)")
	_, _, err := w.GeneratePayload(context.Background(), WriteRequest{})
	require.Error(t, err, "nil-logger construction path MUST still return the typed sentinel on every semantic op")
}

// TestErrSemanticMetadataWriterDisabled_AuditPinString pins the
// canonical error message substring that operators grep for in init
// logs and audit surfaces. The string contract is:
//
//	"DISABLED / NOT_CONFIGURED" — operator-grepable marker
//
// Any future drift that removes or renames the substring fails this
// test, surfacing the operator-audit breakage before it reaches
// production observability dashboards.
func TestErrSemanticMetadataWriterDisabled_AuditPinString(t *testing.T) {
	t.Parallel()

	const auditPin = "DISABLED / NOT_CONFIGURED"
	msg := ErrSemanticMetadataWriterDisabled.Error()
	assert.True(t, strings.Contains(msg, auditPin),
		"ErrSemanticMetadataWriterDisabled MUST contain the canonical audit-pin substring %q for operator grep + observability dashboards; got: %q",
		auditPin, msg)

	// AND the typed sentinel is non-nil (godlike/07 invariant).
	require.NotNil(t, ErrSemanticMetadataWriterDisabled,
		"the typed sentinel MUST be non-nil per godlike/07 (errors.New is required for errors.Is to walk the chain)")
}

// TestMetadataBuilderFunctions_NotDisabled guards against accidental
// "disable the disabled stub by removing the builders" regressions:
// the metadata_builder.go surface (BuildAssetMetadata + extension
// builders + helpers) MUST stay side-effect-free and callable even
// after the stub mode is reintroduced as DISABLED. This test pins
// that BuildAssetMetadata with a fully-populated input produces
// the expected metadata map shape (the original contract), proving
// the builders remain a separable, live, callable surface.
func TestMetadataBuilderFunctions_NotDisabled(t *testing.T) {
	t.Parallel()

	out := BuildAssetMetadata(AssetSemanticInput{
		AssetID:        "asset-abc-123",
		AssetType:      "image",
		Source:         "test-source",
		MediaType:      "photo",
		PromptOriginal: "test prompt",
		Tags:           []string{"tag1", "tag2"},
		Style:          []string{"style1"},
		Confidence:     0.95,
	}, nil)

	assert.Equal(t, "asset-abc-123", out["asset_id"], "BuildAssetMetadata MUST project asset_id (regression guard: builder surface remains canonical)")
	assert.Equal(t, "image", out["asset_type"])
	assert.Equal(t, "test-source", out["source"])
	assert.Equal(t, "photo", out["media_type"])
	assert.Equal(t, 0.95, out["confidence"])
	assert.Equal(t, []string{"tag1", "tag2"}, out["tags"])
	assert.Equal(t, []string{"style1"}, out["style"])

	// Regression guard for CRITICAL review-fix #1 (BuildAssetMetadata
	// redundant empty-string block): the result map MUST NOT contain
	// an empty-string asset_type key when AssetType is intentionally
	// omitted. Pin via a separate input that omits asset_type.
	empty := BuildAssetMetadata(AssetSemanticInput{
		AssetID: "asset-xyz-999",
	}, nil)
	_, hasAssetType := empty["asset_type"]
	assert.False(t, hasAssetType,
		"BuildAssetMetadata MUST omit asset_type key when input is empty (no fake-availability; pre-fix contract preserved after the regression-block removal in metadata_builder.go)")
}
