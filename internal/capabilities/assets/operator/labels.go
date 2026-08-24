package assets

// Label helpers for canonical enum values. Keeping the maps in the
// application layer lets the UI receive ready-to-display labels without
// embedding translation logic in React.

// LifecycleStateLabels returns a map from canonical lifecycle state code
// to a human-readable label.
func LifecycleStateLabels() map[string]string {
	return map[string]string{
		"PREPARING":            "In preparazione",
		"PUBLISHED":            "Pubblicato",
		"STAGING":              "Staging",
		"PROCESSING":           "In elaborazione",
		"ACTIVE":               "Attivo",
		"DELETE_PENDING":       "Eliminazione in attesa",
		"DELETE_REQUESTED":     "Eliminazione richiesta",
		"DRIVE_DELETE_PENDING": "Eliminazione Drive in corso",
		"DRIVE_DELETED":        "Eliminato da Drive",
		"INDEX_DELETE_PENDING": "Eliminazione indice in corso",
		"INDEX_DELETED":        "Indice eliminato",
		"DELETED":              "Eliminato",
		"ERROR":                "Errore lifecycle",
	}
}

// AssetStateLabels returns a map from canonical asset-state code to label.
func AssetStateLabels() map[string]string {
	return map[string]string{
		"DISCOVERED":         "Scoperto",
		"DOWNLOADED":         "Scaricato",
		"NORMALIZED":         "Normalizzato",
		"HASHED":             "Hash calcolato",
		"UPLOADED":           "Caricato",
		"TRANSCRIBED":        "Trascritto",
		"ENRICHED":           "Arricchito",
		"TRANSLATED":         "Tradotto",
		"INDEX_PENDING":      "Indicizzazione in attesa",
		"INDEXED":            "Indicizzato",
		"READY":              "Pronto",
		"READY_MULTILINGUAL": "Pronto multilingua",
		"FAILED_RETRYABLE":   "Fallito (riprova)",
		"FAILED_PERMANENT":   "Fallito permanente",
	}
}

// IndexStateLabels returns a map from canonical index-state code to label.
func IndexStateLabels() map[string]string {
	return map[string]string{
		"NOT_INDEXABLE":               "Non indicizzabile",
		"DISCOVERED":                  "Scoperto",
		"EMBEDDING":                   "Embedding in corso",
		"EMBEDDED":                    "Embedding pronto",
		"INDEXING":                    "Indicizzazione in corso",
		"INDEXED":                     "Indicizzato",
		"EMBEDDING_FAILED":            "Embedding fallito",
		"INDEXING_FAILED":             "Indicizzazione fallita",
		"INDEXING_SKIPPED_NO_INDEXER": "Attesa retry",
		"DELETE_PENDING":              "Eliminazione in corso",
		"DELETED":                     "Eliminato",
	}
}

// MediaTypeLabels returns a map from canonical media-type code to label.
func MediaTypeLabels() map[string]string {
	return map[string]string{
		"stock":        "Stock",
		"clip":         "Clip",
		"image":        "Immagine",
		"audio":        "Audio",
		"document":     "Documento",
		"image_video":  "Video generato",
		"sound_effect": "Effetto sonoro",
		"script":       "Script",
	}
}
