# Index Clip Manifest — Template

Questo file `cmd/admin/manifests/_TEMPLATE.json` è un **placeholder conforme** al
validatore strict di `cmd/admin/index_drive_clip.go`. NON va passato così com'è
al comando: copia / rinomina, sostituisci OGNI valore `REPLACE_*`, poi lancia:

```bash
go run ./cmd/admin index-drive-clip \
    --manifest cmd/admin/manifests/<slug>.json
```

## Vincoli dello strict decoder (`loadIndexClipManifest`)

Il decoder usa `disallowUnknownFields`: qualsiasi campo che NON sia nella lista
sotto provoca un errore di decode. Non puoi aggiungere campi custom (niente
`batch_id`, `pipeline`, `_about`, …). I placeholder sono valori di campi
*validi*.

## Campi

| campo                       | required | tipo                          | note                                                                  |
| --------------------------- | -------- | ----------------------------- | --------------------------------------------------------------------- |
| `drive_file_id`             | sì       | string                        | ID Drive (44 caratteri, parte di `https://drive.google.com/file/d/<id>/view`) |
| `name`                      | sì       | string                        | titolo human-readable                                                 |
| `description`               | sì       | string                        | paragrafo principale, entra in `SearchText`                           |
| `description_alt`           | no       | string                        | variante localizzata (es. IT)                                         |
| `tags`                      | sì (≥1)  | array di string               | anche in `SearchText`                                                 |
| `source`                    | sì       | string enum                   | `clip_drive` / `ai_generated` / `stock` / `youtube_clip`              |
| `category`                  | sì*      | string                        | *serve `category` OPPURE `local_subdir`                              |
| `group`                     | sì       | string                        | es. `funny_animals`, `topfive_fish`                                   |
| `local_subdir`              | no       | string                        | override di `category` per la directory di storage locale             |
| `default_filename`          | no       | string                        | usato SOLO se Drive restituisce `name=""` (raro)                      |
| `duration_fallback_seconds` | no       | int ≥ 0                       | se > 0 richiede `--allow-declared-duration` su `index-drive-clip`     |
| `metadata`                  | no       | oggetto `string→string`       | chiavi tipiche: `content_type`, `subject`, `visual_summary`, `timeline_json`, `hook`, `audio_policy`, `sound_design_plan` |

`*` = uno dei due deve essere valorizzato.

## Behavioral note

Anche compilato correttamente, il template con i `REPLACE_*` è un manifest
"valido al decoder", ma il runtime fallirà alla prima chiamata Drive
(`GetFileMeta(ctx, "REPLACE_WITH_GOOGLE_DRIVE_FILE_ID")` → 404). È un
fail-loud esplicito, non un silent false success (godlike/07).

## Esempi canonici

Vedi `cmd/admin/manifests/beluga.json` (`source: clip_drive`, fallback 9s) e
`cmd/admin/manifests/stargazer-fish.json` (`source: ai_generated`, fail-closed).

## Runbook completo

Vedi `scripts/batch_index_drive_clips.md` per il flusso end-to-end
(reconcile → manifest → batch wrapper → `extract-metadata.ts` → re-embed).
