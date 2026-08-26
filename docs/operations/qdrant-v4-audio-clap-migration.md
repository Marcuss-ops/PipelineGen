# Qdrant v4 — CLAP audio channel migration (design)

Controlled migration that adds the CLAP audio embedding channel
(`laion/clap-htsat-fused`, 512d, Cosine) to the Qdrant index **without
touching the v3 schema or the live v3 collection**. v4 is a brand-new
physical collection built blue/green; `media_assets_current` switches to
it only after verification, and v3 stays behind as the rollback target.

This is the design document required by the godlike/06 SSOT policy:
enabling the audio channel is a deliberate, reviewed migration — never a
silent uncomment inside `DefaultV3Schema()`.

## 1. Obiettivi e non-obiettivi

**Obiettivi**

- Channel audio attivo in una collection v4: `audio` = CLAP-HTSAT, 512d,
  Cosine, L2-normalized — identità dal registry (`models.CLAP`).
- Ricerca semantica audio: query come "applausi", "pioggia forte",
  "motore di automobile", "urla della folla", "musica drammatica",
  "esplosione" → hit attraverso il contenuto audio, non solo il transcript.
- Migrazione controllata: schema v4 → nuova collection → embedding audio
  in background → reindex blue/green → alias switch atomico → verifica →
  rollback disponibile.
- SQLite resta lo store canonico; Qdrant è una proiezione derivata,
  sempre ricostruibile.

**Non-obiettivi**

- NON modificare `DefaultV3Schema()` (v3 resta byte-per-byte immutato).
- NON scrivere mai nella collection fisica v3 esistente.
- NON cambiare i modelli text/visual (E5-base/SigLIP restano, protetti
  dal freeze guard `text_model_freeze_test.go`).
- NON eseguire embedding audio inline nel path di richiesta.
- NON abilitare il canale con un "add vector" sulla collection live
  (Qdrant non permette di mutare la config vettoriale di una collection
  esistente; serve una collection nuova).

## 2. Stato attuale (fatti dal codice)

| Componente | Stato oggi |
|---|---|
| Registry (`internal/kernel/models`) | `CLAP = laion/clap-htsat-fused`, 512d, Apache-2.0, `Enabled: false` |
| Mirror Python (`model_registry_generated.py`) | `CLAP_MODEL_NAME`/`CLAP_MODEL_VERSION`/`CLAP_MODEL_DIMENSIONS=512` (generato) |
| Sidecar (`scripts/services/embedding_server/audio.py`) | endpoint `/embed_audio` + `/embed_audio_from_file` presenti; 501 se CLAP non caricato (`SKIP_CLAP`) |
| Clipindexer (`clipindexer/indexing_api.go:358`) | `indexAudioViaAPI` → `POST /embed_audio_from_file` → persiste su `media_assets.audio_embedding` (colonna JSON TEXT, migration 099) |
| Searcher (`qdrant/search/searcher.go:350`) | interfaccia `AudioEmbedder` **commentata** (dormiente) |
| Schema v3 | channel audio **commentato** in `DefaultV3Schema()` |
| Backfill (`cmd/admin/internal/backfill/backfill_asset_embeddings_db.go`) | conta già `audio_embedding` per la readiness |

Tutto il codice infra esiste ma è dormiente: la migrazione lo attiva e
gli dà una collection + un piano di riempimento.

## 3. Schema v4 (definizione concreta)

Nuovo file/entry nel package `internal/platform/qdrant/schema`
(`qdrant_v4_schema.go`), seguendo il pattern di
`DefaultV3SpeakerSchema` (clona la base, NON la muta):

```go
// DefaultV4AudioSchema returns the v4 index schema: the canonical v3
// channels (text, transcript, visual, bm25_text) plus a live "audio"
// channel (CLAP-HTSAT, 512d). v3 is left untouched; v4 is a NEW
// physical collection built via the blue-green reindex path.
func DefaultV4AudioSchema() *schema.IndexSchema {
	s := schema.DefaultV3Schema() // deep-ish copy: text + transcript + visual + bm25_text
	s.Version = "v4"
	s.PhysicalName = "media_assets_v4_e5_768_siglip_768_clap_512"
	s.DenseVectors = append(s.DenseVectors, schema.EmbeddingSpec{
		Channel:      "audio",
		Model:        models.CLAP.ID,         // laion/clap-htsat-fused (registry SSOT)
		ModelVersion: models.CLAP.Revision,   // 2026-06-26-v1 (registry SSOT)
		Dimensions:   models.CLAP.Dimensions, // 512 (registry SSOT)
		Distance:     "Cosine",
		Normalized:   true,
	})
	return s
}
```

Registrazione nel registry schemi (`verification/schema_registry.go`
`init()`):

```go
defaultSchemaRegistry.Register(schema.DefaultV4AudioSchema())
```

Nessun cambiamento a `DefaultV3Schema()` né al `cfg.Qdrant.CollectionVersion`
default (`"v3"`): v4 è registrato ma **non selezionato** finché l'operatore
non lo sceglie. Il probe di boot `RegisteredVersions()` continua a passare
(≥2 versioni, ora 3).

Registrare v4 come dati è innocuo; selezionarlo è la cutover.

## 4. Attivazione lato registry e sidecar

1. **Registry**: flip `models.CLAP.Enabled = true` (diventa CORE) **alla
   cutover**, insieme all'aggiornamento dei test di coerenza
   (`registry_test.go::TestEnabled_CoreVsOptional`) e alla
   rigenerazione del mirror (`go run ./cmd/model-registry-gen --generate`).
2. **Sidecar**: togliere `SKIP_CLAP=1` dall'ambiente di produzione; il
   server carica CLAP dal mirror (`CLAP_MODEL_NAME`). L'opt-out resta
   `SKIP_CLAP` per i deploys senza audio.
3. **Pre-download pesi** (facoltativo ma consigliato):
   `python3 scripts/tools/model_downloader.py --models laion/clap-htsat-fused`
   — verifica anche il checksum non appena verrà pinnato nel registry.
4. **Probe**: `GET /models` (Go) esteso con un probe CLAP
   (`/embed_audio_from_file`) che verifica id/revision/512d dal registry —
   lo stesso pattern dei probe E5/SigLIP esistenti in
   `httpserver/transport/models.go`.

## 5. Nuova collection Qdrant

- Il reindex blue/green (PR 13) crea la collection fisica **timestamped**
  partendo dal physical name deterministico v4:
  `media_assets_v4_e5_768_siglip_768_clap_512_<ts>`.
- Vettori dense: `text` (768), `transcript` (768), `visual` (768),
  `audio` (512) — tutte dal v4 schema; sparse: `bm25_text`
  (`qdrant/bm25`); payload indexes identici a v3
  (workspace_id, lifecycle_state, embedding_version_*, …).
- **La collection v3 non viene mai toccata** (né create né mutate):
  v4 è una collection nuova che convive con v3 fino allo switch.

## 6. Embedding audio in background

Due canali, entrambi asincroni (mai inline nel request path):

1. **Nuovi asset (outbox)**: l'ingest già emette `asset.index.requested`;
   il dispatcher outbox chiama `indexAudioViaAPI` (esistente) →
   `/embed_audio_from_file` → `media_assets.audio_embedding` → upsert
   Qdrant sul canale `audio`. Niente cambiamenti al contratto outbox.
2. **Corpus storico (backfill)**: il tool
   `backfill_asset_embeddings_db.go` viene esteso (o affiancato da un job
   background) per iterare gli asset senza `audio_embedding` (asset con
   sorgente audio/video), embeddarli via sidecar e persisterli in SQLite.
   Throttling: `clip_indexer.max_concurrent_indexing` resta il bound
   (una GPU, un modello alla volta — CLAP compete con E5/SigLIP per VRAM).

**Metrica di cutover**: la readiness audio è la copertura
`con audio_embedding / totale con sorgente audio` (già calcolata dal
backfill + `readiness.go`). La cutover richiede ≥99% (o un valore
esplicito deciso in revisione).

## 7. Reindex (blue/green)

```bash
# Dry-run: deve mostrare il physical v4
go run ./cmd/admin reindex-qdrant --dry-run --json

# Apply: nuova collection timestamped v4 -> populate -> PR-12 verifier -> alias switch
go run ./cmd/admin reindex-qdrant --apply --json
```

- `--apply` costruisce la collection v4, la popola da SQLite (incluso
  `audio_embedding` per ogni asset), esegue il verifier strict PR-12
  (parità conteggi DB↔Qdrant + controlli per-punto **incluso il canale
  audio**: ogni punto con `audio_embedding` non vuoto deve avere il
  vettore `audio` con 512 dims).
- Lo switch avviene **solo** con `Ready=true`. Su fallimento l'alias resta
  su v3 e la collection parziale resta per ispezione/cleanup.

## 8. Alias switch (atomico)

- `media_assets_current` viene spostato in un'unica operazione atomica
  (`SwitchAlias`/`UpdateAliases`) dalla collection v3 alla collection v4.
- Scritture e letture passano tutte dall'alias: nessun codice legge il
  physical name diretto (il searcher risolve l'alias a runtime, cache 30s).
- La collection v3 **viene mantenuta** come rollback target (registry-aware).

## 9. Verifica (post-cutover)

```bash
# 1. Boot/readiness: handshake embedding + canary audio
curl -s --max-time 8 http://127.0.0.1:8000/qdrant/ready
# 2. Alias -> collection v4
curl -s --max-time 8 http://127.0.0.1:6333/aliases
# 3. Probe modelli: CLAP carico + 512d
curl -s --max-time 8 http://127.0.0.1:8000/models
# 4. Canary audio: query semantiche audio
#    "applausi" / "pioggia forte" / "urla della folla" / "esplosione"
#    -> hit coerenti col dominio, non asset random
```

Il canary audio live è il check più forte: embeddare una query audio via
sidecar e confermare che i top hit corrispondano (stesso approccio del
canary E5 documentato in `qdrant-projection-lag-recovery.md` §6).

## 10. Rollback

- **Alias back alla collection v3** (stessa operazione atomica inversa):
  il canale `audio` semplicemente sparisce; la ricerca semantica torna a
  text/visual senza errori (il fan-out audio va disabilitato a livello
  query/adapter).
- **Nessuna perdita dati**: `media_assets.audio_embedding` vive in SQLite
  (store canonico) ed è conservata; un nuovo reindex ricostruisce Qdrant.
- Il rollback è possibile in qualsiasi momento prima della retention
  sweep; dopo la sweep, si ricostruisce da SQLite.

## 11. Retention / cleanup (post-stabilizzazione)

Dopo che v4 è stabile (canary + search corrette per 1–2 settimane):

```bash
# Sweep registry-aware; il floor keep-last-n=2 protegge active + 1 rollback.
go run ./cmd/admin dr-qdrant apply-retention \
  --retired-prefix=media_assets_v3_e5_768_siglip_768 \
  --json
```

- Le collection v3 diventano `RETIRED` nel projection registry (audit);
  il drop fisico segue le regole esistenti (mai l'active target).
- Il rollback v3 "giusto" da tenere è l'ultima collection v3 — a
  differenza della migrazione nomic→e5, v3 e v4 sono **compatibili** sui
  canali condivisi (text/visual identici), quindi v3 è un rollback valido.

## 12. Gates di sequenza e approvazione

| # | Gate | Verifica | Chi approva |
|---|---|---|---|
| A | Schema v4 registrato (dati), v3 default invariato | `go test ./internal/platform/qdrant/verification/...`, `RegisteredVersions()` | — (nessun effetto runtime) |
| B | CLAP caricato nel sidecar, probe `/models` verde | probe CLAP 512d | operatore |
| C | Backfill audio ≥99% copertura | `readiness.go` / backfill report | operatore |
| D | `reindex-qdrant --dry-run` = v4; `--apply` Ready=true | PR-12 verifier (incl. canale audio) | operatore |
| E | Canary audio passa | query audio semantiche | operatore + review |
| F | `cfg.Qdrant.CollectionVersion: "v4"` + boot handshake verde | boot/readiness | deploy |

La migrazione è **documentata e revisionata** (questo documento + i gate
sopra): nessun cambio silenzioso, coerente con la policy godlike/06.

## 13. Anti-pattern (cosa NON fare)

- ❌ Scommentare il channel audio dentro `DefaultV3Schema()`: muterebbe
  v3, non cambierebbe il physical name e la collection v3 esistente non
  avrebbe il canale — drift silenzioso.
- ❌ Aggiungere il vettore `audio` alla collection v3 live (non supportato
  da Qdrant per collection esistenti; e violerebbe l'isolamento v3/v4).
- ❌ Embedding audio inline nel request path (viola godlike/07: side
  effect async dopo commit DB).
- ❌ Alias switch senza verifier Ready=true.
- ❌ Abilitare la ricerca audio con copertura <99% (query audio su asset
  senza vettore = hit mancanti non diagnosticabili).
- ❌ Cambiare E5/SigLIP insieme a questa migrazione (freeze guard: ogni
  cambio di spazio vettoriale è una migrazione separata).

## 14. Checklist operativa (ordine di esecuzione)

1. [ ] Merge schema v4 + registrazione (Gate A) — nessun effetto runtime.
2. [ ] `go run ./cmd/model-registry-gen --generate` dopo il flip CLAP
       (Gate A/B) + aggiornare `registry_test.go`.
3. [ ] Deploy sidecar senza `SKIP_CLAP`; probe `/models` CLAP verde (Gate B).
4. [ ] Lancio backfill audio in background; attendere ≥99% (Gate C).
5. [ ] `reindex-qdrant --dry-run` → v4; `--apply` → Ready=true (Gate D).
6. [ ] Canary audio (Gate E) + review.
7. [ ] Cutover: `collection_version: "v4"` + alias switch + boot verde
       (Gate F).
8. [ ] Stabilizzazione 1–2 settimane → retention sweep v3 (rollback
       mantenuto fino a stabilizzazione).
