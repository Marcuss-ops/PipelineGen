// Package asset_test pins the canonical contract for ValidateSHA256 +
// SHA256IdempotencyKey (Stock Cutover P0 2.4 — July 2026).
//
// godlike/06 SSOT: tests touch only the public surface from the test
// package (asset_test) — never the internal asset package directly.
// godlike/07 typed-error: ErrSHA256Invalid is reachable via errors.Is
// from any caller; the assertion below locks the contract so future
// wrapping does not break the audit pin.
package asset_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// canonicalHex64 is a 64-char all-lowercase-hex string used as the
// happy-path input. Reused across tests; tests are byte-equivalent
// across runs.
const canonicalHex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// ── ValidateSHA256 — 7 invalid input classes + 1 happy path ──────
//
// Per verdict P0 #3 + AGENTS.md godlike/07: every rejection must be
// observable via errors.Is(err, ErrSHA256Invalid), and the rejection
// reason must be name-able in the message (len-? / non-hex / uppercase)
// so operator logs surface producer-side bugs at the boundary.

func TestValidateSHA256_RejectsSevenInvalidInputClasses(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"len-1", "a"},
		{"len-15", strings.Repeat("a", 15)},
		{"len-63", strings.Repeat("a", 63)},
		{"non-hex (g)", canonicalHex64[:63] + "g"},
		{"uppercase A", strings.ToUpper(canonicalHex64)},
		{"uppercase mid (mixed)", canonicalHex64[:32] + "ABCDEF" + canonicalHex64[38:]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := asset.ValidateSHA256(tc.value)
			require.Error(t, err, "ValidateSHA256(%q) must reject", tc.value)
			assert.True(t, errors.Is(err, asset.ErrSHA256Invalid),
				"rejection must surface ErrSHA256Invalid via errors.Is (got %v)", err)
			assert.Empty(t, got, "rejected input must return empty canonical (no silent lowering)")
		})
	}
}

func TestValidateSHA256_HappyPathCanonicalEcho(t *testing.T) {
	got, err := asset.ValidateSHA256(canonicalHex64)
	require.NoError(t, err)
	assert.Equal(t, canonicalHex64, got, "valid input must echo canonical form unchanged")
}

func TestValidateSHA256_HappyPath_NonCanonicalButValid(t *testing.T) {
	// All-zero pad is a valid 64-char lowercase hex digest; exercise
	// the "valid but boring" path so the byte-stable property is
	// pinned at the zero boundary.
	zeroes := strings.Repeat("0", 64)
	got, err := asset.ValidateSHA256(zeroes)
	require.NoError(t, err)
	assert.Equal(t, zeroes, got)
}

// ── SHA256IdempotencyKey — composition safety contract ─────────────

func TestSHA256IdempotencyKey_HappyPath_ComposesPrefixColonHex16(t *testing.T) {
	got, err := asset.SHA256IdempotencyKey("stock", canonicalHex64)
	require.NoError(t, err)
	want := "stock:" + canonicalHex64[:16]
	assert.Equal(t, want, got, "idempotency-key composition: prefix + ':' + 16 hex chars")
}

func TestSHA256IdempotencyKey_HappyPath_PreservesPrefixVerbatim(t *testing.T) {
	// Multiple prefixes (stock, youtube, artlist, drive, doc, ...) MUST
	// compose without modification — the helper is a typed composition,
	// not a prefix-canonicaliser. Pin the per-prefix round-trip so
	// future prefix-whitespace-trim regressions are caught.
	for _, prefix := range []string{"stock", "youtube", "artlist", "drive", "doc"} {
		got, err := asset.SHA256IdempotencyKey(prefix, canonicalHex64)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(got, prefix+":"),
			"idempotency key for prefix %q must start with %q (got %q)", prefix, prefix+":", got)
	}
}

func TestSHA256IdempotencyKey_PropagatesErrSHA256InvalidForShortInput(t *testing.T) {
	// The exact panic site that motivated this helper:
	//   "stock:" + sha[:16]   panics when len(sha) < 16.
	// SHA256IdempotencyKey MUST reject the short input via
	// ErrSHA256Invalid BEFORE slicing, so the panic path is
	// unreachable for any caller using this helper.
	_, err := asset.SHA256IdempotencyKey("stock", "a")
	require.Error(t, err)
	assert.True(t, errors.Is(err, asset.ErrSHA256Invalid),
		"short hash must surface ErrSHA256Invalid via errors.Is (got %v)", err)
}

func TestSHA256IdempotencyKey_PropagatesErrSHA256InvalidForUppercase(t *testing.T) {
	_, err := asset.SHA256IdempotencyKey("stock", strings.ToUpper(canonicalHex64))
	require.Error(t, err)
	assert.True(t, errors.Is(err, asset.ErrSHA256Invalid),
		"uppercase must surface ErrSHA256Invalid via errors.Is (got %v)", err)
}

func TestSHA256IdempotencyKey_PropagatesErrSHA256InvalidForNonHex(t *testing.T) {
	_, err := asset.SHA256IdempotencyKey("stock", canonicalHex64[:63]+"!")
	require.Error(t, err)
	assert.True(t, errors.Is(err, asset.ErrSHA256Invalid),
		"non-hex char must surface ErrSHA256Invalid via errors.Is (got %v)", err)
}

func TestSHA256IdempotencyKey_PropagatesErrSHA256InvalidForEmpty(t *testing.T) {
	_, err := asset.SHA256IdempotencyKey("stock", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, asset.ErrSHA256Invalid),
		"empty value must surface ErrSHA256Invalid via errors.Is (got %v)", err)
}

// ── godlike/07 message-content audit-pinning ───────────────────────
//
// The error message MUST name the reason (len / non-hex / uppercase)
// so on-call operators can grep logs for producer-side bugs
// without running debugging tools. The contract is locked here so
// future refactors don't strip the reason from the message.

func TestValidateSHA256_ErrorMessageNamesReason(t *testing.T) {
	cases := []struct {
		name         string
		value        string
		wantContains string
	}{
		{"empty", "", "empty value"},
		{"short", "abc", "len=3"},
		{"long", strings.Repeat("a", 70), "len=70"},
		{"non-hex", canonicalHex64[:63] + "z", "lowercase hex"},
		{"uppercase", strings.ToUpper(canonicalHex64), "lowercase hex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := asset.ValidateSHA256(tc.value)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantContains,
				"error message must name the failure reason for log-grepping")
		})
	}
}
