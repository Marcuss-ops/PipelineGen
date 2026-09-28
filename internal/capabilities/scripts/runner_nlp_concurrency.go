// Package scriptgeneration — runner_nlp_concurrency.go owns the Runner's
// NLP/entity-extraction fan-out setter. Split out of runner_deps.go per the
// max_lines_per_file_strict gate.
package scriptgeneration

// SetNLPConcurrency sets the NLP/entity-extraction fan-out width used by the
// SceneReadyCoordinator and the translated-NLP pass. Values <= 0 fall back to
// the certified default (DefaultNLPConcurrency).
//
// It must mirror the Ollama NLP gate capacity: the gate bounds the provider
// calls while this bound sizes the coordinator's own slot pool. Wiring only the
// gate (the pre-existing behaviour) left the coordinator hard-coded at the
// certified default, so lowering scripts.nlp_concurrency to protect the TTS/LLM
// inference budget had no effect on the fan-out that actually issues the work.
func (r *Runner) SetNLPConcurrency(concurrency int) {
	if r == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = DefaultNLPConcurrency
	}
	r.nlpConcurrency = concurrency
}
