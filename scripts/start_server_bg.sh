#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "$0")/.."
source .env
exec ./bin/pipelinegen
