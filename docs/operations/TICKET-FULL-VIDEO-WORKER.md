# TICKET-FULL-VIDEO-WORKER

**Stato:** APPROVATO 24/10/2026 — da implementare (dipende da
TICKET-S3-ASSET-LIBRARY per i riferimenti per-hash).
**Origine:** analisi Drive/servizi del 24/10/2026: il flusso attuale
«scarica stock/clips/overlay, invia voiceover, stitcha sul51 remoto» esiste
solo per il contratto di handoff con la macchina remota. L'assembly
packet-copy (`AssembleCopy` → `pipelinegen-muscles`) è il **nostro binario
Rust locale**: il «51» è una scelta di deployment, non una necessità.
**Ordine consigliato:** dopo la libreria S3, prima di qualsiasi scala
orizzontale.

## Principio

Un job = un video su **un solo worker**: render + `AssembleCopy` + upload
sulla stessa macchina. Nessun passaggio di consegne: stock/clips/overlay/
voiceover sono letti dalla cache locale del worker; in rete girano solo i
manifest (kilobyte) e il video finito. Scalare = aggiungere worker identici
(pod RunPod compresi), non aggiungere ruoli.

## Problema misurato

- Il final worker remoto (51) risolve `drive_file_id`/web link da Drive e
  materializza lì stock, clip, overlay e voiceover: ogni video paga un
  shuttle di rete verso una macchina che non ha renderizzato nulla.
- La barriera «Drive ID certificati prima del final render» e l'outbox
  overlay (98–139 s/run di `drive_publish` asincrono) esistono solo per
  alimentare quell'handoff.
- Il51 remoto impone il contratto `mode=replace` + artefatti opachi
  pre-composti, che a sua volta genera lo scheduler dei conflitti, il binding
  batch fail-closed e il blocco P4: complessità accidentale del contratto,
  non del dominio editoriale.

## Cambio (fasi)

1. **Assembly locale di default:** il percorso render → `AssembleCopy` →
   mux audio → upload diventa il percorso standard del worker; il final job
   remoto resta disponibile come modalità opzionale (`final_job`) finché non
   è certificata la parità.
2. **Riferimenti per-hash:** il payload delle scene usa content-hash
   risolti dalla cache locale/libreria S3; `drive_file_id` esce dal percorso
   caldo (resta solo come metadata storica del catalogo).
3. **Outbox Drive opzionale:** la pubblicazione Drive diventa mirror
   asincrono configurabile (default spento), mai barriera del render.
4. **Bootstrap worker:** ogni macchina nuova (box o pod RunPod) = pull cache
   da S3 + registrazione alla queue; nessun'altra procedura.

## Gate di accettazione

- [ ] Run golden end-to-end su un solo worker: nessun traffico Drive, nessun
      download di asset durante il job dopo il warm-up cache; solo il video
      finito lascia la macchina.
- [ ] Parità pixel/audio tra assembly locale e final job remoto sullo stesso
      piano golden (stesso criterio A/B di TICKET-MANIFEST-IDENTITY).
- [ ] Il51 è opzionale: una run `final_job=false` produce il video completo
      con le stesse identità (hash) della run remota.
- [ ] Retry idempotente: stesso manifest + stessi hash ⇒ nessun lavoro
      ripetuto, nessun artefatto duplicato.

## Dipendenze e rischi

- Dipende da TICKET-S3-ASSET-LIBRARY (riferimenti per-hash e libreria
  condivisa).
- Con l'assembly locale, il modello `mode=replace` opaco resta valido come
  primo passo; la successiva evoluzione a layer/manifest è
  TICKET-MANIFEST-IDENTITY e diventa più facile con questo contratto già
  interno.
- L'upload del video finito è l'unico trasferimento residuo: YouTube API o
  R2, configurabile per worker.
