# AGENTS.md

This file is for coding agents working on the KVT repository. For
guidance on editing a KVT vault directly, see `SKILL.md`.

## Project Shape

KVT is a Go service and CLI in module `github.com/ematvey/kvt`.

Key areas:

- `cmd/kvt`: CLI entrypoint and command wiring.
- `internal/service`: core vault operations, locking, validation,
  read/write/edit/delete, push, and search orchestration.
- `internal/index`: SQLite schema, FTS/vector indexing, list/grep,
  reconciliation, and summary queries.
- `internal/ontology`: `_ontology.yaml` loading and validation.
- `internal/vault`: service-owned `index.md` generation and link
  extraction.
- `internal/httpapi`: REST server.
- `internal/mcp`: MCP server, tools, resources, prompts, and howto
  text.
- `internal/config`: `.kvt/config.yaml` defaults and loading.
- `docs/verification/full-scope-audit.md`: current implementation
  coverage and scoped deviations from `VISION.md`.

## Ground Rules

- Treat markdown vault files as canonical user data. `.kvt/index.db`
  and embedding rows are derived state.
- Do not hand-maintain generated vault `index.md` files in examples or
  tests unless the test is specifically about generated indexes.
- Do not treat `VISION.md` as proof of implemented behavior. Verify
  against code, tests, and `docs/verification/full-scope-audit.md`.
- Preserve existing git history behavior: KVT writes forward commits;
  do not introduce reset, rebase, revert, or force-push behavior.
- Keep paths bundle-relative, lowercase, slash-separated, and safe.
- Keep root `_howto.md` as vault house rules, not as a concept file.
- Do not add an MCP push tool. Push is CLI/REST/service only.
- Keep response budgeting and cursor behavior for large REST/MCP
  outputs.

## Development Workflow

Before editing, inspect the real code paths involved. Prefer narrow
changes that match the existing package boundaries.

Use `apply_patch` for manual edits. Do not rewrite unrelated files or
format the whole repository unless the change requires it.

For Go changes:

```bash
gofmt -w <changed-go-files>
go test ./...
go vet ./...
```

For CLI-affecting changes, also run:

```bash
go build ./cmd/kvt
tmp=$(mktemp -d)
./kvt init --vault "$tmp" --defaults
rm -f ./kvt
```

For Docker-affecting changes, run:

```bash
docker build -t kvt:local .
```

Always finish with:

```bash
git diff --check
git status --short
```

## Testing Expectations

- Add or update tests for behavior changes.
- Prefer real temp git repos and SQLite over mocks where existing
  tests already do that.
- For service behavior, add focused tests under `internal/service`.
- For REST contracts, add tests under `internal/httpapi`.
- For MCP JSON shapes and tool behavior, add tests under
  `internal/mcp`.
- For path, ontology, chunking, response-budget, and search logic,
  keep tests in the owning package.

## Documentation Expectations

When behavior changes, update the docs that describe that behavior:

- `README.md` for user/operator-visible behavior.
- `AGENTS.md` for repository workflow guidance.
- `SKILL.md` for direct KVT vault authoring guidance.
- `docs/verification/full-scope-audit.md` when implementation coverage
  or scoped deviations change.

Keep documentation factual. If something is aspirational in `VISION.md`
but not implemented, call it out as current scope rather than implying
support.

## API Notes

REST routes are implemented in `internal/httpapi/server.go`. MCP tools
are registered in `internal/mcp/tools.go`.

Important contracts:

- REST auth uses `Authorization: Bearer <key>` when `auth.api_keys` is
  configured.
- `GET /concepts/{path}` supports positive 1-based line ranges through
  `start_line` and `end_line`.
- Conflicts return current content and hash.
- Validation failures return structured errors/warnings.
- Large list/grep/log/history responses include cursors and truncation
  flags.
- Request-scoped access params use glob fields `read_globs`,
  `write_globs`, and `deny_globs`. Missing access means unrestricted;
  explicit empty access denies read/write. Keep REST and MCP behavior
  routed through the shared service policy.
- MCP tool names must keep the `kvt_` prefix.

## Git Safety

The user may have local work. Never discard or reset changes you did
not make. Do not run destructive git commands unless explicitly asked.

This repository currently uses normal git branches, not generated
worktrees. Check `git status --short --branch` before committing or
reporting final state.
