package client

// AdmissionObserver receives the admission samples of one Ollama endpoint.
//
// It is a consumer-side port on purpose: this package owns the budget and must
// not import the telemetry package, so the composition root injects an adapter
// (internal/app/wiring → internal/platform/observability) exactly like the
// canonical metrics-injection pattern used elsewhere in the repo. A nil
// observer is valid: the budget works with telemetry disabled.
type AdmissionObserver interface {
	// AdmissionChanged reports the endpoint's ceiling and how many requests are
	// admitted right now. Called after every admission state change, including
	// the return to zero.
	AdmissionChanged(endpoint string, limit int, inFlight int64)
	// AdmissionDeferred reports ONE request that had to wait for a slot: the
	// endpoint was already at its ceiling.
	AdmissionDeferred(endpoint string)
}

// admissionObserverBox avoids storing a nil interface in an atomic.Pointer
// (which would panic) while keeping the read on the hot path lock-free.
type admissionObserverBox struct {
	observer AdmissionObserver
}

// setObserver installs the observation sink for this endpoint. Passing nil
// disables telemetry. The last write wins, which is correct because every
// client for the same endpoint shares this limiter and an equivalent adapter.
func (a *ollamaAdmission) setObserver(observer AdmissionObserver) {
	if a == nil {
		return
	}
	if observer == nil {
		a.observer.Store(nil)
		return
	}
	a.observer.Store(&admissionObserverBox{observer: observer})
}

// observerOrNil returns the installed observer, or nil when telemetry is off.
func (a *ollamaAdmission) observerOrNil() AdmissionObserver {
	if a == nil {
		return nil
	}
	box := a.observer.Load()
	if box == nil {
		return nil
	}
	return box.observer
}

// notifyAdmissionChanged publishes the current ceiling and in-flight count.
// It is called from the admission hot path, so it must stay allocation-free in
// the common case (a map lookup inside the Prometheus GaugeVec).
func (a *ollamaAdmission) notifyAdmissionChanged() {
	observer := a.observerOrNil()
	if observer == nil {
		return
	}
	observer.AdmissionChanged(a.endpoint, a.limit, a.inFlight.Load())
}

// notifyAdmissionDeferred publishes one queued request.
func (a *ollamaAdmission) notifyAdmissionDeferred() {
	observer := a.observerOrNil()
	if observer == nil {
		return
	}
	observer.AdmissionDeferred(a.endpoint)
}

// SetAdmissionObserver installs the telemetry sink for this client's endpoint.
//
// The limiter is shared by every client pointing at the same Ollama URL, so one
// call covers the pools built by the other wiring bundles (embed client, stock
// enrichment, creator) as long as they share the endpoint.
func (c *Client) SetAdmissionObserver(observer AdmissionObserver) {
	if c == nil || c.admission == nil {
		return
	}
	c.admission.setObserver(observer)
	c.admission.notifyAdmissionChanged()
}

// hasAdmissionObserver reports whether telemetry is wired (test/diagnostic
// helper; it never affects admission decisions).
func (c *Client) hasAdmissionObserver() bool {
	return c != nil && c.admission != nil && c.admission.observerOrNil() != nil
}
