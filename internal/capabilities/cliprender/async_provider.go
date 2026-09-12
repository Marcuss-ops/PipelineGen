package cliprender

// AsyncCompletionProvider is an optional capability exposed by a render
// executor wrapper at the composition boundary. It keeps platform wiring out
// of Worker while allowing WithRenderExecutor to discover the durable
// continuation dependencies from the SAME runtime object.
//
// The boolean is explicit so the provider can exist while the production
// cutover switch is disabled. When enabled, nil dependencies remain fail-
// closed in Worker.handleAsyncSubmit.
type AsyncCompletionProvider interface {
	AsyncCompletionDependencies() (ContinuationStore, ContinuationEnqueuer, bool)
}
