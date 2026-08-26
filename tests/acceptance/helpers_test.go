// Package acceptance_test — helpers_test.go: shared stubs for the
// canonical acceptance test battery.
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
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// ── in-memory detail.TextTrackRepository stub ─────────────────────────

type inMemRepo struct {
	mu     sync.Mutex
	tracks map[string]*detail.TextTrack
}

func newInMemRepo() *inMemRepo {
	return &inMemRepo{tracks: map[string]*detail.TextTrack{}}
}

func (r *inMemRepo) key(t detail.TextTrack) string {
	return fmt.Sprintf("%s|%s|%s", t.AssetID, t.LanguageCode, t.TextKind)
}

func (r *inMemRepo) UpsertBatch(_ context.Context, ts []detail.TextTrack) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range ts {
		tc := t
		r.tracks[r.key(tc)] = &tc
	}
	return nil
}

func (r *inMemRepo) FindReady(_ context.Context, assetID, lang string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tracks[fmt.Sprintf("%s|%s|%s", assetID, lang, kind)]
	if !ok || t.Status != detail.TextTrackReady {
		return nil, nil, nil
	}
	dup := *t
	return &dup, nil, nil
}

func (r *inMemRepo) Find(_ context.Context, assetID, lang string, kind detail.TextTrackKind) (*detail.TextTrack, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tracks[fmt.Sprintf("%s|%s|%s", assetID, lang, kind)]
	if !ok {
		return nil, nil
	}
	dup := *t
	return &dup, nil
}

func (r *inMemRepo) ListByAsset(_ context.Context, assetID string) ([]detail.TextTrack, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]detail.TextTrack, 0)
	for _, t := range r.tracks {
		if t.AssetID == assetID {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (r *inMemRepo) ListReadyLanguages(_ context.Context, assetID string, kind detail.TextTrackKind) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]struct{}{}
	for _, t := range r.tracks {
		if t.AssetID == assetID && t.TextKind == kind && t.Status == detail.TextTrackReady {
			seen[t.LanguageCode] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	return out, nil
}

func (r *inMemRepo) FindCurrentForTranslation(_ context.Context, assetID string, kind detail.TextTrackKind, lang, srcHash, model, modelVer, promptVer string) (*detail.TextTrack, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	wantKey := detail.TranslationKey(srcHash, lang, model, modelVer, promptVer)
	for _, t := range r.tracks {
		if t.AssetID != assetID || t.LanguageCode != lang || t.TextKind != kind {
			continue
		}
		if t.TranslationKey == wantKey && t.IsCurrent && t.Status == detail.TextTrackReady {
			dup := *t
			return &dup, nil
		}
	}
	return nil, nil
}

// InsertTranslationWithAuditPredecessor mirrors the SQL semantics:
// idempotency check, flip is_current=1 predecessor, insert new is_current=1.
func (r *inMemRepo) InsertTranslationWithAuditPredecessor(_ context.Context, t detail.TextTrack) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.tracks {
		if existing.AssetID == t.AssetID && existing.LanguageCode == t.LanguageCode &&
			existing.TextKind == t.TextKind &&
			existing.TranslationKey == t.TranslationKey && existing.IsCurrent {
			return nil
		}
	}
	for k, existing := range r.tracks {
		if existing.AssetID == t.AssetID && existing.LanguageCode == t.LanguageCode &&
			existing.TextKind == t.TextKind && existing.IsCurrent {
			existing.IsCurrent = false
			r.tracks[k] = existing
		}
	}
	t.IsCurrent = true
	if t.Status == "" {
		t.Status = detail.TextTrackReady
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	t.UpdatedAt = time.Now().UTC()
	dup := t
	r.tracks[r.key(t)] = &dup
	return nil
}

var _ detail.TextTrackRepository = (*inMemRepo)(nil)

// ── mock translation.TranslationPort ──────────────────────────────────

type mockTranslator struct {
	mu     sync.Mutex
	calls  int
	missFn func(translation.TranslationCommand) (translation.TranslationResult, error)
}

func newMockTranslator() *mockTranslator { return &mockTranslator{} }

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

// ── recording idempotency-aware outbox ────────────────────────────────

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

// Enqueue mirrors production dedup: same eventKey → supersede;
// new eventKey → enqueued. Returns the canonical
// outboxevents.EnqueueResult shape (Inserted + ExistingStatus).
func (o *recordingOutbox) Enqueue(_ context.Context, _ *sql.Tx, eventType, aggID, _aggType, payload, key string) (*outboxevents.EnqueueResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if key != "" {
		if _, seen := o.dedupKeys[key]; seen {
			o.events = append(o.events, recordedEvent{
				EventType: eventType, AggregateID: aggID, EventKey: key, Payload: payload, Action: outboxActionSuperseded,
			})
			return &outboxevents.EnqueueResult{Inserted: false, ExistingStatus: "superseded"}, nil
		}
		o.dedupKeys[key] = struct{}{}
	}
	o.events = append(o.events, recordedEvent{
		EventType: eventType, AggregateID: aggID, EventKey: key, Payload: payload, Action: outboxActionEnqueued,
	})
	return &outboxevents.EnqueueResult{Inserted: true}, nil
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

// All returns a snapshot of every recorded event in insertion
// order (enqueued + superseded). Returned slice is a copy — safe
// for the caller to range/filter without holding o.mu.
func (o *recordingOutbox) All() []recordedEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]recordedEvent, len(o.events))
	copy(out, o.events)
	return out
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

func newSourceTrack(assetID, lang, kind, content string) detail.TextTrack {
	now := time.Now().UTC()
	hash := sha256Hex(content)
	return detail.TextTrack{
		AssetID: assetID, LanguageCode: lang, TextKind: detail.TextTrackKind(kind),
		TextContent:        content,
		SourceType:         detail.TextSourceProvided,
		SourceLanguageCode: lang,
		IsOriginal:         true,
		Provider:           "provided",
		ModelName:          "n/a",
		ModelVersion:       "n/a",
		PromptVersion:      "n/a",
		TextHash:           hash,
		SourceTextHash:     hash,
		IsCurrent:          true,
		Status:             detail.TextTrackReady,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

// newMaterializer builds a canonical Materializer wired to supplied
// stubs. OverrideTargetLanguages carries a "__init__" sentinel
// that satisfies texttracks.ResolverConfig.Validate() — the
// production copy-replaces-via-append behaviour at Materialize
// entry (materializer.go) ensures the sentinel never reaches
// translation time because every test supplies a per-call override.
func newMaterializer(t *testing.T, repo detail.TextTrackRepository, tx translation.TranslationPort, ob texttracks.OutboxEnqueuer) *texttracks.Materializer {
	t.Helper()
	cfg := texttracks.ResolverConfig{
		SourceLanguage:          "en",
		OverrideTargetLanguages: []string{"__init__"},
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
