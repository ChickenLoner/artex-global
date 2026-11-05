#!/usr/bin/env bash
set -euo pipefail

# Render assigns the PORT environment variable arbitrarily, so pass an argument that makes the app listen on that port
# Run normally and keep the skills/data paths
: "${PORT:?The PORT environment variable must be set}"
echo "[*] Starting ARTEX Server on port :$PORT"
exec ./artex -addr ":$PORT"
