# Garlasco animation audit

- Source: `overlay.full.json`
- Plan: `run_efeb24bc-dc13-4f87-a75b-5039ef15e7a2` — 30 items
- Checks: 4/14 PASS

| ID | Check | Status |
| --- | --- | --- |
| G01 | 2 images side by side | FAIL |
| G02 | 3-5 images in one group | NOT_OBSERVED |
| E01 | 2 co-mentioned entities with 2 portraits and 2 captions | FAIL |
| E02 | repeated entities grouped correctly without asset reuse | NOT_OBSERVED |
| M01 | maps of real narrated places | FAIL |
| S01 | short phrases (2-5 words) extracted and animated | PASS |
| L01 | long phrases (6-16 words) with more than one motion family | PASS |
| F01 | group final animation declared | FAIL |
| B01 | at least 2 grounded bullets | FAIL |
| B02 | bullet animation visible and distinct | FAIL |
| C01 | no fabricated numeric comparison | PASS |
| C02 | technical chart canary (grounded values, bars and labels) | NOT_APPLICABLE |
| X01 | replace-mode atomicity (no chart split across colliding overlays) | PASS |
| X02 | non-chart same-frame collisions | WARN |

## G01 — 2 images side by side: FAIL

```json
[]
```

## G02 — 3-5 images in one group: NOT_OBSERVED

```json
[]
```

## E01 — 2 co-mentioned entities with 2 portraits and 2 captions: FAIL

```json
[]
```

## E02 — repeated entities grouped correctly without asset reuse: NOT_OBSERVED

```json
{
 "entities": {},
 "duplicates": []
}
```

## M01 — maps of real narrated places: FAIL

```json
{
 "count": 0,
 "maps": [],
 "ungrounded": []
}
```

## S01 — short phrases (2-5 words) extracted and animated: PASS

```json
[
 {
  "scene_id": "appello-2011",
  "text": "Corte d’Assise d’Appello confermò l’assoluzione",
  "words": 5,
  "motion_id": "phrase_apple_clean_02_blur_focus_snap"
 },
 {
  "scene_id": "appello-2011",
  "text": "quindi evitare due semplificazioni opposte",
  "words": 5,
  "motion_id": "phrase_apple_clean_20_tracking_spread_clean"
 },
 {
  "scene_id": "appello-2011",
  "text": "procuratore generale chiese una condanna",
  "words": 5,
  "motion_id": "phrase_apple_clean_07_slide_up_soft"
 },
 {
  "scene_id": "appello-2011",
  "text": "difesa continuò a sostenere l’innocenza",
  "words": 5,
  "motion_id": "phrase_apple_clean_16_scale_line_build"
 },
 {
  "scene_id": "cassazione-2013",
  "text": "Cassazione ritenne necessari approfondimenti istruttori",
  "words": 5,
  "motion_id": "phrase_apple_clean_11_slide_left_ease"
 },
 {
  "scene_id": "cassazione-2013",
  "text": "Cassazione annullò la decisione d’appello",
  "words": 5,
  "motion_id": "phrase_apple_clean_08_slide_up_spring"
 },
 {
  "scene_id": "appello-bis-2014",
  "text": "Quella conferma arrivò l’anno seguente.",
  "words": 5,
  "motion_id": "phrase_apple_clean_09_slide_down_catch"
 },
 {
  "scene_id": "appello-bis-2014",
  "text": "d’Assise d’Appello dispose la riapertura",
  "words": 5,
  "motion_id": "phrase_apple_clean_17_scale_in_place"
 },
 {
  "scene_id": "appello-bis-2014",
  "text": "procedimento furono quindi esaminati ulteriori",
  "words": 5,
  "motion_id": "phrase_apple_clean_15_scale_hero_focus"
 },
 {
  "scene_id": "appello-bis-2014",
  "text": "processo d’appello bis",
  "words": 3,
  "motion_id": "phrase_apple_clean_28_opacity_cinematic"
 }
]
```

## L01 — long phrases (6-16 words) with more than one motion family: PASS

```json
{
 "count": 10,
 "motions": [
  "apple_expand_from_center",
  "apple_line_cascade",
  "apple_soft_scale",
  "brush_phrase_white_light_sweep",
  "counter_scroll_reveal",
  "doc_03_documentary_quote_marker",
  "hero_scale_focus",
  "isometric_3d_fold",
  "text_3d_camera_push",
  "text_3d_roll_depth"
 ],
 "scenes": [
  "chiara-poggi",
  "appello-2011",
  "appello-2011",
  "cassazione-2013",
  "appello-bis-2014",
  "appello-bis-2014",
  "appello-bis-2014",
  "cassazione-2015",
  "cassazione-2015",
  "chiusura-cronologia"
 ]
}
```

## F01 — group final animation declared: FAIL

```json
[]
```

## B01 — at least 2 grounded bullets: FAIL

```json
{
 "count": 0,
 "grounded": [],
 "ungrounded": [],
 "roles": []
}
```

## B02 — bullet animation visible and distinct: FAIL

```json
[]
```

## C01 — no fabricated numeric comparison: PASS

```json
{
 "cards": 0,
 "bars": 0,
 "groups": {},
 "violations": []
}
```

## C02 — technical chart canary (grounded values, bars and labels): NOT_APPLICABLE

```json
"Charts in a news script may legitimately be zero; the positive canary is e2e_cert/T04 (C01-C05, 28 animated bars)."
```

## X01 — replace-mode atomicity (no chart split across colliding overlays): PASS

```json
{
 "split_chart_groups": []
}
```

## X02 — non-chart same-frame collisions: WARN

```json
{
 "count": 2,
 "pairs": [
  [
   "date-chronology-focus__13-agosto-2007__date_number__01",
   "phrase-apple-clean-18-scale-card-tilt__chiara-poggi__entity_image__01",
   "delitto-e-luogo"
  ],
  [
   "metric-odometer-vertical__26-anni__date_number__01",
   "phrase-apple-clean-23-tracking-precision-lock__chiara-poggi__entity_image__01",
   "chiara-poggi"
  ]
 ]
}
```
