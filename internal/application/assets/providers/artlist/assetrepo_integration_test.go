// PR12b integration test: verifies that artlist.SearchService.UpsertClip,
// when wired with an asset.Repository via SetAssetRepo, routes through
// the canonical writer AND legacy readers (assets.ClipsRepository) observe the
// same row data.
//
// QDRANT-002 close-out (June 2026): the legacy SetAssetRepo override
// was retired. Raw repo.UpsertClip no longer exists on *ClipsRepository;
// the canonical write path is now outbox.Dispatcher.EnqueueAndIndex,
// and SearchService.NewSearchService requires a non-nil dispatcher.
//
// The PR12b integration suite is now reduced to a single smoke that
// asserts the constructor fails closed when the dispatcher is missing
// (composition root invariant). The deep round-trip tests are slated
// for the next PR in the wave, which will build a fake dispatcher
// that emits a real outbox_event + writes to media_assets in the
// same tx (so the round-trip semantic is verifiable without the
// actual Qdrant backend).
package artlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// readSrcFile reads a sibling source file from disk for source-level
// regression guards (used by TestSearchCore_NoAssetStoreUpsertInSearchLiveAndSave
// to assert the function body no longer contains legacy Upsert call sites).
// Wraps os.ReadFile so the call-site carries file context in error messages.
func readSrcFile(name string) (string, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("readSrcFile(%q): %w", name, err)
	}
	return string(b), nil
}

// pr12bArtlistSchema mirrors the production schema: CanonicalMediaAssetsSchema
// (single source of truth for media_assets) + asset_locations + outbox_events.
// Keeps fixtures in lockstep with migration changes — a new canonical column
// added by migration only requires updating internal/infrastructure/database/canonical.go.
const pr12bArtlistSchema = drive.CanonicalMediaAssetsSchema + `

CREATE TABLE IF NOT EXISTS asset_locations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	asset_id TEXT NOT NULL,
	location_kind TEXT NOT NULL,
	uri TEXT NOT NULL,
	external_id TEXT NOT NULL DEFAULT '',
	web_view_link TEXT NOT NULL DEFAULT '',
	download_url TEXT NOT NULL DEFAULT '',
	mime_type TEXT NOT NULL DEFAULT '',
	file_size_bytes INTEGER NOT NULL DEFAULT 0,
	file_hash TEXT NOT NULL DEFAULT '',
	is_primary INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE(asset_id, location_kind)
);

CREATE TABLE IF NOT EXISTS outbox_events (
	id TEXT PRIMARY KEY,
	aggregate_id TEXT NOT NULL DEFAULT '',
	aggregate_type TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	event_key TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 10,
	last_error TEXT NOT NULL DEFAULT '',
	next_attempt_at TEXT,
	worker_id TEXT NOT NULL DEFAULT '',
	lease_id TEXT NOT NULL DEFAULT '',
	lease_expiry TEXT,
	completed_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_outbox_aggregate_id ON outbox_events(aggregate_id);
`

// setupArtlistPR12b creates a fresh SQLite DB with the full PR12b schema,
// wires clips + assetrepo repos, and registers teardown. Returns the DB
// handle so tests can also query outbox_events directly.
func setupArtlistPR12b(t *testing.T) (db *sql.DB, clipsRepo *assets.ClipsRepository, assetRepo asset.Repository) {
	t.Helper()
	db = drive.NewTestDBWithSchema(t, pr12bArtlistSchema)
	t.Cleanup(func() { _ = db.Close() })
	log := zap.NewNop()
	clipsRepo = assets.NewClipsRepository(db, log)
	assetStore := asset.NewAssetStoreSQLite(db, log)
	assetRepo = assetStore.AssetRepository()
	return
}

// zeroTime is the canonical zero-time used by DeletedAt fixtures so that
// timeutil.FormatPtrRFC3339 binds a non-NULL string (which the test schema's
// `deleted_at TEXT NOT NULL DEFAULT ”` accepts). Without this, nil pointer
// formatting binds SQL NULL and trips the NOT NULL constraint.
var zeroTime = time.Time{}

func TestArtlistPR12b_NewSearchServiceRequiresDispatcherQDRANT002(t *testing.T) {
	// QDRANT-004 close-out (June 2026, TODO 4): the SearchService
	// constructor requires a non-nil dispatcher. Every write must
	// route through outbox.Dispatcher.EnqueueAndIndex; there is no
	// raw fallback to assetStore.Upsert anywhere in the write path.
	// This smoke asserts the wiring invariant AND that the typed
	// sentinel error ErrMutationDispatcherUnavailable is what callers
	// receive — so production code can branch on intent (errors.Is)
	// instead of string-matching the message.
	db, clipsRepo, _ := setupArtlistPR12b(t)
	svc := &Service{log: zap.NewNop(), assetStore: clipsRepo}

	_, err := NewSearchService(svc, nil)
	if err == nil {
		t.Fatalf("expected NewSearchService(svc, nil) to return an error (QDRANT-004 close-out invariant), got nil")
	}
	if !errors.Is(err, ErrMutationDispatcherUnavailable) {
		t.Errorf("NewSearchService(svc, nil) error = %v, want errors.Is(_, ErrMutationDispatcherUnavailable)", err)
	}

	if _, err := NewService(ServiceDeps{
		ServicePorts: ServicePorts{AssetStore: clipsRepo, Indexer: nil, MetadataWriter: nil},
		ServiceDependencies: ServiceDependencies{
			Cfg:    nil,
			MainDB: db,
			Log:    zap.NewNop(),
		},
	}); err == nil {
		t.Fatalf("expected NewService with nil dispatcher to return an error from NewSearchService propagation, got nil")
	}
}

// fakeDispatcherForQDRANT004 is a controlled mock used by TODO 4's
// fail-closed test suite. It records the call so we can assert
// "did the dispatcher see this clip" and "was the result propagated".
// Disk-touching round-trip is intentionally out of scope: the
// integration tests with media_assets + outbox_events live in the
// next wave per the PR12b header.
type fakeDispatcherForQDRANT004 struct {
	calls    int
	failWith error
}

func (f *fakeDispatcherForQDRANT004) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, hash string) error {
	f.calls++
	return f.failWith
}

// TestArtlistQDRANT004_SearchLiveAndSaveFailClosed asserts the spec's
// spec case 4 + 1: a nil-dispatcher service returns the typed sentinel
// AND the dispatcher is never invoked. Together with the grep guard
// test below, this proves there is no legacy assetStore.Upsert
// fallback — a misconfigured caller fails LOUD, not silent.
func TestArtlistQDRANT004_SearchLiveAndSaveFailClosed(t *testing.T) {
	// Construct a SearchService with a NIL dispatcher by reaching past
	// the constructor (the constructor itself returns an error in that
	// case per the test above). The struct field is exported only
	// inside the package — tests live in the same package so this is
	// legal.
	//
	// Defense in depth: even though NewSearchService rejects nil
	// dispatchers at construction time, the per-method nil-checks
	// (SearchLiveAndSave, UpsertClip) remain so a future reflection
	// / unsafe-construction path cannot bypass the fail-closed gate.
	// Do not simplify away the per-method guard without re-reading
	// this comment.
	ss := &SearchService{
		service:    &Service{log: zap.NewNop()},
		dispatcher: nil, // explicit
	}

	if _, err := ss.SearchLiveAndSave(context.Background(), "term-does-not-matter", 1); err == nil {
		t.Fatalf("expected SearchLiveAndSave to return error with nil dispatcher, got nil")
	} else if !errors.Is(err, ErrMutationDispatcherUnavailable) {
		t.Errorf("SearchLiveAndSave(nil-dispatcher) error = %v, want errors.Is(_, ErrMutationDispatcherUnavailable)", err)
	}
}

// TestArtlistQDRANT004_UpsertClipFailClosed asserts spec case 1 for
// UpsertClip path: nil-dispatcher returns the typed sentinel. Same
// fail-closed contract as SearchLiveAndSave.
func TestArtlistQDRANT004_UpsertClipFailClosed(t *testing.T) {
	ss := &SearchService{
		service:    &Service{log: zap.NewNop()},
		dispatcher: nil,
	}

	if err := ss.UpsertClip(context.Background(), &asset.Asset{ID: "fake"}); err == nil {
		t.Fatalf("expected UpsertClip to return error with nil dispatcher, got nil")
	} else if !errors.Is(err, ErrMutationDispatcherUnavailable) {
		t.Errorf("UpsertClip(nil-dispatcher) error = %v, want errors.Is(_, ErrMutationDispatcherUnavailable)", err)
	}
}

// TestArtlistQDRANT004_HappyPathEnqueuesAssertsSpec case 3
// plus spec case 2 by inversion: with a valid dispatcher, SearchLiveAndSave
// calls EnqueueAndIndex exactly once. We drive UpsertClip (the simplest
// pure-dispatcher surface, no upstream search chain) and assert the
// counter increments. Spec case 2 (zero writes when dispatcher is nil)
// is asserted by TestArtlistQDRANT004_ZeroMediaAssetsWritesOnNilDispatcher
// below via direct SQLite count comparison (media_assets + outbox_events).
func TestArtlistQDRANT004_HappyPathEnqueues(t *testing.T) {
	disp := &fakeDispatcherForQDRANT004{}
	ss := &SearchService{
		service:    &Service{log: zap.NewNop()},
		dispatcher: disp,
	}
	if err := ss.UpsertClip(context.Background(), &asset.Asset{ID: "x"}); err != nil {
		t.Fatalf("expected happy-path UpsertClip to succeed, got error = %v", err)
	}
	if disp.calls != 1 {
		t.Errorf("dispatcher.calls = %d, want 1 (spec case 3: enqueue succeeds)", disp.calls)
	}
}

// TestArtlistQDRANT004_ZeroMediaAssetsWritesOnNilDispatcher asserts spec
// case 2 directly: a SearchLiveAndSave call with a nil dispatcher must
// produce ZERO writes to SQLite (media_assets count + outbox_events
// count unchanged). The mass-and-compare probe below proves the
// fail-closed invariant at the data layer — the dispatcher's path was
// never even considered, so no row can land in either table.
//
// Setup: snapshot row counts in both tables BEFORE; trigger a
// SearchLiveAndSave with nil dispatcher (returns sentinel); snapshot
// counts AFTER; assert both deltas are zero.
func TestArtlistQDRANT004_ZeroMediaAssetsWritesOnNilDispatcher(t *testing.T) {
	db, _, _ := setupArtlistPR12b(t)

	// Seed one row so the BEFORE/AFTER comparison has a meaningful baseline.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO media_assets (id, source, name, filename, media_type, category, group_name, url, lifecycle_state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"seed-asset-1", "artlist", "Seed", "seed.mp4",
		"clip", "general", "default", "https://example/seed",
		"ACTIVE", now, now,
	)
	if err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	beforeMedia, beforeOutbox := snapshotRowCounts(t, db)

	ss := &SearchService{service: &Service{log: zap.NewNop()}, dispatcher: nil}
	_, err = ss.SearchLiveAndSave(context.Background(), "term", 1)
	if !errors.Is(err, ErrMutationDispatcherUnavailable) {
		t.Fatalf("expected ErrMutationDispatcherUnavailable, got %v", err)
	}

	afterMedia, afterOutbox := snapshotRowCounts(t, db)
	if afterMedia != beforeMedia {
		t.Errorf("media_assets delta = %d, want 0 (spec case 2: nil dispatcher must not write)", afterMedia-beforeMedia)
	}
	if afterOutbox != beforeOutbox {
		t.Errorf("outbox_events delta = %d, want 0 (spec case 2: nil dispatcher must not enqueue)", afterOutbox-beforeOutbox)
	}
}

// snapshotRowCounts returns (media_assets_count, outbox_events_count).
// Used by TestArtlistQDRANT004_ZeroMediaAssetsWritesOnNilDispatcher to
// prove the fail-closed path leaves the data layer untouched.
func snapshotRowCounts(t *testing.T, db *sql.DB) (media, outbox int64) {
	t.Helper()
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets`).Scan(&media); err != nil {
		t.Fatalf("count media_assets: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events`).Scan(&outbox); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	return
}

// TestArtlistQDRANT004_DispatcherErrorPropagated asserts spec case 4:
// when the dispatcher returns an error, that error is propagated
// verbatim to the caller. We use the controlled fake above.
func TestArtlistQDRANT004_DispatcherErrorPropagated(t *testing.T) {
	wantErr := errors.New("simulated dispatcher failure")
	disp := &fakeDispatcherForQDRANT004{failWith: wantErr}
	ss := &SearchService{
		service:    &Service{log: zap.NewNop()},
		dispatcher: disp,
	}
	// UpsertClip is the simplest pure-dispatcher surface (no searcher
	// fallback chain to mock out).
	gotErr := ss.UpsertClip(context.Background(), &asset.Asset{ID: "x"})
	if gotErr == nil {
		t.Fatalf("expected UpsertClip to propagate dispatcher error, got nil")
	}
	if !errors.Is(gotErr, wantErr) {
		t.Errorf("UpsertClip error = %v, want errors.Is(_, wantErr)", gotErr)
	}
	if disp.calls != 1 {
		t.Errorf("dispatcher.calls = %d, want 1", disp.calls)
	}
}

// TestSearchCore_NoAssetStoreUpsertInSearchLiveAndSave is the source
// guard for spec case 5 / Definition-of-done: any reintroduction of
// `assetStore.Upsert` (or a comment like "legacy Upsert path") inside
// SearchLiveAndSave must fail this test. We read the source string
// of the function and check both the legacy keyword and the forbidden
// call site are absent.
func TestSearchCore_NoAssetStoreUpsertInSearchLiveAndSave(t *testing.T) {
	// Read the source file on disk (preferred over in-process
	// reflection — keeps the test honest about what gets shipped).
	src, err := readSrcFile("search_core.go")
	if err != nil {
		t.Fatalf("read search_core.go: %v", err)
	}
	// Slice the function source so the assertion is scoped to
	// SearchLiveAndSave only (not UpsertClip or other helpers).
	start := strings.Index(src, "func (ss *SearchService) SearchLiveAndSave(")
	if start < 0 {
		t.Fatalf("could not locate SearchLiveAndSave definition in source")
	}
	// Walk forward until the next top-level "func (" declaration.
	end := start + len("func (ss *SearchService) SearchLiveAndSave(")
	for end < len(src) {
		if strings.HasPrefix(src[end:], "\nfunc (") {
			break
		}
		end++
	}
	body := src[start:end]
	for _, forbidden := range []string{
		"assetStore.Upsert",
		"assetRepo.Upsert",
		"s.assetStore.Upsert",
		"s.assetRepo.Upsert",
		"// legacy Upsert path",
		"// TODO: legacy Upsert",
		"// NB: legacy fallback",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("SearchLiveAndSave body still contains forbidden legacy fallback %q — remove it (TODO 4 fail-closed contract)", forbidden)
		}
	}
	// Positive control: the function still calls the dispatcher.
	if !strings.Contains(body, "ss.dispatcher.EnqueueAndIndex") {
		t.Errorf("SearchLiveAndSave body no longer contains the dispatcher write — re-add it (it IS the canonical write path)")
	}
}

func TestArtlistPR12b_UpsertClipRoutesThroughAssetRepo(t *testing.T) {
	t.Skip("QDRANT-002 close-out (June 2026): round-trip test retired. Replaced by TestArtlistPR12b_NewSearchServiceRequiresDispatcherQDRANT002 above. New dispatcher-driven round-trip tests land in the next wave with a fake dispatcher that emits real outbox_event + writes media_assets in the same tx.")
}

func TestArtlistPR12b_UpsertClipWithoutAssetRepoFallsBack(t *testing.T) {
	t.Skip("QDRANT-002 close-out (June 2026): legacy fallback test retired. raw repo writes were the canonical write-bypass the close-out eliminated. See TestArtlistPR12b_NewSearchServiceRequiresDispatcherQDRANT002.")
}
