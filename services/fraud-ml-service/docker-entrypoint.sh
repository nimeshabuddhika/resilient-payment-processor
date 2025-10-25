#!/usr/bin/env sh
set -eu

# Ensure a clean slate for multiprocess files on each start
if [ -n "${PROMETHEUS_MULTIPROC_DIR:-}" ] && [ -d "$PROMETHEUS_MULTIPROC_DIR" ]; then
  rm -f "$PROMETHEUS_MULTIPROC_DIR"/*.db 2>/dev/null || true
fi

exec "$@"
