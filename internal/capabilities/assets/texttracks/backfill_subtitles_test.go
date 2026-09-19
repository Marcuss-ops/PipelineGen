// Package texttracks — backfill_subtitles_test.go: pins the canonical
// per-language subtitle-artifact delivery step (POSTGRES-MEDIA-CUTOVER
// follow-up, September 2026).
//
// This step now has two callers — the operator backfill and the
// `asset.text.materialize` job fast path that the direct YouTube fan-out
// uses — so the contract under test is the contract both depend on: every
// READY language gets its own artifact, and an inapplicable asset says so
// instead of silently doing nothing.
package texttracks

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// subtitleTrackRepoStub implements exactly the two reads the delivery step
// performs. The embedded interface is deliberately left nil so any new
// dependency the step grows panics loudly in tests instead of passing on a
// zero value.
type subtitleTrackRepoStub struct {
	detail.TextTrackRepository
	ready map[string][]detail.TimedCue
	// text holds the per-language transcript TEXT. A translated track is
	// written with text and no timing, which is the real production state
	// the delivery step has to repair.
	text map[string]string
}

func (r *subtitleTrackRepoStub) ListReadyLanguages(_ context.Context, _ string, _ detail.TextTrackKind) ([]string, error) {
	langs := make([]string, 0, len(r.ready))
	for lang, cues := range r.ready {
		if len(cues) > 0 {
			langs = append(langs, lang)
		}
	}
	return langs, nil
}

func (r *subtitleTrackRepoStub) FindReady(_ context.Context, _, lang string, _ detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	cues, ok := r.ready[lang]
	if !ok {
		return nil, nil, nil
	}
	return &detail.TextTrack{
		ID:           7,
		LanguageCode: lang,
		TextKind:     detail.TextTrackTranscript,
		TextContent:  r.text[lang],
		Status:       detail.TextTrackReady,
		IsCurrent:    true,
	}, cues, nil
}

// subtitleCueWriterRecorder captures ReplaceTranscriptCues writes and applies
// them to the stub, mirroring the canonical writer's replace semantics so the
// delivery loop observes the timing it just persisted.
type subtitleCueWriterRecorder struct {
	repo  *subtitleTrackRepoStub
	calls []map[string][]detail.TimedCue
}

func (w *subtitleCueWriterRecorder) ReplaceTranscriptCues(_ context.Context, _ string, byLang map[string][]detail.TimedCue) error {
	cp := make(map[string][]detail.TimedCue, len(byLang))
	for lang, cues := range byLang {
		cp[lang] = append([]detail.TimedCue(nil), cues...)
		if w.repo != nil {
			w.repo.ready[lang] = cp[lang]
		}
	}
	w.calls = append(w.calls, cp)
	return nil
}

// subtitleLayoutStub is a fixed SubtitleFolderResolver.
type subtitleLayoutStub struct{ loc SubtitleLocation }

func (s subtitleLayoutStub) ResolveSubtitleLocation(context.Context, string, string) (SubtitleLocation, error) {
	return s.loc, nil
}

func newSubtitleDeliveryService(t *testing.T, ready map[string][]detail.TimedCue) (*BackfillService, *subtitlePublisherRecorder) {
	t.Helper()
	artRepo := &subtitleArtifactRepoRecorder{}
	pub := &subtitlePublisherRecorder{}
	return &BackfillService{
		repo:            &subtitleTrackRepoStub{ready: ready},
		subArtRepo:      artRepo,
		subMaterializer: NewSubtitleArtifactMaterializer(artRepo, t.TempDir(), pub),
		driveFolderID:   "folder-default",
		log:             zap.NewNop(),
	}, pub
}

func youtubeClip(id string) *asset.Asset {
	return &asset.Asset{
		ID:       id,
		Source:   asset.Source("youtube"),
		Filename: id + ".mp4",
		Duration: 2 * time.Second,
	}
}

// TestMaterializeSubtitleArtifacts_DeliversEveryReadyLanguage is the core
// regression: a YouTube clip with its configured translations must end up
// with one uploaded subtitle artifact per READY language, not just the
// original. This is the guarantee the direct YouTube path lacked.
func TestMaterializeSubtitleArtifacts_DeliversEveryReadyLanguage(t *testing.T) {
	ready := map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1000, Text: "hello"}},
		"it": {{StartMs: 0, EndMs: 1000, Text: "ciao"}},
		"de": {{StartMs: 0, EndMs: 1000, Text: "hallo"}},
	}
	svc, pub := newSubtitleDeliveryService(t, ready)

	rep, err := svc.MaterializeSubtitleArtifacts(
		context.Background(), youtubeClip("clip-1"), "en", []string{"it", "de"}, detail.TextTrackTranscript,
	)
	if err != nil {
		t.Fatalf("MaterializeSubtitleArtifacts: %v", err)
	}
	if rep.Skipped {
		t.Fatalf("a youtube clip must not be skipped; reason=%q", rep.SkipReason)
	}
	if rep.Delivered != 3 {
		t.Fatalf("delivered = %d, want 3 (one artifact per READY language)", rep.Delivered)
	}
	if len(pub.requests) != 3 {
		t.Fatalf("Drive publishes = %d, want 3 (every language must reach Drive, not only the original)", len(pub.requests))
	}
	if len(rep.Failed) != 0 {
		t.Fatalf("unexpected per-language failures: %v", rep.Failed)
	}
	// The languages are reported so an operator can see the delivered set.
	if len(rep.Languages) != 3 {
		t.Fatalf("languages = %v, want the 3 READY languages", rep.Languages)
	}
}

// TestMaterializeSubtitleArtifacts_IsIdempotent pins that a second delivery
// over identical content reuses the recorded artifact instead of uploading a
// duplicate — the fan-out may run again for the same clip, and Drive must
// not accumulate copies.
func TestMaterializeSubtitleArtifacts_IsIdempotent(t *testing.T) {
	ready := map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1000, Text: "hello"}},
	}
	svc, pub := newSubtitleDeliveryService(t, ready)
	ctx := context.Background()

	if _, err := svc.MaterializeSubtitleArtifacts(ctx, youtubeClip("clip-1"), "en", nil, detail.TextTrackTranscript); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if len(pub.requests) != 1 {
		t.Fatalf("first delivery publishes = %d, want 1", len(pub.requests))
	}
	if _, err := svc.MaterializeSubtitleArtifacts(ctx, youtubeClip("clip-1"), "en", nil, detail.TextTrackTranscript); err != nil {
		t.Fatalf("second delivery: %v", err)
	}
	if len(pub.requests) != 1 {
		t.Fatalf("an identical re-delivery MUST reuse the artifact; publishes = %d, want 1", len(pub.requests))
	}
}

// TestMaterializeSubtitleArtifacts_SkipReasonsAreExplicit pins the
// fail-honest posture: every inapplicable case is named, so "no subtitles
// expected" is never confused with "delivery silently did nothing".
func TestMaterializeSubtitleArtifacts_SkipReasonsAreExplicit(t *testing.T) {
	ready := map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1000, Text: "hello"}},
	}
	svc, pub := newSubtitleDeliveryService(t, ready)
	ctx := context.Background()

	cases := []struct {
		name   string
		asset  *asset.Asset
		kind   detail.TextTrackKind
		reason string
	}{
		{name: "no asset", asset: nil, kind: detail.TextTrackTranscript, reason: "no_asset"},
		{name: "empty id", asset: &asset.Asset{Source: asset.Source("youtube")}, kind: detail.TextTrackTranscript, reason: "no_asset"},
		{
			name:   "source does not take subtitles",
			asset:  &asset.Asset{ID: "clip-1", Source: asset.Source("artlist")},
			kind:   detail.TextTrackTranscript,
			reason: "source_does_not_take_subtitles",
		},
		{
			name:   "kind is not transcript",
			asset:  youtubeClip("clip-1"),
			kind:   detail.TextTrackDescription,
			reason: "text_kind_is_not_transcript",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := svc.MaterializeSubtitleArtifacts(ctx, tc.asset, "en", nil, tc.kind)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !rep.Skipped || rep.SkipReason != tc.reason {
				t.Fatalf("skipped=%v reason=%q, want skipped=true reason=%q", rep.Skipped, rep.SkipReason, tc.reason)
			}
		})
	}
	if len(pub.requests) != 0 {
		t.Fatalf("no skip case may publish; publishes = %d", len(pub.requests))
	}
}

// TestMaterializeSubtitleArtifacts_NilMaterializerIsSkippedNotPanicked pins
// the degraded-composition contract: a service built without a subtitle
// materializer reports the skip instead of panicking on boot paths that
// legitimately omit it.
func TestMaterializeSubtitleArtifacts_NilMaterializerIsSkippedNotPanicked(t *testing.T) {
	svc := &BackfillService{repo: &subtitleTrackRepoStub{}, log: zap.NewNop()}
	rep, err := svc.MaterializeSubtitleArtifacts(context.Background(), youtubeClip("clip-1"), "en", nil, detail.TextTrackTranscript)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.Skipped || rep.SkipReason != "no_subtitle_materializer" {
		t.Fatalf("skipped=%v reason=%q, want the explicit no_subtitle_materializer skip", rep.Skipped, rep.SkipReason)
	}
}

// TestMaterializeSubtitleArtifacts_AlignsTranslatedCuesOntoSourceTiming pins
// the delivery step's subtitle-readiness repair.
//
// The materializer writes each translated language as TEXT WITHOUT TIMING, so
// a clip could hold ten READY transcript rows and still produce a single .ass:
// the delivery loop silently skipped every language whose track had no cues.
// The fix projects the translated full text onto the SOURCE timing (the
// canonical CuesWithText invariant) and PERSISTS it through the canonical cue
// writer, so every other consumer sees the same timing.
func TestMaterializeSubtitleArtifacts_AlignsTranslatedCuesOntoSourceTiming(t *testing.T) {
	src := []detail.TimedCue{
		{StartMs: 0, EndMs: 1000, Text: "hello world"},
		{StartMs: 1000, EndMs: 2000, Text: "how are you"},
	}
	ready := map[string][]detail.TimedCue{
		"en": src,
		"it": nil, // translated: text present, timing absent
	}
	svc, pub := newSubtitleDeliveryService(t, ready)
	stub := &subtitleTrackRepoStub{ready: ready, text: map[string]string{"it": "ciao mondo come stai"}}
	svc.repo = stub
	rec := &subtitleCueWriterRecorder{repo: stub}
	svc.cues = rec

	rep, err := svc.MaterializeSubtitleArtifacts(
		context.Background(), youtubeClip("clip-1"), "en", []string{"it"}, detail.TextTrackTranscript,
	)
	if err != nil {
		t.Fatalf("MaterializeSubtitleArtifacts: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("cue alignment writes = %d, want exactly 1 batch", len(rec.calls))
	}
	batch := rec.calls[0]
	// ReplaceTranscriptCues rewrites the WHOLE asset, so the source language
	// MUST ride along or aligning the translation would delete the source cues.
	if len(batch["en"]) != len(src) {
		t.Fatalf("source cues must be re-sent in the same batch; got %d, want %d", len(batch["en"]), len(src))
	}
	aligned, ok := batch["it"]
	if !ok {
		t.Fatal("the translated language must be aligned, not left text-only")
	}
	if len(aligned) != len(src) {
		t.Fatalf("aligned cues = %d, want one per source window (%d)", len(aligned), len(src))
	}
	if aligned[0].StartMs != src[0].StartMs || aligned[0].EndMs != src[0].EndMs {
		t.Fatalf("aligned cue 0 window = %d..%d, want the SOURCE window %d..%d — timing must not drift",
			aligned[0].StartMs, aligned[0].EndMs, src[0].StartMs, src[0].EndMs)
	}
	if aligned[0].Text == "" {
		t.Fatal("aligned cue must carry the translated text")
	}
	// And now that timing exists, the artifact reaches Drive for that language.
	if rep.Delivered != 2 {
		t.Fatalf("delivered = %d, want 2 (source + aligned translation)", rep.Delivered)
	}
	if len(rep.UnTimed) != 0 {
		t.Fatalf("a repaired language must not be reported as untimed: %v", rep.UnTimed)
	}
	if _, ok := pub.byAsset["clip-1"]; !ok {
		t.Fatal("the clip must have been published after alignment")
	}
}

// TestMaterializeSubtitleArtifacts_PreservesExistingCuesDuringAlignment pins
// the whole-asset replacement invariant: repairing one text-only language
// must not delete timing already present for another language.
func TestMaterializeSubtitleArtifacts_PreservesExistingCuesDuringAlignment(t *testing.T) {
	src := []detail.TimedCue{{StartMs: 0, EndMs: 1000, Text: "hello"}}
	de := []detail.TimedCue{{StartMs: 0, EndMs: 1000, Text: "hallo"}}
	ready := map[string][]detail.TimedCue{"en": src, "de": de, "it": nil}
	svc, pub := newSubtitleDeliveryService(t, ready)
	stub := &subtitleTrackRepoStub{ready: ready, text: map[string]string{"it": "ciao"}}
	svc.repo = stub
	rec := &subtitleCueWriterRecorder{repo: stub}
	svc.cues = rec

	rep, err := svc.MaterializeSubtitleArtifacts(
		context.Background(), youtubeClip("clip-preserve"), "en", []string{"it", "de"}, detail.TextTrackTranscript,
	)
	if err != nil {
		t.Fatalf("MaterializeSubtitleArtifacts: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("cue alignment writes = %d, want exactly 1 batch", len(rec.calls))
	}
	batch := rec.calls[0]
	for _, lang := range []string{"en", "de", "it"} {
		if len(batch[lang]) != 1 {
			t.Fatalf("replacement batch lost %s cues: got %v", lang, batch)
		}
	}
	if batch["de"][0].Text != "hallo" {
		t.Fatalf("existing de cue was rewritten unexpectedly: %v", batch["de"])
	}
	if rep.Delivered != 3 {
		t.Fatalf("delivered = %d, want 3 after preserving existing cues", rep.Delivered)
	}
	if len(pub.requests) != 3 {
		t.Fatalf("publishes = %d, want 3", len(pub.requests))
	}
}

// TestMaterializeSubtitleArtifacts_ReportsUntimedLanguagesInsteadOfSkippingSilently
// is the regression for the SECOND half of the ten-assets-one-file bug.
//
// The first half was the missing projection (pinned above). The second half
// was that a language WITH text and WITHOUT cues was skipped by a bare
// `continue`: no failure entry, no counter, nothing for an operator to see.
// Nine languages could be missing their artifact while every report said the
// clip was fully delivered. This test pins the fail-honest replacement: the
// language is NAMED in UnTimed, with a reason, and it is NOT confused with a
// language that was never materialized at all.
func TestMaterializeSubtitleArtifacts_ReportsUntimedLanguagesInsteadOfSkippingSilently(t *testing.T) {
	src := []detail.TimedCue{{StartMs: 0, EndMs: 1000, Text: "hello world"}}
	ready := map[string][]detail.TimedCue{
		"en": src,
		"it": nil, // translated text present, timing absent
	}
	svc, pub := newSubtitleDeliveryService(t, ready)
	stub := &subtitleTrackRepoStub{ready: ready, text: map[string]string{"it": "ciao mondo"}}
	svc.repo = stub
	// No cue writer wired: the alignment pass cannot run, which is exactly the
	// degraded composition whose silence hid the bug.
	svc.cues = nil

	rep, err := svc.MaterializeSubtitleArtifacts(
		context.Background(), youtubeClip("clip-1"), "en", []string{"it", "es"}, detail.TextTrackTranscript,
	)
	if err != nil {
		t.Fatalf("MaterializeSubtitleArtifacts: %v", err)
	}
	if rep.Delivered != 1 {
		t.Fatalf("delivered = %d, want 1 (only the source language has timing)", rep.Delivered)
	}
	if len(rep.Failed) != 0 {
		t.Fatalf("an untimed translation is not an upload failure; got %v", rep.Failed)
	}
	reason, ok := rep.UnTimed["it"]
	if !ok {
		t.Fatalf("the untimed language MUST be reported; got %v", rep.UnTimed)
	}
	if reason == "" {
		t.Fatal("the untimed report must carry a reason")
	}
	if _, ok := rep.UnTimed["es"]; ok {
		t.Fatalf("a language with no track at all must not be reported as untimed: %v", rep.UnTimed)
	}
	if len(pub.requests) != 1 {
		t.Fatalf("publishes = %d, want 1 (no artifact may be uploaded without cues)", len(pub.requests))
	}
}

// cueTranslatorStub is a translation.TranslationPort that records every cue
// text it was asked to translate and answers deterministically.
type cueTranslatorStub struct {
	mu      sync.Mutex
	seen    []string
	targets map[string]bool
	err     error
}

func (s *cueTranslatorStub) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, cmd.Text)
	if s.targets == nil {
		s.targets = map[string]bool{}
	}
	s.targets[cmd.TargetLang] = true
	if s.err != nil {
		return translation.TranslationResult{}, s.err
	}
	return translation.TranslationResult{TranslatedText: cmd.TargetLang + ":" + cmd.Text}, nil
}

func (s *cueTranslatorStub) translatedTexts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// TestMaterializeSubtitleArtifacts_PrefersTimingFaithfulCueTranslation pins
// the quality path: when a CueTranslator is wired, a translated language gets
// cue-per-cue translation with the SOURCE windows, instead of the whole
// translated text being sliced across those windows by word count (which can
// split a sentence mid-phrase).
func TestMaterializeSubtitleArtifacts_PrefersTimingFaithfulCueTranslation(t *testing.T) {
	src := []detail.TimedCue{
		{StartMs: 0, EndMs: 1000, Text: "hello world"},
		{StartMs: 1000, EndMs: 2000, Text: "how are you"},
	}
	ready := map[string][]detail.TimedCue{"en": src, "it": nil}
	svc, _ := newSubtitleDeliveryService(t, ready)
	stub := &subtitleTrackRepoStub{ready: ready, text: map[string]string{"it": "ciao mondo come stai"}}
	svc.repo = stub
	rec := &subtitleCueWriterRecorder{repo: stub}
	svc.cues = rec

	port := &cueTranslatorStub{}
	svc.cueTranslator = NewCueTranslator(port, "en", "", 2, zap.NewNop())

	if _, err := svc.MaterializeSubtitleArtifacts(
		context.Background(), youtubeClip("clip-1"), "en", []string{"it"}, detail.TextTrackTranscript,
	); err != nil {
		t.Fatalf("MaterializeSubtitleArtifacts: %v", err)
	}

	seen := port.translatedTexts()
	if len(seen) != len(src) {
		t.Fatalf("per-cue translation calls = %d, want one per SOURCE cue (%d); got %v", len(seen), len(src), seen)
	}
	// The fan-out is concurrent, so the CALL order is not part of the contract;
	// the translated WINDOWS are (asserted by index below).
	sorted := append([]string(nil), seen...)
	sort.Strings(sorted)
	if sorted[0] != "hello world" || sorted[1] != "how are you" {
		t.Fatalf("the translator must receive the SOURCE cue texts, got %v", sorted)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("cue writes = %d, want 1 batch", len(rec.calls))
	}
	aligned := rec.calls[0]["it"]
	if len(aligned) != len(src) {
		t.Fatalf("aligned cues = %d, want %d (1:1 with the source)", len(aligned), len(src))
	}
	if aligned[1].Text != "it:how are you" {
		t.Fatalf("cue 1 text = %q, want the per-cue translation (not a word-count slice)", aligned[1].Text)
	}
	if aligned[1].StartMs != src[1].StartMs || aligned[1].EndMs != src[1].EndMs {
		t.Fatalf("cue 1 window = %d..%d, want the SOURCE window %d..%d",
			aligned[1].StartMs, aligned[1].EndMs, src[1].StartMs, src[1].EndMs)
	}
}

// TestMaterializeSubtitleArtifacts_FallsBackWhenCueTranslationFails pins the
// degrade path: a failing per-cue translator must NOT leave the language
// without timing. The whole-text distribution still runs.
func TestMaterializeSubtitleArtifacts_FallsBackWhenCueTranslationFails(t *testing.T) {
	src := []detail.TimedCue{
		{StartMs: 0, EndMs: 1000, Text: "hello world"},
		{StartMs: 1000, EndMs: 2000, Text: "how are you"},
	}
	ready := map[string][]detail.TimedCue{"en": src, "it": nil}
	svc, _ := newSubtitleDeliveryService(t, ready)
	stub := &subtitleTrackRepoStub{ready: ready, text: map[string]string{"it": "ciao mondo come stai"}}
	svc.repo = stub
	rec := &subtitleCueWriterRecorder{repo: stub}
	svc.cues = rec

	port := &cueTranslatorStub{err: errors.New("argos sidecar down")}
	svc.cueTranslator = NewCueTranslator(port, "en", "", 2, zap.NewNop())

	if _, err := svc.MaterializeSubtitleArtifacts(
		context.Background(), youtubeClip("clip-1"), "en", []string{"it"}, detail.TextTrackTranscript,
	); err != nil {
		t.Fatalf("MaterializeSubtitleArtifacts: %v", err)
	}
	aligned := rec.calls[0]["it"]
	if len(aligned) != len(src) {
		t.Fatalf("aligned cues = %d, want %d via the whole-text fallback", len(aligned), len(src))
	}
	if aligned[0].Text == "" {
		t.Fatal("the fallback must still fill the cue text")
	}
	if strings.HasPrefix(aligned[0].Text, "it:") {
		t.Fatalf("the failing per-cue path must not be used: %q", aligned[0].Text)
	}
}

// TestMaterializeSubtitleArtifacts_PublishesIntoTheResolvedSubtitleLayout pins
// the single-owner Drive layout: the artifacts go to the ROOT+SUBPATH the
// resolver returns (the folder that also holds the .txt sidecar), instead of
// the legacy <asset folder>/Ass Sub/ tree.
func TestMaterializeSubtitleArtifacts_PublishesIntoTheResolvedSubtitleLayout(t *testing.T) {
	ready := map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1000, Text: "hello"}},
	}
	svc, pub := newSubtitleDeliveryService(t, ready)
	svc.subtitleFolders = subtitleLayoutStub{loc: SubtitleLocation{
		FolderID: "subtitle-root",
		Subpath:  []string{"youtube_subtitles", "VID"},
	}}

	if _, err := svc.MaterializeSubtitleArtifacts(
		context.Background(), youtubeClip("clip-1"), "en", nil, detail.TextTrackTranscript,
	); err != nil {
		t.Fatalf("MaterializeSubtitleArtifacts: %v", err)
	}
	if len(pub.requests) != 1 {
		t.Fatalf("publishes = %d, want 1", len(pub.requests))
	}
	req := pub.requests[0]
	if req.DestinationFolderID != "subtitle-root" {
		t.Fatalf("DestinationFolderID = %q, want the resolved subtitle root", req.DestinationFolderID)
	}
	want := []string{"youtube_subtitles", "VID"}
	if len(req.DestinationSubpath) != len(want) {
		t.Fatalf("DestinationSubpath = %v, want %v (the .txt sidecar's folder)", req.DestinationSubpath, want)
	}
	for i := range want {
		if req.DestinationSubpath[i] != want[i] {
			t.Fatalf("DestinationSubpath = %v, want %v", req.DestinationSubpath, want)
		}
	}
}

// TestContainsTextKind pins the payload predicate the job handler uses to
// decide whether subtitle delivery applies to a materialize job.
func TestContainsTextKind(t *testing.T) {
	if !containsTextKind([]string{"description", "transcript"}, detail.TextTrackTranscript) {
		t.Fatal("transcript present in a multi-kind payload must be detected")
	}
	if containsTextKind([]string{"description"}, detail.TextTrackTranscript) {
		t.Fatal("a payload without transcript must not trigger subtitle delivery")
	}
	if containsTextKind(nil, detail.TextTrackTranscript) {
		t.Fatal("an empty payload must not trigger subtitle delivery")
	}
}
