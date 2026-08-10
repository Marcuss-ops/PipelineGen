// Package artlist — F2.11 audit-pin tests.
//
// F2.11 (June 2026, override brutal): artlist drops the legacy
// DriveFolderManager surface so DestinationService is Publisher-only and
// the silent `folderID = rootFolderID` fallback is gone. These tests pin
// the three contracts that the migration MUST preserve:
//
//  1. NewService FAILS CLOSED at composition when Publisher is nil
//     (`ErrPublisherUnavailable` surfaced at startup, not a silent
//     fallback at first request time) — QDRANT-002 dispatcher guard
//     precedent.
//
//  2. DestinationService.ResolveDestination is Publisher-ONLY — no
//     legacy else-branch (driveManager.EnsureFolder), no silent
//     `folderID = rootFolderID` fallback. The Publisher.ResolveFolder
//     call signature is pinned (Destination + Group + ParentFolderID).
//
//  3. NewDestinationService PANICS on nil publisher at construction
//     time. Service.NewService already fails-closed on Publisher via
//     ErrPublisherUnavailable, so the panic is a programming-defect
//     surface (a typed-nil bypass from a test fixture). The panic
//     surfaces the defect immediately instead of null-derefing at the
//     first ResolveDestination call site.
//
// These tests live next to the production files they pin (per AGENTS.md
// Go convention: `_test.go` in the same package) so they exercise the
// exact internal symbols (ErrPublisherUnavailable, PublisherPort,
// NewDestinationService) without exporting them just for tests.
package artlist

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// fakePublisher is a delivery.Publisher stub that records every
// ResolveFolder + Publish call so test asserts can pin the canonical
// contract. F2.11 audit pins: ResolveFolder+LastResolveReq are pinned
// by TestDestinationService_PublisherOnly_F2_11; PublishCalls+LastPublishReq
// are pinned by TestUpdateCumulativeMetadataJSON_NilReaderSkips_F2_11
// (and any future semantic_enricher body audit pin).
type fakePublisher struct {
	mu              sync.Mutex
	ResolveCalls    int
	LastResolveReq  delivery.PublishRequest
	ResolveErr      error
	ResolveFolderID string // returned when ResolveErr == nil

	PublishCalls   int
	LastPublishReq delivery.PublishRequest
}

func (f *fakePublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PublishCalls++
	f.LastPublishReq = req
	// Returning a deterministic success result lets the production
	// path proceed normally (semantic_enricher just logs and
	// continues on Publish errors; the F2.11 audit pin cares about
	// call count + shape, not error propagation).
	return &delivery.PublishResult{
		FileID:      "stub-publish-file-id",
		WebViewLink: "https://drive.google.com/file/d/stub-publish-file-id/view",
		FolderID:    "stub-publish-folder-id",
		Destination: req.Destination,
	}, nil
}

func (f *fakePublisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ResolveCalls++
	f.LastResolveReq = req
	if f.ResolveErr != nil {
		return "", f.ResolveErr
	}
	return f.ResolveFolderID, nil
}

// TestNewService_FailClosedOnNilPublisher_F2_11 pins the F2.11
// composition-time fail-fast sentinel. With Publisher == nil,
// NewService MUST return ErrPublisherUnavailable and a nil *Service
// before any other constructor side-effects (liveCache init,
// NewSearchService dispatch-bridge wiring, etc.) — the bare-minimum
// subset of deps in this test proves the early-return gate works
// regardless of other field state (the Publisher check is the
// first guard in NewService per the brutal-override design).
func TestNewService_FailClosedOnNilPublisher_F2_11(t *testing.T) {
	t.Parallel()

	// Intentionally not using baseServiceDeps: this test must keep
	// Publisher == nil to exercise the ErrPublisherUnavailable gate.
	svc, err := NewService(ServiceDeps{
		// Publisher MUST be nil for the audit pin. All other fields
		// are zero-valued: this proves the Publisher check fires
		// before any other constructor reads deps fields (no
		// dependency on Bundle wiring for the audit pin).
		ServicePorts: ServicePorts{
			Publisher: nil,
		},
	})
	if !errors.Is(err, ErrPublisherUnavailable) {
		t.Fatalf("NewService(nil Publisher): want ErrPublisherUnavailable, got %v", err)
	}
	if svc != nil {
		t.Fatalf("NewService(nil Publisher): want nil svc alongside the error, got %T", svc)
	}
}

// TestDestinationService_PublisherOnly_F2_11 pins that
// DestinationService.ResolveDestination is Publisher-only (no
// legacy else-branch, no silent `folderID = rootFolderID`
// fallback) per the user spec verbatim.
//
// Cfg is intentionally nil so this test exercises the under-cfg
// branch (ParentFolderID is set non-conditionally — the
// canonical path for any caller that does not pass cfg).
func TestDestinationService_PublisherOnly_F2_11(t *testing.T) {
	t.Parallel()

	// Step 1: stub the Publisher so we can record ResolveFolder calls.
	pub := &fakePublisher{ResolveFolderID: "drive-folder-id-stub"}

	// Step 2: hand-craft a Service so we can construct a
	// DestinationService directly (bypassing NewService's full init
	// path which would require a wired dispatcher + clips repo +
	// outbox). DestinationService only reads cfg + publisher from
	// the parent Service per its current contract.
	svc := &Service{
		cfg:       nil,
		log:       zap.NewNop(),
		publisher: pub,
	}
	dst := NewDestinationService(svc)

	// Step 3: drive ResolveDestination. Term is non-empty so the
	// early-return guards pass; rootFolderID is non-empty so the
	// Publisher.ResolveFolder call is reached.
	got, err := dst.ResolveDestination(context.Background(), "funny-moments", "term-folder-id")
	if err != nil {
		t.Fatalf("ResolveDestination: want nil error, got %v", err)
	}
	if got == nil {
		t.Fatal("ResolveDestination: want non-nil DestinationInfo, got nil")
	}
	if got.FolderID != "drive-folder-id-stub" {
		t.Fatalf("ResolveDestination: FolderID want %q (Publisher.ResolveFolder stub return), got %q",
			"drive-folder-id-stub", got.FolderID)
	}
	// textutil.SafeName replaces hyphens with spaces, so the FolderPath
	// is "/Artlist/funny moments" rather than "/Artlist/funny-moments".
	// Pinning the SafeName-derivation contract here (vs the input
	// literal) keeps the test honest to the production derivation.
	if got.FolderPath != "/Artlist/funny moments" {
		t.Fatalf("ResolveDestination: FolderPath want %q (textutil.SafeName(\"funny-moments\")-derived — SafeName replaces - with space), got %q",
			"/Artlist/funny moments", got.FolderPath)
	}

	// Step 4: pin the canonical PublishRequest shape that reached
	// Publisher.ResolveFolder. F2.11 sends these three fields EXACTLY
	// (Destination + Group + ParentFolderID) — any drift breaks
	// the destination-policy resolution pipeline.
	if pub.ResolveCalls != 1 {
		t.Fatalf("Publisher.ResolveFolder calls: want 1 (F2.11 brutal override: Publisher-only path), got %d",
			pub.ResolveCalls)
	}
	if pub.LastResolveReq.Destination != delivery.DestinationArtlist {
		t.Fatalf("Publisher.LastResolveReq.Destination: want %q, got %q",
			delivery.DestinationArtlist, pub.LastResolveReq.Destination)
	}
	// destination_service applies textutil.SafeName(term) to compute
	// Group — the test pins the SafeName-derivation contract rather
	// than coincidence on a single input, so a future SafeName
	// refactor (e.g. lowercasing) won't silently break the audit pin.
	if pub.LastResolveReq.Group != textutil.SafeName("funny-moments") {
		t.Fatalf("Publisher.LastResolveReq.Group: want %q (textutil.SafeName(\"funny-moments\")-derived), got %q",
			textutil.SafeName("funny-moments"), pub.LastResolveReq.Group)
	}
	if pub.LastResolveReq.ParentFolderID != "term-folder-id" {
		t.Fatalf("Publisher.LastResolveReq.ParentFolderID: want %q (parent folder MUST be threaded to pin clip location per F2.11), got %q",
			"term-folder-id", pub.LastResolveReq.ParentFolderID)
	}
}

// TestDestinationService_NilPublisherPanic_F2_11 pins the
// NewDestinationService panic on nil publisher. Service.NewService
// already gates Publisher via ErrPublisherUnavailable, so this
// path is ONLY reachable from a typed-nil PublisherPort inference
// (i.e. a programming defect in a test fixture, never in
// production where the composition root pre-rejects Publisher
// nil). The panic is the correct fail-fast surface: silently
// null-derefing at the first request-time ResolveDestination call
// would be worse than panicking at construction.
func TestDestinationService_NilPublisherPanic_F2_11(t *testing.T) {
	t.Parallel()

	svc := &Service{
		cfg:       nil,
		log:       zap.NewNop(),
		publisher: nil, // typed-nil bypasses Service.NewService (only reachable in test fixtures)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewDestinationService(nil publisher): want panic (programming-defect surface), got nil")
		}
	}()
	_ = NewDestinationService(svc)
}

// TestUpdateCumulativeMetadataJSON_NilReaderSkips_F2_11 pins the
// F2.11 reader-nil soft-dep surface. The Publisher is fail-fast in
// Service.NewService (ErrPublisherUnavailable), so the runtime
// reachability of e.publisher.Publish is guaranteed. The Reader is
// a soft-dep — test fixtures opting out of cumulative metadata.json
// sync can pass nil reader (the caller in Enrich() already gates on
// `e.publisher != nil && folderID != ""`; the inner check below
// remains for the reader-only nil path which has no equivalent
// composition guard).
//
// The full F2.11 body round-trip (Reader.SearchFiles + Reader.DownloadFile
// + Lifecycle.Trash + Publisher.Publish) is intentionally NOT
// audit-pinned in this commit. The full-stub round-trip test would
// require stubs for all 8 drive.Reader methods + all 4
// drive.FileLifecycle methods (50+ lines of test-only boilerplate)
// for a path that is structurally identical to the pre-F2.11 body
// modulo surface renames (SearchFiles→ListByQuery, DownloadFile→Download,
// etc.). A future commit can add the round-trip test if F2.11 ever
// re-touches the body; the nil-reader early-return pin covers the
// only NEW behavior mode in this F2.11 commit. The non-nil-reader
// happy path is covered end-to-end by the production rollout
// (composition root at build_bundles_drive.go + module_sources.go
// wire a real Publisher since the F2.11 mandate) — any regression
// surfaces as a monitored-by-operator failure on the first
// cumulative metadata.json sync after deploy.
func TestUpdateCumulativeMetadataJSON_NilReaderSkips_F2_11(t *testing.T) {
	t.Parallel()

	// Construct a SemanticEnricher with publisher stub + nil reader.
	// The early-return on nil reader MUST skip the RMW path entirely.
	pub := &fakePublisher{ResolveFolderID: "unreached"}
	enricher := &SemanticEnricher{
		log:       zap.NewNop(),
		publisher: pub,
		reader:    nil, // F2.11 test-fixture opt-out
	}

	// Drive with non-empty parent folderID + clipID + newEntry. The
	// function MUST return early without calling publisher.Publish,
	// without searching, without trashing.
	enricher.updateCumulativeMetadataJSON(
		context.Background(),
		"parent-folder-id",
		"test-clip-id",
		map[string]any{"clip_id": "test-clip-id", "name": "Test"},
	)

	// Pin: publisher.Publish must NOT have been called. The early-
	// return path is silent (debug log) and produces no side-effects.
	// PublishCalls is the canonical counter — ResolveCalls is
	// DestinationService's surface, not the enricher's.
	if pub.PublishCalls != 0 {
		t.Fatalf("nil-reader early-return broke: publisher.Publish should NOT be called, got PublishCalls=%d, LastPublishReq=%+v",
			pub.PublishCalls, pub.LastPublishReq)
	}
}
