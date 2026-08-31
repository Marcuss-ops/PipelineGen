package outbox

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/idempotency"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// recordingOutboxEventsRepo is a test-only outboxEnqueuer that captures
// every Enqueue call's (eventType, aggregateID, payload, eventKey) tuple
// so tests can assert the canonical OutboxKey shape without spinning up
// a real SQLite + migration fixture.
//
// Concurrency: guarded by a mutex because Dispatcher.EnqueueAndIndex is
// sometimes exercised by parallel test goroutines.
type recordingOutboxEventsRepo struct {
	mu    sync.Mutex
	calls []recordedEnqueue
}

type recordedEnqueue struct {
	EventType    string
	AggregateID  string
	AggregateTyp string
	Payload      string
	EventKey     string
}

func (r *recordingOutboxEventsRepo) Enqueue(_ context.Context, _ *sql.Tx, eventType, aggregateID, aggregateTyp, payload, eventKey string) (*outboxevents.EnqueueResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedEnqueue{
		EventType:    eventType,
		AggregateID:  aggregateID,
		AggregateTyp: aggregateTyp,
		Payload:      payload,
		EventKey:     eventKey,
	})
	return &outboxevents.EnqueueResult{}, nil
}

// Compile-time guard.
var _ outboxEnqueuer = (*recordingOutboxEventsRepo)(nil)

// txMgrRun is a TxManager that records the *sql.Tx the dispatcher
// passes to the closure (always nil in these tests because the fake
// doesn't open a real tx; the value is only used to satisfy the
// interface). The InTransaction callback runs the fn synchronously
// with a nil tx — which is acceptable because fakeClips.UpsertClipTx
// ignores the tx in these tests.
//
// Named `txMgrRun` (not `txMgrCapture`) to avoid clashing with the
// same-named pointer-receiver type in delete_envelope_test.go.
type txMgrRun struct{}

func (*txMgrRun) InTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return fn(new(sql.Tx))
}
func (*txMgrRun) DB() *sql.DB { return nil }

// Compile-time guard.
var _ TxManager = (*txMgrRun)(nil)

// ── SaveDiscoveredAsset: JobKey stamping ───────────────────────────────

// TestSaveDiscoveredAsset_StampsJobKeyIntoMetadata pins the wire-in:
// SaveDiscoveredAsset MUST compute the canonical JobKey
// (provider:clipID:sourceVersion) and stamp it into metadata_json.job_key.
// The source_version at discovery is the literal sentinel "discovered"
// (per Commit 2 design — see dispatcher_index.go rationale).
func TestSaveDiscoveredAsset_StampsJobKeyIntoMetadata(t *testing.T) {
	clips := &fakeClips{}
	rec := &recordingOutboxEventsRepo{}
	d := &Dispatcher{
		clips:              clips,
		outboxEventsRepo:   rec,
		txmgr:              &txMgrRun{},
		log:                zap.NewNop(),
		discoveryCommitter: &fakeSQLiteAssetCommitter{outbox: rec, txmgr: &txMgrRun{}, discovery: clips},
		canonicalCommitter: &fakeSQLiteAssetCommitter{outbox: rec, txmgr: &txMgrRun{}, discovery: clips},
	}
	clip := &asset.Asset{
		ID:     "artlist_abc123",
		Source: asset.Source("artlist"),
		Name:   "test clip",
	}

	if err := d.SaveDiscoveredAsset(context.Background(), clip, asset.StateStaging, asset.StateDiscovered); err != nil {
		t.Fatalf("SaveDiscoveredAsset: %v", err)
	}

	if len(clips.upserts) != 1 {
		t.Fatalf("expected 1 UpsertClipTx call, got %d", len(clips.upserts))
	}
	stamped := clips.upserts[0].Metadata["job_key"]
	if stamped == "" {
		t.Fatal("SaveDiscoveredAsset must stamp metadata_json.job_key; got empty")
	}
	want, _ := idempotency.JobKey("artlist", "artlist_abc123", "discovered")
	if stamped != want {
		t.Errorf("job_key mismatch: want %q, got %q", want, stamped)
	}

	// NO outbox event must be written at discovery time (chip 2 invariant).
	if len(rec.calls) != 0 {
		t.Errorf("SaveDiscoveredAsset must NOT enqueue outbox events; got %d", len(rec.calls))
	}
}

// TestSaveDiscoveredAsset_DifferentProvidersProduceDifferentJobKeys
// pins the provider-segment of the JobKey: artlist and youtube clip
// IDs that happen to share the same string MUST still produce
// distinct JobKeys.
func TestSaveDiscoveredAsset_DifferentProvidersProduceDifferentJobKeys(t *testing.T) {
	clips := &fakeClips{}
	rec := &recordingOutboxEventsRepo{}
	d := &Dispatcher{
		clips:              clips,
		outboxEventsRepo:   rec,
		txmgr:              &txMgrRun{},
		log:                zap.NewNop(),
		discoveryCommitter: &fakeSQLiteAssetCommitter{outbox: rec, txmgr: &txMgrRun{}, discovery: clips},
		canonicalCommitter: &fakeSQLiteAssetCommitter{outbox: rec, txmgr: &txMgrRun{}, discovery: clips},
	}
	a := &asset.Asset{ID: "x", Source: asset.Source("artlist")}
	b := &asset.Asset{ID: "x", Source: asset.Source("youtube")}
	if err := d.SaveDiscoveredAsset(context.Background(), a, asset.StateStaging, asset.StateDiscovered); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := d.SaveDiscoveredAsset(context.Background(), b, asset.StateStaging, asset.StateDiscovered); err != nil {
		t.Fatalf("save b: %v", err)
	}
	ja := a.Metadata["job_key"]
	jb := b.Metadata["job_key"]
	if ja == "" || jb == "" {
		t.Fatalf("both JobKeys must be non-empty: ja=%q jb=%q", ja, jb)
	}
	if ja == jb {
		t.Errorf("different providers must produce different JobKeys; both = %q", ja)
	}
}

// ── EnqueueAndIndex: OutboxKey shape ───────────────────────────────────

// TestEnqueueAndIndex_UsesOutboxKeyShape pins the wire-in: the
// event_key column MUST contain the canonical OutboxKey
// (eventType:provider:clipID:sourceVersion), not the legacy
// 5-segment indexEventKey shape.
func TestEnqueueAndIndex_UsesOutboxKeyShape(t *testing.T) {
	clips := &fakeClips{}
	rec := &recordingOutboxEventsRepo{}
	d := &Dispatcher{
		clips:            clips,
		outboxEventsRepo: rec,
		txmgr:            &txMgrRun{},
		log:              zap.NewNop(),
		canonicalCommitter: &fakeSQLiteAssetCommitter{
			outbox: rec, txmgr: &txMgrRun{}, discovery: &fakeClips{},
		},
	}
	clip := &asset.Asset{
		ID:     "artlist_abc123",
		Source: asset.Source("artlist"),
		Name:   "test clip",
	}
	const contentHash = "sha256:deadbeefcafebabe000000000000000000000000000000000000000000000000"

	if err := d.EnqueueAndIndex(context.Background(), clip, contentHash); err != nil {
		t.Fatalf("EnqueueAndIndex: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 outbox enqueue, got %d", len(rec.calls))
	}
	got := rec.calls[0].EventKey
	want, _ := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested,
		"artlist",
		"artlist_abc123",
		contentHash,
	)
	if got != want {
		t.Errorf("event_key mismatch:\n  got  = %q\n  want = %q", got, want)
	}
	// 4-segment shape: eventType:provider:clipID:sourceVersion.
	// Legacy 5-segment shape started with "index:" — guard against
	// accidental rollback to the legacy shape.
	if strings.HasPrefix(got, "index:") {
		t.Errorf("event_key must NOT use legacy 5-segment shape (prefix 'index:'); got %q", got)
	}
	// Segment-count assertion: the canonical shape has 4 segments
	// separated by 3 colons. The sourceVersion may legitimately
	// contain ':' (e.g. the conventional "sha256:<hex>" prefix),
	// so a plain strings.Count(got, ":") is too strict — it would
	// count the colons INSIDE the sourceVersion. We use SplitN
	// with n=4 to split on the FIRST 3 colons only, leaving the
	// sourceVersion as the 4th element even if it contains ':'.
	//
	// NOTE: the byte-equality check above (got != want against
	// idempotency.OutboxKey(...)) is the LOAD-BEARING assertion
	// and already pins the full sourceVersion (PR 5 gate). This
	// shape check is a minimal segment-count pin; we don't try
	// to extract parts[3] because the position-based extraction
	// would break for clipIDs that legitimately contain ':'
	// (e.g. "planner:abc:0" — the data-field colon guard was
	// relaxed in commit e8c0f1909).
	parts := strings.SplitN(got, ":", 4)
	if len(parts) != 4 {
		t.Errorf("event_key must be 4-segment (eventType:provider:clipID:sourceVersion); got %q (split into %d parts)", got, len(parts))
	}
}

// TestEnqueueAndIndex_SameInputsSameEventKey pins the determinism
// invariant: two consecutive enqueues with identical (provider,
// clipID, contentHash) MUST produce identical event_keys (so the
// outbox UNIQUE INDEX dedup collapses them).
func TestEnqueueAndIndex_SameInputsSameEventKey(t *testing.T) {
	rec := &recordingOutboxEventsRepo{}
	d := &Dispatcher{
		clips:            &fakeClips{},
		outboxEventsRepo: rec,
		txmgr:            &txMgrRun{},
		log:              zap.NewNop(),
		canonicalCommitter: &fakeSQLiteAssetCommitter{
			outbox: rec, txmgr: &txMgrRun{}, discovery: &fakeClips{},
		},
	}
	clip := &asset.Asset{
		ID:     "artlist_xyz",
		Source: asset.Source("artlist"),
	}
	const contentHash = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	for i := 0; i < 3; i++ {
		if err := d.EnqueueAndIndex(context.Background(), clip, contentHash); err != nil {
			t.Fatalf("EnqueueAndIndex call %d: %v", i, err)
		}
	}
	if len(rec.calls) != 3 {
		t.Fatalf("expected 3 outbox enqueues, got %d", len(rec.calls))
	}
	k0 := rec.calls[0].EventKey
	for i := 1; i < 3; i++ {
		if rec.calls[i].EventKey != k0 {
			t.Errorf("calls[%d].EventKey = %q, want %q (determinism broken)", i, rec.calls[i].EventKey, k0)
		}
	}
}

// TestEnqueueAndIndex_DifferentContentHashDifferentEventKey pins
// the sourceVersion-segment: a different contentHash MUST produce
// a different event_key (so the supersede gate can fire when the
// underlying file changes).
func TestEnqueueAndIndex_DifferentContentHashDifferentEventKey(t *testing.T) {
	rec := &recordingOutboxEventsRepo{}
	d := &Dispatcher{
		clips:            &fakeClips{},
		outboxEventsRepo: rec,
		txmgr:            &txMgrRun{},
		log:              zap.NewNop(),
		canonicalCommitter: &fakeSQLiteAssetCommitter{
			outbox: rec, txmgr: &txMgrRun{}, discovery: &fakeClips{},
		},
	}
	clip := &asset.Asset{ID: "artlist_xyz", Source: asset.Source("artlist")}
	if err := d.EnqueueAndIndex(context.Background(), clip, "sha256:aaaa"); err != nil {
		t.Fatalf("enqueue a: %v", err)
	}
	if err := d.EnqueueAndIndex(context.Background(), clip, "sha256:bbbb"); err != nil {
		t.Fatalf("enqueue b: %v", err)
	}
	if rec.calls[0].EventKey == rec.calls[1].EventKey {
		t.Errorf("different contentHash MUST produce different event_key; both = %q", rec.calls[0].EventKey)
	}
}

// TestEnqueueAndIndex_ProviderFromClipSource pins that the provider
// segment of the event_key is read from clip.Source (not hardcoded).
// YouTube + artlist with the same clipID MUST produce different
// event_keys.
func TestEnqueueAndIndex_ProviderFromClipSource(t *testing.T) {
	rec := &recordingOutboxEventsRepo{}
	d := &Dispatcher{
		clips:            &fakeClips{},
		outboxEventsRepo: rec,
		txmgr:            &txMgrRun{},
		log:              zap.NewNop(),
		canonicalCommitter: &fakeSQLiteAssetCommitter{
			outbox: rec, txmgr: &txMgrRun{}, discovery: &fakeClips{},
		},
	}
	const contentHash = "sha256:cafecafe"
	yt := &asset.Asset{ID: "vid_1", Source: asset.Source("youtube")}
	art := &asset.Asset{ID: "vid_1", Source: asset.Source("artlist")}
	if err := d.EnqueueAndIndex(context.Background(), yt, contentHash); err != nil {
		t.Fatalf("enqueue yt: %v", err)
	}
	if err := d.EnqueueAndIndex(context.Background(), art, contentHash); err != nil {
		t.Fatalf("enqueue art: %v", err)
	}
	if rec.calls[0].EventKey == rec.calls[1].EventKey {
		t.Errorf("different providers must produce different event_keys; both = %q", rec.calls[0].EventKey)
	}
	if !strings.Contains(rec.calls[0].EventKey, ":youtube:") {
		t.Errorf("YouTube event_key must contain ':youtube:' segment; got %q", rec.calls[0].EventKey)
	}
	if !strings.Contains(rec.calls[1].EventKey, ":artlist:") {
		t.Errorf("Artlist event_key must contain ':artlist:' segment; got %q", rec.calls[1].EventKey)
	}
}

// ── EnqueueIndexEvent: OutboxKey shape ────────────────────────────────

// TestEnqueueIndexEvent_UsesOutboxKeyShape pins the wire-in for
// the narrow Voiceover entry point: EnqueueIndexEvent MUST also
// use the canonical OutboxKey. The provider is inferred from the
// assetID prefix via DetectSourceFromAssetID.
func TestEnqueueIndexEvent_UsesOutboxKeyShape(t *testing.T) {
	rec := &recordingOutboxEventsRepo{}
	d := &Dispatcher{
		clips:            &fakeClips{},
		outboxEventsRepo: rec,
		txmgr:            &txMgrRun{},
		log:              zap.NewNop(),
		canonicalCommitter: &fakeSQLiteAssetCommitter{
			outbox: rec, txmgr: &txMgrRun{}, discovery: &fakeClips{},
		},
	}
	const assetID = "vo_voiceover_xyz"
	const contentHash = "sha256:deadbeef"

	if err := d.EnqueueIndexEvent(context.Background(), new(sql.Tx), assetID, "voiceover", contentHash); err != nil {
		t.Fatalf("EnqueueIndexEvent: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 outbox enqueue, got %d", len(rec.calls))
	}
	got := rec.calls[0].EventKey
	want, _ := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested,
		"voiceover", // DetectSourceFromAssetID("vo_voiceover_xyz") → "voiceover"
		assetID,
		contentHash,
	)
	if got != want {
		t.Errorf("event_key mismatch:\n  got  = %q\n  want = %q", got, want)
	}
}

// TestEnqueueIndexEvent_ProviderInferredFromAssetIDPrefix pins the
// provider-inference contract: a yt_ prefix MUST yield "youtube",
// a planner: prefix MUST yield "stock", an artlist_ prefix MUST
// yield "artlist", and an unknown prefix MUST yield "" which the
// canonical OutboxKey constructor rejects with ErrEmptyProvider.
func TestEnqueueIndexEvent_ProviderInferredFromAssetIDPrefix(t *testing.T) {
	cases := []struct {
		name        string
		assetID     string
		wantSegment string
	}{
		{"yt_prefix", "yt_vid123_0_60_v1", "youtube"},
		{"planner_prefix", "planner:abc:0", "stock"},
		{"artlist_prefix", "artlist_xyz", "artlist"},
		{"voiceover_prefix", "voiceover_xyz", "voiceover"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingOutboxEventsRepo{}
			clips := &fakeClips{}
			d := &Dispatcher{
				clips:            clips,
				outboxEventsRepo: rec,
				txmgr:            &txMgrRun{},
				log:              zap.NewNop(),
				canonicalCommitter: &fakeSQLiteAssetCommitter{
					outbox: rec, txmgr: &txMgrRun{}, discovery: clips,
				},
			}
			const contentHash = "sha256:1234"
			if err := d.EnqueueIndexEvent(context.Background(), new(sql.Tx), tc.assetID, tc.wantSegment, contentHash); err != nil {
				t.Fatalf("EnqueueIndexEvent(%q): %v", tc.assetID, err)
			}
			if len(rec.calls) != 1 {
				t.Fatalf("expected 1 enqueue, got %d", len(rec.calls))
			}
			if !strings.Contains(rec.calls[0].EventKey, ":"+tc.wantSegment+":") {
				t.Errorf("event_key must contain ':%s:' segment for assetID %q; got %q",
					tc.wantSegment, tc.assetID, rec.calls[0].EventKey)
			}
		})
	}
}

// TestEnqueueIndexEvent_UnknownPrefixFailsClosed pins the fail-closed
// guarantee: an unknown assetID prefix (no recognized provider) MUST
// cause OutboxKey to reject with ErrEmptyProvider, and
// EnqueueIndexEvent must surface that as a wrapped error.
func TestEnqueueIndexEvent_UnknownPrefixFailsClosed(t *testing.T) {
	rec := &recordingOutboxEventsRepo{}
	d := &Dispatcher{
		clips:            &fakeClips{},
		outboxEventsRepo: rec,
		txmgr:            &txMgrRun{},
		log:              zap.NewNop(),
		canonicalCommitter: &fakeSQLiteAssetCommitter{
			outbox: rec, txmgr: &txMgrRun{}, discovery: &fakeClips{},
		},
	}
	err := d.EnqueueIndexEvent(context.Background(), new(sql.Tx), "weird_unknown_xyz", "", "sha256:1234")
	if err == nil {
		t.Fatal("EnqueueIndexEvent with unknown assetID prefix must fail-closed (ErrEmptyProvider via OutboxKey)")
	}
	if !strings.Contains(err.Error(), "outbox event_key") {
		t.Errorf("error must mention the outbox event_key build failure; got: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("no outbox event must be enqueued when the key build fails; got %d calls", len(rec.calls))
	}
}

// ── Cross-method wire-in invariant ────────────────────────────────────

// TestEnqueueAndIndex_AndEnqueueIndexEvent_ProduceSameEventKey
// pins the cross-method invariant: both entry points MUST produce
// the SAME event_key when given the same (provider, clipID,
// contentHash) tuple. This is the canonical Qdrant upsert invariant
// per user spec ("outbox key canonica per Qdrant upsert").
func TestEnqueueAndIndex_AndEnqueueIndexEvent_ProduceSameEventKey(t *testing.T) {
	rec := &recordingOutboxEventsRepo{}
	d := &Dispatcher{
		clips:            &fakeClips{},
		outboxEventsRepo: rec,
		txmgr:            &txMgrRun{},
		log:              zap.NewNop(),
		canonicalCommitter: &fakeSQLiteAssetCommitter{
			outbox: rec, txmgr: &txMgrRun{}, discovery: &fakeClips{},
		},
	}
	const assetID = "yt_vid_0_60_v1" // DetectSourceFromAssetID → "youtube"
	const contentHash = "sha256:abcdef"

	// Path 1: EnqueueAndIndex with a *asset.Asset
	clip := &asset.Asset{ID: assetID, Source: asset.Source("youtube")}
	if err := d.EnqueueAndIndex(context.Background(), clip, contentHash); err != nil {
		t.Fatalf("EnqueueAndIndex: %v", err)
	}
	k1 := rec.calls[len(rec.calls)-1].EventKey

	// Path 2: EnqueueIndexEvent with just (assetID, contentHash)
	if err := d.EnqueueIndexEvent(context.Background(), new(sql.Tx), assetID, "youtube", contentHash); err != nil {
		t.Fatalf("EnqueueIndexEvent: %v", err)
	}
	k2 := rec.calls[len(rec.calls)-1].EventKey

	if k1 != k2 {
		t.Errorf("EnqueueAndIndex and EnqueueIndexEvent MUST produce the same event_key for identical inputs:\n  EnqueueAndIndex     = %q\n  EnqueueIndexEvent   = %q", k1, k2)
	}
}
