package operator

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// IndexHealthInput carries the minimal facts needed to decide what the
// operator UI should display for the "index health" column.
type IndexHealthInput struct {
	IndexState          asset.IndexState
	ContentHash         string
	IndexedContentHash  string
	PendingOutboxEvents int
}

// ResolveIndexHealth is the single source of truth for translating the
// backend index state into a visual projection. It is deterministic,
// side-effect free, and owned entirely by the application layer.
//
// Rules (in order):
//
//   - IndexState = NOT_INDEXABLE → NOT_INDEXABLE
//   - IndexState = EMBEDDING     → EMBEDDING
//   - IndexState = INDEXING      → INDEXING
//   - IndexState = INDEXING_SKIPPED_NO_INDEXER → RETRY_WAIT
//   - IndexState = INDEXED + current hash == indexed hash → INDEXED
//   - IndexState = INDEXED + current hash != indexed hash → STALE
//   - IndexState in {EMBEDDING_FAILED, INDEXING_FAILED} → FAILED
//   - IndexState is a deletion state                   → DELETED
//   - otherwise                                        → UNKNOWN
func ResolveIndexHealth(input IndexHealthInput) IndexHealthView {
	switch input.IndexState {
	case asset.StateNotIndexable:
		return IndexHealthView{
			Code:        string(IndexHealthNotIndexable),
			Label:       "Non indicicizzabile",
			Severity:    "neutral",
			Description: "L'asset non è eleggibile per l'indicizzazione.",
			Terminal:    true,
			Retryable:   false,
		}
	case asset.StateDiscovered, asset.StateIndexPending:
		return IndexHealthView{
			Code:        string(IndexHealthPending),
			Label:       "In attesa",
			Severity:    "info",
			Description: "L'asset è in coda per l'elaborazione.",
			Terminal:    false,
			Retryable:   false,
		}
	case asset.StateEmbedding:
		return IndexHealthView{
			Code:        string(IndexHealthEmbedding),
			Label:       "Embedding in corso",
			Severity:    "info",
			Description: "Generazione degli embedding in corso.",
			Terminal:    false,
			Retryable:   false,
		}
	case asset.StateEmbedded:
		if input.PendingOutboxEvents > 0 {
			return IndexHealthView{
				Code:        string(IndexHealthPending),
				Label:       "In attesa",
				Severity:    "info",
				Description: "Embedding generati ma l'evento di indicizzazione è ancora in coda.",
				Terminal:    false,
				Retryable:   false,
			}
		}
		return IndexHealthView{
			Code:        string(IndexHealthIndexing),
			Label:       "Pronto per l'indicizzazione",
			Severity:    "info",
			Description: "Embedding generati; in attesa dell'upsert su Qdrant.",
			Terminal:    false,
			Retryable:   false,
		}
	case asset.StateIndexing:
		return IndexHealthView{
			Code:        string(IndexHealthIndexing),
			Label:       "Indicizzazione in corso",
			Severity:    "info",
			Description: "Upsert su Qdrant in corso.",
			Terminal:    false,
			Retryable:   false,
		}
	case asset.StateIndexingSkippedNoIndexer:
		return IndexHealthView{
			Code:        string(IndexHealthRetryWait),
			Label:       "Attesa retry",
			Severity:    "warning",
			Description: "L'indicizzatore non è disponibile; verrà ritentato automaticamente.",
			Terminal:    false,
			Retryable:   true,
		}
	case asset.StateIndexed:
		if input.ContentHash != "" && input.IndexedContentHash != "" &&
			input.ContentHash != input.IndexedContentHash {
			return IndexHealthView{
				Code:        string(IndexHealthStale),
				Label:       "Stale",
				Severity:    "warning",
				Description: "Il punto Qdrant è associato a un hash precedente.",
				Terminal:    false,
				Retryable:   true,
			}
		}
		return IndexHealthView{
			Code:        string(IndexHealthIndexed),
			Label:       "Indicizzato",
			Severity:    "success",
			Description: "Il punto Qdrant è aggiornato e coerente.",
			Terminal:    true,
			Retryable:   false,
		}
	case asset.StateEmbeddingFailed, asset.StateIndexingFailed:
		return IndexHealthView{
			Code:        string(IndexHealthFailed),
			Label:       "Fallito",
			Severity:    "error",
			Description: "L'indicizzazione è fallita e richiede intervento.",
			Terminal:    true,
			Retryable:   true,
		}
	case asset.StateIndexDeletePending, asset.StateDELETED:
		return IndexHealthView{
			Code:        string(IndexHealthDeleted),
			Label:       "Eliminato",
			Severity:    "neutral",
			Description: "Il punto Qdrant è stato rimosso.",
			Terminal:    true,
			Retryable:   false,
		}
	case asset.StateIndexFailed:
		return IndexHealthView{
			Code:        string(IndexHealthFailed),
			Label:       "Fallito",
			Severity:    "error",
			Description: "Stato di indicizzazione fallito (compatibilità legacy).",
			Terminal:    true,
			Retryable:   true,
		}
	}

	return IndexHealthView{
		Code:        string(IndexHealthUnknown),
		Label:       "Sconosciuto",
		Severity:    "neutral",
		Description: "Stato di indicizzazione non riconosciuto.",
		Terminal:    false,
		Retryable:   false,
	}
}
