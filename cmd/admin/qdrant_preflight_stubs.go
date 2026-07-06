// cmd/admin/qdrant_preflight_stubs.go — stub test implementations
// extracted from qdrant_preflight.go (PR-PREFLIGHT-SPLIT, July 2026).
//
// Tests 3-8, 10, 11 are forward-pointer stubs returning
// ErrPreflightNotImplemented per godlike/07 NO-FAKE-AVAILABILITY.
// Each stub will be replaced by a real implementation in its per-test
// follow-up PR.
package main

import (
	"context"
	"fmt"
)

// testOutboxEventsCreated (PR-QDRANT-PREFLIGHT-TEST-3):
// Stub gate. Real implementation lands in PR-QDRANT-PREFLIGHT-TEST-3-IMPL
// forward-pointer (per per-test PR granularity). Returns ErrPreflightNotImplemented
// so the CI gate FAIL-closes loudly per godlike/07 NO-FAKE-AVAILABILITY.
// A future Test 3 implementation will: POST /api/script/generate-from-clips with
// admin token; capture asset_id + job_id; assert SELECT outbox_events WHERE
// aggregate_id=<id> AND event_type='asset.index.requested' exists.
func testOutboxEventsCreated(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-3-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testOutboxEventsCompleted (PR-QDRANT-PREFLIGHT-TEST-4):
// Stub gate. See Test 3 forward-pointer note.
func testOutboxEventsCompleted(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-4-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testMediaAssetsIndexStateIndexed (PR-QDRANT-PREFLIGHT-TEST-5):
// Stub gate.
func testMediaAssetsIndexStateIndexed(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-5-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testQdrantScrollFindsAsset (PR-QDRANT-PREFLIGHT-TEST-6):
// Stub gate. Will POST /points/scroll with filter asset_id=<seed>.
func testQdrantScrollFindsAsset(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-6-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testHybridSearchScore (PR-QDRANT-PREFLIGHT-TEST-7):
// Stub gate. Will GET /internal/v1/media/search?q=<seed desc>.
func testHybridSearchScore(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-7-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testSupersedeGate (PR-QDRANT-PREFLIGHT-TEST-8):
// Stub gate. Will ingest 2nd copy of asset with different source_version
// + verify 2 outbox events with same aggregate_id but different source_version.
func testSupersedeGate(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-8-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testDeleteTombstone (PR-QDRANT-PREFLIGHT-TEST-10-DELETE-TOMBSTONE):
// Stub gate. Will DELETE /api/assets/clips/<sandbox-id> + verify
// media_assets.lifecycle_state='DELETED' + GET /points/<id> returns 404.
func testDeleteTombstone(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-10-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testVoiceoverPiggyback (PR-QDRANT-PREFLIGHT-TEST-11-VOICEOVER):
// Stub gate. Will SubmitAsync voiceover.generate + wait 3 min + assert
// outbox_events asset.index.requested completed + Qdrant scroll finds vo asset.
func testVoiceoverPiggyback(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-11-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}
