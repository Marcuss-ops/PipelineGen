"""CLI entry point for the embedding server.

`python3 -m scripts.services.embedding_server --port 8001` starts the
FastAPI app under uvicorn. After the sub-package split, the systemd unit
docs/systemd/pipelinegen-embedding-server.service was updated to use the
`-m` invocation so Python resolves `scripts.services.embedding_server`
as a top-level package from the project root.

Flags:
  --port  TCP port to listen on (default 8001).
  --host  Bind address (default 127.0.0.1).
"""

import argparse

import uvicorn

from . import app  # noqa: F401  (imported for side-effect: load models + register routes)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=8001)
    parser.add_argument("--host", default="127.0.0.1")
    args = parser.parse_args()
    uvicorn.run(app, host=args.host, port=args.port)


if __name__ == "__main__":
    main()
