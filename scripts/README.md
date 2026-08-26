# Scripts — PipelineGen

## Directory Structure (August 2026)

```
scripts/
├── core/                         # Shared libraries
│   └── ollama_client.py           #  Ollama client: generate(), chat(), generate_json()
├── bridges/                      # Go→Python bridges (called from Go via exec.Command)
│   ├── argos_bridge/              #  Argos Translate bridge (core, server)
│   ├── argos_server.py            #  Argos translation server entrypoint
│   ├── argos_translator.py        #  Argos translation CLI
│   ├── book_processor/            #  Book rewriting pipeline (PDF/EPUB)
│   ├── book_summarizer.py         #  Book processor entry point
│   ├── edge_tts_bridge/           #  Edge TTS bridge (boundaries, server, request, voice_resolver)
│   ├── generate_embedding.py      #  One-shot E5 embedding
│   ├── local_nlp_gpu.py           #  Local NLP GPU utilities
│   ├── login.py                   #  Authentication bridge
│   ├── semantic_tagger/           #  Semantic metadata via Ollama + taxonomy
│   ├── slide_worker.py            #  Slide worker entry point
│   ├── slide_worker_runtime/      #  Slide generation runtime (generation, extraction, dispatcher, etc.)
│   ├── storage_utils.py           #  Storage utilities
│   ├── tts_edge.py                #  Text-to-speech via Edge TTS (single call)
│   ├── tts_edge_server.py         #  Persistent Edge TTS server
│   └── whisper_transcriber.py     #  Whisper transcription bridge
├── services/                     # Persistent ML servers (HTTP, called from Go)
│   ├── embedding_server/          #  E5 embeddings + CLIP + CLAP (__main__, models, audio, text, visual)
│   ├── reranker_server.py         #  CrossEncoder reranking
│   ├── model_registry_generated.py #  Python mirror of internal/kernel/models (generated)
│   └── device_policy.py           #  GPU/CPU device selection policy
├── tools/                        # Manual CLI utilities (not called from Go)
│   ├── argos_install_models.py    #  Install Argos Translate language models
│   ├── generate_drive_token.py    #  OAuth2 token generation for Google Drive
│   ├── model_downloader.py        #  Download + verify ML model weights (registry SSOT)
│   ├── resolve_drive_ids.py       #  Resolve Drive file/folder IDs
│   ├── sync_drive_qdrant.py       #  Sync Drive contents to Qdrant
│   ├── transcribe_detect_lang.py  #  Transcription + language detection
│   ├── whisper_preflight.py       #  Whisper preflight check
│   └── whisper_runtime.py         #  Whisper runtime execution
├── tests/                        # Manual test data
│   └── seed_search_data.sql       #  Search test seed data
├── admin/                        # Administrative Go tooling
│   ├── architecture_p1_finalize.py
│   ├── generate_routes_yaml.go    #  Route manifest generator
│   ├── routes_yaml_ast.go
│   ├── routes_yaml_dedup.go
│   ├── routes_yaml_discovery.go
│   └── routes_yaml_types.go
├── archcheck/                    # Architecture governance CLI (legacy-burndown ratchet)
│   ├── main.go                    #  CLI entrypoint
│   ├── baseline/                  #  Baseline comparison + seeding
│   ├── gate/                      #  Gate runner
│   ├── gates/                     #  C2 gates (registry, route manifest, source catalog)
│   ├── testdata/                  #  Test fixtures (deprecations, report schema)
│   ├── checks*.go                 #  Architecture checks
│   ├── deprecations_*.go          #  Deprecation loader, validator, migrator
│   ├── phase0_*.go                #  Phase 0 report-mode checks
│   └── snapshot_test.go           #  Golden-file snapshot test
├── ci/                           # CI verification scripts
│   ├── architecture/checks/       #  Per-check shell scripts (15–73)
│   ├── architecture/checks/lib/   #  Shared CI libraries (00–59)
│   ├── verify-*.py                #  Python verification runners
│   ├── verify-changed.sh
│   ├── verify-split-contract.sh
│   ├── verify-stock-claim.sh
│   ├── verify-stock-receipt.sh
│   ├── ci-*.sh                    #  CI helpers (clean checkout, no-secrets, submodule, etc.)
│   ├── get-fingerprint.sh
│   ├── node-version-check.sh
│   ├── reconcile-pipeline.py
│   └── whisper-deployment-contract_test.py
├── lib/                          # Shell libraries
│   ├── dotenv.sh
│   └── artlist_pipeline_*.sh      #  Artlist pipeline helpers
├── systemd/                      # Systemd units and sudoers
│   ├── pipelinegenctl
│   ├── pipelinegen.service.d/     #  whisper.conf, youtube-dlp.conf
│   ├── ollama.service.d/          #  gpu.conf
│   ├── artlist-scraper-headful.conf
│   ├── sudoers/                   #  Operator access installers
│   └── README.md
├── hooks/                        # Git hooks
│   ├── pre-commit
│   └── pre-push
├── operations/                   # Operational shell scripts
│   ├── certify_media_registry_qdrant.sh
│   └── inspect_media_asset.sh
├── overlay-cert/                 # Overlay certification
│   └── verify_overlay_prepare_live.py
├── seed_fixture/                 # Fixture seeding tool
│   └── main.go
├── start_embedding_server.sh      #  Embedding sidecar launcher
├── operate_script_generate.sh     #  Operational script generation
├── worker-bootstrap-smoke.sh      #  Worker bootstrap smoke test
├── verify-ffmpeg.sh               #  FFmpeg verification
├── verify-image-digest.sh         #  Image digest verification
├── verify-whisper.sh              #  Whisper verification
├── verify_nlp_online_images_docs_certification.sh
├── batch_index_drive_clips.md     #  Batch indexing documentation
├── ci-architectural-checks.sh     #  Architectural CI checks entrypoint
├── ci-bypass-audit.sh            #  CI bypass audit
├── cosign-sign.sh                 #  Cosign image signing
├── regenerate_token.sh            #  Token regeneration
├── rotate_token.sh                #  Token rotation
├── velox_client.py                #  Velox broker client
├── run_stock.py                   #  Stock pipeline runner
├── youtube_boxer_stock_e2e.py     #  YouTube boxer stock E2E script
├── yt-dlp-pipeline                #  yt-dlp pipeline wrapper
├── with-velox-auth                #  Velox auth wrapper
├── with-velox-auth_test.sh        #  Velox auth wrapper test
├── generate_drive_token.py        #  Root-level token generation
├── requirements-argos.txt         #  Argos Python dependencies
└── requirements-whisper.txt       #  Whisper Python dependencies
```

## Architecture Note

Go owns the API layer, job system, database, and orchestration. Python owns ML model inference (sentence-transformers, faster-whisper, edge-tts, CrossEncoder). The boundary is deliberate — Python ML libraries have no native Go equivalent.

When adding new functionality, prefer Go for orchestration and API logic. Use Python only when the task requires a Python ML library that has no Go alternative.

---

## Environment Variables

| Variable | Default | Used by |
|----------|---------|---------|
| `OLLAMA_URL` | `http://localhost:11434` | bridges/semantic_tagger, core/ollama_client |
| `EMBEDDING_SERVER_URL` | `http://127.0.0.1:8001` | start_embedding_server.sh, Go clip indexer |
| `PIPELINEGEN_EMBEDDING_DEVICE` | `auto` | Embedding sidecar device: `auto`, `cpu`, or `cuda` |
| `PIPELINEGEN_EMBEDDING_REQUIRE_GPU` | `0` | Embedding sidecar: fail closed when CUDA is unavailable |
| `PIPELINEGEN_RERANKER_DEVICE` | `auto` | Reranker device: `auto`, `cpu`, or `cuda` |
| `PIPELINEGEN_RERANKER_REQUIRE_GPU` | `0` | Reranker: fail closed when GPU is required but unavailable |