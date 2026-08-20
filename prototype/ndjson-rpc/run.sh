#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

echo "=== Building ==="
go build -o bin/plugin ./plugin
go build -o bin/host ./host

echo ""
echo "=== Running host against plugin ==="
mkdir -p plugins
cp bin/plugin plugins/
bin/host ./plugins
