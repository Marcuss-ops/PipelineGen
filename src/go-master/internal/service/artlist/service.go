package artlist

// Questo file è stato refattorizzato.
// Le funzioni originali sono state suddivise nei seguenti moduli:
// - stats_service.go: statistiche e diagnostiche
// - search_service.go: ricerche (DB e live)
// - clip_service.go: gestione ciclo vita clip (download, upload, process)
// - runs.go: gestione run Artlist

// Codice morto rimosso:
// - Sync (non sincronizza realmente)
// - Reindex (ritorna risposta fittizia)
// - PurgeStale (non fa nulla)

// Per retrocompatibilità, le funzioni sono accessibili tramite gli altri moduli.
