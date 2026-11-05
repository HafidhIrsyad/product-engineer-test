#!/usr/bin/env bash
# Presentation scenarios (ARCHITECTURE.md §18.7). Run `make up` and `make seed` first.
# Scenarios are added together with their endpoints; for now it only checks the API is up.
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"

step() { printf '\n== %s ==\n' "$1"; }

step "Health check"
curl -sS -i "$BASE_URL/healthz"
echo
