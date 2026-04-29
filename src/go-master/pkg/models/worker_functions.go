package models



// IsOnline returns true if the worker is online
func (w *Worker) IsOnline() bool {
	return w.Status == WorkerStatusOnline || w.Status == WorkerStatusIdle || w.Status == WorkerBusy
}

// HasCapability checks if the worker has a specific capability
func (w *Worker) HasCapability(cap WorkerCapability) bool {
	for _, c := range w.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// CanAcceptJob checks if the worker can accept a new job
func (w *Worker) CanAcceptJob() bool {
	if !w.IsOnline() {
		return false
	}
	return w.CurrentJobID == ""
}
