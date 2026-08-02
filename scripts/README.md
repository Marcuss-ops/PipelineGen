# Python Scripts — PipelineGen

## Directory Structure (June 2026)

```
scripts/
├── core/                  # Shared libraries (single source of truth)
│   └── ollama_client.py   #   Client Ollama unico: generate(), chat(), generate_json()
├── bridges/               # Go→Python bridges (chiamati da Go via exec.Command)
│   ├── animate_image.py    #   Zoom-out MP4 da immagine statica
│   ├── book_processor/     #   Pipeline riscrittura libri (PDF/EPUB)
│   ├── book_summarizer.py  #   Entry point CLI per book_processor
│   ├── generate_embedding.py  # One-shot embedding E5
│   ├── semantic_tagger.py  #   Metadata semantico via Ollama + taxonomy
│   └── tts_edge.py         #   Text-to-speech via Edge TTS
├── services/              # Server ML persistenti (FastAPI, chiamati via HTTP)
│   ├── embedding_server.py #   E5 embeddings + CLIP + CLAP
│   └── reranker_server.py  #   CrossEncoder reranking
├── tools/                 # Utility CLI manuali (non chiamate da Go)
│   ├── analyze_interesting_segments.py
│   ├── agent_script_writer.py
│   ├── batch_generate_and_merge.py
│   ├── benchmark_search.py
│   ├── generate_drive_token.py
│   ├── send_batch_book.py
│   ├── send_batch_book_v2.py
│   ├── sketchfab_client.py
│   ├── synth_sfx.py
│   ├── transcribe_detect_lang.py
│   └── update_language.py
├── experiments/           # Sperimentali, non in produzione
│   └── sound_designer.py  #   SFX automatico via Vision LLM
├── tests/                 # Test manuali / suite
│   ├── test_all_exports.py
│   └── test_book_summarizer.py
├── ci-architectural-checks.sh
└── README.md
```

## Architecture Note

Go owns the API layer, job system, database, and orchestration. Python owns ML model inference (sentence-transformers, faster-whisper, edge-tts, CrossEncoder). The boundary is deliberate — Python ML libraries have no native Go equivalent.

When adding new functionality, prefer Go for orchestration and API logic. Use Python only when the task requires a Python ML library that has no Go alternative.

---

## Shared Library: `core/ollama_client.py`

- **Metodi**: `generate()` (raw prompt), `chat()` (structured messages), `generate_json()` (con cleanup markdown)
- **Endpoint**: Supporta sia `/api/generate` che `/api/chat` di Ollama
- **Usato da**: `bridges/book_processor/llm.py`, `bridges/agent_writer/llm.py`, `bridges/semantic_tagger.py`, `experiments/sound_designer.py`
- **Dependencies**: solo stdlib (`urllib`, `json`, `re`)

---

## Go→Python Bridges (`bridges/`)

Chiamati dal backend Go via `exec.Command`.

| Script | Chiamato da | Dipendenze |
|--------|-------------|------------|
| `book_summarizer.py` + `book_processor/` | `internal/media/books/service.go` | Ollama, PyMuPDF, ffmpeg |
| `semantic_tagger.py` | `internal/media/semantic/tagger.go` | Ollama, PyYAML |
| `generate_embedding.py` | `internal/media/association/embeddings.go` | sentence-transformers |
| `tts_edge.py` | `internal/media/audioasset/processor.go` | edge-tts |
| `animate_image.py` | `docs/images/animate.go` | ffmpeg |

---

## ML Services Persistente (`services/`)

Server FastAPI che girano come processi separati e vengono chiamati via HTTP.

| Script | Porta | Chiamato da |
|--------|-------|-------------|
| `embedding_server.py` | 8001 | `internal/media/clipindexer/`, `realtime/embedding_adapter.go` |
| `reranker_server.py` | 8091 | `internal/reranker/client.go` |

---

## Utility CLI (`tools/`)

Script standalone per operazioni batch o one-shot. **Non chiamati da Go.**

| Script | Scopo |
|--------|-------|
| `update_language.py` | Rilevamento lingua batch via faster-whisper |
| `transcribe_detect_lang.py` | Trascrizione + lingua via faster-whisper |
| `sketchfab_client.py` | Client API Sketchfab (search + download) |
| `generate_drive_token.py` | Generazione token OAuth2 Google Drive |
| `benchmark_search.py` | Benchmark ricerca semantica |
| `agent_script_writer.py` | Generazione script batch |
| `send_batch_book.py` / `send_batch_book_v2.py` | Invio batch libri (2 versioni) |
| `batch_generate_and_merge.py` | Generazione + merge batch |
| `analyze_interesting_segments.py` | Analisi segmenti interessanti |
| `synth_sfx.py` | Sintesi effetti sonori |

---

## Environment Variables

| Variable | Default | Used by |
|----------|---------|---------|
| `OLLAMA_URL` | `http://localhost:11434` | `bridges/semantic_tagger.py` |
| `EMBEDDING_SERVER_URL` | `http://127.0.0.1:8001` | `start_embedding_server.sh` / Go clip indexer |
