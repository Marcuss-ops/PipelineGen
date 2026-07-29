package voiceover

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/immutability"
)

// ── PR-VO-AUDIT-P01 helpers ─────────────────────────────────────────

// fail is the canonical failure normaliser. The previous implementation
// forward-stored the literal status (e.g. "tts_failed", "upload_failed",
// "missing_folder_id") and relied on downstream checks
// `if item.Status == "failed"` to gate the pipeline. Those checks missed
// every failure literal that wasn't exactly "failed", so a tts_failed in
// synthesizeStage could fall through Stage 2 with `Status == "tts_failed"`,
// then finalizeStage committed the record with `Status = "completed"` —
// silent false-success (the canonical audit P0.1 bug).
//
// The fix normalises every failure to Status(StatusFailed) regardless of
// the specific code, then records the code in item.Errors (omitempty-driven)
// so the forensic trail survives. The downstream aggregate check becomes
// `if item.Status == StatusFailed { ok = false }` — no more substring matching.
//
// Nil err case: the helper tolerates nil err (Error stays empty) so callers
// that want to mark a StatusFailed without a concrete message can still
// surface the failure code in Errors[] without lying about an empty message
// string.
func (i *BatchItem) fail(code FailureCode, err error) BatchItem {
	i.Status = Status(StatusFailed)
	if err != nil {
		i.Error = err.Error()
	}
	i.Errors = append(i.Errors, code)
	return *i
}

func (i BatchItem) isSuccessful() bool {
	return strings.TrimSpace(string(i.Status)) == "completed" && strings.TrimSpace(i.Error) == ""
}

// normalizeBatchRequest performs the canonical pre-flight normalisation
// for voiceover batch handlers, returning a fresh BatchRequest value.
// The previous implementation mutated `req_in.X = ...` in place, which
// made the typed copy-on-write primitive pkg/immutability.CloneWith[T]
// inspect-call impossible without a pointer-to-pointer round-trip.
//
// This rewrite swaps the signature from `*BatchRequest -> *BatchRequest`
// to `BatchRequest -> BatchRequest` (value semantics) — matching the
// canonical RunTagRequest pattern at artlist/run_service.go and keeping
// the migration honest: the function now returns the NORMALIZED value,
// and callers receive the clone directly.
//
// SHALLOW-CLONE SEMANTICS (see pkg/immutability/copy.go godlike/06
// SSOT docblock): mutation must be REPLACEMENT (`r.Languages =
// []Language{...}` / `r.VoiceOverrides = map[string]string{...}`)
// rather than index/deref, otherwise the changes bleed through to
// the shared slice/map backing storage. The caller's original
// BatchRequest value is byte-equivalent to its pre-call value for
// all primitive fields; composite fields are freshly bound.
func normalizeBatchRequest(req_in BatchRequest) BatchRequest {
	// INPUT-IMMUTABILITY-COPY-ON-WRITE-MIGRATION: see architecture/
	// deprecations.yaml for the godlike/07 audit-pin + the godlike
	// forward-pointer entry.
	cloned := immutability.CloneWith(req_in, func(r *BatchRequest) {
		if r.FilenameTemplate == "" {
			r.FilenameTemplate = "{slug}_{lang}.mp3"
		}
		// PR-VO-A2: route through the canonical asset.PipelineStrategy
		// normaliser so process.go's `req_in.Strategy == "replace"`
		// branch matches the single source of truth for the three
		// production strategies (verify / skip / replace). Unknown
		// inputs collapse to "verify" — the read-through-cache
		// default — which is the historically documented "no force"
		// behaviour of NormalizeStrategy.
		r.Strategy = string(asset.NormalizeStrategy(r.Strategy, false))
		if len(r.Languages) == 0 {
			// PR-VO-TYPED-PRIMITIVES (July 2026): untyped string
			// literal implicitly converts to the Language named
			// type — wire shape unchanged.
			r.Languages = []Language{"en"}
		}
		if len(r.VoiceOverrides) == 0 {
			if hydrated := voiceOverridesFromMetadata(r.Metadata); len(hydrated) > 0 {
				// REPLACEMENT (not index mutation): slice/map backing
				// storage is shared between cloned and original under
				// pkg/immutability.CloneWith SHALLOW-CLONE semantics.
				// See pkg/immutability/copy.go godlike/06 SSOT docblock.
				r.VoiceOverrides = map[string]string{}
				for k, v := range hydrated {
					r.VoiceOverrides[k] = v
				}
			}
		}
	})
	return cloned
}

func voiceOverridesFromMetadata(metadata map[string]any) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["voice_overrides"]
	if !ok || raw == nil {
		return nil
	}
	out := map[string]string{}
	switch typed := raw.(type) {
	case map[string]string:
		for lang, voice := range typed {
			if lang == "" || voice == "" {
				continue
			}
			out[lang] = voice
		}
	case map[string]any:
		for lang, value := range typed {
			voice, ok := value.(string)
			if !ok || lang == "" || voice == "" {
				continue
			}
			out[lang] = voice
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildRequestID() string {
	return "vo_" + time.Now().Format("20060102_150405") + "_" + randomSuffix(6)
}

func randomSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	size := (n + 1) / 2
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().Format("150405")
	}
	return hex.EncodeToString(buf)[:n]
}
