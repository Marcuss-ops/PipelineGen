# TICKET-S3-ASSET-LIBRARY

**Stato:** APPROVATO 24/10/2026 — da implementare (fase MinIO subito, fase R2
al primo budget).
**Origine:** analisi Drive/servizi del 24/10/2026: il magazzino pesante
(~100 GB di stock) vive su Drive e viene pullato per-job; il percorso caldo
deve diventare una libreria content-addressed condivisa con cache locale per
worker, pensata per la farm parallela (RunPod) da subito.
**Ordine consigliato:** primo dei ticket Drive-exit: è il presupposto degli
altri.

## Principio

La libreria degli asset (stock, clip, overlay, voiceover, font, map plate) è
un object store **S3-compatibile** indirizzato per content-hash
(`s3://…/{sha256}`): byte immutabili + manifest che elenca gli hash. Il
disco locale di ogni worker è **solo cache**, mai verità: cache-hit = zero
rete, cache-miss = un pull per hash. Nessuno scrive sulla libreria durante i
job ⇒ nessun conflitto possibile, per costruzione. Backend oggi MinIO sul box
(0 €), domani Cloudflare R2 (stessa API, cambio endpoint): la scelta S3 è
quella che rende la scalata a pagamento un cambio di configurazione e non un
progetto.

## Problema misurato

- Il materiale pesante (~100 GB stock) è su Drive: il processing lo **pulla
  per-job** durante l'assembly (stock/clips via `drive_file_id`, overlay via
  link Drive, voiceover via publish seriale ~5,5 s/item).
- `drive_publish` misurato: **2,4–3,0 s/item** (range 2,0–6,8), 36–57 upload
  per run, 98–139 s somma, più la barriera «nessun final render prima dei
  Drive ID certificati».
- Drive come secondo catalogo genera l'intera classe di bug già pagata:
  identità tripla (legacy_file_md5 / SHA-256 / Drive ID), 422, 758
  materializzazioni da bonificare, 24 binding in quarantena, bug cartelle
  duplicate («Boxe»).
- I pod futuri (RunPod) non possono dipendere da pull Drive per-job: serve
  una libreria da cui ogni worker faccia warm-up una tantum.

## Cambio (fasi)

1. **MinIO locale con API S3** come backend della libreria; migrare i
   riferimenti asset al formato per-hash (`s3://…/{sha256}`). L'ObjectStore
   esistente (`/objects/{sha256}`, verify-on-write/read) è già questa
   interfaccia: l'operazione è portarlo dietro API S3, non inventare un
   store.
2. **Warm-up una tantum** della libreria da Drive (rclone + verifica
   per-hash, come già fatto per l'idratazione L3: 574 oggetti, 0 mismatch):
   dopo il warm-up, **nessuna chiamata Drive durante i job**.
3. **Politica locale-first**: il materializer risolve dalla cache locale;
   metrica `asset_fetch` con cache-hit/miss per worker.
4. **Fase R2 (al budget):** sync della libreria su Cloudflare R2 (~1,5 $/mese
   per 100 GB, egress gratis), endpoint sostituito in configurazione; i pod
   RunPod fanno bootstrap pullando la cache da R2 su volume persistente.

## Gate di accettazione

- [ ] Run golden end-to-end con **zero richieste Drive** nel percorso caldo
      (contatore chiamate Drive = 0 per la run).
- [ ] Warm-up una tantum completato con verifica per-hash: 0 mismatch, 0
      binding senza bytes certificati (stesso criterio dell'idratazione L3).
- [ ] Dopo il warm-up: ogni hash viene scaricato **al più una volta per
      worker** (cache-hit ratio misurata per worker ≥ 95% sul secondo job).
- [ ] Switch MinIO → R2 = solo endpoint: stessa run golden identica su R2,
      nessuna modifica al codice.

## Dipendenze e rischi

- I riferimenti `drive_file_id` nel payload final job vanno sostituiti per
  hash: è il lavoro di `TICKET-FULL-VIDEO-WORKER`, da allineare.
- R2 e S3-compatibile non sono identici (consistency, multipart): il percorso
  MinIO locale certifica il codice, la fase R2 certifica il backend.
- Il backup resta fuori dal critical path: rclone notturno opzionale
  (libreria → R2 o Drive), mai sincrono al render.
