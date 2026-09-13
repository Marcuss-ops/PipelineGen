# Scripts — PipelineGen

## Directory Structure (verified 2026-09-13)

```
scripts/
├── ci/                           # CI verification
│   ├── verify-component.py        #  Component runner (canonical)
│   ├── verify_changed-components.py, verify-all-components.py
│   ├── verify-component-coverage.py
│   ├── verify_component_{cache,core,fingerprint,registry,runner}.py
│   ├── verify_runtime.py
│   ├── get-fingerprint.sh
│   ├── ci-no-secrets-audit.sh
│   ├── ci-submodule-integrity.sh
│   └── check_clip_render_cutover.sh
├── hooks/                        # Git hooks
│   ├── pre-commit
│   └── pre-push
├── lib/                          # Shell libraries
│   ├── dotenv.sh
│   └── canonical_db_path.sh
├── systemd/                      # Systemd units + operator sudoers
│   ├── pipelinegenctl
│   ├── pipelinegen.service, pipelinegen-worker.service, chronon3d.service
│   ├── pipelinegen.service.d/     #  whisper.conf, youtube-dlp.conf, chronon-warm.conf
│   ├── pipelinegen-embedding-server.service.d/  #  render-isolation.conf
│   ├── ollama.service.d/          #  gpu.conf
│   ├── sudoers/                   #  pipelinegen-operator(.template)
│   └── README.md
├── bridges/                      # Go→Python bridges (exec.Command)
│   ├── edge_tts_bridge/           #  TTS bridge (boundaries, server, request, voice_resolver)
│   ├── tts_edge.py, tts_edge_server.py
│   └── whisper_transcriber.py
├── services/                     # Persistent ML servers (HTTP, called from Go)
│   ├── embedding_server/          #  E5 + CLIP + CLAP (__main__, models, audio, text, visual)
│   ├── device_policy.py           #  GPU/CPU device selection policy
│   └── model_registry_generated.py #  Python mirror of internal/kernel/models (generated)
├── tools/                        # Manual CLI utilities (not called from Go)
│   ├── whisper_preflight.py
│   └── whisper_runtime.py
├── admin/                        # Route-manifest generator (Go)
├── bench/                        # Headless benchmark drivers (lib/ + report/)
├── dev/                          #  e2e-up.sh
├── operations/                   #  migrate-media-text-tracks-once.go
├── seed_fixture/                 #  Fixture seeding tool (Go)
├── os.sh, preflight-e2e.sh, regen_hotspots.py, regenerate_token.sh
├── verify-whisper.sh, yt-dlp-pipeline, with-velox-auth
├── requirements-argos.txt, requirements-whisper.txt
├── batch_index_drive_clips.md
└── README.md
```

**Not in the tree** (deleted by commit `7e6965aab`, "purge 94% shell + 87%
python dust"; do not cite them as present): `ci-architectural-checks.sh`,
`rotate_token.sh`, `velox_client.py`, `verify-ffmpeg.sh`,
`verify-image-digest.sh`, `cosign-sign.sh`, `start_embedding_server.sh`,
`ci-bypass-audit.sh`, `run_stock.py`, `youtube_boxer_stock_e2e.py`,
`with-velox-auth_test.sh`, `operations/inspect_media_asset.sh`,
`operations/certify_media_registry_qdrant.sh`, `overlay-cert/`, `core/`, and
the former `bridges/*` + `tools/*` families beyond the two listed above.
Admin credential rotation is manual (see `AGENTS.md`, § Authentication SSOT).

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
