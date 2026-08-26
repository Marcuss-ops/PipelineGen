package defaults

import (
)

// VoiceoverConfig is the canonical SSOT for voiceover generation
// defaults. Pre-fix scattered literals this SSOT replaces (June
// 2026, Step 4 PR2 — DRIFT-DEFAULTS-VOICEOVER):
//
//   - "{slug}_{lang}.mp3" filename template, inlined at
//     internal/application/voiceover/types.go::normalizeBatchRequest.
//   - "en" default language, inlined in the same function
//     (req.Languages = []string{"en"}).
//   - "verify" default strategy via
//     asset.NormalizeStrategy(req.Strategy, false), also inlined.
//
// Every consumer MUST read from DefaultVoiceoverConfig() rather than
// re-implementing these literals inline. A future "rename mp3 to
// wav" or "switch default language to Italian" change is then a
// one-line edit; pre-fix it required grep + reasoning about which
// call sites must agree.
//
// The request-ID format ("vo_" prefix + 6-hex random suffix) is
// intentionally NOT part of this SSOT: it is an internal detail of
// voiceover/types.go::buildRequestID, not a user-facing default. A
// future ULID-style refactor of buildRequestID would touch the
// types.go literal directly without needing an SSOT change.
//
// Shape is intentionally tiny (3 leaf fields) to keep pkg/defaults
// leaf-only: zero imports from internal/, only consumed by callers
// crossing the infra→application seam.
type VoiceoverConfig struct {
	// DefaultFilenameTemplate is the template applied when
	// BatchRequest.FilenameTemplate is empty. Supports the same
	// placeholders the legacy inlined template did ({slug}, {lang},
	// {time}, {date}).
	DefaultFilenameTemplate string

	// DefaultStrategy is the pipeline strategy used when
	// BatchRequest.Strategy is empty. Must be one of the canonical
	// asset.PipelineStrategy values: "verify" / "skip" / "replace".
	// "verify" is the legacy default; legacy callers were
	// NormalizeStrategy(req.Strategy, false) which collapses empty
	// input to "verify".
	DefaultStrategy string

	// DefaultLanguage is the BCP-47 code applied when
	// BatchRequest.Languages is empty. Legacy callers defaulted to
	// "en"; Italian-only deployments can flip this SSOT without
	// touching any call site.
	DefaultLanguage string
}

// DefaultVoiceoverConfig returns the canonical DRIFT-DEFAULTS-VOICEOVER
// SSOT. Treat the returned value as immutable per consumer site (no
// process-global mutation — copy and adjust locally if needed).
func DefaultVoiceoverConfig() VoiceoverConfig {
	return VoiceoverConfig{
		DefaultFilenameTemplate: "{slug}_{lang}.mp3",
		DefaultStrategy:         "verify",
		DefaultLanguage:         "en",
	}
}
