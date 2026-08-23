package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	translationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// translationUseCaseAdapter is the canonical thin struct that
// adapts the pure TranslateScriptSpec function to the typed
// ports.TranslationUseCase port. The composition root wires this
// adapter via NewTranslationUseCaseAdapter so the postprocessor
// layer can inject the canonical function value without importing
// the usecase package (breaks the adapters → usecase import cycle).
//
// godlike/06 SSOT (one canonical owner per fact): the adapter
// lives in usecase/ (NOT in adapters/) because the pure function
// it wraps lives in usecase/. The adapter's only job is the typed
// port satisfaction — every method body is a single
// byte-for-byte delegation.
//
// godlike/07 minimum-blast-radius: the zero-value struct satisfies
// the port (method is nil-safe at the call site); the
// composition root never wires a nil adapter. A future
// signature-drift in ports.TranslationUseCase surfaces as a
// build failure at the compile-time pin below.
type translationUseCaseAdapter struct{}

// NewTranslationUseCaseAdapter returns the canonical
// ports.TranslationUseCase adapter that delegates to the pure
// TranslateScriptSpec function. The composition root calls this
// once during TranslationProcessor wiring.
//
// godlike/07 minimum-blast-radius: the adapter is a zero-size
// struct (no fields, no state); the only method body is a single
// delegation line to the canonical pure function.
func NewTranslationUseCaseAdapter() ports.TranslationUseCase {
	return &translationUseCaseAdapter{}
}

// TranslateScriptSpec satisfies ports.TranslationUseCase by
// delegating to the canonical pure function byte-for-byte.
func (*translationUseCaseAdapter) TranslateScriptSpec(
	ctx context.Context,
	in *scriptpkg.ModelScriptOutputV1,
	evidence *scriptpkg.ClipEvidence,
	targetLang string,
	translator ports.ScriptTranslator,
) (out *scriptpkg.ModelScriptOutputV1, warnings []string, err error) {
	// Adapt the typed port (interface) to the function value the
	// pure function expects. Nil-port → returns
	// ErrTranslationTranslatorMissing via the adapter closure so the
	// pure function fails fast with the canonical typed sentinel.
	fn := translationpkg.TranslatorFunc(func(ctx context.Context, text, lang string) (string, error) {
		if translator == nil {
			return "", ErrTranslationTranslatorMissing
		}
		return translator.Translate(ctx, text, lang)
	})
	return TranslateScriptSpec(ctx, in, evidence, targetLang, fn)
}

// Compile-time pin: translationUseCaseAdapter satisfies
// ports.TranslationUseCase. A future signature drift in the port
// surfaces as a build failure, not a runtime panic.
var _ ports.TranslationUseCase = (*translationUseCaseAdapter)(nil)

// translationReasonClassifierAdapter is the canonical thin struct
// that adapts the pure ClassifyReason function to the typed
// ports.TranslationReasonClassifier port. Same rationale as
// translationUseCaseAdapter (composition-root-injected, breaks
// the adapters → usecase import cycle).
//
// godlike/07 typed-error contract: the adapter is a zero-size
// struct; the only method body is a single delegation line.
type translationReasonClassifierAdapter struct{}

// NewTranslationReasonClassifierAdapter returns the canonical
// ports.TranslationReasonClassifier adapter that delegates to the
// pure ClassifyReason function. The composition root calls this
// once during TranslationProcessor wiring.
func NewTranslationReasonClassifierAdapter() ports.TranslationReasonClassifier {
	return &translationReasonClassifierAdapter{}
}

// ClassifyReason satisfies ports.TranslationReasonClassifier by
// delegating to the canonical pure function byte-for-byte.
func (*translationReasonClassifierAdapter) ClassifyReason(s string) ports.TranslationWarningReason {
	return ClassifyReason(s)
}

// Compile-time pin: translationReasonClassifierAdapter satisfies
// ports.TranslationReasonClassifier.
var _ ports.TranslationReasonClassifier = (*translationReasonClassifierAdapter)(nil)
