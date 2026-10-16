// Package overlays — animation_selector.go: single owner for animation selection.
//
// Fase 3 (piano unificazione animazioni): BuildPlan assegna motion provvisorie
// in più punti, entity_image_compose riassegna, animation_policy ricampiona.
// Questo file introduce l'unico servizio di selezione:
//
//	Pipeline: BuildSemanticItems → ResolveAssets → ApplyBudget →
//	  ComposeImageGroups → ResolveAnimationSelections (qui) →
//	  ComposeChains → ValidateAndSeal → EnqueueRenderJobs
//
// La selezione avviene DOPO budget+composizione (solo allora si conosce il
// numero di asset 1-5 e il layout). Le trasformazioni successive (chain,
// style) sono validazione/adattamento tecnico registrato, mai nuovo sampling.
//
// Comportamento: riusa gli esistenti selettori deterministici come strategie
// interne, quindi i golden restano byte-identici. Le modalità auto/prefer/
// exact hanno contratti distinti: prefer ammette fallback, exact fallisce
// chiuso (mai sostituzione silenziosa da no-repeat/sampler/composizione).
package overlays

import (
	"context"
	"fmt"
	"strings"
)

// SelectionMode è il contratto canonico auto/prefer/exact (§4 del piano).
type SelectionMode string

const (
	// SelectionAuto: il sistema sceglie una motion compatibile e riproducibile.
	SelectionAuto SelectionMode = "auto"
	// SelectionPrefer: prova la scelta richiesta, fallback esplicitamente autorizzato.
	SelectionPrefer SelectionMode = "prefer"
	// SelectionExact: usa esattamente la scelta oppure restituisce errore.
	SelectionExact SelectionMode = "exact"
)

// AnimationSelectionRequest è l'unico input del selector. Il chiamante non
// ricostruisce mai un pool: dichiara target, scena, elemento, seed e contesto.
type AnimationSelectionRequest struct {
	Target    string
	Mode      SelectionMode
	SceneID   string
	ElementID string
	Seed      string
	// Words serve al lane testuale (compatibilità per cardinalità).
	Words int
	// Family restringe al lane editoriale (es. phrase family), "" = nessuna.
	Family string
	// Ordinal è la posizione no-repeat nella sequenza del run.
	Ordinal int
	// Limit è il corrispondente laneAnimationCount (0 = default certificato).
	Limit int
	// ExplicitPool è la pool caller-supplied (phrase_motions espliciti); nil = catalogo.
	ExplicitPool []string
	// RequestedMotion è la motion richiesta per prefer/exact.
	RequestedMotion string
	// CatalogDigest pinna la vista capacità runtime versionata.
	CatalogDigest string
}

// AnimationSelection è il risultato con provenance (§9: report diagnostico).
type AnimationSelection struct {
	MotionID      string
	Params        map[string]any
	CatalogDigest string
	Mode          SelectionMode
	Reason        string
}

// AnimationSelector è l'unico owner della scelta in PipelineGen.
type AnimationSelector interface {
	Resolve(ctx context.Context, req AnimationSelectionRequest) (AnimationSelection, error)
}

// DefaultAnimationSelector riusa gli algoritmi esistenti come strategie,
// eliminando hash/duplicati sparsi senza riscrivere le regole di varietà.
type DefaultAnimationSelector struct {
	CatalogDigest string
}

func (s DefaultAnimationSelector) Resolve(_ context.Context, req AnimationSelectionRequest) (AnimationSelection, error) {
	mode := req.Mode
	if mode == "" {
		mode = SelectionAuto
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return AnimationSelection{}, fmt.Errorf("animation selector: target is required")
	}
	seed := strings.TrimSpace(req.Seed)
	scene := strings.TrimSpace(req.SceneID)
	if scene == "" {
		scene = "run"
	}
	// exact: nessuna sostituzione silenziosa da sampler/no-repeat/composizione.
	if mode == SelectionExact {
		id := strings.TrimSpace(req.RequestedMotion)
		if id == "" {
			return AnimationSelection{}, fmt.Errorf("animation selector: exact mode requires requested motion for target %q", target)
		}
		return AnimationSelection{
			MotionID:      id,
			CatalogDigest: s.CatalogDigest,
			Mode:          mode,
			Reason:        fmt.Sprintf("exact target=%s scene=%s element=%s", target, req.SceneID, req.ElementID),
		}, nil
	}
	// prefer: prova la richiesta, fallback esplicitamente autorizzato.
	if mode == SelectionPrefer && strings.TrimSpace(req.RequestedMotion) != "" {
		return AnimationSelection{
			MotionID:      strings.TrimSpace(req.RequestedMotion),
			CatalogDigest: s.CatalogDigest,
			Mode:          mode,
			Reason:        fmt.Sprintf("prefer requested target=%s scene=%s element=%s", target, req.SceneID, req.ElementID),
		}, nil
	}
	// auto: sampling deterministico per target, stessa pipeline di filtri:
	// capability lookup → compatibility → deterministic sampling → no-repeat
	// (il no-repeat è applicato dal chiamante via Ordinal, qui mai reseed).
	var motion string
	var reason string
	switch target {
	case "important_phrase", "text_phrase", "phrase":
		if req.Words > 0 && req.Words < 6 {
			motion = selectShortPhraseMotionLimited(seed, "short_phrase_group", req.Ordinal, req.Words, req.ExplicitPool, req.Limit)
		} else if req.Words >= 6 {
			motion = selectLongPhraseMotionLimited(seed, "run", req.Ordinal, req.ExplicitPool, req.Limit)
		} else {
			motion = selectPhraseMotionLimited(seed, "run", req.Ordinal, req.ExplicitPool, req.Limit)
		}
		reason = fmt.Sprintf("auto phrase words=%d ordinal=%d", req.Words, req.Ordinal)
	case "heavy_phrase":
		motion = selectHeavyPhraseMotionLimited(seed, "run", req.Ordinal, req.ExplicitPool, req.Limit)
		reason = fmt.Sprintf("auto heavy ordinal=%d", req.Ordinal)
	case "short_phrase":
		motion = selectShortPhraseMotionLimited(seed, "short_phrase_group", req.Ordinal, req.Words, req.ExplicitPool, req.Limit)
		reason = fmt.Sprintf("auto short words=%d ordinal=%d", req.Words, req.Ordinal)
	case "image":
		pool := req.ExplicitPool
		if len(pool) == 0 {
			pool = automaticSingleImageMotionCandidates
		}
		motion = selectImageMotion(seed, scene, req.Ordinal, limitedMotionPool(pool, req.Limit))
		reason = fmt.Sprintf("auto image ordinal=%d", req.Ordinal)
	case "caption":
		motion = SelectEntityCaptionMotionAt(seed, scene, req.Ordinal)
		reason = fmt.Sprintf("auto caption ordinal=%d", req.Ordinal)
	case "entity_image":
		pool := generatedEntityImageMotionCandidates
		motion = rotateMotionAtOffset(0, req.Ordinal, limitedMotionPool(pool, req.Limit))
		reason = fmt.Sprintf("auto entity_image ordinal=%d", req.Ordinal)
	case "date":
		pool := DatePresentationMotionCandidates()
		if len(pool) == 0 {
			break
		}
		motion = pool[SeededGroupIndex(seed+"\x00"+scene, target, req.ElementID, len(pool))]
		reason = fmt.Sprintf("auto date element=%s", req.ElementID)
	case "metric", "number":
		pool := MetricPresentationMotionCandidates()
		if len(pool) == 0 {
			break
		}
		motion = pool[SeededGroupIndex(seed+"\x00"+scene, target, req.ElementID, len(pool))]
		reason = fmt.Sprintf("auto metric element=%s", req.ElementID)
	case "map", "map_camera", "map_composition", "map_label":
		pool := mapMotionIDs()
		if len(pool) == 0 {
			break
		}
		motion = pool[SeededGroupIndex(seed+"\x00"+scene, target, req.ElementID, len(pool))]
		reason = fmt.Sprintf("auto map target=%s element=%s", target, req.ElementID)
	default:
		return AnimationSelection{}, fmt.Errorf("animation selector: unsupported target %q", target)
	}
	if motion == "" {
		return AnimationSelection{}, fmt.Errorf("animation selector: no compatible motion for target %q", target)
	}
	return AnimationSelection{
		MotionID:      motion,
		CatalogDigest: s.CatalogDigest,
		Mode:          mode,
		Reason:        reason,
	}, nil
}

// ResolveAnimationSelections è il singolo passaggio owner (§3, step 5).
// Oggi è validazione (fail-closed): verifica che ogni item semanticamente
// identificabile abbia una motion e che chain/container non ne inventino di
// nuove. Non ricampiona: le scelte restano quelle del planner/compose finché
// la migrazione per-lane (frasi→immagini→caption→mappe→number/metric) non
// sposta ogni lane dentro DefaultAnimationSelector.Resolve.
func ResolveAnimationSelections(items []OverlayItem) error {
	for i := range items {
		it := &items[i]
		// Chain container: la motion autorevole vive nei segmenti/child,
		// il parent deve restare vuoto (chain_coalesce.fuseChain).
		if IsChainItemKind(it.Kind) {
			if strings.TrimSpace(it.MotionID) != "" {
				return fmt.Errorf("animation selector: chain container %q must not carry MotionID %q (segments own it)", it.ID, it.MotionID)
			}
			continue
		}
		// Composite entity_image con layer: ogni child possiede la motion.
		if it.Kind == string(KindEntityImage) && len(it.ImageLayers) > 0 {
			if strings.TrimSpace(it.MotionID) != "" {
				return fmt.Errorf("animation selector: composite parent %q must not carry MotionID (child layers own it)", it.ID)
			}
			for _, layer := range it.ImageLayers {
				if strings.TrimSpace(layer.MotionID) == "" {
					return fmt.Errorf("animation selector: composite child %q:%q missing MotionID", it.ID, layer.ID)
				}
			}
			continue
		}
	}
	return nil
}
