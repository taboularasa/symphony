#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"

cd "$repo_root"

if [ -f go.mod ]; then
  go mod download
else
  echo "No Go module yet; nothing to initialize."
fi
