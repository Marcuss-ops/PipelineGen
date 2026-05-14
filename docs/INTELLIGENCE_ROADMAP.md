# PipelineGen: Future Intelligent Implementations

Questo documento delinea le evoluzioni proposte per rendere PipelineGen un sistema di media processing più intelligente, autonomo e "creativo", sfruttando il nuovo database unificato e le capacità di Hybrid Search.

---

## 1. Vision-Based Auto-Tagging & Enrichment
**Obiettivo**: Arricchire automaticamente i metadati degli asset che hanno descrizioni scarse o assenti.

*   **Logica**: Utilizzare il server di embedding (CLIP) per analizzare il frame centrale delle clip video e delle immagini.
*   **Azione**: Estrarre tag descrittivi (es. "drone shot", "urban sunset", "high contrast") e salvarli nel campo `tags` del DB.
*   **Impatto**: Miglioramento drastico della ricerca semantica senza intervento manuale.

## 2. Narrative Continuity & Diversity Scoring
**Obiettivo**: Migliorare la qualità del "montaggio" automatico evitando ripetizioni visive e garantendo coerenza stilistica.

*   **Diversity Penalty**: Se una clip ha un `phash` (visual hash) troppo simile a una clip già selezionata per il video corrente, il suo punteggio viene penalizzato per favorire la varietà.
*   **Style Matching**: Analizzare la palette cromatica o lo stile (es. "dark", "vibrant") delle clip precedenti per suggerire asset che mantengano una continuità estetica nel video.

## 3. Predictive Harvesting (Smart Scraper)
**Obiettivo**: Anticipare le necessità dell'utente popolando il database in modo proattivo.

*   **Trend Analysis**: Analizzare i termini di ricerca più frequenti e gli script generati negli ultimi 7 giorni.
*   **Gap Detection**: Se il sistema rileva un alto interesse per un tema (es. "AI Robotics") ma una bassa disponibilità di asset nel DB unificato, avvia automaticamente job di scraping su Artlist/YouTube in background.

## 4. Automated B-Roll Sequence Engine
**Obiettivo**: Passare dalla selezione di singole clip alla creazione di "sequenze" logiche.

*   **Storytelling Logic**: Implementare schemi di montaggio predefiniti (es. *Wide Shot -> Medium Shot -> Close Up*).
*   **Action Matching**: Se lo script parla di un'azione specifica (es. "running"), cercare sequenze di clip che mostrano progressione nell'azione.

## 5. Smart Deduplication & Resolution Upscaling
**Obiettivo**: Ottimizzare lo spazio e garantire la massima qualità visiva.

*   **Cross-Source Deduplication**: Usare il `phash` e gli embedding per identificare se lo stesso asset è presente sia come clip YouTube che come Artlist.
*   **Auto-Selection**: In caso di duplicati, il sistema sceglie automaticamente la versione con risoluzione/bitrate maggiore e segna le altre come "mirror".

---

## Stato dell'Infrastruttura
Tutte queste feature sono ora possibili grazie alla consolidazione del database:
- [x] Database Unificato (`media.db.sqlite`)
- [x] Schema flessibile (`metadata_json`)
- [x] Supporto per Embedding Semantici
- [x] Supporto per Perceptual Hashing (phash)
12. Generative Gap-Filling

Come Instagram con AI Backdrops

    Obiettivo: se non hai la clip, la crei.
    Logica: quando Predictive Harvesting trova un gap (es. "drone shot of Tokyo at night" manca), invece di solo scrapare, lancia SDXL-Turbo o AnimateDiff per generare 3s di B-roll sintetico.
    Azione: salva con source: "gen" e prompt. Usa lo stesso embedding per cercarlo dopo.
    Impatto: database mai vuoto.
13. Social Feedback Loop

Come il ranking di Meta

    Obiettivo: PipelineGen impara da cosa funziona.
    Logica: ogni video esportato riceve metriche (watch time, CTR). Salvale in una tabella performance.
    Azione: fai fine-tuning del tuo scoring: le clip usate in video con alta retention ottengono un boost permanente nel DB.
    Impatto: il sistema diventa più intelligente ogni settimana, senza riaddestramenti manuali.

Quando PipelineGen vede che un long-form ha un picco di retention tra 4:12 e 4:27, il Harvester estrae automaticamente quel segmento, lo indicizza come nuovo asset type='short_candidate', genera 3 varianti di hook con il tuo Generative Gap-Filling, e te le propone già pronte per Shorts.