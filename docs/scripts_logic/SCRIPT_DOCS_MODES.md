# Script Docs Modes

Questa pagina descrive i mode esposti dal backend `script-docs`.

## Obiettivo

La scelta del tipo di video è fatta dalla call API.
Il backend esegue il mode richiesto e mantiene i fallback interni a quel mode.

## Endpoint

- `GET /api/script-docs/modes`
- `POST /api/script-docs/generate`
- `POST /api/script-docs/generate/stock`
- `POST /api/script-docs/generate/fullartlist`
- `POST /api/script-docs/generate/imagesfull`
- `POST /api/script-docs/generate/imagesonly`
- `POST /api/script-docs/generate/mixed`
- `POST /api/script-docs/generate/jitstock`

## Mode supportati

### `default`

- Alias: `stock`
- Uso: generazione stock-first standard
- Fallback interni: `dynamic-cache -> stockdb -> artlist -> llm-expansion -> dynamic-search`
- JIT stock: no
- Lingua default consigliata: `it`

### `fullartlist`

- Uso: generazione Artlist-only
- Fallback interni: solo Artlist
- JIT stock: no
- Lingua default consigliata: `en`

### `images_full`

- Uso: generazione ricca di immagini, con pianificazione e download immagini
- Fallback interni: `imagesdb-cache -> entityimages -> download`
- JIT stock: no
- Lingua default consigliata: `en`

### `images_only`

- Uso: generazione solo immagini, senza embedding di clip
- Fallback interni: `imagesdb-cache -> entityimages -> download`
- JIT stock: no
- Lingua default consigliata: `en`

### `mixed`

- Uso: per capitolo il sistema può scegliere clip o immagini
- Fallback interni: `clip -> image`
- JIT stock: no
- Lingua default consigliata: `it`

### `jitstock`

- Uso: stock just-in-time quando mancano asset utili in Drive
- Fallback interni: `stockdb -> artlist -> youtube -> gemma -> download -> drive`
- JIT stock: sì
- Lingua default consigliata: `it`

## Risposta del endpoint `/modes`

Il backend restituisce una lista con:

- `mode`
- `label`
- `description`
- `fallbacks`
- `allows_jit`
- `default_lang`

## Esempio

```bash
curl -X GET http://localhost:8080/api/script-docs/modes
```

```json
{
  "ok": true,
  "modes": [
    {
      "mode": "default",
      "label": "stock",
      "description": "Stock-first video generation with standard clip matching and Artlist fallback.",
      "fallbacks": ["dynamic-cache", "stockdb", "artlist", "llm-expansion", "dynamic-search"],
      "allows_jit": false,
      "default_lang": ["it"]
    }
  ]
}
```

## Nota operativa

Se vuoi un comportamento deterministico lato prodotto, scegli il mode nella chiamata API e non lasciare al backend la scelta del tipo video.
