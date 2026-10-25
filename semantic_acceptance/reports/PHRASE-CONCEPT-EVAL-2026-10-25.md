# Phrase / visual-concept / object measurement — 2026-10-25

Fixture: `fixtures/garlasco_2013_reconstructed.txt` (2,013 words). Gold: reconstructed from historical briefs, not independent annotation.

| Check | Verdetto | Dettaglio |
|---|---|---|
| `visual_concept_recall_all` | **PASS** | 19/19 gold concepts matched |
| `visual_concept_recall_core` | **PASS** | 15/15 core concepts matched (threshold 0.75) |
| `keyword_named_share` | **PASS** | 28/136 unique important words name a gold entity/concept (0.21); generic visual words are outside the gold by design |
| `phrases_grounded` | **PASS** | 121 phrase candidates, 0 ungrounded |
| `phrases_dedup` | **PASS** | 0 duplicate phrase texts inside their scene |
| `objects_annotated` | **NOT_RUN** | 7 OBJECT mentions emitted (bicicletta, campioni, documenti, documento, fascicolo, pedali, reperto); the reconstructed gold annotates no objects, so object precision/recall needs an object-annotated corpus |
| `phrases_annotated` | **NOT_RUN** | 121 phrase candidates are grounded; the reconstructed gold annotates no important phrases, so phrase precision/recall needs phrase annotations |
| `queries_grounded` | **PASS** | 2212 query rungs, 0 not verbatim source slices |

## Misure

```json
{
  "gold_concepts": 19,
  "gold_core_concepts": 15,
  "emitted_entities": 405,
  "emitted_important_words": 136,
  "phrase_candidates": 121,
  "query_rungs": 2212,
  "keyword_named_share": 0.2059
}
```
