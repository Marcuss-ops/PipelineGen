# GeoNames gazetteer — misura sul corpus Garlasco

Corpus: `/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/semantic_acceptance/fixtures/garlasco_full.txt` (2797 parole, 12 scene da 220 parole).

Indice: **69433 places**, **349787 aliases**, generato da GeoNames `cities500`
(feature class P, popolazione >= 5000, più capitali; CC BY 4.0).

Baseline: seed curato `entity_gazetteer.json` senza `places` né
`place_ambiguities`. Abilitato: indice completo dietro la stessa tabella.

## Esito

| Livello | Baseline | GeoNames | Gold recall (baseline -> GeoNames) |
| --- | --- | --- | --- |
| VisualNER | 5 loc / 7 menzioni | 6 loc / 8 menzioni | 1/2 -> 2/2 |
| semantic-engine manifest | 5 loc / 7 menzioni | 6 loc / 8 menzioni | 1/2 -> 2/2 |

## Cosa cambia

- Aggiunte: Pavia
- Rimosse: nessuna

## Controlli di precisione

- `Corte`: PASS (escluso)
- `Come`: PASS (escluso)
- `Pavia`: PASS (riconosciuto)

## Note

- Il corpus non nomina Garlasco né Vigevano: le uniche località del gold
  sono Milano e Pavia.
- Nel manifest del semantic-engine `Procura di Pavia` resta un'ORG, ma
  l'upgrade legale tocca solo l'entità con lo stesso inizio: `Pavia`
  sopravvive come LOCATION e anche il manifest passa a 2/2.
- La posizione `Corte` (tribunale) e `Come` (avverbio) restano fuori:
  il tag UDPipe e la lista di ambiguità italiane le escludono.
