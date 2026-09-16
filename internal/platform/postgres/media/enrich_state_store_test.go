// Package media_test — enrich_state_store_test.go pins the MEDIA-SSOT P2-9
// Phase 2 enrichment cutover on the live PostgreSQL media SSOT.
//
// The enrichment state machine was wired to the operational SQLite store while
// pgmedia's patch path wrote enrich_state on the SSOT — one fact, two writers,
// two engines — and the VLM sweep selected PENDING rows from SQLite and claimed
// them there. These tests assert the properties that make the migration to a
// single engine safe:
//
//   - a transition lands on the media SSOT (read back on the same handle)
//   - the CAS form loses when the row is not in the expected from-state, which
//     is what stops two sweep ticks claiming the same PENDING row
//   - enrich_state_updated_at and its TIMESTAMPTZ mirror are written together
//     (the migration-004 pair the gate declared but no writer satisfied)
//   - the reader's fence selects an OLD stamp and excludes a FRESH one
//   - an unstamped row compares as OLDER, preserving the retired SQLite
//     behaviour where an empty TEXT stamp sorted before every real date
package media_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

func TestMediaEnrichStateStoreTransitionAndDualWrite(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const id = "yt_enrich_store_v1"
	seedIndexableAsset(t, db, id)

	store := pgmedia.NewMediaEnrichStateStore(db)

	// 1. Unconditional set: PENDING (the ingest stamp path).
	if err := store.SetEnrichState(ctx, id, asset.EnrichStatePending); err != nil {
		t.Fatalf("SetEnrichState(PENDING): %v", err)
	}
	got, err := store.GetEnrichState(ctx, id)
	if err != nil {
		t.Fatalf("GetEnrichState: %v", err)
	}
	if got != asset.EnrichStatePending {
		t.Fatalf("state = %q, want %q", got, asset.EnrichStatePending)
	}

	// The migration-004 dual-write: both the TEXT stamp and its typed mirror must
	// be populated from the SAME value. Before this pass NOTHING wrote the mirror.
	var textStamp string
	var mirrorSet, updatedMirrorSet bool
	if err := db.QueryRow(`SELECT enrich_state_updated_at,
		(enrich_state_updated_at_ts IS NOT NULL), (updated_at_ts IS NOT NULL)
		FROM media_assets WHERE id = $1`, id).Scan(&textStamp, &mirrorSet, &updatedMirrorSet); err != nil {
		t.Fatalf("read dual-write state: %v", err)
	}
	if !mirrorSet {
		t.Error("enrich_state_updated_at_ts must be written with its TEXT sibling (migration 004 pair)")
	}
	if !updatedMirrorSet {
		t.Error("updated_at_ts must be written with its TEXT sibling (migration 004 pair)")
	}
	if textStamp == "" {
		t.Error("enrich_state_updated_at must carry the transition stamp")
	}

	// 2. CAS success: PENDING -> ENRICHING.
	if err := store.SetEnrichStateIfCurrent(ctx, id, asset.EnrichStatePending, asset.EnrichStateEnriching); err != nil {
		t.Fatalf("CAS PENDING->ENRICHING: %v", err)
	}
	if got, _ = store.GetEnrichState(ctx, id); got != asset.EnrichStateEnriching {
		t.Fatalf("state = %q, want %q", got, asset.EnrichStateEnriching)
	}

	// 3. CAS loss: the row is no longer PENDING, so a competing sweeper tick must
	//    lose. This is the claim fence the whole sweep depends on.
	if err := store.SetEnrichStateIfCurrent(ctx, id, asset.EnrichStatePending, asset.EnrichStateEnriching); err == nil {
		t.Fatal("expected a CAS loss when the row is not in the expected from-state")
	}
	if got, _ = store.GetEnrichState(ctx, id); got != asset.EnrichStateEnriching {
		t.Errorf("a lost CAS must not mutate the row; state = %q", got)
	}
	// The error text is load-bearing: EnrichStateMachine.Transition classifies a
	// lost CAS by matching this phrase, so a reworded message silently changes the
	// sweeper's behaviour. Pin it.
	err = store.SetEnrichStateIfCurrent(ctx, id, asset.EnrichStatePending, asset.EnrichStateEnriching)
	if err == nil || !strings.Contains(err.Error(), "asset row missing or current state mismatch") {
		t.Errorf("lost-CAS error = %v, want it to contain the phrase the state-machine wrapper matches", err)
	}

	// 4. Unknown asset: both forms must report the missing row.
	if err := store.SetEnrichState(ctx, "yt_enrich_store_absent_v1", asset.EnrichStatePending); err == nil {
		t.Error("expected an error for an absent asset")
	}
	if _, err := store.GetEnrichState(ctx, "yt_enrich_store_absent_v1"); err == nil {
		t.Error("expected an error reading an absent asset")
	}
}

// TestMediaEnrichmentCandidateReaderFenceSemantics pins the selector's window,
// ordering and the empty-stamp equivalence that a literal SQLite->PostgreSQL
// translation would have broken.
func TestMediaEnrichmentCandidateReaderFenceSemantics(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	const (
		oldPending   = "yt_enrich_cand_old_v1"
		freshPending = "yt_enrich_cand_fresh_v1"
		enriched     = "yt_enrich_cand_enriched_v1"
		folderRow    = "yt_enrich_cand_folder_v1"
		noLocalPath  = "yt_enrich_cand_nolocal_v1"
		unstamped    = "yt_enrich_cand_unstamped_v1"
	)
	for _, id := range []string{oldPending, freshPending, enriched, folderRow, noLocalPath, unstamped} {
		seedIndexableAsset(t, db, id)
	}

	// The selector requires local_path <> '', so every fixture that must be
	// VISIBLE is given one here; noLocalPath is blanked afterwards.
	stamp := func(id string, state asset.EnrichState, ts string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `UPDATE media_assets
			SET enrich_state = $1,
			    enrich_state_updated_at = $2::text,
			    enrich_state_updated_at_ts = NULLIF($2, '')::timestamptz,
			    local_path = '/tmp/' || $3
			WHERE id = $3`, string(state), ts, id); err != nil {
			t.Fatalf("stamp %s: %v", id, err)
		}
	}
	ago := func(d time.Duration) string { return time.Now().UTC().Add(-d).Format(time.RFC3339) }

	stamp(oldPending, asset.EnrichStatePending, ago(10*time.Minute))
	stamp(freshPending, asset.EnrichStatePending, ago(5*time.Second))
	stamp(enriched, asset.EnrichStateEnriched, ago(10*time.Minute))
	stamp(unstamped, asset.EnrichStatePending, "") // empty TEXT -> NULL mirror
	// media_type = 'folder' and local_path = '' are the two exclusion predicates.
	stamp(folderRow, asset.EnrichStatePending, ago(10*time.Minute))
	if _, err := db.ExecContext(ctx, `UPDATE media_assets SET media_type = 'folder' WHERE id = $1`, folderRow); err != nil {
		t.Fatalf("shape folder row: %v", err)
	}
	stamp(noLocalPath, asset.EnrichStatePending, ago(10*time.Minute))
	if _, err := db.ExecContext(ctx, `UPDATE media_assets SET local_path = '' WHERE id = $1`, noLocalPath); err != nil {
		t.Fatalf("shape no-local-path row: %v", err)
	}
	// The two exclusion fixtures must NOT be the only survivors, so assert the
	// visible baseline before the filtered assertions below.

	reader := pgmedia.NewMediaEnrichmentCandidateReader(db)
	got, err := reader.PendingEnrichCandidates(ctx, 30*time.Second, 50)
	if err != nil {
		t.Fatalf("PendingEnrichCandidates: %v", err)
	}

	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen[oldPending] {
		t.Errorf("an old PENDING row past the fence must be selected; got %v", got)
	}
	// Empty-stamp equivalence: SQLite compared '' against datetime('now', ?) as
	// OLDER, so the retired selector DID select these rows. A literal translation
	// to `NULL < x` would have silently dropped them.
	if !seen[unstamped] {
		t.Errorf("an unstamped PENDING row must keep comparing as older (SQLite empty-string parity); got %v", got)
	}
	if seen[freshPending] {
		t.Errorf("a PENDING row inside the claim fence must NOT be selected; got %v", got)
	}
	if seen[enriched] {
		t.Errorf("a non-PENDING row must NOT be selected; got %v", got)
	}
	if seen[folderRow] {
		t.Errorf("a media_type='folder' row must NOT be selected; got %v", got)
	}
	if seen[noLocalPath] {
		t.Errorf("a local_path='' row must NOT be selected; got %v", got)
	}
	// Oldest first: the unstamped row sorts as -infinity and the old row next.
	if len(got) >= 2 && got[len(got)-1] != oldPending {
		t.Errorf("expected oldest-first ordering, got %v", got)
	}

	// limit is honoured, and a limit inside the fence yields nothing.
	limited, err := reader.PendingEnrichCandidates(ctx, 30*time.Second, 1)
	if err != nil {
		t.Fatalf("limited PendingEnrichCandidates: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("limit=1 returned %d rows, want 1", len(limited))
	}
	// A fence wider than every real stamp excludes all stamped candidates — but
	// NOT the unstamped one, which compares as -infinity and is therefore outside
	// every fence. That is the SQLite parity stated positively: there, an empty
	// stamp was '' and '' < datetime('now', '-1 hour') is true for any fence
	// width. Asserting this explicitly stops a future 'tidy-up' from dropping the
	// COALESCE and silently losing unstamped rows from the sweep forever.
	wide, err := reader.PendingEnrichCandidates(ctx, time.Hour, 50)
	if err != nil {
		t.Fatalf("wide-fence PendingEnrichCandidates: %v", err)
	}
	if len(wide) != 1 || wide[0] != unstamped {
		t.Errorf("a wide fence must select ONLY the unstamped row (SQLite empty-string parity), got %v", wide)
	}
}

// TestMediaEnrichStateStoreFailsClosedWithoutHandle pins the no-fallback contract
// for every method: an unwired store errors instead of reporting a successful
// transition, which would let the sweeper believe it claimed a row it never
// touched.
func TestMediaEnrichStateStoreFailsClosedWithoutHandle(t *testing.T) {
	requirePostgresDSN(t)
	ctx := context.Background()
	var s *pgmedia.MediaEnrichStateStore
	if err := s.SetEnrichState(ctx, "x", asset.EnrichStatePending); err == nil {
		t.Error("expected a fail-closed error from a nil store (SetEnrichState)")
	}
	if err := s.SetEnrichStateIfCurrent(ctx, "x", asset.EnrichStatePending, asset.EnrichStateEnriching); err == nil {
		t.Error("expected a fail-closed error from a nil store (SetEnrichStateIfCurrent)")
	}
	if _, err := s.GetEnrichState(ctx, "x"); err == nil {
		t.Error("expected a fail-closed error from a nil store (GetEnrichState)")
	}
	var r *pgmedia.MediaEnrichmentCandidateReader
	if _, err := r.PendingEnrichCandidates(ctx, time.Second, 1); err == nil {
		t.Error("expected a fail-closed error from a nil candidate reader")
	}
}
