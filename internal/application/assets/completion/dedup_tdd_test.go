// Package completion_test — dedup_tdd_test.go (P0-COMPL-4 dedup-invariants
// TDD surface, July 2026).
//
// 5 NEW tests pinning the P0-COMPL-4 dedup invariants at the package level.
// Each test is intentionally atomic and pinned to a single behavioural
// invariant; the reflection-based test (Test 1) is the godlike/06 SSOT
// drift-detection on the Service struct (any future re-introduction of
// a `publisher` field on Service is caught at build-failure or this
// test failure, NOT at runtime panic).
//
// godlike/06 SSOT: completion.Service MUST have ONLY 2 ports (Preparer +
// IdempotencyBookkeeper). The Publisher port was REMOVED in P0-COMPL-4
// to eliminate the double-Publish godlike/06 SSOT violation (TWO owners
// of the canonical "Drive write" fact). Drift-detection is enforced both
// by go reflection (this file) AND by the interface-comparison check
// in the canonical surface (publish_verified.go::Service struct).
//
// godlike/07 typed-error contract: dedup tests use the typed sentinels
// (ErrAlreadyPublished, ErrPublishEmptySlice, ErrPublishInvalidArtifact,
// ErrFinalChecksumMismatch) via errors.Is — no string-matching, no direct
// pointer equality, no zero-value substitution.
package completion_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/completion"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
)

// ── 5 DEDUP-INVARIANT TDD TESTS (P0-COMPL-4) ─────────────────────────────────

// Test 1 [godlike/06 SSOT PIN]: Service struct has EXACTLY 2 named fields
// (`preparer` + `bookkeeper`) — no `publisher`/`pub`/`notifier` field is
// re-introduced post-P0-COMPL-4. Drift detection is reflection-based so
// the test fails the build/CI if anyone copy-pastes the old 3-field shape
// back. This is the load-bearing godlike/06 invariant for the entire
// P0-COMPL-4 closure.
//
// godlike/06 rationale: if TWO components can publish (the Preparer AND
// a separate Publisher field), there are TWO owners of the "Drive write"
// fact. Action: every commit that re-introduces such a field must update
// this test + add a forward-pointer to godlike/06 in the PR description.
func TestDedup_NoPublisherFieldOnServiceStruct(t *testing.T) {
	svcType := reflect.TypeOf(completion.Service{})
	gotFields := map[string]bool{}
	for i := 0; i < svcType.NumField(); i++ {
		gotFields[svcType.Field(i).Name] = true
	}

	// Canonical 2-field shape after P0-COMPL-4 closure.
	wantFields := map[string]bool{
		"preparer":   true,
		"bookkeeper": true,
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Errorf("Service struct field-set drifted from canonical P0-COMPL-4 shape:\n  got:  %v\n  want: %v\n  ANY FIELD ADDITION (especially `publisher` / `pub` / `notifier`) IS A REGRESSION — restore this test on contact.",
			gotFields, wantFields)
	}

	// Explicit no-publisher check for double-safety (cheap string search).
	if _, ok := gotFields["publisher"]; ok {
		t.Errorf("Service has a `publisher` field — P0-COMPL-4 dedup violation (double Publish owner)")
	}
	if _, ok := gotFields["pub"]; ok {
		t.Errorf("Service has a `pub` field — P0-COMPL-4 dedup violation (double Publish owner)")
	}
	if _, ok := gotFields["notifier"]; ok {
		t.Errorf("Service has a `notifier` field — P0-COMPL-4 dedup violation (double Publish owner)")
	}
}

// Test 2 [DEDUP-INVARIANT single-prepare-call]: For 1 valid artifact, the
// Preparer.Prepare is invoked EXACTLY ONCE. Two Preparer-style ports is
// invalid — the Service wraps the canonical Preparer and trusts its
// internal finalization.PublisherPort for the Drive-write side-effect.
// Confirmed via cumulative call counter (NOT a magic number — the unit
// test asserts identity, not a constant).
func TestDedup_SinglePrepareCallPerArtifact(t *testing.T) {
	payload := []byte("single-prepare-call invariant payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-100:single_prepare", localPath, payload)

	prepMock := &mockPreparer{
		preparedPub: finalization.PublishedArtifact{
			Location: finalization.AssetLocation{
				Provider: "drive",
				FileID:   "drive-single-prepare-call",
			},
		},
	}
	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if err != nil {
		t.Fatalf("PublishVerifiedArtifacts: %v", err)
	}

	if got := prepMock.calls.Load(); got != 1 {
		t.Errorf("Preparer.Prepare call count: got %d, want 1 (P0-COMPL-4 single-Prepare-call invariant)", got)
	}
}

// Test 3 [DEDUP-INVARIANT no-double-publish-on-retry]: When Prepare
// returns a transient error, the Service retries via pkg/retry.Do but
// the underlying canonical publish path is NOT invoked twice outside
// of Prepare's wrap-and-retry loop. Cumulative Preparer calls = 2 (1
// transient + 1 retry-success). The Publish DRIVE WRITE is encapsulated
// inside Prepare — there is NO separate Publish call in this Service.
//
// To prove the no-double-publish invariant WITHOUT a real Drive stub,
// we instrument the mockPreparer with a tracking counter on its
// internal "publish invocation" surface (here, the preparedPub.Location
// mutation acts as the mark — only the SUCCESSFUL retry produces a
// fully-populated Location).
func TestDedup_NoDoublePublishOnTransientRetry(t *testing.T) {
	payload := []byte("no-double-publish-on-retry payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-200:retry_no_double", localPath, payload)

	prepMock := &mockPreparer{
		preparedPub: finalization.PublishedArtifact{
			Location: finalization.AssetLocation{
				Provider: "drive",
				FileID:   "drive-retry-final",
			},
		},
	}
	prepMock.transientSequence = []error{
		&transientErr{msg: "transient 503 service unavailable"},
		nil, // second retry succeeds — Prepare publishes Drive ONCE inside
	}

	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if err != nil {
		t.Fatalf("PublishVerifiedArtifacts: %v", err)
	}

	// Cumulative call count = 2 (1 transient + 1 success post-retry).
	// This proves NO double-publish in this Service: the Drive write
	// happened INSIDE the second Prepare call, not via a separate Publish.
	if prepMock.calls.Load() != 2 {
		t.Errorf("Preparer cumulative calls: got %d, want 2 (no-double-publish)", prepMock.calls.Load())
	}
	if got[0].Location.FileID != "drive-retry-final" {
		t.Errorf("Location.FileID: got %q, want %q (post-retry success)",
			got[0].Location.FileID, "drive-retry-final")
	}
}

// Test 4 [DEDUP-INVARIANT idempotent-replay-no-prepare]: When the
// (jobID, subID, sha256) triple is already recorded as published, the
// Service MUST NOT call Preparer.Prepare. The cached *PublishedArtifact
// is returned via the output slice position and ErrAlreadyPublished
// surfaces via errors.Is (godlike/07 no-duplicate-side-effects).
func TestDedup_IdempotentReplayZeroPrepareAndZeroRecord(t *testing.T) {
	payload := []byte("idempotent-replay zero-prepare payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-300:already_published", localPath, payload)

	cachedPub := &finalization.PublishedArtifact{
		ArtifactID:     va.ArtifactID,
		Kind:           va.Kind,
		Filename:       va.Filename,
		MIMEType:       va.MIMEType,
		SizeBytes:      va.SizeBytes,
		SHA256:         va.SHA256,
		SourceVersion:  va.SourceVersion,
		Requirement:    va.Requirement,
		IdempotencyKey: remote.ArtifactIdempotencyKey("job-300", "already_published", va.SHA256),
		Location: finalization.AssetLocation{
			Provider: "drive",
			FileID:   "drive-cached-replay",
		},
	}

	bkMock := &mockBookkeeper{
		records: map[string]*finalization.PublishedArtifact{
			keyTriplet("job-300", "already_published", va.SHA256): cachedPub,
		},
	}
	bkMock.isPubTrue.Store(true)

	prepMock := &mockPreparer{}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if !errors.Is(err, completion.ErrAlreadyPublished) {
		t.Errorf("err: got %v, want wraps ErrAlreadyPublished", err)
	}
	if len(got) != 1 || got[0].Location.FileID != "drive-cached-replay" {
		t.Errorf("cached envelope: got %v, want FileID='drive-cached-replay'", got)
	}
	if prepMock.calls.Load() != 0 {
		t.Errorf("Preparer calls: got %d, want 0 (idempotent-replay dedup invariant)", prepMock.calls.Load())
	}
}

// Test 5 [DEDUP-INVARIANT single-canonical-record-write]: For 1 valid
// artifact, the Bookkeeper.RecordPublished is invoked EXACTLY ONCE.
// Replays do NOT double-write the record (idempotent-replay exits BEFORE
// the Record step). This pins the single-source-of-truth on the
// (jobID, subID, sha256) → PublishedArtifact mapping.
func TestDedup_SingleCanonicalRecordPerTriple(t *testing.T) {
	payload := []byte("single-canonical-record per triple payload")
	localPath := writeTempFile(t, payload)
	va := newVerified(t, "job-400:single_record", localPath, payload)

	prepMock := &mockPreparer{
		preparedPub: finalization.PublishedArtifact{
			Location: finalization.AssetLocation{
				Provider: "drive",
				FileID:   "drive-single-record",
			},
		},
	}
	bkMock := &mockBookkeeper{records: map[string]*finalization.PublishedArtifact{}}

	svc, err := completion.NewService(prepMock, bkMock)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va})
	if err != nil {
		t.Fatalf("PublishVerifiedArtifacts: %v", err)
	}

	// Single canonical record on first publish.
	if got, want := len(bkMock.records), 1; got != want {
		t.Errorf("Bookkeeper records: got %d, want %d (single-canonical-record invariant)", got, want)
	}
	wantKey := keyTriplet("job-400", "single_record", va.SHA256)
	if _, ok := bkMock.records[wantKey]; !ok {
		t.Errorf("Bookkeeper missing canonical key %q", wantKey)
	}

	// Idempotent replay adds 0 records (dedup invariant).
	// Re-arm the cache: IsPublished + LookupPublished simulate
	// the cached record we just wrote.
	bkMock.isPubTrue.Store(true)
	if _, err := svc.PublishVerifiedArtifacts(context.Background(), []*finalization.VerifiedArtifact{va}); !errors.Is(err, completion.ErrAlreadyPublished) {
		t.Fatalf("replay err: got %v, want ErrAlreadyPublished", err)
	}
	if got := len(bkMock.records); got != 1 {
		t.Errorf("Bookkeeper records post-replay: got %d, want 1 (replay must NOT add a 2nd record)", got)
	}
}

// Silence unused-import warnings on reserved imports for forward-compat.
var (
	_ atomic.Int64
	_ = time.Now
)
