package cliprender

// worker_options.go — With* port-injection chain for the clip.render Worker.
// Extracted from worker.go (godlike/08 strict 600-LOC cap); each setter
// returns the receiver so call sites stay chainable.

// WithSubtitleCompiler attaches the canonical ASS compiler. Optional: when
// subtitles are disabled no compiler is needed; when enabled and nil, the
// worker fails closed with ErrSubtitleCompileUnavailable (never a plan
// without its ASS artifact).
func (w *Worker) WithSubtitleCompiler(c SubtitleCompiler) *Worker {
	if w != nil {
		w.subtitles = c
	}
	return w
}

// WithRenderExecutor attaches the RenderingGen/Chronon render boundary. A
// missing executor remains a typed failure; a sealed plan is never reported
// as a rendered clip. If the composition runtime also exposes the optional
// AsyncCompletionProvider capability, this is the ONE discovery point for the
// continuation dependencies — no second wiring registry is introduced.
func (w *Worker) WithRenderExecutor(r RenderExecutor) *Worker {
	if w != nil {
		w.renderer = r
		if provider, ok := r.(AsyncCompletionProvider); ok {
			store, enqueuer, enabled := provider.AsyncCompletionDependencies()
			if enabled {
				w.continuationStore = store
				w.continuationEnqueuer = enqueuer
				w.asyncCompletion = true
			}
		}
	}
	return w
}

// WithContinuationStore attaches the durable CAS-backed resume store used by
// the asynchronous submit/settle boundary.
func (w *Worker) WithContinuationStore(s ContinuationStore) *Worker {
	if w != nil {
		w.continuationStore = s
	}
	return w
}

// WithContinuationEnqueuer attaches the durable job enqueue port for the
// settle continuation. The worker stays capability-only; composition owns the
// concrete job service and parent-link injection.
func (w *Worker) WithContinuationEnqueuer(e ContinuationEnqueuer) *Worker {
	if w != nil {
		w.continuationEnqueuer = e
	}
	return w
}

// WithRenderPublisher attaches the canonical Drive publication + SQLite
// commit boundary. Production composition must wire it before exposing the
// route; tests may omit it when exercising preparation only.
func (w *Worker) WithRenderPublisher(p RenderPublisher) *Worker {
	if w != nil {
		w.publisher = p
	}
	return w
}

// WithDestinationFolderResolver attaches the canonical Drive leaf-folder
// resolver. Optional: requests that publish directly into a pre-resolved
// destination.drive_folder_id never need it. A request that carries
// destination.subfolder_name without a wired resolver fails closed at
// Handle time (typed error) — the publisher never creates folders, so a
// missing resolver must never degrade into a silent root upload.
func (w *Worker) WithDestinationFolderResolver(r DestinationFolderResolver) *Worker {
	if w != nil {
		w.folderResolver = r
	}
	return w
}

// WithOverlaySegmentResolver attaches the overlay.render artifact resolver
// (render_job_id → materialized segment). Optional: when the request
// declares no overlay no resolver is needed; when an overlay IS declared and
// no resolver is wired, the worker fails closed with a typed error — a
// phantom segment is never composited.
func (w *Worker) WithOverlaySegmentResolver(r OverlaySegmentResolver) *Worker {
	if w != nil {
		w.overlayResolver = r
	}
	return w
}

// WithOutputProber attaches the post-render byte probe. When wired, the worker
// certifies actual bytes via ProbeOutput→ValidateContract before Publish.
// Optional in tests; required in production.
func (w *Worker) WithOutputProber(p OutputProber) *Worker {
	if w != nil {
		w.outputProber = p
	}
	return w
}

// Handle is the job.Handler-shaped entry point bound to the Master.
