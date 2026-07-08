# kvt-fuse: FUSE Filesystem for KVT Vault

**Status:** Spec
**Date:** 2026-07-08

## Overview

`kvt-fuse` is a Linux FUSE (Filesystem in Userspace) helper that exposes a KVT
vault as a native filesystem. It mounts the vault at a user-specified path so
that AI agents can use plain shell verbs (`cat`, `grep`, `sed`, `git log`,
`mv`, `rm`) against KVT-backed content.

Every file write through the mount triggers a KVT REST API call and produces a
git commit on the daemon side — preserving the same granular auto-commit the
HTTP/MCP tools provide.

## Architecture

```
Agent shell (cat, grep, sed, git)
       │
       ▼  FUSE protocol
┌──────────────────────┐
│    kvt-fuse          │  Rust binary (static musl, ~5 MB)
│    (fuser crate)     │
├──────────────────────┤
│  Working copy model: │
│  open()  → GET from KVT → local tmp file
│  write() → accumulate in tmp
│  close() → POST/PATCH to KVT HTTP API
├──────────────────────┤
│  IPC: HTTP to KVT    │  http://localhost:8200
└──────────┬───────────┘
           │ HTTP
           ▼
┌──────────────────────┐
│  KVT daemon          │  localhost:8200
│  (Go service)        │
├──────────────────────┤
│  Git auto-commit     │
│  Search index        │
│  Ontology validation │
└──────────────────────┘
```

## FUSE Operation → KVT API Mapping

| FUSE Callback | KVT API | Notes |
|---|---|---|
| `lookup(path)` | `GET /concepts/{path}` | Check file exists, return attr |
| `getattr(path)` | `GET /concepts/{path}` | Return file attributes (size, mtime) |
| `readdir(dir)` | `GET /concepts?path_prefix={dir}` | List directory contents |
| `open(path, RDONLY)` | `GET /concepts/{path}` | Fetch content → working copy |
| `read(fh, offset, size)` | (from working copy) | Serve cached content |
| `write(fh, offset, data)` | (to working copy) | Buffer writes |
| `flush/release(fh)` | `POST /concepts` (new) or `PATCH /concepts/{path}` (edit) | Push to KVT + git commit |
| `create(path, mode)` | (allocate working copy) | Empty file, no API call yet |
| `unlink(path)` | `DELETE /concepts/{path}` | Remove from vault |
| `rename(from, to)` | `POST /concepts/{to}` + `DELETE /concepts/{from}` | Copy + delete |
| `mkdir(path)` | (no-op) | KVT is flat, but return success |
| `rmdir(path)` | `GET /concepts?path_prefix={path}` | Check empty, then no-op |
| `chmod` / `chown` | (no-op, return 0) | KVT has no POSIX permissions |
| `statfs` | Return default values | Report reasonable block counts |

## Open-to-Close Consistency

The mount does NOT stream bytes to KVT on every `write()` call. Instead:

1. **`open(path, O_RDONLY)`** — the helper fetches content from KVT via
   `GET /concepts/{path}` and writes it to a local scratch file in the
   working-copy directory.

2. **`write(fh, offset, data)`** — bytes accumulate in the scratch file;
   nothing hits KVT yet.

3. **`flush(fh)` / `release(fh)`** — the helper:
   - Computes the SHA-256 of the scratch file.
   - If the file was opened for writing and content changed:
     - If unmodified from KVT head → do nothing (dedup).
     - If a new file (`base_hash` empty) → `POST /concepts` with content.
     - If an edit → `PATCH /concepts/{path}` with full new content.
   - Cleans up the scratch file.

4. **`open(path, O_WRONLY | O_CREAT | O_TRUNC)`** — no fetch; empty scratch.
   On close, POST to KVT.

This means:
- `echo "x" > foo` produces **one** version per `close()`.
- Two concurrent writers produce whichever finishes last wins (KVT's last-write-wins; conflict detection via `base_hash` is future work).
- `touch` with no content change produces zero API calls.

## Mount Layout

```
/workspace/               (mountpoint)
├── desks/
│   ├── swing/
│   │   ├── index.md
│   │   └── monitor/
│   │       └── dashboard.md
│   ├── intraday/
│   │   ├── index.md
│   │   └── sessions/
│   └── social/
├── assets/
│   ├── AAPL/
│   │   └── index.md
│   └── AMD/
├── inbox/
├── macro/
├── pulse/
...                       (full vault tree)
└── .kvt/                 (excluded from FUSE)
```

The vault is mounted at the specified mountpoint. The root of the mount
corresponds to the KVT vault root. All concept documents appear as regular
files. Virtual files (`.agent-fs/conflicts`, etc.) are not in scope for v1.

## CLI Interface

```
kvt-fuse --mountpoint /workspace --api-url http://localhost:8200
```

Options:

| Flag | Default | Description |
|---|---|---|
| `--mountpoint` | (required) | Mount path |
| `--api-url` | `http://localhost:8200` | KVT HTTP API base URL |
| `--api-key` | `""` | Optional bearer token for KVT auth |
| `--log-file` | `~/.kvt/fuse.log` | Log file path |
| `--allow-other` | `false` | Allow other UIDs to access mount (needs `user_allow_other` in `/etc/fuse.conf`) |

## Build

```
cd cmd/kvt-fuse && cargo build --release
cross build --release --target x86_64-unknown-linux-musl
cross build --release --target aarch64-unknown-linux-musl
```

A stripped musl binary should be ~5 MB.

## Dependencies

- `fuser` crate — Rust FUSE bindings (libfuse3)
- `tokio` — async runtime for HTTP calls
- `reqwest` — HTTP client to KVT API
- `clap` — CLI argument parsing
- `sha2` — SHA-256 for content dedup
- `tracing` — structured logging
- `anyhow` — error handling

## Out of Scope (v1)

- Conflict detection / `base_hash` on writes
- Virtual sidecar files (`.agent-fs/conflicts.ndjson`)
- Symlink support within the vault
- Extended attributes
- POSIX lock support
- Write permissions/access control (KVT handles that via its API)