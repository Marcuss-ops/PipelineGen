package audioasset

import (
	"strings"

	"go.uber.org/zap"
)

// NewProcessorWithBridge constructs a Processor bound to an explicit
// sidecar script and provider identity. The local-TTS pilot passes an
// empty legacyScript on purpose: when the local worker is unavailable the
// call MUST fail, never fall back to the Edge CLI (which would produce
// audio attributed to the local provider).
func NewProcessorWithBridge(
	pythonScriptsDir, bridgeScript, legacyScript, providerID string,
	log *zap.Logger,
) *Processor {
	if strings.TrimSpace(bridgeScript) == "" {
		panic("audioasset.NewProcessorWithBridge: bridgeScript is required")
	}
	if strings.TrimSpace(providerID) == "" {
		providerID = DefaultTTSProviderID
	}
	return &Processor{
		pythonScriptsDir: pythonScriptsDir,
		log:              log,
		bridgeScript:     bridgeScript,
		legacyScript:     legacyScript,
		providerID:       providerID,
		requestSlots:     make(chan struct{}, DefaultTTSRequestConcurrency),
		postprocessSlots: make(chan struct{}, DefaultTTSPostprocessConcurrency),
	}
}

// Canonical sidecar identity for the Edge bridge. The local pilot passes
// its own values to NewProcessorWithBridge.
const (
	DefaultTTSBridgeScript = "bridges/tts_edge_server.py"
	DefaultTTSLegacyScript = "bridges/tts_edge.py"
	DefaultTTSProviderID   = "edge-tts"
)

// ProviderID returns the canonical provider identity attached to every
// synthesis result. Empty values fail back to the Edge identity so a
// zero-value Processor (tests) stays deterministic.
func (p *Processor) ProviderID() string {
	if p == nil {
		return ""
	}
	if strings.TrimSpace(p.providerID) == "" {
		return DefaultTTSProviderID
	}
	return p.providerID
}

// bridgeScriptPath returns the configured bridge script, falling back to
// the Edge default for zero-value Processors constructed in tests.
func (p *Processor) bridgeScriptPath() string {
	if p == nil || strings.TrimSpace(p.bridgeScript) == "" {
		return DefaultTTSBridgeScript
	}
	return p.bridgeScript
}

// SetBridgeEnv merges extra environment variables into the sidecar process
// (per-key override). Called before the first Generate; safe to call with
// nil/empty. The map is copied so the caller cannot mutate live state.
func (p *Processor) SetBridgeEnv(env map[string]string) {
	if p == nil {
		return
	}
	copied := make(map[string]string, len(env))
	for key, value := range env {
		copied[key] = value
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bridgeEnv = copied
}

// SetRequestConcurrency bounds simultaneous requests sent to the persistent
// Edge TTS worker. It is deliberately separate from the application fan-out:
// the sidecar is async, but the remote service may throttle excessive sockets.
func (p *Processor) SetRequestConcurrency(n int) {
	if p == nil {
		return
	}
	if n <= 0 {
		n = DefaultTTSRequestConcurrency
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requestSlots = make(chan struct{}, n)
}
