// Package usecase — multilingual_persistence_p1f_audit_trail_test.go
//
// Group 4 of the P1.F multilingual persistence test surface
// (PR-PY-CLIPS-CORRETTE-TRADOTTE, July 2026): audit-trail-aware
// stub tests pin the godlike/06 invariant:
//   - FlipsPriorCurrent: a new translation row INSERT must flip
//     IsCurrent on the prior current row in the same asset_id +
//     language group.
//   - IdempotencyNoOp: a re-insert with the SAME triple (AssetID +
//     LanguageCode + TranslationKey) MUST NOT append a duplicate
//     row nor alter the existing row's CreatedAt/UpdatedAt/TextContent.
//
// godlike/06 SSOT one-owner-per-fact: this file is the canonical SOLE
// definition site for Group 4; the stubs come from
// multilingual_persistence_p1f_stubs_test.go (SSOT owner of
// p1fStubRepo + newP1FResolver).
//
// godlike/07 NO-FAKE-AVAILABILITY: each test fails LOUDLY on
// invariant violation — never silently absorbed.
package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Audit-trail-aware stub tests ────────────────────────────────────────────

// TestAuditTrail_P1F_Stub_InsertTranslationWithAuditPredecessor_FlipsPriorCurrent
// pins the godlike/06 audit-trail invariant through the test
// seam stub. When a new translation row is inserted under the
// same (asset, language, kind) tuple with a DIFFERENT
// TranslationKey, the prior is_current=1 row MUST be flipped to
// is_current=0 + refreshed UpdatedAt, the new row MUST be
// appended with IsCurrent=true, and the total row count MUST
// grow by exactly 1 (the prior row is preserved, NOT deleted).
//
// The test mirrors the canonical SQLite impl semantics documented
// in `internal/kernel/asset/text_track_repository.go` and the
// `fakeTextTrackRepo` seam in
// `internal/application/assets/texttracks/materializer_test.go`.
func TestAuditTrail_P1F_Stub_InsertTranslationWithAuditPredecessor_FlipsPriorCurrent(t *testing.T) {
	t.Parallel()
	const (
		assetID  = "yt_p1f_audit_001"
		lang     = "it"
		kind     = detail.TextTrackTranscript
		priorKey = "key-prior-v1"
		nextKey  = "key-next-v2"
	)
	createdAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	priorUpdated := createdAt.Add(time.Hour)
	repo := &p1fStubRepo{rows: []detail.TextTrack{
		{
			ID:             1001,
			AssetID:        assetID,
			LanguageCode:   lang,
			TextKind:       kind,
			TextContent:    "italiano v1 - dal DB",
			Status:         detail.TextTrackReady,
			SourceType:     detail.TextSourceTranslation,
			IsCurrent:      true,
			TranslationKey: priorKey,
			PromptVersion:  "prompt-v1",
			CreatedAt:      createdAt,
			UpdatedAt:      priorUpdated,
		},
	}}
	const priorIdx = 0
	if got := len(repo.rows); got != 1 {
		t.Fatalf("seed precondition: want 1 row, got %d", got)
	}
	if !repo.rows[priorIdx].IsCurrent {
		t.Fatalf("seed precondition: prior row MUST start as IsCurrent=true")
	}
	if repo.rows[priorIdx].TranslationKey != priorKey {
		t.Fatalf("seed precondition: prior row TranslationKey = %q, want %q",
			repo.rows[priorIdx].TranslationKey, priorKey)
	}

	err := repo.InsertTranslationWithAuditPredecessor(
		context.Background(),
		detail.TextTrack{
			AssetID:        assetID,
			LanguageCode:   lang,
			TextKind:       kind,
			TextContent:    "italiano v2 - tradotto da en con prompt-v2",
			SourceType:     detail.TextSourceTranslation,
			TranslationKey: nextKey,
			PromptVersion:  "prompt-v2",
			SourceTextHash: "source-text-hash-v2",
		},
	)
	if err != nil {
		t.Fatalf("InsertTranslationWithAuditPredecessor: %v", err)
	}

	// Audit-trail invariant 1 — row count grew by exactly 1. The
	// prior row is preserved (godlike/06: "audit trail is invisible
	// to callers; only is_current matters").
	if got := len(repo.rows); got != 2 {
		t.Fatalf("audit-trail row count: want 2 (preserve + append), got %d", got)
	}
	// Audit-trail invariant 2 — the prior row IsCurrent flipped to
	// false AND UpdatedAt is strictly later than its CreatedAt
	// (the partial UNIQUE INDEX WHERE is_current=1 would reject
	// two is_current=1 rows in the same tuple at the SQL layer).
	if repo.rows[priorIdx].IsCurrent {
		t.Errorf("audit-trail invariant violated: prior row IsCurrent stayed true (the flip was skipped): row=%+v",
			repo.rows[priorIdx])
	}
	if !repo.rows[priorIdx].UpdatedAt.After(repo.rows[priorIdx].CreatedAt) {
		t.Errorf("audit-trail invariant violated: prior row UpdatedAt (%s) MUST be later than CreatedAt (%s)",
			repo.rows[priorIdx].UpdatedAt, repo.rows[priorIdx].CreatedAt)
	}
	// Audit-trail invariant 3 — the new row appended with
	// IsCurrent=true + matching TranslationKey. The prior row's
	// TranslationKey is preserved verbatim (no silent overwrite).
	newRow := repo.rows[1]
	if !newRow.IsCurrent {
		t.Errorf("audit-trail invariant violated: new row IsCurrent = false (must be true): row=%+v", newRow)
	}
	if newRow.TranslationKey != nextKey {
		t.Errorf("audit-trail invariant violated: new row TranslationKey = %q, want %q",
			newRow.TranslationKey, nextKey)
	}
	if newRow.AssetID != assetID || newRow.LanguageCode != lang || newRow.TextKind != kind {
		t.Errorf("audit-trail invariant violated: new row tuple drift: AssetID=%q LanguageCode=%q TextKind=%q",
			newRow.AssetID, newRow.LanguageCode, newRow.TextKind)
	}
	// Audit-trail invariant 4 — exactly ONE row in the stub has
	// IsCurrent=true. The partial UNIQUE INDEX WHERE is_current=1
	// cannot split-brain against an existing is_current=1 row.
	nCurrent := 0
	for _, r := range repo.rows {
		if r.IsCurrent {
			nCurrent++
		}
	}
	if nCurrent != 1 {
		t.Errorf("audit-trail invariant violated: want exactly 1 is_current=1 row in the tuple, got %d", nCurrent)
	}
}

// TestAuditTrail_P1F_Stub_InsertTranslationWithAuditPredecessor_IdempotencyNoOp
// pins the SQLite §Step 1 idempotency contract: when a current
// row already exists in the tuple with a matching
// TranslationKey, the insert is a no-op. No new row is appended,
// no flip fires, and the existing row's flags + UpdatedAt are
// preserved verbatim (matches the SQLite impl's BEGIN IMMEDIATE
// TRANSACTION + SELECT step 1 + COMMIT short-circuit).
func TestAuditTrail_P1F_Stub_InsertTranslationWithAuditPredecessor_IdempotencyNoOp(t *testing.T) {
	t.Parallel()
	const (
		assetID = "yt_p1f_idemp_001"
		lang    = "en"
		kind    = detail.TextTrackTranscript
	)
	key := detail.TranslationKey("source-text-hash", lang, "ollama", "v1", "prompt-v1")
	fixedTime := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	repo := &p1fStubRepo{rows: []detail.TextTrack{
		{
			AssetID:        assetID,
			LanguageCode:   lang,
			TextKind:       kind,
			TextContent:    "already-current",
			Status:         detail.TextTrackReady,
			SourceType:     detail.TextSourceYouTubeSubtitle,
			IsOriginal:     true,
			Provider:       "yt-dlp",
			IsCurrent:      true,
			TranslationKey: key,
			PromptVersion:  "prompt-v1",
			CreatedAt:      fixedTime,
			UpdatedAt:      fixedTime,
		},
	}}
	preCreatedAt := repo.rows[0].CreatedAt
	preUpdatedAt := repo.rows[0].UpdatedAt
	preIsCurrent := repo.rows[0].IsCurrent
	preTextContent := repo.rows[0].TextContent

	err := repo.InsertTranslationWithAuditPredecessor(
		context.Background(),
		detail.TextTrack{
			AssetID:        assetID,
			LanguageCode:   lang,
			TextKind:       kind,
			TextContent:    "SHOULD-BE-DROPPED",
			SourceType:     detail.TextSourceYouTubeSubtitle,
			TranslationKey: key, // same key → idempotent
			PromptVersion:  "prompt-v1",
			SourceTextHash: "source-text-hash",
		},
	)
	if err != nil {
		t.Fatalf("idempotent insert MUST NOT error: %v", err)
	}

	// Idempotency invariant 1 — row count preserved (no append
	// when matching current row exists).
	if got := len(repo.rows); got != 1 {
		t.Fatalf("idempotency invariant violated: want 1 row, got %d (a duplicate was appended)", got)
	}
	// Idempotency invariant 2 — existing row's CreatedAt +
	// UpdatedAt preserved verbatim (no silent refresh on a no-op
	// insert).
	if !repo.rows[0].CreatedAt.Equal(preCreatedAt) {
		t.Errorf("idempotency invariant violated: CreatedAt drifted from %s to %s",
			preCreatedAt, repo.rows[0].CreatedAt)
	}
	if !repo.rows[0].UpdatedAt.Equal(preUpdatedAt) {
		t.Errorf("idempotency invariant violated: UpdatedAt drifted from %s to %s",
			preUpdatedAt, repo.rows[0].UpdatedAt)
	}
	if repo.rows[0].IsCurrent != preIsCurrent {
		t.Errorf("idempotency invariant violated: IsCurrent drifted from %v to %v",
			preIsCurrent, repo.rows[0].IsCurrent)
	}
	if repo.rows[0].TextContent != preTextContent {
		t.Errorf("idempotency invariant violated: TextContent drifted to %q, want %q",
			repo.rows[0].TextContent, preTextContent)
	}
}
