# PipelineGen Web Admin

UI React/Vite per gestire i database media di PipelineGen: Artlist, Stock, YouTube Clips, Voiceover e Images.

## Installazione

```bash
cd src/go-master/web-admin
npm install
npm run dev
```

## Backend

In sviluppo Vite fa proxy verso `http://localhost:8080` per tutte le chiamate `/api`.

Puoi cambiare target:

```bash
VITE_API_PROXY_TARGET=http://localhost:8080 npm run dev
```

## Endpoint usati

- `GET /api/media/:source/clips`
- `POST /api/media/:source/clips/:id/verify`
- `POST /api/media/:source/clips/:id/reupload`
- `POST /api/media/:source/clips/:id/reprocess`
- `POST /api/media/:source/clips/:id/trash`
- `POST /api/media/:source/clips/:id/delete`
- `POST /api/artlist/run`

Endpoint admin consigliati da aggiungere nel backend:

- `PATCH /api/admin/:source/clips/:id`
- `POST /api/admin/:source/clips`
- `DELETE /api/admin/:source/clips/:id/db-only`
- `POST /api/admin/:source/clips/bulk-update`

Se il backend non è acceso, la UI mostra mock data per permettere sviluppo frontend.
