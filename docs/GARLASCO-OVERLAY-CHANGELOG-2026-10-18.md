# Garlasco overlay run — problemi e correzioni (18 ottobre 2026)

## Obiettivo

Generare il video completo mantenendo invariati i 15 `source_text`, con 40 overlay totali: 10 frasi brevi, 10 frasi lunghe, 8 immagini contestuali, 5 immagini di entità, 4 date e 3 mappe Chronon Editorial.

## Problemi riscontrati

- **Il worker poteva restare fermo dopo un risveglio perso.** Il passaggio tra la coda e la notifica del worker aveva una finestra in cui un job poteva rimanere in attesa. La correzione del wake-up è stata mantenuta e il servizio locale è stato riavviato e verificato.
- **Il testo fissato non restava verbatim.** Gli hint per le frasi potevano finire aggiunti alla narrazione. La materializzazione ora mantiene il testo sorgente esatto e usa gli hint solo per gli overlay.
- **Il limite per scena veniva inviato nel campo sbagliato.** `max_overlays_per_scene` va sull’elemento della richiesta, non dentro `script_params`; la posizione errata viene rifiutata dal decoder stretto.
- **Le date erano filtrate dalle impostazioni delle singole scene.** Il gruppo globale era abilitato, ma varie scene avevano `numbers: disabled`; il piano R9 ha quindi conservato soltanto 2 date su 4. Il payload R10 abilita le date nelle scene pertinenti e il codice deduplica una data completa dalla sua forma abbreviata, mantenendo l’ancoraggio all’audio.
- **Le frasi non raggiungevano il budget.** R9 conteneva 14 frasi. La selezione ora riserva corsie distinte per frasi brevi e lunghe e il payload R11 aggiunge proposte ancorate al testo sorgente. La verifica finale R11 conta ancora 13 frasi: il budget 10+10 non è raggiunto e il criterio resta aperto.
- **Le immagini contestuali non raggiungevano il budget.** R9 materializzava 4 immagini contestuali. L’esperimento con 4 ricerche ha acquisito 86 asset senza superare in sicurezza la validazione; R11 usa 2 ricerche e un limite candidati più stretto. R11 ha prodotto 5 immagini contestuali e 5 overlay di entità (4 `entity_image` e 1 `entity_card`): il budget 8+5 non è raggiunto.
- **Il renderer delle mappe rifiutava `chronon_editorial`.** Il selettore dei basemap non riconosceva lo stile editoriale offline. È stato aggiornato per ammetterlo; il retry R9 ha completato le tre mappe senza errore.
- **Date e mappe nella stessa scena collidono nel renderer.** R10 ha aggiunto una data alla scena della mappa Garlasco e Chronon ha rifiutato il piano per sovrapposizione temporale. R11 mantiene le date nella scena dedicata e accorcia solo le finestre necessarie per separare date consecutive; le date singole conservano la durata standard.
- **L’invio remoto su `51.91.11.36` non espone il contratto di submit usato dal payload.** Il preflight ha confermato che il servizio è pronto, ma la lettura media restituisce 404 e `POST /api/v1/jobs` rifiuta il campo `type`. Nessun job è stato creato da quel tentativo. Il rerun R10 è stato quindi inviato al servizio locale compatibile con `/api/script/generate`.

## Evidenza prima delle correzioni

R9 è terminato con rendering completato, ma il piano conteneva 28 overlay: 14 frasi, 9 immagini complessive, 2 date e 3 mappe. Il testo sorgente contava 733 parole ed era marcato verbatim. Un primo tentativo di mappa era fallito per lo stile basemap non riconosciuto; il retry è riuscito dopo la correzione del renderer.

## Correzioni e verifica

- Aggiornata la selezione delle frasi brevi/lunghe, il riempimento delle immagini contestuali e l’estrazione delle date complete con timing audio.
- Aggiunti test mirati per le corsie frase, la classificazione per numero di parole, le immagini sequenziali e le date esplicite.
- Verificati i pacchetti Go `internal/capabilities/scripts` e `internal/capabilities/overlays`.
- Il deploy locale è riuscito: `/health` ha risposto e l’hash SHA-256 del processo attivo coincideva con quello del binario appena compilato.
- **R10:** non valido; il renderer ha respinto la collisione data/mappa. Il job è terminato con errore e non viene contato come output.
- **R11:** rendering completato con stato `SUCCEEDED`, ma il piano non soddisfa il requisito. Job `job_1792352385352138746_dfb1865c`; run `run_417596f5-412f-40fb-8511-1748ce8843a6`; durata video 300648 ms. Il piano contiene 28 overlay: 13 frasi, 5 immagini contestuali, 5 overlay di entità (4 immagini e 1 scheda), 2 date e 3 mappe. Le date attese erano 4. La somma delle categorie previste (10+10+8+5+4+3) è 40, ma R11 è un rendering riuscito tecnicamente e non un risultato editoriale conforme. Il conteggio è stato letto direttamente da `overlay_plan.items` nella risposta completa del job.
- **Push Chronon3d:** il primo pre-push ha bloccato due accessi non canonici a `Frame::value` e la lettura di un manifest JSON con BOM nel controllo architetturale. Entrambe le cause sono state corrette; il successivo pre-push è passato (6/6 gate, 206/206 regole architetturali).

## Riferimenti

- Payload R11: `ops/jobs/garlasco_completo_40_overlay_chronon_mappe_nuove_20261018_r11.generate.json`
- Cartella sorgente Drive: ID `1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS`
