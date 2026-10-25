# GLiNER2 measured comparison — 2026-10-25

Fixture: `fixtures/garlasco_2013_reconstructed.txt` (2,013 words, 15 scenes). Gold reconstructed from historical briefs, not independent annotation. Model: `fastino/gliner2-multi-v1`, threshold 0.50, CPU.

| Metrica | GLiNER2 | Semantic engine |
|---|---|---|
| Entità uniche (recall) | 19/20 | 20/20 |
| Recall tipizzato | 19/20 | 20/20 |
| Recall menzioni (min_mentions) | 37/38 | 34/38 |
| Precision menzioni nominate | 0.68 (47/69) | 1.00 (47/47) |
| Recall temporale unico (surface vs ISO) | 11/11 | 11/11 |
| Recall temporale menzioni | 12/12 | 12/12 |

## Performance GLiNER2

```json
{
  "model_load_seconds": 14.52,
  "scenes": 15,
  "latency_p50_seconds": 4.41,
  "latency_total_seconds": 72.05,
  "process_cpu_seconds": 1643.91,
  "peak_rss_mb": 3015.7,
  "engine_wall_seconds": 17.39,
  "note": "single CPU run on this host; engine includes worker start-up and model load"
}
```

## Relation probe (non scored)

```json
{
  "relation_types": [
    "located in",
    "legal outcome of"
  ],
  "pairs": 15,
  "examples": [
    {
      "relation": "located in",
      "head": "Garlasco",
      "tail": "Pavia"
    },
    {
      "relation": "legal outcome of",
      "head": "13 agosto 2007",
      "tail": "13 agosto 2007"
    },
    {
      "relation": "located in",
      "head": "Chiara Poggi",
      "tail": "casa"
    },
    {
      "relation": "located in",
      "head": "rilievi",
      "tail": "sulla scena"
    },
    {
      "relation": "legal outcome of",
      "head": "risposte",
      "tail": "giudici"
    },
    {
      "relation": "located in",
      "head": "Procura di Pavia",
      "tail": "Pavia"
    },
    {
      "relation": "located in",
      "head": "Pavia",
      "tail": "Pavia"
    },
    {
      "relation": "located in",
      "head": "Andrea Sempio",
      "tail": "televisione"
    },
    {
      "relation": "located in",
      "head": "scienza",
      "tail": "aula"
    },
    {
      "relation": "legal outcome of",
      "head": "bicicletta",
      "tail": "accertamento"
    }
  ],
  "scored": false,
  "reason": "GLiNER2 relations join two entity spans; the engine's legal facts are subject/predicate/value/date claims and the reconstructed gold annotates no relation set, so relation precision/recall cannot be compared 1:1."
}
```

## Falsi positivi nominati GLiNER2

avvocati, difensori, difesa, fidanzato, giudice, giudici, indagato, legali, magistrati, parti, periti, provincia di pavia, ufficio
