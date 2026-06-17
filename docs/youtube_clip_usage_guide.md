# Guida Operativa: Uso Corretto delle YouTube Clips

Questa guida spiega come usare correttamente la pipeline YouTube di PipelineGen per ottenere clip pulite, ricercabili e coerenti con il manifest, i metadata e la semantica.

## Obiettivo

Una clip non deve essere solo un file `.mp4` scaricato e caricato su Drive. Deve diventare un asset ricercabile, con:

- titolo pulito
- transcript pulito
- summary fedele
- hook utile
- topics reali
- persone separate tra speaker e menzionati
- score di qualità realistico
- eventuale gruppo duplicati
- eventuale cluster tematico

## Endpoint da usare

Usa sempre l'endpoint reale:

`POST /api/clips/process`

Esempio:

```json
{
  "url": "https://www.youtube.com/watch?v=LkWU9lB2zEQ",
  "strategy": "replace",
  "concurrency": 3,
  "destination": {
    "group": "general",
    "folder_id": "1Tv_H7Wn225yra-QF4egJBr61IIIoxC15"
  }
}
```

## Regole pratiche

### 1. Usa un solo video alla volta quando stai validando

Se devi testare il comportamento della pipeline, usa un solo video e un solo `folder_id`. Evita di mescolare run diversi nello stesso folder se stai facendo debug.

### 2. Usa `strategy: replace` quando vuoi rifare davvero il job

`replace` forza la rigenerazione del risultato corrente.

Usalo quando:

- hai cambiato la logica di metadata
- hai sistemato il ranking
- hai pulito i manifest vecchi
- vuoi eliminare residui di run precedenti

### 3. Non affidarti al nome grezzo della clip

`name` e `raw_name` possono essere utili come fallback o debug, ma la ricerca deve usare soprattutto:

- `clean_title`
- `clip_summary`
- `hook`
- `topics`
- `speakers`
- `mentioned_people`
- `embedding_text`
- `clean_transcript`

### 4. `description` non va embeddato così com'è

La description di YouTube spesso contiene:

- sponsor
- link
- CTA
- merch
- promo code
- boilerplate ripetuto

Per questo la description va salvata, ma non deve essere usata in modo grezzo come base principale della semantic search.

### 5. Se il transcript è sporco, usa il transcript pulito

Nel file metadata ci sono due livelli:

- `raw_transcript`
- `clean_transcript`

La search semantica deve usare `clean_transcript`.

## Struttura corretta della clip

Ogni clip dovrebbe avere almeno questi campi:

```json
{
  "id": "yt_LkWU9lB2zEQ_895_1015",
  "name": "raw segment name",
  "raw_name": "raw segment name",
  "clean_title": "Mike Tyson on Self-Belief and Childhood Trust",
  "start_seconds": 895,
  "end_seconds": 1015,
  "duration_seconds": 120,
  "filename": "clip.mp4",
  "status": "processed",
  "clean_transcript": "clean spoken text...",
  "clip_summary": "summary fedele al transcript...",
  "hook": "frase forte della clip...",
  "topics": ["fighting", "identity", "trust"],
  "speakers": ["mike tyson", "theo von"],
  "mentioned_people": [],
  "people": ["mike tyson", "theo von"],
  "quality_score": 0.8,
  "search_visibility": "high",
  "embedding_text": "testo strutturato per embedding..."
}
```

## Interpretazione dei campi

### `clean_title`

Deve descrivere il contenuto reale della clip, non il video intero.

Buono:

- `Mike Tyson on Self-Belief and Childhood Trust`
- `Discussion on Love and Self-Perception`
- `Observing Mike Waiting at the Corner`

Sbagliato:

- `Mike Tyson and Theo Von Live Interview`
- `This Past Weekend w/ Theo Von #658`

### `clip_summary`

Deve essere fedele al transcript della clip, non alla description generale del video.

Regola:

- massimo 1-2 frasi
- niente contenuti inventati
- niente sponsor
- niente riassunti generici del video intero

### `hook`

Deve essere una frase utile per:

- preview
- shorts
- ricerca
- thumbnail/testo breve

### `topics`

Devono essere concetti, non parole casuali.

Buoni:

- `boxing motivation`
- `childhood belief`
- `self-worth`
- `love`
- `relationships`

Da evitare:

- `the`
- `yeah`
- `stuff`
- `live`
- `http`
- `code`

### `speakers` vs `mentioned_people`

Separali sempre.

- `speakers`: chi parla davvero nella clip
- `mentioned_people`: persone nominate nel discorso ma non presenti come speaker

Esempio:

```json
{
  "speakers": ["mike tyson", "theo von"],
  "mentioned_people": ["brad pitt"]
}
```

### `quality_score`

Non deve essere 1 solo perché la clip esiste.

Deve riflettere:

- chiarezza
- utilità narrativa
- qualità del taglio
- completezza del pensiero
- densità del contenuto
- presenza di spam/sponsor

Linee guida:

- `0.80 - 1.00`: clip forte, molto utile
- `0.55 - 0.79`: clip buona
- `0.30 - 0.54`: clip mediocre o parziale
- `< 0.30`: clip debole, intro, rumore o taglio scarso

### `search_visibility`

Serve a controllare quanto una clip debba emergere nei risultati.

Valori consigliati:

- `high`
- `normal`
- `low`
- `poor`

Regola pratica:

- clip forti e molto ricercabili: `high`
- clip utili ma non perfette: `normal`
- clip rumorose, corte o poco chiare: `low`
- clip da nascondere quasi sempre: `poor`

## Duplicati

Quando la pipeline rileva clip uguali o quasi uguali:

- assegna `duplicate_group_id`
- marca `is_duplicate`
- conserva `duplicate_of`
- segnala `is_best_version`

Regola:

- la versione migliore resta quella da mostrare per default
- le altre non vanno eliminate per forza
- vanno solo penalizzate nella search

## Topic cluster

Le clip possono essere raggruppate in cluster tematici.

Campi:

- `topic_cluster_id`
- `topic_cluster_label`
- `topic_cluster_size`
- `topic_cluster_rank`

Questo serve per:

- browsing
- filtri
- discovery
- search semantica più coerente

## Come leggere il manifest

Nel folder della clip troverai:

- `clip_manifest.json`
- `clip_manifest.txt`
- `metadata_<clip_id>.json` per ogni clip

Il manifest è la vista aggregata del folder.

Il metadata per clip è la fonte più precisa per:

- semantic search
- deduplica
- clustering
- ranking

## Errori da evitare

- usare description YouTube grezza come embedding principale
- mettere sponsor, CTA e link nei topic
- usare un solo campo `people` per tutto
- lasciare `filename: "."`
- salvare `tags` come stringa JSON invece che array
- tenere in `search_text` rumore inutile
- considerare una clip buona solo perché è stata processata

## Flusso consigliato

1. Processa il video con `/api/clips/process`
2. Genera clip e salva il manifest
3. Arricchisci i metadata con summary, hook, topics, speakers, people
4. Calcola score di qualità e visibilità
5. Rileva duplicati
6. Costruisci i topic cluster
7. Indicizza la search usando `embedding_text` e `clean_transcript`
8. Reranka i risultati prima di mostrarli

## Regola finale

Se una clip non è abbastanza chiara da essere descritta in modo specifico, è meglio abbassarne lo score e non forzarne la visibilità.

La qualità della library dipende più dalla pulizia dei metadata che dal numero di clip generate.
