// Package e2e — Qdrant chain E2E test for YouTube clip ingestion
// (PR-QDRANT-E2E-YOUTUBE, wave architecture/current.yaml#QDRANT-CHAIN-VERIFY-2026-07-04).
//
// Hermetic E2E test exercising the PRODUCTION shape of the full
// YouTube→Qdrant chain against a mock Qdrant REST surface. Each of
// the 5 subtests spins up its own in-memory SQLite + mock Qdrant to
// guarantee hermetic isolation (no shared state across subtests).
//
// ARCHITECTURE — Production path exercised here:
//
//	YouTube clip download (proxied by CommitClipAndIndexEvent)
//	   ↓
//	ClipAtomicWriterAdapter.CommitClipAndIndexEvent
//	   ↓ (single-TX, BLOCKER #2 closure)
//	media_assets row (lifecycle_state=ACTIVE) + outbox_events (event_key=...)
//	   ↓ (worker)
//	outbox Repository.ClaimNext → handler → IndexWriter.UpsertFromClip
//	   ↓
//	transport.Client.UpsertPoints(POST /collections/<alias>/points)
//	   ↓
//	mock-Qdrant in-process map[pointID]payload
//	   ↓
//	outbox Repository.MarkCompleted (CAS source_version → INDEXED)
//
// godlike/07 OBLIGATIONS verified per subtest (4 assertions):
//  1. media_assets.index_state = INDEXED
//  2. Qdrant scroll finds the asset_id (= mock map lookup)
//  3. Search returns the result (= mock query handler)
//  4. payload.lifecycle_state = ACTIVE for ACTIVE assets
//
// godlike/06 SSOT: the test wires the PRODUCTION *ClipAtomicWriterAdapter,
// *IndexWriter, *PayloadMapper (with canonical searchtext.Registry wired
// per QdrantRuntime pattern), and *outboxevents.Repository. The ONLY
// mock is the Qdrant REST transport surface — produced by httptest.NewServer
// mimicking /collections/<alias>/points (PUT) + /points/scroll +
// /points/query. No production-shape code is bypassed.
//
// PRE-EXISTING BUILD ISSUES carry-forward (per architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04):
//   - monitor/enqueue.go (strings.ToLower undefined)
//   - monitor/scheduler.go (NewUnboundJobEnqueuer undefined)
//   - stockpipeline/run_upload.go (file missing)
//   - module_media.go (clips.Deps.MutationsDispatcher literal obsolete)
//   - images/routing (import cycle — fixed by commit e52005cc but
//     still flagged carry-forward per godlike/07 no-fake-availability)
//
// The new test file does NOT import any of those pre-existing-build-issue
// packages. It exercises ONLY the production surfaces that compile today
// (artlist test imports mirror the same constraint — see the
// artlist_full_run_test.go header comment).
package e2e
