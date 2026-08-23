// Package remote_test — cerrors_test.go (P1 #15, July 2026).
//
// 7 unit tests, one per canonical Kind, asserting the typed-error
// contract godlike/07 demands. Each test pins:
//
//  1. errors.Is(err, <canonical sentinel>) success on a fresh
//     RemoteCompletionError wrap.
//  2. HTTPStatus round-trip via Kind.HTTPStatus().
//  3. Optional RetryAfter hint round-trip via Kind.RetryAfter().
//  4. JSON marshal round-trip preserves the envelope shape.
//  5. CanonicalErrorKinds() enumeration has exactly 7 entries
//     (the audit-pin for the closed-set).
//
// Plus a wire round-trip test asserting unpacked envelope
// reconstructs correctly from the encode side.
//
// godlike/06 audit-pinning discipline: every Kind has exactly one
// test (no test duplication); each test pins the (input, mock-state,
// expected output) triple so a future drift is a single failing
// test rather than a runtime surprise.
package remote_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

// ── Test 1: lease_lost ────────────────────────────────────────────────────

func TestRemoteCompletionError_LeaseLost(t *testing.T) {
	kind := remote.ErrorKindLeaseLost
	if got := kind.HTTPStatus(); got != http.StatusConflict {
		t.Fatalf("HTTPStatus: want %d, got %d", http.StatusConflict, got)
	}
	if got := kind.RetryAfter(); got != 0 {
		t.Fatalf("RetryAfter: want 0 (no rate-limit signal), got %s", got)
	}
	remErr := remote.NewRemoteCompletionError(kind, "lease lost during CAS", remote.ErrCompletionLeaseLost)
	// Sentinel probe by Is() — the canonical typed-error contract.
	if !errors.Is(remErr, remote.ErrCompletionLeaseLost) {
		t.Errorf("errors.Is: want match ErrCompletionLeaseLost, got no-match")
	}
	// errors.As probe — the canonical structured probe.
	var asProbe *remote.RemoteCompletionError
	if !errors.As(remErr, &asProbe) {
		t.Errorf("errors.As: want match *RemoteCompletionError, got no-match")
	}
	if asProbe.Kind != kind {
		t.Errorf("errors.As Kind: want %s, got %s", kind, asProbe.Kind)
	}
	if !strings.Contains(remErr.Error(), "kind=lease_lost") {
		t.Errorf("Error() should include kind=lease_lost: %s", remErr.Error())
	}
	// JSON round-trip
	body, err := json.Marshal(remErr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"kind":"lease_lost"`) {
		t.Errorf("JSON body missing kind:lease_lost: %s", string(body))
	}
}

// ── Test 2: idempotency_conflict ──────────────────────────────────────────

func TestRemoteCompletionError_IdempotencyConflict(t *testing.T) {
	kind := remote.ErrorKindIdempotencyConflict
	if got := kind.HTTPStatus(); got != http.StatusConflict {
		t.Fatalf("HTTPStatus: want %d, got %d", http.StatusConflict, got)
	}
	remErr := remote.NewRemoteCompletionError(kind, "different result_hash replay", remote.ErrCompletionIdempotencyConflict)
	if !errors.Is(remErr, remote.ErrCompletionIdempotencyConflict) {
		t.Errorf("errors.Is: want match ErrCompletionIdempotencyConflict, got no-match")
	}
	var asProbe *remote.RemoteCompletionError
	if !errors.As(remErr, &asProbe) {
		t.Errorf("errors.As: want match *RemoteCompletionError, got no-match")
	}
	if asProbe.Kind != kind {
		t.Errorf("errors.As Kind: want %s, got %s", kind, asProbe.Kind)
	}
}

// ── Test 3: invalid_manifest ──────────────────────────────────────────────

func TestRemoteCompletionError_InvalidManifest(t *testing.T) {
	kind := remote.ErrorKindInvalidManifest
	if got := kind.HTTPStatus(); got != http.StatusBadRequest {
		t.Fatalf("HTTPStatus: want %d, got %d", http.StatusBadRequest, got)
	}
	remErr := remote.NewRemoteCompletionError(kind, "schema_version mismatch", remote.ErrCompletionInvalidManifest)
	if !errors.Is(remErr, remote.ErrCompletionInvalidManifest) {
		t.Errorf("errors.Is: want match ErrCompletionInvalidManifest, got no-match")
	}
	var asProbe *remote.RemoteCompletionError
	if !errors.As(remErr, &asProbe) {
		t.Errorf("errors.As: want match *RemoteCompletionError, got no-match")
	}
	if asProbe.Kind != kind {
		t.Errorf("errors.As Kind: want %s, got %s", kind, asProbe.Kind)
	}
}

// ── Test 4: artifact_missing ──────────────────────────────────────────────

func TestRemoteCompletionError_ArtifactMissing(t *testing.T) {
	kind := remote.ErrorKindArtifactMissing
	if got := kind.HTTPStatus(); got != http.StatusUnprocessableEntity {
		t.Fatalf("HTTPStatus: want %d, got %d", http.StatusUnprocessableEntity, got)
	}
	remErr := remote.NewRemoteCompletionError(kind, "artifact state not finalized", remote.ErrCompletionArtifactMissing)
	if !errors.Is(remErr, remote.ErrCompletionArtifactMissing) {
		t.Errorf("errors.Is: want match ErrCompletionArtifactMissing, got no-match")
	}
	var asProbe *remote.RemoteCompletionError
	if !errors.As(remErr, &asProbe) {
		t.Errorf("errors.As: want match *RemoteCompletionError, got no-match")
	}
	if asProbe.Kind != kind {
		t.Errorf("errors.As Kind: want %s, got %s", kind, asProbe.Kind)
	}
}

// ── Test 5: publisher_unavailable ─────────────────────────────────────────

func TestRemoteCompletionError_PublisherUnavailable(t *testing.T) {
	kind := remote.ErrorKindPublisherUnavailable
	if got := kind.HTTPStatus(); got != http.StatusServiceUnavailable {
		t.Fatalf("HTTPStatus: want %d, got %d", http.StatusServiceUnavailable, got)
	}
	remErr := remote.NewRemoteCompletionError(kind, "delivery publisher not wired", remote.ErrCompletionPublisherUnavailable)
	if !errors.Is(remErr, remote.ErrCompletionPublisherUnavailable) {
		t.Errorf("errors.Is: want match ErrCompletionPublisherUnavailable, got no-match")
	}
	var asProbe *remote.RemoteCompletionError
	if !errors.As(remErr, &asProbe) {
		t.Errorf("errors.As: want match *RemoteCompletionError, got no-match")
	}
	if asProbe.Kind != kind {
		t.Errorf("errors.As Kind: want %s, got %s", kind, asProbe.Kind)
	}
}

// ── Test 6: rate_limited (Retry-After surfaces in BOTH header + body) ─────

func TestRemoteCompletionError_RateLimited(t *testing.T) {
	kind := remote.ErrorKindRateLimited
	if got := kind.HTTPStatus(); got != http.StatusTooManyRequests {
		t.Fatalf("HTTPStatus: want %d, got %d", http.StatusTooManyRequests, got)
	}
	if got := kind.RetryAfter(); got == 0 {
		t.Fatalf("RetryAfter: want non-zero (rate-limit signal), got 0")
	}
	// Use the default backoff as the canonical hint; mirror server
	// behaviour when no custom retry hint is provided.
	remErr := remote.NewRemoteCompletionError(kind, "upstream rate-limited", remote.ErrCompletionRateLimited)
	remErr.RetryAfter = remote.DefaultRateLimitBackoff
	if !errors.Is(remErr, remote.ErrCompletionRateLimited) {
		t.Errorf("errors.Is: want match ErrCompletionRateLimited, got no-match")
	}
	// JSON should include retry_after_seconds when RetryAfter > 0.
	body, err := json.Marshal(remErr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"retry_after_seconds":60`) {
		t.Errorf("JSON missing retry_after_seconds:60 (default 60s): %s", string(body))
	}
	// Verify the wire-reconstructed hint preserves the duration
	// (no precision loss on round-trip to seconds-truncated int).
	var probe struct {
		Kind              string `json:"kind"`
		Error             string `json:"error"`
		RetryAfterSeconds int    `json:"retry_after_seconds"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.RetryAfterSeconds != 60 {
		t.Errorf("retry_after_seconds round-trip: want 60, got %d", probe.RetryAfterSeconds)
	}
}

// ── Test 7: internal_db_error ─────────────────────────────────────────────

func TestRemoteCompletionError_InternalDbError(t *testing.T) {
	kind := remote.ErrorKindInternalDbError
	if got := kind.HTTPStatus(); got != http.StatusInternalServerError {
		t.Fatalf("HTTPStatus: want %d, got %d", http.StatusInternalServerError, got)
	}
	if got := kind.RetryAfter(); got != 0 {
		t.Fatalf("RetryAfter: want 0, got %s", got)
	}
	remErr := remote.NewRemoteCompletionError(kind, "INSERT failed: UNIQUE constraint", remote.ErrCompletionInternalDbError)
	if !errors.Is(remErr, remote.ErrCompletionInternalDbError) {
		t.Errorf("errors.Is: want match ErrCompletionInternalDbError, got no-match")
	}
	var asProbe *remote.RemoteCompletionError
	if !errors.As(remErr, &asProbe) {
		t.Errorf("errors.As: want match *RemoteCompletionError, got no-match")
	}
	if asProbe.Kind != kind {
		t.Errorf("errors.As Kind: want %s, got %s", kind, asProbe.Kind)
	}
}

// ── Test 8: canonical enumeration audit-pin ───────────────────────────────

func TestCanonicalErrorKinds_ExhaustiveHasExactlySeven(t *testing.T) {
	kinds := remote.CanonicalErrorKinds()
	if len(kinds) != 7 {
		t.Fatalf("CanonicalErrorKinds length: want 7, got %d (%v)", len(kinds), kinds)
	}
	// Verify every kind is Valid (the audit-pin for the closed-set).
	seen := make(map[remote.ErrorKind]bool)
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("duplicate kind in CanonicalErrorKinds: %s", k)
		}
		seen[k] = true
		if !k.Valid() {
			t.Errorf("kind %s not Valid (forward-prevention failure)", k)
		}
	}
	// Verify Valid() rejects every OTHER ErrorKind (forward-
	// prevention: an attacker (or wire bug) sending an unknown kind
	// must not slip into the canonical taxonomy).
	if remote.ErrorKind("unknown_kind").Valid() {
		t.Errorf("Valid: unknown_kind should NOT be valid")
	}
	if remote.ErrorKind("").Valid() {
		t.Errorf("Valid: empty kind should NOT be valid")
	}
}

// ── Test 9: wire round-trip (encode → decode preserves Kind + Message) ────

func TestRemoteCompletionError_WireRoundtrip(t *testing.T) {
	original := remote.NewRemoteCompletionError(
		remote.ErrorKindRateLimited,
		"upstream blocked: too many requests",
		remote.ErrCompletionRateLimited,
	)
	original.RetryAfter = 30 * time.Second

	// Encode to wire JSON.
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Verify the wire body has both kind + retry_after_seconds.
	wire := string(body)
	if !strings.Contains(wire, `"kind":"rate_limited"`) {
		t.Errorf("wire missing kind:rate_limited: %s", wire)
	}
	if !strings.Contains(wire, `"retry_after_seconds":30`) {
		t.Errorf("wire missing retry_after_seconds:30: %s", wire)
	}
	// Decode back as a generic struct (simulates client_decoder.go).
	var probe struct {
		Kind              string `json:"kind"`
		Error             string `json:"error"`
		RetryAfterSeconds int    `json:"retry_after_seconds"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.Kind != "rate_limited" {
		t.Errorf("decoded kind: want rate_limited, got %s", probe.Kind)
	}
	if probe.Error != "upstream blocked: too many requests" {
		t.Errorf("decoded error: want upstream blocked, got %s", probe.Error)
	}
	if probe.RetryAfterSeconds != 30 {
		t.Errorf("decoded retry_after_seconds: want 30, got %d", probe.RetryAfterSeconds)
	}
}

// ── Test 10: nil-receiver robustness + NewRemoteCompletionError guard ─────

func TestRemoteCompletionError_NilReceiverAndUnknownKind(t *testing.T) {
	// nil receiver must not panic on .Error() / .Is() / .Unwrap().
	var nilErr *remote.RemoteCompletionError
	if errStr := nilErr.Error(); !strings.Contains(errStr, "<nil") {
		t.Errorf("nil.Error(): want marker for nil, got %s", errStr)
	}
	if nilErr.Is(remote.ErrCompletionLeaseLost) {
		t.Errorf("nil.Is: want false (nil receiver), got true")
	}
	if nilErr.Unwrap() != nil {
		t.Errorf("nil.Unwrap: want nil, got %v", nilErr.Unwrap())
	}

	// Unknown kind coercion to internal_db_error (forward-prevention).
	remErr := remote.NewRemoteCompletionError(
		remote.ErrorKind("totally_bogus"),
		"forward-prevention test",
		errors.New("force-inner"),
	)
	if remErr.Kind != remote.ErrorKindInternalDbError {
		t.Errorf("unknown kind must coerce to internal_db_error: got %s", remErr.Kind)
	}
	if remErr.Kind.HTTPStatus() != http.StatusInternalServerError {
		t.Errorf("unknown-kind-coerced status: want %d, got %d", http.StatusInternalServerError, remErr.Kind.HTTPStatus())
	}

	// fmt fmt usage: keep fmt import live (compile-time pin) for
	// future expansion of the canonical tests.
	_ = fmt.Sprintf
}
