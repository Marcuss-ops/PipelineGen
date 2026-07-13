// Package acceptance_test — helpers_test.go: shared stubs for the
// canonical acceptance test battery.
//
// Purpose: godlike/06 isolates the in-memory representations of
// production ports here so each acceptance_*_test.go file can
// express its category's behaviour against the SAME fixtures.
//
// Sibling files (one per category, per the user spec):
//   - acceptance_idempotency_test.go  (category a)
//   - acceptance_multilingual_test.go (category b)
//   - acceptance_invalidation_test.go (category c)
//   - acceptance_recovery_test.go     (category d)
//   - acceptance_search_test.go       (category e)
//   - acceptance_specscene_test.go    (category f)
package acceptance_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ── in-memory asset.TextTrackRepository stub ─────────────────────────

type inMemRepo struct {
	mu     sync.Mutex
	tracks map[string]*asset.TextTrack // key = (assetID|language|kind)
}

func newInMemRepo() *inMemRepo {
	return &inMemRepo{tracks: map[string]*asset.TextTrack{}}
}

func (r *inMemRepo) key(t asset.TextTrack) string {
	return fmt.Sprintf("%s|%s|%s", t.AssetID, t.LanguageCode, t.TextKind)
}

func (r *inMemRepo) UpsertBatch(_ context.Context, ts []asset.TextTrack) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range ts {
		tc := t
		r.tracks[r.key(tc)] = &tc
	}
	return nil
}

func (r *inMemRepo) FindReady(_ context.Context, assetID, lang string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tracks[fmt.Sprintf("%s|%s|%s", assetID, lang, kind)]
	if !ok || t.Status != asset.TextTrackReady {
		return nil, nil, nil
	}
	dup := *t
	return &dup, nil, nil
}

func (r *inMemRepo) Find(_ context.Context, assetID, lang string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tracks[fmt.Sprintf("%s|%s|%s", assetID, lang, kind)]
	if !ok {
		return nil, nil
	}
	dup := *t
	return &dup, nil
}

func (r *inMemRepo) ListByAsset(_ context.Context, assetID string) ([]asset.TextTrack, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]asset.TextTrack, 0)
	for _, t := range r.tracks {
		if t.AssetID == assetID {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (r *inMemRepo) ListReadyLanguages(_ context.Context, assetID string, kind asset.TextTrackKind) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]struct{}{}
	for _, t := range r.tracks {
		if t.AssetID == assetID && t.TextKind == kind && t.Status == asset.TextTrackReady {
			seen[t.LanguageCode] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	return out, nil
}

func (r *inMemRepo) FindCurrentForTranslation(_ context.Context, assetID string, kind asset.TextTrackKind, lang, srcHash, model, modelVer, promptVer string) (*asset.TextTrack, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	wantKey := asset.TranslationKey(srcHash, lang, model, modelVer, promptVer)
	for _, t := range r.tracks {
		if t.AssetID != assetID || t.LanguageCode != lang || t.TextKind != kind {
			continue
		}
		if t.TranslationKey == wantKey && t.IsCurrent && t.Status == asset.TextTrackReady {
			dup := *t
			return &dup, nil
		}
	}
	return nil, nil
}

// InsertTranslationWithAuditPredecessor atomically (in-memory)
// flips the prior is_current=1 row for (asset, lang, kind) to
// is_current=0 and inserts the new row. Idempotent on the same
// translation_key fingerprint.
func (r *inMemRepo) InsertTranslationWithAuditPredecessor(_ context.Context, t asset.TextTrack) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Idempotency check first.
	for _, existing := range r.tracks {
		if existing.AssetID == t.AssetID && existing.LanguageCode == t.LanguageCode &&
			existing.TextKind == t.TextKind &&
			existing.TranslationKey == t.TranslationKey && existing.IsCurrent {
			return nil
		}
	}
	// Flip predecessors.
	for k, existing := range r.tracks {
		if existing.AssetID == t.AssetID && existing.LanguageCode == t.LanguageCode &&
			existing.TextKind == t.TextKind && existing.IsCurrent {
			existing.IsCurrent = false
			r.tracks[k] = existing
		}
	}
	// Insert with hard is_current=1.
	t.IsCurrent = true
	if t.Status == "" {
		t.Status = asset.TextTrackReady
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	t.UpdatedAt = time.Now().UTC()
	dup := t
	r.tracks[r.key(t)] = &dup
	return nil
}

var _ asset.TextTrackRepository = (*inMemRepo)(nil)

// ── mock translation.TranslationPort ──────────────────────────────────

type mockTranslator struct {
	mu    sync.Mutex
	calls int
	// missFn decides how to translate. Default = deterministic prefix.
	missFn func(translation.TranslationCommand) (translation.TranslationResult, error)
}

func newMockTranslator() *mockTranslator {
	return &mockTranslator{}
}

func (m *mockTranslator) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.missFn != nil {
		return m.missFn(cmd)
	}
	return translation.TranslationResult{
		TranslatedText: fmt.Sprintf("[%s] %s", cmd.TargetLang, cmd.Text),
		UsedProvider:   "mock",
		UsedModel:      "stub",
		SourceLang:     cmd.SourceLang,
		TargetLang:     cmd.TargetLang,
		CacheStatus:    "miss",
		Confidence:     0.95,
	}, nil
}

func (m *mockTranslator) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

var _ translation.TranslationPort = (*mockTranslator)(nil)

// ── recording idempotency-aware outbox (mirrors outbox dedup) ────────

type outboxAction string

const (
	outboxActionEnqueued   outboxAction = "enqueued"
	outboxActionSuperseded outboxAction = "superseded"
)

type recordedEvent struct {
	EventType   string
	AggregateID string
	EventKey    string
	Payload     string
	Action      outboxAction
}

type recordingOutbox struct {
	mu        sync.Mutex
	events    []recordedEvent
	dedupKeys map[string]struct{}
}

func newRecordingOutbox() *recordingOutbox {
	return &recordingOutbox{dedupKeys: map[string]struct{}{}}
}

// Enqueue mirrors the production outbox dedup surface. If the
// supplied eventKey has already been enqueued in this recorder,
// the new call is recorded as "superseded" and returns an
// ExistingStatus-tagged EnqueueResult (mirroring the
// outboxevents.EnqueueResult.Inserted-false path). The mock uses
// the same idempotency decision the production caller relies on.
//
// The EnqueueResult struct (internal/infrastructure/database/
// sqlite/outboxevents/envelope.go) carries EventID / Inserted /
// ExistingStatus — NOT Action / EventKey. Tests probe the
// recorder's own EnqueuedCount / SupersededCount which mirror
// the production supersede-gate semantics.
func (o *recordingOutbox) Enqueue(_ context.Context, _ *sql.Tx, eventType, aggID, _aggType, payload, key string) (*outboxevents.EnqueueResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if key != "" {
		if _, seen := o.dedupKeys[key]; seen {
			o.events = append(o.events, recordedEvent{
				EventType: eventType, AggregateID: aggID, EventKey: key, Payload: payload, Action: outboxActionSuperseded,
			})
			return &outboxevents.EnqueueResult{
				Inserted:       false,
				ExistingStatus: "superseded",
			}, nil
		}
		o.dedupKeys[key] = struct{}{}
	}
	o.events = append(o.events, recordedEvent{
		EventType: eventType, AggregateID: aggID, EventKey: key, Payload: payload, Action: outboxActionEnqueued,
	})
	return &outboxevents.EnqueueResult{
		Inserted:       true,
		ExistingStatus: "",
	}, nil
}

func (o *recordingOutbox) All() []recordedEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]recordedEvent, len(o.events))
	copy(out, o.events)
	return out
}

func (o *recordingOutbox) EnqueuedCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, e := range o.events {
		if e.Action == outboxActionEnqueued {
			n++
		}
	}
	return n
}

func (o *recordingOutbox) SupersededCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, e := range o.events {
		if e.Action == outboxActionSuperseded {
			n++
		}
	}
	return n
}

var _ texttracks.OutboxEnqueuer = (*recordingOutbox)(nil)

// ── factories ─────────────────────────────────────────────────────────

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func newSourceTrack(assetID, lang, kind, content string) asset.TextTrack {
	now := time.Now().UTC()
	hash := sha256Hex(content)
	return asset.TextTrack{
		AssetID: assetID, LanguageCode: lang, TextKind: asset.TextTrackKind(kind),
		TextContent:        content,
		SourceType:         asset.TextSourceProvided,
		SourceLanguageCode: lang,
		IsOriginal:         true,
		Provider:           "provided",
		ModelName:          "n/a",
		ModelVersion:       "n/a",
		PromptVersion:      "n/a",
		TextHash:           hash,
		SourceTextHash:     hash,
		IsCurrent:          true,
		Status:             asset.TextTrackReady,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

// newMaterializer builds a canonical Materializer wired to the
// supplied stubs. The OverrideTargetLanguages path is used so
// acceptance tests can drive the candidate set per call without
// re-validating operator-misuse constraints on ResolverConfig
// (ResolverConfig has NO TargetLanguages field — it's Registry or
// OverrideTargetLanguages). The override path is the canonical
// backfill escape hatch the production materializer exposes for
// operator overrides.
func newMaterializer(t *testing.T, repo asset.TextTrackRepository, tx translation.TranslationPort, ob texttracks.OutboxEnqueuer) *texttracks.Materializer {
	t.Helper()
	cfg := texttracks.ResolverConfig{
		SourceLanguage:          "en",
		OverrideTargetLanguages: nil, // set per call via Materialize override
		TranslationModel:        "stub-model",
		ModelVersion:            "v1",
		PromptVersion:           "p1",
	}
	m, err := texttracks.NewMaterializer(repo, tx, ob, cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMaterializer: %v", err)
	}
	return m
}
