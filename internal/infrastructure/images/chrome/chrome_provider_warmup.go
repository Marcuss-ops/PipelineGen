// Package images — chrome_provider_warmup.go owns explicit warmup hooks
// for the persistent Chrome/Playwright worker.
package images

import "context"

// Warmup ensures the persistent worker has been started and is ready to
// receive generate requests. It is safe to call repeatedly.
func (p *ChromeImageProvider) Warmup(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ensureStarted(ctx)
}

// TriggerPrewarm satisfies the generation-provider warmup seam. The current
// Chrome worker only needs to be started once, so count is informational.
func (p *ChromeImageProvider) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	_ = jobID
	_ = count
	_ = p.Warmup(ctx)
}
