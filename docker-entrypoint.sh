#!/bin/sh
set -e
git config --global --add safe.directory /workspace 2>/dev/null || true

# Remove stale vault lock from unclean shutdowns
if [ -f /workspace/.kvt/lock ] && [ -f /workspace/.kvt/index.db ]; then
    rm -f /workspace/.kvt/lock
fi

exec /usr/local/bin/kvt "$@"