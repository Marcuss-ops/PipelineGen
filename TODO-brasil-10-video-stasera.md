# Piano operativo — 10 video brasiliani completi

## Stato verificato

- [x] Il payload di generazione accetta `items[].channel_id`; il valore è portato nella richiesta e seleziona il profilo editoriale (`config/channel_profiles.yaml`).
- [x] Il payload Milton usa ora `channel_id: "crime"`.
- [x] Il worker 51 ha già completato con successo il render finale Milton: MP4 H.264 1920×1080, AAC stereo, 22:07; l'artefatto è stato ispezionato e il report è in `TODO-milton-finaljob-performance.md`.
- [x] Il payload Milton indirizza il render a Drive con `drive_folder_id` e `drive_subfolder_name`.
- [x] Il canary Milton è un `final_job`: PipelineGen prepara il payload finale e lo invia al worker remoto 51 per il render. Questo è il passaggio richiesto.
- [x] `media_plan.provider_policy.youtube: disabled` resta corretto: le fonti sono i clip già selezionati; non è il controllo del worker remoto.
- [ ] Verificare che `channel_id` e destinazione Drive siano visibili nella risposta di enqueue e nei dettagli della queue, non soltanto nel payload persistito. Il canale editoriale (`crime`) non va confuso con un account/destinazione di pubblicazione YouTube.

## Azioni — ordine di lavoro

### A. Contratto payload e queue

- [ ] Tracciare `items[].channel_id` dall'HTTP/API di enqueue fino al run persistito, ai child job e alla vista/endpoint di queue.
- [ ] Aggiungere alla vista della queue nome canale e destinazione di pubblicazione leggibili, conservando gli ID canonici. Non dedurre il canale YouTube dal profilo editoriale.
- [ ] Mantenere distinta la consegna del render al worker 51 dalla pubblicazione pubblica su YouTube. Per questa sessione è autorizzato il render remoto; non avviare pubblicazione YouTube.
- [ ] Rendere visibili nel payload e nella queue `channel_id`, folder/nome Drive, `final_job` e stato del render remoto.

### B. Preparare la batch da 10

- [ ] Fare inventario delle fonti brasiliane già disponibili (clip, trascrizioni, documenti e asset) e associare a ciascun video un dossier verificabile.
- [ ] Selezionare 10 storie con fonti sufficienti; non riutilizzare Milton come se fosse un nuovo video. Segnare quali sono già completi e quali vanno ancora scritti.
- [ ] Per ogni storia preparare script PT-BR con attribuzioni, date/nomi/numeri solo se presenti nelle fonti, struttura scena per scena e chiusura; revisione lingua e controllo anti-ripetizione.
- [ ] Preparare 10 payload autonomi con ID/idempotency univoci, `channel_id: "crime"`, destinazione Drive controllata e `publish.youtube.enabled: false`.
- [ ] Preflight: fonti/permessi, immagini, voce PT-BR, durata prevista, profilo grafico, audio, sottotitoli e destinazione Drive. Bloccare il submit se manca un requisito.

### C. Generare e chiudere

- [x] Usare Milton come canary: 10 immagini (una per scena), 15 phrase overlay, `final_job: true`, TTS PT-BR di oltre 20 minuti e Google Docs attivo. Il render finale è già riuscito sul worker 51 ed è stato ispezionato.
- [ ] Per gli altri nove video, attendere il gate sul payload/queue, poi inviarli al worker remoto senza avviare la pubblicazione pubblica YouTube.
- [ ] Verificare tutti e 10 gli MP4 con probe codec/durata, audio, frame iniziali/centrali/finali, corretto nome/cartella Drive e corrispondenza canale.
- [ ] Correggere e rigenerare ogni fallimento; chiudere la batch solo quando sono presenti 10 artefatti validi e tracciati.

## Gate di chiusura

La batch è chiusa quando ci sono 10 video PT-BR completi, tutti verificati dal worker remoto e salvati nella destinazione Drive corretta, con `channel_id` mostrato nel payload e nella queue. Il render remoto è previsto; la pubblicazione pubblica su YouTube resta fuori da questa sessione.

## Milton: intro e job già lanciati

- [x] Il payload Milton include tre clip intro, in quest'ordine: `yt_kBWlon1GMs0_0_15_v1`, `yt_ogu6YuDUdnQ_65_84_v1`, `yt_DLFi4zEjI4Q_55_68_v1`; titolo “Cold open: Milton Leite PRESO”, audio originale.
- [x] Lo storico locale riporta il job corretto `job_1790685246972813718_7a031056` completato e render worker 51 `job_208996a016b4626c` riuscito; altri rerun documentati sono `job_1790680098346939116_7b6fe57d` e `job_1790686427181755584_88c0b9ab`, entrambi riusciti.
- [x] Il report registra inoltre un primo rerun cancellato (`job_1790675141082688511_a54c5b0b`) e il QA precedente al job corretto interrotto dopo aver trovato l’audio intro non materializzato; il successivo job corretto è quello riuscito sopra. Controllare lo stato live prima di qualsiasi retry: lo storico scritto non sostituisce la queue corrente.
- [ ] Prima di un nuovo submit Milton, cercare per correlation/idempotency key e verificare la queue corrente per evitare duplicati. Mantenere sempre le tre intro clip configurate.

## Primo job Milton ricreato — completato

- [x] Nuovo payload completo: `ops/jobs/milton_leite_preso_ptbr_first_final_20260929T183052Z.generate.json` (correlazione univoca; cartella Drive dedicata).
- [x] Submit accettato: `job_1790706666114218187_45b38b0c`; run `run_1790706666200033859_c11b99b6bdb5` — `SUCCEEDED`.
- [x] Validati nel risultato 10 overlay immagine + 15 phrase overlay; tre clip intro rimangono configurate nel payload.
- [x] Script di 3.731 parole; audio finale 1.479.144 ms (~24m39s), quindi oltre i 20 minuti richiesti.
- [x] `final_job` completato dal worker remoto: `job_79fa742e9d250fbd` — `SUCCEEDED` (`host_57_131_20_173`). Nessun publish pubblico YouTube richiesto.
- [x] Google Docs child `job_1790707016403034260_34af3816` — `SUCCEEDED`.
- [ ] QA visivo/ascolto e verifica della destinazione Drive sull’artefatto appena prodotto; poi passare ai nove video successivi.
