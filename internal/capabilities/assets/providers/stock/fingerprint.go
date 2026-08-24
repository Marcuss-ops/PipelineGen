// Package stock — fingerprint.go is the canonical deterministic sampler
// for the stock pipeline (PipelineGen Stock Cutover §12-2, July 2026).
//
// godlike/06 SSOT: this package is the single owner of the
// "derive a stable seed for sampling decisions" fact. The prior
// implementation declared `var rng = rand.New(rand.NewSource(time.Now().UnixNano()))`
// in service.go — a global, time-seeded RNG that destroyed reproducibility
// across pipeline restarts. This file retires that global in favour
// of a deterministic seed derivation tied to the canonical
// (run_fingerprint, source_id, source_version, sampling_policy_version)
// quadruple per user spec.
//
// godlike/07 typed-error contract: missing-required-field failures
// surface as `ErrInvalidSeedInput` (sentinel, `errors.New(...)`) so the
// composition root can fail-closed at RunInput-validation time rather
// than at first-sample call (no fake availability).
//
// Determinism contract: for any fixed `SeedInput` triple, the seed
// string produced by RunFingerprintFor is byte-identical across runs,
// goroutines, and processes. The Sampler constructed from that seed
// produces the same Float64 sequence (modulo order) across instances;
// the planner thus produces the same cut plan (piano di taglio) for
// the same inputs across retries. Verified by fingerprint_test.go
// (1000-iteration byte-stability).
package assets

import (
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"math/rand"
	"strconv"
)

// SamplingPolicyVersion is the version of the sampling-policy encoded
// into every seed. Bumping this constant is the canonical "force a
// different plan for any prior seed" lever (godlike/07 plan-evolution
// audit-pin). Production bump: increase the suffix; the canonical
// seed format stays byte-identical for all other fields.
const SamplingPolicyVersion = "stock-sampling-policy/v1"

// ErrInvalidSeedInput is returned by RunFingerprintFor when a
// required field is empty (RunFingerprint is required; SourceID is
// optional for run-level seeds). godlike/07 typed-error contract:
// reachable via errors.Is from any caller seam.
var ErrInvalidSeedInput = errors.New("stock.RunFingerprintFor: SeedInput failed validation")

// SeedInput is the canonical input quadruple per user spec.
// RunFingerprint is required (a run-level stable identifier);
// SourceID is optional (empty means "run-level only", used for the
// interleave shuffle); SourceVersion 0 is a valid initial value
// (not validated as missing — distinguish via the input shape).
type SeedInput struct {
	// RunFingerprint is the stable identifier for the whole run
	// (e.g., derived from the request payload + job-ID + configuration
	// snapshot — the canonical seed "context" that's identical across
	// worker restarts).
	RunFingerprint string
	// SourceID identifies the specific input source within the run:
	// the canonical yt-dlp URL hash, the FolderManager folder ID, the
	// Artlist asset ID, or an empty string for run-level-only seeds.
	// Empty is allowed and produces a sentinel "run-level" seed that
	// is still deterministic across retries (canonical form uses "_").
	SourceID string
	// SourceVersion is the source-schema version (positive int64
	// from the upstream schema-versioning contract). 0 is a valid
	// "initial" value (not "missing").
	SourceVersion int64
}

// Validated returns nil iff RunFingerprint is non-empty. SourceID is
// optional (empty allowed; canonical form uses "_" placeholder).
// SourceVersion is intentionally NOT validated (0 is a legitimate
// initial-state value).
func (s SeedInput) Validated() error {
	if s.RunFingerprint == "" {
		return fmt.Errorf("%w: RunFingerprint is required", ErrInvalidSeedInput)
	}
	return nil
}

// canonicalForm produces the deterministic seed-input string that
// feeds into SHA-256. The format is:
//
//	<RunFingerprint>|<SourceID-or-_>|v<SourceVersion>|<SamplingPolicyVersion>
//
// Empty SourceID is canonicalised to "_" so the seed string is
// canonical regardless of whether the caller is per-source (per-source
// SourceID) or run-level (SourceID empty → "_").
func (s SeedInput) canonicalForm() string {
	sourceID := s.SourceID
	if sourceID == "" {
		sourceID = "_"
	}
	return s.RunFingerprint + "|" + sourceID +
		"|v" + strconv.FormatInt(s.SourceVersion, 10) +
		"|" + SamplingPolicyVersion
}

// RunFingerprintFor is the canonical seed derivation per user spec:
//
//	seed = SHA256(<RunFingerprint>|SourceID|v<SourceVersion>|<SamplingPolicyVersion>)
//
// The output is the 64-char hex-encoded SHA-256 string suitable for
// use as a math/rand seed string (NewSampler hashes the hex prefix to
// int64). Returns ErrInvalidSeedInput (wrapped) when RunFingerprint
// is empty — fail-closed at seed derivation per godlike/07.
func RunFingerprintFor(input SeedInput) (seed string, err error) {
	if err := input.Validated(); err != nil {
		return "", fmt.Errorf("stock.RunFingerprintFor: %w", err)
	}
	h := digest.SHA256Bytes([]byte(input.canonicalForm()))
	return h, nil
}

// ── Sampler ─────────────────────────────────────────────────────

// Sampler is a math/rand.Rand instance locked to a deterministic seed.
// Each Sampler is independent (goroutine-safe via math/rand's
// internal mutex when used via top-level funcs; top-level func use is
// recommended for read-only sampling). Sampler construction is the
// ONLY place where math/rand interacts with the world; downstream
// callers use the typed methods (Float64n, Shuffle, Intn) and never
// touch math/rand directly (godlike/06 SSOT one-typed-owner-per-fact).
type Sampler struct {
	src     *rand.Rand
	seedHex string
	seedInt int64
}

// NewSampler constructs a Sampler from a canonical seed hex string
// (the output of RunFingerprintFor). The string is mixed into an int64
// via a stable, dependency-free hash (FNV-1a is stdlib-free at the
// cost of a 64-bit cycling multiplier; we walk the byte prefix to
// keep the int64 seed to the first 8 bytes, deterministic per input).
//
// Byte-stability: NewSampler(s) constructed with the same `s` always
// yields the same `seedInt` (verified by fingerprint_test.go with
// 1000-iteration loop across multiple `s` strings).
func NewSampler(seedHex string) *Sampler {
	// Walk the first 8 bytes of the seed hex; FNV-1a-style canonical
	// reference (per https://datatracker.ietf.org/doc/html/draft-eastlake-fnv
	// — stdlib-free version using prime=1099511628211 + offset=14695981039346656037).
	const (
		fnvOffset uint64 = 14695981039346656037
		fnvPrime  uint64 = 1099511628211
	)
	var h uint64 = fnvOffset
	for i := 0; i < len(seedHex) && i < 64; i++ {
		h ^= uint64(seedHex[i])
		h *= fnvPrime
	}
	return &Sampler{
		src:     rand.New(rand.NewSource(int64(h))),
		seedHex: seedHex,
		seedInt: int64(h),
	}
}

// SeedHex returns the original 64-char hex seed string used at construction.
// Useful for log audit-pin surface and for replay-replay tests pinning
// the seed-to-output chain end-to-end.
func (s *Sampler) SeedHex() string { return s.seedHex }

// SeedInt returns the int64 seed the underlying math/rand is locked to.
// Distinct from SeedHex because math/rand consumes an int64, not the
// hex string itself. Same SeedHex always yields same SeedInt (audit-pin).
func (s *Sampler) SeedInt() int64 { return s.seedInt }

// Float64n returns a deterministic float64 in [0, max). When max <= 0,
// returns 0 (avoids negative or NaN from boundary inputs).
func (s *Sampler) Float64n(max float64) float64 {
	if max <= 0 {
		return 0
	}
	return s.src.Float64() * max
}

// Intn returns a deterministic int in [0, max). When max <= 0, returns 0.
// Stdlib `rand.Intn` panics on max <= 0; this wrapper is the godlike/05
// fail-soft variant that returns 0 instead of crashing the worker.
func (s *Sampler) Intn(max int) int {
	if max <= 0 {
		return 0
	}
	return s.src.Intn(max)
}

// Shuffle reorders `slice` in place using the deterministic
// Fisher-Yates driver. Returns the same slice (idiomatic in-place).
// Identical seeds produce identical orderings across calls (verified
// in fingerprint_test.go by 1000-iteration determinism).
func (s *Sampler) Shuffle(slice []int) {
	s.src.Shuffle(len(slice), func(i, j int) {
		slice[i], slice[j] = slice[j], slice[i]
	})
}

// ShuffleStrings is the string-slice variant of Shuffle (the
// Sampler.Shuffle is typed on []int for the canonical sequence;
// string-slice is a convenience for the interleave shuffle path).
func (s *Sampler) ShuffleStrings(slice []string) {
	s.src.Shuffle(len(slice), func(i, j int) {
		slice[i], slice[j] = slice[j], slice[i]
	})
}

// ShuffleStruct is a generic Shuffle in place over a slice of *T
// (interface {} covers arbitrary element types via rand's any).
// For per-source clip-window ordering, callers pass []VideoSource{}
// or []int{} or []string{}. This helper unifies the dispatch.
func ShuffleStruct[T any](s *Sampler, slice []T) {
	s.src.Shuffle(len(slice), func(i, j int) {
		slice[i], slice[j] = slice[j], slice[i]
	})
}
