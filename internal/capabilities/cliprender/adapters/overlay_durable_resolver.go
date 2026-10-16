package adapters

import (
	"context"
	"fmt"
)

// Fase 6 (piano unificazione): migrare il resolver clip dalla cache locale
// (render_key → <root>/overlays/…) ad artifact durevoli certificati prodotti
// dalla queue remota (jobID → ArtifactManifest). Questo file introduce il
// contratto senza rimuovere il percorso locale: il cutover avviene solo dopo
// aver verificato tutti i consumer (clip.render continua a comporre in un
// singolo passaggio Chronon). Il renderer locale GPU in PipelineGen
// (rendering_runtime.go / overlay_handlers.go) resta in sola lettura finché
// i job overlay.render locali non hanno più consumer.
type DurableOverlayArtifact struct {
	JobID      string
	ArtifactID string
	URI        string
	SHA256     string
}

// DurableOverlayResolver risolve artifact certificati durevoli del backend
// remoto. Implementazioni: queue remota (RenderingGen), object store, Drive.
type DurableOverlayResolver interface {
	Resolve(ctx context.Context, jobID string) (DurableOverlayArtifact, error)
}

// ResolveWithDurableFallback prova prima il resolver durevole, poi la cache
// locale (compatibilità durante la migrazione). Ritorna errore fail-closed se
// entrambi mancano: nessun segmento fantasma raggiunge mai il piano sigillato.
func ResolveWithDurableFallback(ctx context.Context, durable DurableOverlayResolver, jobID string, localFallback func() (string, error)) (string, error) {
	if durable != nil && jobID != "" {
		if art, err := durable.Resolve(ctx, jobID); err == nil && art.URI != "" {
			return art.URI, nil
		}
	}
	if localFallback == nil {
		return "", fmt.Errorf("overlay durable resolver: no artifact for job %q and no local fallback", jobID)
	}
	return localFallback()
}
