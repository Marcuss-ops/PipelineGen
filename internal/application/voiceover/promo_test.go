// Package voiceover — promo_test.go (June 2026 cutover: BLOC5.3
// bridge-cutover test removed).
//
// The previous TestPromo_AccodesCanonicalChild audited the
// voiceoverGenBridge → ProcessVoiceoverItemUseCase invariant (BLOC5.3
// commit-1, June 2026). ProcessVoiceoverItemUseCase was never
// committed in this branch (forward-deferred to BLOC5.4), so the
// bridge was reverted to the legacy Service.GenerateWithDestination
// route, and this test file is reduced to a package-level marker so
// the directory remains a valid Go package.
//
// When BLOC5.4 lands the canonical per-item pipeline, regenerate the
// bridge-cutover audit pin here using the narrow VoiceoverItemExecutor
// port (see voiceover/ports.go).
package voiceover
