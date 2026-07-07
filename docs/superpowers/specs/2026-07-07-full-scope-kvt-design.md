# Full-Scope KVT Implementation Design

Date: 2026-07-07

## Purpose

KVT is a Go service for OKF-conformant knowledge vaults. The
implementation must cover the full scope in `VISION.md`: git-backed
markdown storage, ontology-aware validation, SQLite FTS/vector search,
REST and MCP APIs, CLI operations, async embeddings and push, config,
Docker packaging, and tests that treat the spec as the contract.

This design is not an MVP carve-out. Work can land in verifiable
vertical slices, but the architecture must preserve the complete final
surface instead of introducing temporary behavior that conflicts with
the full specification.

## Architecture

The system is organized around one core service package. CLI commands,
REST handlers, and MCP tools all call this same service layer. This
keeps the write path identical regardless of the entry point and makes
agent-visible behavior testable once.

The core write path is:

1. Normalize and validate the bundle-relative path.
2. Read the current file state and enforce any `base_hash` precondition.
3. Parse markdown frontmatter and body.
4. Inject the authoritative service `timestamp`.
5. Validate against `_ontology.yaml` using strict or advisory mode.
6. Apply the create, replace, edit, or delete operation.
7. Regenerate affected service-owned `index.md` files.
8. Update SQLite metadata, chunks, FTS, links, and fields
   synchronously.
9. Commit the file/index/index-metadata changes through the real `git`
   binary.
10. Enqueue async embedding and push work according to config.

Reads never mutate the vault. They read from the canonical files and
the derived index, returning explicit freshness or degraded-mode status
when vector data or async work is behind.

## Package Layout

The implementation should use focused packages with narrow contracts:

- `cmd/kvt`: CLI entry point and command wiring.
- `internal/config`: defaults, YAML config, environment override
  handling, and validation.
- `internal/pathutil`: bundle-relative path normalization, validation,
  and slug suggestions.
- `internal/frontmatter`: markdown frontmatter parse, render, hash,
  and timestamp mutation.
- `internal/ontology`: `_ontology.yaml` loading, path rules, field
  constraints, validation, and type summaries.
- `internal/vault`: filesystem traversal, concept reads, service-owned
  index regeneration, link extraction, summary/list helpers, and
  vault locking.
- `internal/gitops`: all real `git` binary operations, branch checks,
  commits, history, file history, remote status, and push.
- `internal/chunk`: heading-aware markdown chunking with breadcrumb
  text, offset tracking, atomic code blocks, and table handling.
- `internal/index`: SQLite schema, migrations, reconciliation, rebuild,
  docs/chunks/FTS/fields/links, vec table integration, and index meta.
- `internal/embed`: embedder interface plus OpenAI-compatible and
  Ollama native adapters.
- `internal/rerank`: OpenAI-compatible LLM rerank adapter and fallback
  behavior.
- `internal/search`: FTS query, vector query, RRF fusion, result
  aggregation, and degraded-mode reporting.
- `internal/service`: the API-neutral application service and typed
  request/response models.
- `internal/httpapi`: REST routing, auth, encoding, and route tests.
- `internal/mcp`: MCP server, tools, resources, prompt/instructions,
  and tool descriptions.
- `internal/responsebudget`: shared soft response budget and cursor
  truncation helpers.

Package boundaries should keep domain rules out of transport handlers.
Transport packages translate protocol requests into service calls and
format protocol-specific responses only.

## Vault and Git Semantics

Markdown files and git history are canonical. `.kvt/` contains only
runtime or derived artifacts: `config.yaml`, `index.db`, lockfiles,
queue state, and push/index status.

`kvt init` supports empty-directory bootstrap and existing-repository
adoption. Empty bootstrap creates a `main` branch, `.gitignore`,
root `index.md`, starter `_ontology.yaml`, `.kvt/config.yaml`, and an
initial commit. Adoption appends missing KVT support files without
rewriting existing content or branches.

`kvt serve` refuses uninitialized vaults, detached HEAD, wrong vault
branch, and already-locked vaults. These conditions are surfaced in
`/health` and CLI errors. Writes are serialized by an in-process
writer lock; reads continue while writes are waiting.

Every write produces exactly one forward git commit. KVT never resets,
reverts, rebases, force-pushes, or rewrites history. Restoring older
content is a normal new write commit.

Push is an operator concern. `POST /push` and `kvt push` trigger it;
there is no MCP push tool. Configured automatic push modes are
`off`, `on_change`, and `debounced`. Pushes are asynchronous,
fast-forward only, retried with backoff, and never fail the write that
created the commits.

## Ontology and Validation

`_ontology.yaml` is optional. When present, it defines types, required
fields, optional fields, field constraints, unknown-type policy, and
path-to-type rules.

Validation supports:

- Required frontmatter fields.
- Unknown type handling: `allow`, `warn`, or `reject`, defaulting to
  `warn`.
- Enum constraints with ontology-order sorting.
- Regex pattern constraints.
- `ref` constraints to bundle-relative paths, with optional target
  type checking.
- Path rules that require files under a glob to carry a specific type.
- Broken body links and broken `ref` links during whole-vault
  validation.

Writes validate current content at write time. Changing the ontology
does not retroactively block existing files; `kvt validate` reports
drift on demand.

## Indexing and Search

SQLite is a derived artifact in `.kvt/index.db`. Rebuilds must be able
to reconstruct it from the working tree.

The schema includes:

- `kb_docs`: document metadata, content hash, title, type, tags,
  update timestamp, and embedding status.
- `kb_chunks`: chunk text, heading breadcrumb, byte offsets, and
  document path.
- `kb_fts`: FTS5 virtual table over chunks and title-oriented text.
- `kb_vec`: sqlite-vec `vec0` virtual table for chunk embeddings when
  vector search is configured and available.
- `kb_links`: body links and typed `ref` links.
- `kb_fields`: flattened frontmatter key/value rows for list/filter
  queries.
- `kb_meta`: schema version, embedder model, dimensions, and index
  provenance.

Every write synchronously updates docs, chunks, FTS, links, and fields
so keyword search is current immediately. Embeddings run asynchronously
with retries. Pending or failed embeddings produce degraded search:
FTS still works, vector contribution is omitted, and health/summary
expose the backlog.

Search executes FTS and vector retrieval against chunks, aggregates
chunk hits to documents using best matching chunks, fuses ranks with
weighted RRF, and optionally reranks top candidates through an
OpenAI-compatible LLM. Rerank failures or timeouts return the fused
order unchanged.

Full index rebuild is triggered by schema or embedder metadata
mismatch. Rebuild writes a temp database and swaps it atomically so
search remains available during rebuild whenever an old index exists.

## REST Surface

REST exposes the operations in `VISION.md`:

- `GET /health`
- `GET /summary`
- `POST /search`
- `POST /grep`
- `GET /concepts`
- `GET /concepts/{path...}`
- `GET /history/{path...}`
- `GET /log`
- `POST /concepts`
- `PATCH /concepts/{path...}`
- `DELETE /concepts/{path...}`
- `GET /types`
- `POST /validate`
- `POST /push`

Greedy path routes preserve bundle-relative slash-separated paths.
Optional bearer auth is enabled only when `auth.api_keys` is non-empty.

## MCP Surface

The MCP server exposes the same domain operations with fixed `kvt_`
tool names:

- `kvt_summary`
- `kvt_howto`
- `kvt_search`
- `kvt_grep`
- `kvt_list`
- `kvt_read`
- `kvt_types`
- `kvt_log`
- `kvt_history`
- `kvt_write`
- `kvt_edit`
- `kvt_delete`
- `kvt_validate`

MCP instructions promote search-first and write-back workflows.
Descriptions and errors are part of the agent contract and must be
tested. `kvt_howto` is also exposed as an MCP resource and prompt, but
the tool form is primary.

`kvt_write` and `kvt_edit` return the resulting content hash because
the service mutates content by injecting `timestamp`. `kvt_edit` uses
exact string replacement with uniqueness checks unless `replace_all`
is requested; failed matches return a closest candidate.

## CLI Surface

The CLI commands are:

- `kvt init --vault <path>`
- `kvt serve --vault <path>`
- `kvt reindex --vault <path>`
- `kvt validate --vault <path>`
- `kvt push --vault <path>`

All commands also accept `KVT_VAULT`. `init --defaults` skips the
interactive questionnaire and writes deterministic default config.

## Configuration

Config lives in `.kvt/config.yaml` by default. An explicit `--config`
path may override it. The vault path itself is never stored in config;
it comes from the CLI flag or `KVT_VAULT`.

Config sections match the spec: `embedder`, `llm`, `search`, `git`,
`server`, `auth`, and `limits`. Defaults should make local single-user
operation straightforward: no auth, HTTP on port 8200, no forced
remote push, and FTS-only behavior acceptable when no embedder is
reachable.

## Error Handling and Response Budgets

Errors should be actionable for agents and operators:

- Invalid path errors include a slugified suggestion.
- Validation errors list missing or invalid fields and the expected
  constraints.
- Conflict errors include current content and hash for rebase/retry.
- Edit-match failures explain whether the string was absent or
  ambiguous and include the closest candidate when available.
- Wrong branch, detached HEAD, lock contention, push failures, embed
  backlog, and index freshness surface through health and summary.

Unbounded outputs share `limits.max_response_chars`. Truncated
responses state that they were truncated and include a cursor or
continuation token. Truncation is never silent.

## Docker

The repository should ship a Dockerfile and compose example. The image
contains the `kvt` binary and the real `git` binary. A typical compose
setup bind-mounts a host vault to `/workspace`, exposes port 8200, and
can pass through embedder/LLM environment variables and SSH agent
configuration for git push.

## Testing Strategy

The test suite is the executable form of `VISION.md`.

Unit tests cover:

- Path normalization and slug suggestions.
- Frontmatter parsing/rendering/hash behavior.
- Timestamp overwrite.
- Ontology validation, path rules, enum/pattern/ref constraints, and
  unknown-type policy.
- Markdown link extraction.
- Chunking, breadcrumbs, heading splits, size bounds, code blocks, and
  tables.
- RRF fusion and response truncation.

Integration tests cover:

- `kvt init` bootstrap and adoption.
- Real git commits: one write equals one commit.
- `base_hash` optimistic concurrency conflicts.
- Service-owned `index.md` regeneration.
- `timestamp` authority.
- Real SQLite indexing and FTS search.
- Reconciliation of out-of-band edits.
- Full rebuild on schema or embedder metadata change.
- Vault lock exclusion.
- Local bare-repo push modes and divergence handling.

API tests cover:

- REST route behavior and auth on/off.
- MCP tool request/response behavior.
- Pagination/cursors/truncation.
- Golden-ish wording for validation, conflict, and edit-match errors.

Intelligence tests use deterministic local HTTP stubs at the network
seam for embedders and LLM rerankers. They must cover healthy hybrid
search, embedder-down FTS fallback, and reranker-down fused-order
fallback.

## Implementation Order

Implementation should land in vertical slices that preserve full-scope
interfaces:

1. Foundation: config, path/frontmatter, vault traversal, ontology,
   service models, and tests.
2. Git-backed vault lifecycle: init, locking, branch checks, read,
   write, edit, delete, index regeneration, validation, commits, log,
   history, and tests.
3. SQLite indexing: schema, sync doc/chunk/FTS/fields/links indexing,
   list/grep/summary/read backlinks, reconciliation, rebuild, and
   tests.
4. Search intelligence: chunker hardening, embed interfaces, OpenAI
   and Ollama adapters, async embedding queue, vector search, RRF,
   rerank, degraded modes, and deterministic tests.
5. REST server: all routes, auth, health, response budgets, and tests.
6. MCP server: all tools, resources/prompts, instructions,
   descriptions, errors, and tests.
7. Push and operations: async push modes, `POST /push`, `kvt push`,
   Dockerfile, compose example, and operational tests.
8. Completion audit: requirement-by-requirement pass against
   `VISION.md`, with gaps closed before claiming full completion.

This ordering is incremental, but the accepted target remains the full
project scope.
