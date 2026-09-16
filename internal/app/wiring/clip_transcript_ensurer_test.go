// Package wiring — clip_transcript_ensurer_test.go
//
// Pins the runtime adapter behind the script-language ↔ clip-association
// check. The adapter delegates to the canonical materialization pipeline and
// then VERIFIES the outcome, because BackfillService reports per-asset
// failures in its result rather than as an error.
package wiring

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	coreasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── stubs ─────────────────────────────────────────────────────────────────

type ensurerTestLister struct {
	assets []*coreasset.Asset
	err    error
	calls  int
}

func (l *ensurerTestLister) List(_ context.Context, _ coreasset.Filter) ([]*coreasset.Asset, error) {
	l.calls++
	return l.assets, l.err
}

// ensurerTestReader is a mutable READY-track set, exactly like the real store
// after the materializer persists a row.
type ensurerTestReader struct {
	tracks map[string]*detail.TextTrack
}

func newEnsurerTestReader() *ensurerTestReader {
	return &ensurerTestReader{tracks: map[string]*detail.TextTrack{}}
}

func (r *ensurerTestReader) key(assetID, lang string) string { return assetID + ":" + lang }

func (r *ensurerTestReader) put(assetID, lang string) {
	r.tracks[r.key(assetID, lang)] = &detail.TextTrack{
		AssetID:      assetID,
		LanguageCode: lang,
		TextKind:     detail.TextTrackTranscript,
		TextContent:  "materialized",
		Status:       detail.TextTrackReady,
	}
}

func (r *ensurerTestReader) FindReady(_ context.Context, assetID, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	if kind != detail.TextTrackTranscript {
		return nil, nil, nil
	}
	if track, ok := r.tracks[r.key(assetID, languageCode)]; ok {
		return track, nil, nil
	}
	return nil, nil, nil
}

func (r *ensurerTestReader) ListReadyLanguages(_ context.Context, _ string, _ detail.TextTrackKind) ([]string, error) {
	return []string{}, nil
}

type ensurerTestMaterializer struct {
	reader *ensurerTestReader
	// persist controls whether the fake pipeline actually produces a READY row.
	persist bool
	err     error
	calls   []texttracks.BackfillOptions
	assets  []string
}

func (m *ensurerTestMaterializer) ProcessAsset(_ context.Context, assetItem *coreasset.Asset, opts texttracks.BackfillOptions) (texttracks.BackfillAssetResult, error) {
	m.calls = append(m.calls, opts)
	if assetItem != nil {
		m.assets = append(m.assets, assetItem.ID)
	}
	if m.err != nil {
		return texttracks.BackfillAssetResult{}, m.err
	}
	if m.persist && assetItem != nil && len(opts.TargetLanguages) > 0 {
		m.reader.put(assetItem.ID, opts.TargetLanguages[0])
	}
	return texttracks.BackfillAssetResult{AssetID: opts.Source}, nil
}

func ensurerTestAsset(id string) *coreasset.Asset {
	return &coreasset.Asset{ID: id, Source: coreasset.Source("youtube")}
}

// ── guards ────────────────────────────────────────────────────────────────

func TestClipTranscriptEnsurer_FailsClosedWithoutPipeline(t *testing.T) {
	e := &clipTranscriptEnsurer{}
	err := e.EnsureReadyTextTrack(context.Background(), "a", "en", detail.TextTrackTranscript)
	if err == nil || !strings.Contains(err.Error(), "backfill pipeline is not wired") {
		t.Fatalf("nil pipeline must fail closed; got %v", err)
	}
}

func TestClipTranscriptEnsurer_FailsClosedWithoutAssetLister(t *testing.T) {
	e := &clipTranscriptEnsurer{backfill: &ensurerTestMaterializer{}}
	err := e.EnsureReadyTextTrack(context.Background(), "a", "en", detail.TextTrackTranscript)
	if err == nil || !strings.Contains(err.Error(), "asset lister is not wired") {
		t.Fatalf("nil lister must fail closed; got %v", err)
	}
}

func TestClipTranscriptEnsurer_RejectsEmptyInputs(t *testing.T) {
	reader := newEnsurerTestReader()
	e := &clipTranscriptEnsurer{
		clips:    &ensurerTestLister{assets: []*coreasset.Asset{ensurerTestAsset("a")}},
		reader:   reader,
		backfill: &ensurerTestMaterializer{reader: reader},
	}
	for _, tc := range []struct{ assetID, lang string }{{"", "en"}, {"a", "  "}} {
		if err := e.EnsureReadyTextTrack(context.Background(), tc.assetID, tc.lang, detail.TextTrackTranscript); err == nil {
			t.Fatalf("empty input (%q,%q) must fail closed", tc.assetID, tc.lang)
		}
	}
}

// ── idempotence ───────────────────────────────────────────────────────────

func TestClipTranscriptEnsurer_ReadyTrackIsANoOp(t *testing.T) {
	reader := newEnsurerTestReader()
	reader.put("asset-1", "en")
	lister := &ensurerTestLister{}
	mat := &ensurerTestMaterializer{reader: reader, persist: true}
	e := &clipTranscriptEnsurer{clips: lister, reader: reader, backfill: mat}

	if err := e.EnsureReadyTextTrack(context.Background(), "asset-1", "en", detail.TextTrackTranscript); err != nil {
		t.Fatalf("already-READY track must be a no-op: %v", err)
	}
	if lister.calls != 0 || len(mat.calls) != 0 {
		t.Fatalf("READY track must not trigger load/materialization; lister=%d materialize=%d", lister.calls, len(mat.calls))
	}
}

// ── create and persist ────────────────────────────────────────────────────

func TestClipTranscriptEnsurer_MaterializesAndVerifies(t *testing.T) {
	reader := newEnsurerTestReader()
	lister := &ensurerTestLister{assets: []*coreasset.Asset{ensurerTestAsset("asset-2")}}
	mat := &ensurerTestMaterializer{reader: reader, persist: true}
	e := &clipTranscriptEnsurer{clips: lister, reader: reader, backfill: mat, sourceLanguage: "en"}

	if err := e.EnsureReadyTextTrack(context.Background(), "asset-2", "it", detail.TextTrackTranscript); err != nil {
		t.Fatalf("materialization must succeed: %v", err)
	}
	if len(mat.calls) != 1 {
		t.Fatalf("ProcessAsset calls = %d, want 1", len(mat.calls))
	}
	got := mat.calls[0]
	if got.SourceLanguage != "en" || len(got.TargetLanguages) != 1 || got.TargetLanguages[0] != "it" {
		t.Fatalf("materialization options = %+v", got)
	}
	if got.TextKind != detail.TextTrackTranscript {
		t.Fatalf("TextKind = %q", got.TextKind)
	}
	if mat.assets[0] != "asset-2" {
		t.Fatalf("materialized asset = %q", mat.assets[0])
	}
	// The track really is persisted in the reader after the call.
	track, _, err := reader.FindReady(context.Background(), "asset-2", "it", detail.TextTrackTranscript)
	if err != nil || track == nil {
		t.Fatalf("the materialized track must be persisted; track=%v err=%v", track, err)
	}
}

func TestClipTranscriptEnsurer_AssetNotFoundFailsClosed(t *testing.T) {
	reader := newEnsurerTestReader()
	e := &clipTranscriptEnsurer{
		clips:    &ensurerTestLister{assets: nil},
		reader:   reader,
		backfill: &ensurerTestMaterializer{reader: reader, persist: true},
	}
	err := e.EnsureReadyTextTrack(context.Background(), "missing", "en", detail.TextTrackTranscript)
	if err == nil || !strings.Contains(err.Error(), "not found in the media SSOT") {
		t.Fatalf("missing asset must fail closed; got %v", err)
	}
}

func TestClipTranscriptEnsurer_MaterializerErrorFailsClosed(t *testing.T) {
	reader := newEnsurerTestReader()
	e := &clipTranscriptEnsurer{
		clips:    &ensurerTestLister{assets: []*coreasset.Asset{ensurerTestAsset("asset-3")}},
		reader:   reader,
		backfill: &ensurerTestMaterializer{reader: reader, err: errors.New("argos down")},
	}
	err := e.EnsureReadyTextTrack(context.Background(), "asset-3", "en", detail.TextTrackTranscript)
	if err == nil || !strings.Contains(err.Error(), "argos down") {
		t.Fatalf("materializer error must surface; got %v", err)
	}
}

// TestClipTranscriptEnsurer_SuccessWithoutTrackStillFailsClosed is the key
// non-vacuity pin: BackfillService returns a nil error for per-asset failures
// (they are reported in the result), so an ensurer that trusted the nil error
// would silently let the resolver continue without subtitles.
func TestClipTranscriptEnsurer_SuccessWithoutTrackStillFailsClosed(t *testing.T) {
	reader := newEnsurerTestReader()
	mat := &ensurerTestMaterializer{reader: reader, persist: false} // reports success, persists nothing
	e := &clipTranscriptEnsurer{
		clips:    &ensurerTestLister{assets: []*coreasset.Asset{ensurerTestAsset("asset-4")}},
		reader:   reader,
		backfill: mat,
	}
	err := e.EnsureReadyTextTrack(context.Background(), "asset-4", "en", detail.TextTrackTranscript)
	if err == nil {
		t.Fatal("a nil error from the pipeline is not enough: the track must be verified READY")
	}
	if !strings.Contains(err.Error(), "after materialization") {
		t.Fatalf("error must name the failed verification; got %v", err)
	}
}

func TestClipTranscriptEnsurer_WithoutReaderCannotVerify(t *testing.T) {
	mat := &ensurerTestMaterializer{reader: newEnsurerTestReader(), persist: false}
	e := &clipTranscriptEnsurer{
		clips:    &ensurerTestLister{assets: []*coreasset.Asset{ensurerTestAsset("asset-5")}},
		reader:   nil,
		backfill: mat,
	}
	err := e.EnsureReadyTextTrack(context.Background(), "asset-5", "en", detail.TextTrackTranscript)
	if err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("an unverifiable outcome must fail closed; got %v", err)
	}
}
