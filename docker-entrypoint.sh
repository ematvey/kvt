#!/bin/sh
set -e
git config --global --add safe.directory /workspace 2>/dev/null || true
exec /usr/local/bin/kvt "$@"