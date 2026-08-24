// Unit tests for Generator (PR-VO-A5/A6, June 2026). Second-pass after
// the wildcard-stub refactor that surfaced the Normalize() lowercase
// invariant on cmd.Locale.
//
// PR-VO-A5 = correct Total/Failed/Success/OK accounting in Response.
// PR-VO-A6 = strict translator failure (default fail-closed) +
// literal-mode opt-in AllowUntranslated (silent end-to-end skip).
//
// Conventions enforced by both code + tests:
//   - resp.Total == len(targets) regardless of how many translations
//     failed.
//   - resp.Failed counts translation+voiceover failures in strict mode.
//     In LITERAL lenient mode, translation failures do NOT increment
//     Failed; voiceover failures DO.
//   - resp.OK = (resp.Failed == 0). In LITERAL lenient mode, even
//     when translations failed, resp.OK stays true.
//   - voGen.Generate receives Locale lowercased (Normalize() in
//     domain/voiceover/command.go applies strings.ToLower) — the
//     wildcard stub doesn't care about locale shape; only per-locale
//     failures opt-in via byLocaleErr.
//   - Translation failures (ErrTranslationFailed, ErrTranslationEmpty)
//     AND voiceover failures (ErrVoiceoverFailed) use typed sentinels.
//     Tests assert via errors.Is so refactors of the wrapping machine
//     cannot silently break the contract.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	domainvo "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// stubVO is a VoiceoverGenerator that returns success for ANY locale,
// unless byLocaleErr carries an entry for the lowercased locale after
// Normalize() (which the production generator always applies). The
// wildcard shape avoids a class of test bug where the stub map had to
// be kept in sync with domain/voiceover/command.go::Normalize's
// `strings.ToLower` invariant. failAll bypasses byLocaleErr for tests
// that want to fail-everywhere.
//
// `calls` records every locale Generate was invoked for so strict-mode
// tests can pin "voiceover was NOT called when translation failed".
type stubVO struct {
	byLocaleErr map[string]error
	failAll     bool
	calls       []string
}

func (s *stubVO) Generate(_ context.Context, cmd domainvo.GenerateVoiceoverCommand) (*domainvo.Result, error) {
	// Normalize is applied by the producer — but we re-normalise here
	// defensively in case a future refactor skips it. Cheap + safe.
	locale := strings.ToLower(strings.TrimSpace(cmd.Locale))
	s.calls = append(s.calls, locale)

	if s.failAll {
		return nil, errors.New("voiceover failed: stubVO.failAll=true")
	}
	if err, ok := s.byLocaleErr[locale]; ok {
		return nil, err
	}
	return &domainvo.Result{
		OK: true,
		VoiceoverSynthesisResult: domainvo.VoiceoverSynthesisResult{
			Locale: locale,
		},
		DriveLink:   "https://drive.google.com/file/d/" + locale,
		DriveFileID: "stub-drive-" + locale,
		Status:      "generated",
	}, nil
}

type translatorResp struct {
	text string
	err  error
}

// mkTranslator keys by the friendly display name (e.g. "Italian").
func mkTranslator(overrides map[string]translatorResp) translation.TranslatorFunc {
	return func(_ context.Context, _, langName string) (string, error) {
		if r, ok := overrides[langName]; ok {
			return r.text, r.err
		}
		return "auto-translated:" + langName, nil
	}
}

func mkEmptyTranslator(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func defaultTargets(t *testing.T) []translation.LanguageTarget {
	t.Helper()
	return translation.DefaultPromoLanguages()
}

// errorOrSentinel maps a Result.Error surface string back to a wrapped
// error we can errors.Is against. Production wraps inner errors via
// `fmt.Errorf("%w: %v", ErrFoo, innerErr)`, so the surface string
// starts with the canonical ErrFoo.Error() text. Some sentinels
// (e.g. ErrTranslationEmpty) are emitted bare when no inner error
// exists, so we accept EITHER form:
//   - exact equality: r.Error == s.Error()
//   - scaffolded wrap: r.Error starts with s.Error()+": "
func errorOrSentinel(r Result) error {
	if r.Error == "" {
		return nil
	}
	for _, s := range []error{ErrTranslationFailed, ErrTranslationEmpty, ErrVoiceoverFailed} {
		if s == nil {
			continue
		}
		if r.Error == s.Error() {
			return s
		}
		if strings.HasPrefix(r.Error, s.Error()+": ") {
			return s // canonical: errors.Is(s, s) == true
		}
	}
	return errors.New(r.Error)
}

// ---------------------------------------------------------------------------
// PR-VO-A5: Total == len(targets); OK reflects actual state
// ---------------------------------------------------------------------------

func TestPromoGenerator_AllSucceed_OKTrue(t *testing.T) {
	targets := defaultTargets(t)
	vo := &stubVO{}
	gen := NewGenerator(mkTranslator(nil), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{Text: "Hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != len(targets) {
		t.Errorf("Total = %d, want %d", resp.Total, len(targets))
	}
	if resp.Success != len(targets) {
		t.Errorf("Success = %d, want %d", resp.Success, len(targets))
	}
	if resp.Failed != 0 {
		t.Errorf("Failed = %d, want 0", resp.Failed)
	}
	if !resp.OK {
		t.Errorf("OK = false, want true")
	}
	if len(resp.Results) != len(targets) {
		t.Errorf("len(Results) = %d, want %d", len(resp.Results), len(targets))
	}
	if len(vo.calls) != len(targets) {
		t.Errorf("voiceover called %d times, want %d", len(vo.calls), len(targets))
	}
	for _, r := range resp.Results {
		if !r.OK || r.DriveLink == "" {
			t.Errorf("per-language Result malformed: %+v", r)
		}
	}
}

func TestPromoGenerator_VoiceoverFailures_AccountedInFailed(t *testing.T) {
	targets := defaultTargets(t)
	const failCount = 2
	vo := &stubVO{
		byLocaleErr: make(map[string]error),
	}
	// voGen sees Locale (lower-cased by Normalize); the stub normalises
	// the key so per-locale failures opt-in via byLocaleErr after
	// lowercase.
	for i := 0; i < failCount; i++ {
		vo.byLocaleErr[lowercaseLocale(targets[i].Code)] = errors.New("tts quota exceeded")
	}
	gen := NewGenerator(mkTranslator(nil), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{Text: "Hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != len(targets) {
		t.Errorf("Total = %d, want %d", resp.Total, len(targets))
	}
	if resp.Success != len(targets)-failCount {
		t.Errorf("Success = %d, want %d", resp.Success, len(targets)-failCount)
	}
	if resp.Failed != failCount {
		t.Errorf("Failed = %d, want %d", resp.Failed, failCount)
	}
	if resp.OK {
		t.Errorf("OK = true, want false")
	}
	// Voiceover was attempted for every locale (fail logged inside voGen).
	if len(vo.calls) != len(targets) {
		t.Errorf("voiceover must be attempted for every locale in real-run; got %d", len(vo.calls))
	}

	// Verify the failing entries wrap ErrVoiceoverFailed.
	voFailCount := 0
	for _, r := range resp.Results {
		if r.Error == "" {
			continue
		}
		if errors.Is(errorOrSentinel(r), ErrVoiceoverFailed) {
			voFailCount++
		}
	}
	if voFailCount != failCount {
		t.Errorf("expected %d ErrVoiceoverFailed entries, got %d", failCount, voFailCount)
	}
}

// lowercaseLocale echoes Normalize()'s lower+trim step. Re-exported so
// tests do not depend on the production module's lowercase behaviour
// from a different angle.
func lowercaseLocale(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ---------------------------------------------------------------------------
// PR-VO-A6: strict translator (default fail-closed)
// ---------------------------------------------------------------------------

func TestPromoGenerator_AllTranslationsFail_OKFalse_Strict(t *testing.T) {
	targets := defaultTargets(t)
	realNames := make(map[string]translatorResp, len(targets))
	for _, t0 := range targets {
		realNames[t0.Name] = translatorResp{err: errors.New("ollama timeout")}
	}
	vo := &stubVO{
		byLocaleErr: make(map[string]error),
	}
	gen := NewGenerator(mkTranslator(realNames), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{Text: "Hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.OK {
		t.Fatal("OK must be false when all translations failed (strict)")
	}
	if resp.Total != len(targets) {
		t.Errorf("Total = %d, want %d", resp.Total, len(targets))
	}
	if resp.Success != 0 {
		t.Errorf("Success = %d, want 0", resp.Success)
	}
	if resp.Failed != len(targets) {
		t.Errorf("Failed = %d, want %d", resp.Failed, len(targets))
	}
	if len(resp.Results) != len(targets) {
		t.Errorf("len(Results) = %d, want %d (strict surfaces every failure)", len(resp.Results), len(targets))
	}
	if len(vo.calls) != 0 {
		t.Errorf("voiceover must NOT be called when every translation failed; got %d calls: %v", len(vo.calls), vo.calls)
	}
	for _, r := range resp.Results {
		if r.OK {
			t.Errorf("per-language Result.OK must be false; got %+v", r)
		}
		if r.DriveLink != "" {
			t.Errorf("DriveLink must be empty for translation-failed entry; got %q", r.DriveLink)
		}
		if !errors.Is(errorOrSentinel(r), ErrTranslationFailed) {
			t.Errorf("Result.Error must wrap ErrTranslationFailed; got %q", r.Error)
		}
	}
}

func TestPromoGenerator_MixedTranslationFailures_OKFalse_Strict(t *testing.T) {
	targets := defaultTargets(t)
	const txFailCount = 3
	realNames := make(map[string]translatorResp, len(targets))
	for i, t0 := range targets {
		if i < txFailCount {
			realNames[t0.Name] = translatorResp{err: errors.New("model unavailable")}
		}
	}
	vo := &stubVO{}
	gen := NewGenerator(mkTranslator(realNames), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{Text: "Hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.OK {
		t.Fatal("OK must be false when some translations failed (strict)")
	}
	if resp.Total != len(targets) {
		t.Errorf("Total = %d, want %d", resp.Total, len(targets))
	}
	wantSuccess := len(targets) - txFailCount
	if resp.Success != wantSuccess {
		t.Errorf("Success = %d, want %d", resp.Success, wantSuccess)
	}
	if resp.Failed != txFailCount {
		t.Errorf("Failed = %d, want %d (only translation failures in strict)", resp.Failed, txFailCount)
	}
	if len(resp.Results) != len(targets) {
		t.Errorf("len(Results) = %d, want %d (strict)", len(resp.Results), len(targets))
	}
	if len(vo.calls) != wantSuccess {
		t.Errorf("voiceover called %d times, want %d (only successful translations)", len(vo.calls), wantSuccess)
	}
}

// ---------------------------------------------------------------------------
// PR-VO-A6: opt-in AllowUntranslated — LITERAL semantics
// ---------------------------------------------------------------------------

func TestPromoGenerator_AllowUntranslated_LiteralSilentSkip(t *testing.T) {
	targets := defaultTargets(t)
	const txFailCount = 2
	realNames := make(map[string]translatorResp, len(targets))
	for i, t0 := range targets {
		if i < txFailCount {
			realNames[t0.Name] = translatorResp{err: errors.New("rate limited")}
		}
	}
	vo := &stubVO{}
	gen := NewGenerator(mkTranslator(realNames), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{
		Text:              "Hello",
		AllowUntranslated: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != len(targets) {
		t.Errorf("Total = %d, want %d", resp.Total, len(targets))
	}
	if resp.Failed != 0 {
		t.Errorf("Failed = %d, want 0 (LITERAL lenient: tx failure does NOT count)", resp.Failed)
	}
	if !resp.OK {
		t.Errorf("OK must be true in LITERAL lenient (failure was explicitly allowed); got false")
	}
	if resp.Success != len(targets)-txFailCount {
		t.Errorf("Success = %d, want %d", resp.Success, len(targets)-txFailCount)
	}
	if len(resp.Results) != len(targets)-txFailCount {
		t.Errorf("len(Results) = %d, want %d (lenient drops failed entries)", len(resp.Results), len(targets)-txFailCount)
	}
	if len(vo.calls) != len(targets)-txFailCount {
		t.Errorf("voiceover called %d times, want %d", len(vo.calls), len(targets)-txFailCount)
	}
}

func TestPromoGenerator_AllowUntranslated_AllSucceed_NoChange(t *testing.T) {
	targets := defaultTargets(t)
	vo := &stubVO{}
	gen := NewGenerator(mkTranslator(nil), vo, nil)
	resp, err := gen.Generate(context.Background(), &Request{
		Text:              "Hello",
		AllowUntranslated: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.OK || resp.Total != len(targets) || resp.Success != len(targets) || resp.Failed != 0 {
		t.Fatalf("strict baseline mismatch under lenient: %+v", resp)
	}
}

func TestPromoGenerator_AllowUntranslated_VoiceoverFailureStillCounts(t *testing.T) {
	targets := defaultTargets(t)
	const voFailCount = 2
	vo := &stubVO{
		byLocaleErr: make(map[string]error),
	}
	for i := 0; i < voFailCount; i++ {
		vo.byLocaleErr[lowercaseLocale(targets[i].Code)] = errors.New("tts engine down")
	}
	gen := NewGenerator(mkTranslator(nil), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{
		Text:              "Hello",
		AllowUntranslated: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Failed != voFailCount {
		t.Errorf("Failed = %d, want %d (voiceover failures always count)", resp.Failed, voFailCount)
	}
	if resp.OK {
		t.Errorf("OK must be false when voiceover fails (NOT gated by AllowUntranslated)")
	}
	if len(vo.calls) != len(targets) {
		t.Errorf("voiceover called %d times, want %d", len(vo.calls), len(targets))
	}
}

// ---------------------------------------------------------------------------
// Empty-translation guard
// ---------------------------------------------------------------------------

func TestPromoGenerator_EmptyTranslation_TreatedAsFailure_Strict(t *testing.T) {
	targets := defaultTargets(t)
	vo := &stubVO{}
	gen := NewGenerator(mkEmptyTranslator, vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{Text: "Hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.OK {
		t.Fatal("OK must be false when every translation returned empty payload")
	}
	if resp.Failed != len(targets) {
		t.Errorf("Failed = %d, want %d", resp.Failed, len(targets))
	}
	for _, r := range resp.Results {
		if !errors.Is(errorOrSentinel(r), ErrTranslationEmpty) {
			t.Errorf("Result.Error must wrap ErrTranslationEmpty; got %q", r.Error)
		}
	}
	if len(vo.calls) != 0 {
		t.Errorf("voiceover must NOT be called when translation was empty; got %d", len(vo.calls))
	}
}

func TestPromoGenerator_EmptyTranslation_LenientSilentSkip(t *testing.T) {
	vo := &stubVO{}
	gen := NewGenerator(mkEmptyTranslator, vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{
		Text:              "Hello",
		AllowUntranslated: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// LITERAL lenient: empty-payload is treated as translation
	// failure, so opt-in halves the path silently.
	if !resp.OK {
		t.Errorf("OK must be true under LITERAL lenient (empty-payload is a translation failure)")
	}
	if resp.Failed != 0 {
		t.Errorf("Failed = %d, want 0 in LITERAL lenient", resp.Failed)
	}
	if len(resp.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0 (lenient drops all empty-payload entries)", len(resp.Results))
	}
	if len(vo.calls) != 0 {
		t.Errorf("voiceover must NOT be called under lenient empty-payload; got %d", len(vo.calls))
	}
}

// ---------------------------------------------------------------------------
// PR-VO-A5: Dry-run accounting
// ---------------------------------------------------------------------------

func TestPromoGenerator_DryRun_AllSucceed_OKTrue(t *testing.T) {
	targets := defaultTargets(t)
	vo := &stubVO{}
	gen := NewGenerator(mkTranslator(nil), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{Text: "Hello", DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.OK || resp.Total != len(targets) || resp.Success != len(targets) || resp.Failed != 0 {
		t.Fatalf("dry-run all-success: %+v", resp)
	}
	for _, r := range resp.Results {
		if r.DriveLink != "" {
			t.Errorf("dry-run Result must have empty DriveLink; got %q", r.DriveLink)
		}
	}
}

func TestPromoGenerator_DryRun_TranslationFails_FailedCountsAndNoVoiceoverAttempted(t *testing.T) {
	targets := defaultTargets(t)
	const txFailCount = 2
	realNames := make(map[string]translatorResp, len(targets))
	for i, t0 := range targets {
		if i < txFailCount {
			realNames[t0.Name] = translatorResp{err: errors.New("dry-run failure")}
		}
	}
	vo := &stubVO{}
	gen := NewGenerator(mkTranslator(realNames), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{Text: "Hi", DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.OK {
		t.Fatal("OK must be false when some translations failed (dry-run strict)")
	}
	if resp.Total != len(targets) {
		t.Errorf("Total = %d, want %d", resp.Total, len(targets))
	}
	if resp.Failed != txFailCount {
		t.Errorf("Failed = %d, want %d", resp.Failed, txFailCount)
	}
	if resp.Success != len(targets)-txFailCount {
		t.Errorf("Success = %d, want %d", resp.Success, len(targets)-txFailCount)
	}
	if len(vo.calls) != 0 {
		t.Errorf("dry-run must NOT call voiceover; got %d", len(vo.calls))
	}
}

// ---------------------------------------------------------------------------
// PR-VO-A5: Custom Languages subset filtering
// ---------------------------------------------------------------------------

func TestPromoGenerator_LanguagesSubset_AccountingRespectsFilter(t *testing.T) {
	targets := defaultTargets(t)
	if len(targets) < 2 {
		t.Skip("default promo languages set too small to test subset")
	}
	subset := []string{targets[0].Code, targets[1].Code}
	vo := &stubVO{}
	gen := NewGenerator(mkTranslator(nil), vo, nil)

	resp, err := gen.Generate(context.Background(), &Request{
		Text:      "Hello",
		Languages: subset,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != len(subset) {
		t.Errorf("Total = %d, want %d (subset)", resp.Total, len(subset))
	}
	if resp.Success != len(subset) {
		t.Errorf("Success = %d, want %d", resp.Success, len(subset))
	}
	if !resp.OK || resp.Failed != 0 {
		t.Fatalf("subset all-success: %+v", resp)
	}
	if len(resp.Results) != len(subset) {
		t.Errorf("len(Results) = %d, want %d", len(resp.Results), len(subset))
	}
	if len(vo.calls) != len(subset) {
		t.Errorf("voiceover called %d times, want %d", len(vo.calls), len(subset))
	}
}

// ---------------------------------------------------------------------------
// Stale sentinel-error regression pin
// ---------------------------------------------------------------------------

// TestErrTranslationFailed_PrefixLocked: the sentinel recovers its
// canonical prefix even when wrapped via fmt.Errorf("%w: %v", ...).
// errors.Is reaches. If a future PR renames the sentinel or the
// wrapping machine drops %w, this test pin fails loudly.
//
// Note: t.Fatalf does NOT support %w (testing.common is fmt-v-only).
// We use %%w to print the literal "%w" token in the failure message,
// and shape the args as "sentinel=%v wrapped=%v" so the test logs
// stay grep-friendly even after a future directive break.
func TestErrTranslationFailed_PrefixLocked(t *testing.T) {
	inner := errors.New("ollama 500")
	wrapped := fmt.Errorf("%w: %v", ErrTranslationFailed, inner)
	if !errors.Is(wrapped, ErrTranslationFailed) {
		t.Fatalf("ErrTranslationFailed must be reached via errors.Is after %%w wrapping; got sentinel=%v wrapped=%v", ErrTranslationFailed, wrapped)
	}
	if !strings.HasPrefix(wrapped.Error(), ErrTranslationFailed.Error()+": ") {
		t.Fatalf("wrapped prefix must start with sentinel+': '; got %q", wrapped.Error())
	}

	wrappedEmpty := fmt.Errorf("%w", ErrTranslationEmpty)
	if !errors.Is(wrappedEmpty, ErrTranslationEmpty) {
		t.Fatalf("ErrTranslationEmpty must be reachable from a %%w-only wrap; got sentinel=%v wrapped=%v", ErrTranslationEmpty, wrappedEmpty)
	}
	if wrappedEmpty.Error() != ErrTranslationEmpty.Error() {
		t.Fatalf("ErrTranslationEmpty wrap should preserve the sentinel message verbatim; got %q", wrappedEmpty.Error())
	}

	wrappedVO := fmt.Errorf("%w: %v", ErrVoiceoverFailed, inner)
	if !errors.Is(wrappedVO, ErrVoiceoverFailed) {
		t.Fatalf("ErrVoiceoverFailed must be reached via errors.Is after %%w wrapping; got sentinel=%v wrapped=%v", ErrVoiceoverFailed, wrappedVO)
	}
}
