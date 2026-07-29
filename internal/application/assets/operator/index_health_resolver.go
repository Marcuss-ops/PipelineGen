package operator

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// IndexHealthInput contains the read-model data needed by the resolver.
// It intentionally does NOT include the asset lifecycle: the index health
// is computed from the index state, hash consistency and pending work.
type IndexHealthInput struct {
	IndexState          asset.IndexState
	ContentHash         string
	IndexedContentHash  string
	PendingOutboxEvents int
	LastError           string
}

// ResolveIndexHealth is the single owner of the visual projection of the
// index state. It maps canonical domain states into the UI-facing
// IndexHealthView. No other package should compute these rules.
func ResolveIndexHealth(input IndexHealthInput) IndexHealthView {
	code := resolveCode(input)
	return buildView(code, input.LastError)
}

func resolveCode(input IndexHealthInput) IndexHealthCode {
	switch input.IndexState {
	case asset.StateNotIndexable:
		return IndexHealthNotIndexable

	case asset.StateDiscovered, asset.StateIndexPending:
		return IndexHealthPending

	case asset.StateEmbedding:
		return IndexHealthEmbedding

	case asset.StateEmbedded:
		// EMBEDDED means the asset has vectors in SQLite but the Qdrant
		// upsert has not yet been confirmed. If there is a pending outbox
		// event the worker has been told to upsert; otherwise it is ready
		// to be picked up.
		if input.PendingOutboxEvents > 0 {
			return IndexHealthPending
		}
		return IndexHealthIndexing

	case asset.StateIndexing:
		return IndexHealthIndexing

	case asset.StateIndexed:
		if isStale(input) {
			return IndexHealthStale
		}
		return IndexHealthIndexed

	case asset.StateEmbeddingFailed, asset.StateIndexingFailed, asset.StateIndexFailed:
		return IndexHealthFailed

	case asset.StateIndexingSkippedNoIndexer:
		return IndexHealthRetryWait

	case asset.StateIndexDeletePending, asset.StateDELETED:
		return IndexHealthDeleted

	default:
		return IndexHealthUnknown
	}
}

func isStale(input IndexHealthInput) bool {
	if input.ContentHash == "" {
		return false
	}
	return input.ContentHash != input.IndexedContentHash
}

func buildView(code IndexHealthCode, lastError string) IndexHealthView {
	label, severity, terminal, retryable := metadataForCode(code)
	description := lastError
	if description == "" {
		description = defaultDescriptionForCode(code)
	}
	return IndexHealthView{
		Code:        string(code),
		Label:       label,
		Severity:    severity,
		Description: description,
		Terminal:    terminal,
		Retryable:   retryable,
	}
}

func metadataForCode(code IndexHealthCode) (label, severity string, terminal, retryable bool) {
	switch code {
	case IndexHealthNotIndexable:
		return "Non indicizzabile", "neutral", true, false
	case IndexHealthPending:
		return "In attesa", "warning", false, false
	case IndexHealthEmbedding:
		return "Embedding in corso", "info", false, false
	case IndexHealthIndexing:
		return "Indicizzazione in corso", "info", false, false
	case IndexHealthIndexed:
		return "Indicizzato", "success", true, false
	case IndexHealthStale:
		return "Stale", "warning", false, true
	case IndexHealthRetryWait:
		return "Attesa retry", "warning", false, true
	case IndexHealthFailed:
		return "Fallito", "error", true, true
	case IndexHealthDeleted:
		return "Eliminato", "neutral", true, false
	case IndexHealthUnknown:
		return "Sconosciuto", "neutral", false, false
	default:
		return string(code), "neutral", false, false
	}
}

func defaultDescriptionForCode(code IndexHealthCode) string {
	switch code {
	case IndexHealthNotIndexable:
		return "Asset non idoneo all'indicizzazione"
	case IndexHealthPending:
		return "L'asset è in coda per l'indicizzazione"
	case IndexHealthEmbedding:
		return "Generazione embedding in corso"
	case IndexHealthIndexing:
		return "Upsert su Qdrant in corso"
	case IndexHealthIndexed:
		return "Asset indicizzato e coerente"
	case IndexHealthStale:
		return "L'asset è cambiato dopo l'ultima indicizzazione"
	case IndexHealthRetryWait:
		return "In attesa di retry dell'indexer"
	case IndexHealthFailed:
		return "Indicizzazione fallita"
	case IndexHealthDeleted:
		return "Asset eliminato dall'indice"
	case IndexHealthUnknown:
		return "Stato di indicizzazione non riconosciuto"
	default:
		return fmt.Sprintf("UNKNOWN: %s", code)
	}
}
