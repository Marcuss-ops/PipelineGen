// Package usecase — process_segment_preferred_languages_test.go: pins the
// Fase 5 policy wiring (PR-SUBS-CLIP-WINDOW).
//
// The extraction pipeline reads media.multilingual.* into
// ProcessSegmentObservabilityDeps.PreferredLanguages at composition time,
// but step6to9 used to build its TextTrackAcquireRequest WITHOUT that list.
// An empty list means "no preference" to the resolver, so the configured
// language policy never reached the chain that picks the clip's subtitles:
// the priority-2 DB fan-out was skipped entirely and priority 3+4 accepted
// whatever subtitle language the fetcher surfaced — i.e. the operator's
// correctly configured language had no effect on the downloaded clips.
//
// The pin is behavioural, not structural: the subtitle port returns an
// ENGLISH bundle, and the assertion is whether the chain falls through to
// Whisper (English not preferred) or keeps the subtitles (English preferred).
// Only step6to9 forwarding u.observability.PreferredLanguages can make those
// two cases differ.
package usecase

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// cannedSubtitlePort returns a fixed bundle (the ENGLISH captions that the
// first test case must reject) and counts the calls, so the policy test can
// tell "consulted and rejected" apart from "never consulted".
type cannedSubtitlePort struct {
	bundle *detail.ResolvedTextBundle
	calls  int
}

func (p *cannedSubtitlePort) FetchSegmentSubtitles(_ context.Context, _ string, _, _ int) (*detail.ResolvedTextBundle, error) {
	p.calls++
	return p.bundle, nil
}

func (p *cannedSubtitlePort) SliceSubtitles(_ context.Context, _ string, _, _ int, _ string) error {
	return ErrSubtitleUnavailable
}

var _ youtubeports.SubtitleFetcherPort = (*cannedSubtitlePort)(nil)

// englishSubtitleSource returns a subtitle port whose bundle is English —
// the language that is deliberately NOT preferred by the first test case.
func englishSubtitleSource() *cannedSubtitlePort {
	return &cannedSubtitlePort{bundle: &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    "English captions from the source video",
		Cues:         []detail.TimedCue{{StartMs: 1_000, EndMs: 3_000, Text: "English captions"}},
		SourceType:   detail.TextSourceYouTubeSubtitle,
		IsOriginal:   true,
		Provider:     "yt-dlp",
	}}
}

// runExecuteWithPolicy drives one full cache-miss extraction with the given
// PreferredLanguages and returns the subtitle/Whisper call counters.
func runExecuteWithPolicy(t *testing.T, preferred []string) (subs *cannedSubtitlePort, tport *countingTranscriber) {
	t.Helper()

	realPath := filepath.Join(t.TempDir(), "clip.mp4")
	require.NoError(t, os.WriteFile(realPath, []byte("fake audio bytes"), 0o644))

	core, media, metadata, observability := validProcessSegmentDeps()
	core.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
	core.Hash = testStubHash{}
	observability.PreferredLanguages = preferred

	subs = englishSubtitleSource()
	tport = &countingTranscriber{text: "trascrizione italiana da whisper"}
	media.TextTrackResolver = &TextTrackResolver{
		Repo:        noRowsRepo{},
		Subtitles:   subs,
		Transcriber: tport,
		Log:         zap.NewNop(),
	}

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_preflang_e2e",
		OutDir:  t.TempDir(),
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "PrefLang"},
		Index:   0,
	}
	out, err := uc.Execute(context.Background(), cmd)
	require.NoError(t, err, "Execute must succeed")
	require.Equal(t, "processed", out.Status)
	return subs, tport
}

// Case A: the configured policy prefers Italian only, the video offers
// English captions. The chain MUST reject the subtitle bundle and fall
// through to Whisper — which only happens when the configured list reaches
// the acquisition request. Without the wiring the English bundle is accepted
// (empty list = "no preference") and Whisper stays silent.
func TestExecute_PreferredLanguagesRejectNonPreferredSubtitles(t *testing.T) {
	subs, tport := runExecuteWithPolicy(t, []string{"it"})

	require.Equal(t, 1, subs.calls, "the subtitle port must still be consulted")
	require.Equal(t, int32(1), atomic.LoadInt32(&tport.calls),
		"English subtitles are not in the configured [it] policy: the chain must fall through to Whisper. "+
			"Whisper was never consulted — u.observability.PreferredLanguages is not reaching TextTrackAcquireRequest")
}

// Case B: the configured policy contains English, so the very same subtitle
// bundle must be ACCEPTED and Whisper must stay silent. This is the mirror
// assertion: it proves the rejection in case A is driven by the policy list,
// not by some other change in the chain.
func TestExecute_PreferredLanguagesAcceptPreferredSubtitles(t *testing.T) {
	subs, tport := runExecuteWithPolicy(t, []string{"it", "en"})

	require.Equal(t, 1, subs.calls, "the subtitle port must be consulted")
	require.Equal(t, int32(0), atomic.LoadInt32(&tport.calls),
		"English is in the configured policy: the subtitle bundle must win and Whisper must not run")
}
