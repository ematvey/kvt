# KVT — Knowledge Vault

**A knowledge base as a service.**

KVT is a Go service that manages OKF-conformant knowledge bundles,
exposing them via a REST API and an MCP server. It combines the
simplicity of git-backed markdown files with state-of-the-art hybrid
search and a configurable ontology layer.

---

## Design Tenets

1. **Format-first.** Knowledge is stored as [OKF v0.1](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md) bundles — plain
   markdown with YAML frontmatter. The format is the contract.
2. **Hybrid search from day one.** Every query combines FTS5 keyword
   search with vector similarity (via sqlite-vec). Optionally reranked
   by an external LLM.
3. **Pluggable intelligence.** Embedders sit behind a small interface
   with two built-in implementations: OpenAI-compatible
   (`/v1/embeddings`) and Ollama native (`/api/embed`). New embedder
   APIs are one adapter away. LLM via any OpenAI-compatible endpoint.
   No baked-in model.
4. **Concurrent agents.** Multiple AI agents and humans can read/write
   simultaneously. Writes are serialized and committed directly to
   the vault branch; optimistic concurrency (content-hash
   preconditions) rejects stale writes with an error the agent can
   react to.
5. **Ontology-aware.** A configurable ontology defines which types
   exist and which frontmatter fields they require. Validation runs
   in the write path — strict (reject) or advisory (warn).
6. **Portable.** The vault directory path is supplied at startup
   (`kvt serve --vault <path>` / `KVT_VAULT`): any host path locally,
   `/workspace` in a container. Everything — markdown, git history,
   SQLite index — lives in that directory.

---

## Architecture

```
┌──────────────┐     ┌─────────────────────────────────────┐
│  Agent/AI    │────▶│          KVT Service (Go)            │
│  (MCP cli)   │     │                                     │
└──────────────┘     │  ┌───────────┐  ┌─────────────────┐  │
                     │  │ REST API  │  │   MCP Server    │  │
┌──────────────┐     │  │ (net/http)│  │ (mcp.go SDK)    │  │
│  Human/CLI   │────▶│  └─────┬─────┘  └────────┬────────┘  │
└──────────────┘     │        │                  │           │
                     │  ┌─────┴──────────────────┴────────┐  │
                     │  │         Core Engine             │  │
                     │  │  ┌────────┐ ┌────────┐ ┌─────┐  │  │
                     │  │  │ Search │ │ Onto-  │ │ Git │  │  │
                     │  │  │ Index  │ │ logy   │ │ Ops │  │  │
                     │  │  │(SQLite)│ │(YAML)  │ │     │  │  │
                     │  │  └───┬────┘ └───┬────┘ └──┬──┘  │  │
                     │  └──────┼──────────┼──────────┼─────┘  │
                     └─────────┼──────────┼──────────┼────────┘
                               │          │          │
                         ┌─────▼──────────▼──────────▼─────┐
                         │        Vault Directory           │
                         │  --vault <path> (host dir, or    │
                         │   /workspace in a container)     │
                         │                                  │
                         │  ├── index.md     (OKF bundle)   │
                         │  ├── _ontology.yaml              │
                         │  ├── people/alice.md             │
                         │  ├── systems/db.md               │
                         │  ├── .git/        (versioning)   │
                         │  └── .kvt/index.db  (SQLite)     │
                         └──────────────────────────────────┘
                                  │
                        ┌─────────▼─────────┐
                        │  External Embedder │
                        │ (OpenAI-compat or  │
                        │   Ollama native)   │
                        └───────────────────┘
```

---

## Components

### 1. OKF Bundle (the vault)

The primary store is a directory of markdown files conforming to OKF
v0.1. Every concept is one `.md` file with YAML frontmatter including
at minimum a `type` field.

Reserved file: `index.md` (per-directory listing) is maintained by
the service, not by agents.

`index.md` is a pure function of the directory it lists. On every
write the affected directory's index is regenerated from frontmatter
metadata — one line per child (link, title, one-line description),
subdirectories first, then concepts, alphabetical. Deterministic:
the same vault state always produces byte-identical indexes. A write
that creates or empties a directory also regenerates parent indexes
up the chain to the root. The bundle-root `index.md` additionally
declares `okf_version` in its frontmatter, per the spec. Indexes are
length-controlled like `kvt_summary`: at most
`limits.index_max_entries` per section (default 50), overflow
collapsing to "… and N more — see `kvt_list`".

The reserved file is excluded from the search index — navigation
listings would pollute results and waste embeddings. It remains
readable via `kvt_read` like any file.

Paths are strictly normalized: bundle-relative, forward-slash
separated, lowercase, each segment matching `[a-z0-9_][a-z0-9._-]*`
— no spaces, no unicode, no `..`, nothing that needs URL encoding or
breaks on any filesystem. A write with a non-conforming path is
rejected, and the error carries the slugified suggestion
(`people/John Smith.md` → "did you mean `people/john-smith.md`?").
Out-of-band files that violate the rule surface as validation
warnings, not crashes.

### 2. Ontology (`_ontology.yaml`)

An optional YAML file at the bundle root defining acceptable types and
their expected frontmatter fields:

```yaml
types:
  Person:
    required: [title, description, email]
    optional: [github, slack]
  System:
    required: [title, description]
    optional: [repo, docs_url]
  Incident:
    required: [title, severity, status]
    optional: [affects]
    fields:
      severity: {enum: [low, medium, high, critical]}
      status: {enum: [open, investigating, resolved]}
      affects: {ref: System}  # bundle path to a System concept

unknown_types: warn  # allow | warn | reject
```

The ontology maps to files via the `type` field in frontmatter.
For stronger guarantees, path-based mapping rules can be added:

```yaml
rules:
  - path: people/**   # glob pattern
    type: Person
  - path: systems/**
    type: System
```

Validation runs in the service's write path. In strict mode a
violating write is rejected; in advisory mode it is accepted and
warnings are returned in the API response.

Field constraints are optional and deliberately shallow: `enum`,
`pattern` (regex), and `ref`. A `ref` value is a bundle-relative
path; validation checks that the target exists and, when a target
type is declared, that it matches. `ref` fields also feed `kb_links`,
so typed relations appear among the backlinks `kvt_read` returns,
alongside body links.

Documents whose `type` is absent from the ontology are governed by
`unknown_types` (`allow` / `warn` / `reject`, default `warn`) — OKF
requires consumers to tolerate unknown types, so `reject` is opt-in.

Changing `_ontology.yaml` never retroactively invalidates existing
documents: writes are validated at write time, and `kvt_validate`
re-checks the whole vault on demand to report drift.

Agents discover the ontology through `kvt_types`, which returns the
full schema per type — required/optional fields and their
constraints — so a valid concept can be constructed without
guessing.

### 3. SQLite Search Index

A local SQLite database (`.kvt/index.db`). It is a derived artifact —
always rebuildable from the vault, never the source of truth.

- **`kb_docs`** — Document metadata (path, content hash, title, type,
  tags, updated_at, embed status).
- **`kb_chunks`** — Chunked document content (doc path, heading
  breadcrumb, byte offsets, text). The unit of search.
- **`kb_fts`** — FTS5 virtual table over chunks (porter tokenizer,
  unicode61).
- **`kb_vec`** — `vec0` virtual table (sqlite-vec) over chunk
  embeddings (cosine distance).
- **`kb_links`** — Markdown links extracted at index time (from_path,
  to_path); backs the backlinks returned by `kvt_read`.
- **`kb_fields`** — Frontmatter field key/value pairs for structured
  queries.
- **`kb_meta`** — Index provenance: schema version, embedder
  model/dimensions it was built with.

#### Chunking

Chunks — not whole documents — are embedded and searched. Whole-doc
embeddings degrade on anything longer than a paragraph and hit
embedder context limits. Strategy: structure-aware splitting with
size bounds.

1. Split on ATX headings (H1–H3 are split points; H4+ stays inside
   its parent section).
2. Merge adjacent sections under ~100 tokens; split sections over
   ~800 tokens on paragraph boundaries. Target 200–400 tokens per
   chunk. Code blocks and tables are atomic — never split internally.
3. Before embedding, prepend a contextual breadcrumb to each chunk:
   document title + heading path + `type`. This cheap form of
   contextual retrieval disambiguates section text that doesn't stand
   alone. The breadcrumb is embedded, not stored as content.
4. The frontmatter is its own chunk (chunk 0): all fields serialized
   as readable key/value text. It doubles as a doc-level summary for
   retrieval and makes every frontmatter field findable in both FTS
   and vector space.
5. Both FTS and vector search operate on chunks; results aggregate to
   documents (best chunk wins), so fusion compares like with like and
   hits can quote the matching section via stored offsets.

#### Indexing lifecycle

- **Synchronous:** every write updates `kb_docs`, `kb_chunks`,
  `kb_fts`, `kb_links`, `kb_fields` in the same operation. Keyword
  search is never stale.
- **Asynchronous:** embeddings go through a work queue with retries.
  Docs pending embedding are flagged and still served by FTS; if the
  embedder is down, search degrades to FTS-only and `/health` exposes
  the backlog.
- **Reconciliation:** a background scan compares content hashes in
  `kb_docs` against the working tree to pick up out-of-band changes
  (direct file edits, `git pull`). The scan never descends into
  `.git/` or `.kvt/`.
- **Full rebuild:** triggered when `kb_meta` disagrees with the
  configured embedder model/dimensions or schema version — vec0
  tables have fixed dimensions, so changing the embedding model means
  drop and re-embed. Rebuilds write to a temp database and swap it in
  atomically, so search stays available throughout.

### 4. Hybrid Search Pipeline

1. **FTS5 keyword search** — Token-match across chunk text and titles.
2. **Vector search** — Embed query via external embedder, cosine
   distance against `kb_vec`.
3. **Fusion** — Reciprocal rank fusion (RRF). Rank-based, so BM25 and
   cosine scores never need normalizing against each other;
   per-source weights remain configurable.
4. **Optional LLM rerank** — Top N candidates sent to external LLM for
   relevance scoring (0–10). Best-effort: on LLM failure or timeout,
   the fused order is returned unchanged.

### 5. Git Ops

All writes commit directly to one vault branch:

1. Writes are serialized by an in-process lock (single writer; reads
   are never blocked).
2. Each write operation validates against the ontology, applies the
   change, and commits with a structured message (agent provenance in
   the body). One write = one commit.
3. Optimistic concurrency: a write may carry the content hash the
   client last read (`base_hash`). If the file changed since, the
   write is rejected with a conflict error carrying the current
   content — the agent re-reads, re-applies, and retries.
4. The service maintains the OKF reserved file in the same commit:
   the affected directory's `index.md` is regenerated so navigation
   never drifts. It also sets the OKF-recommended `timestamp`
   frontmatter field on every write and edit — the service's clock is
   authoritative, and a client-supplied `timestamp` is always
   overwritten.

Fresh vaults use `main`: `kvt init` creates new repositories with
`git init -b main`. During guided setup KVT asks which branch is the
vault branch; the default is `main` for fresh vaults and the current
checked-out branch (`git symbolic-ref --short HEAD`) for adopted
repositories. KVT records that choice in `.kvt/config.yaml` instead
of inferring it from `.git/config` or renaming the repo's branch.
`kvt serve` only writes while the working tree is checked out on that
branch; a detached HEAD or different branch is an operator error
surfaced in `/health`.

**Remote push.** Remotes belong to the repo, not to KVT: the vault's
own git config supplies the remote and the credentials (SSH agent,
credential helper — a repo mounted from the host brings all of this
with it). KVT only decides *when* to push, to `remote_name`
(default `origin`). Push modes: `on_change` (push after every
commit), `debounced` (push at most once per interval — default 5m —
whenever new commits exist), or `off`. Pushes run asynchronously
with exponential backoff on failure and never block or fail a
write; push status (last push, commits ahead, last error) surfaces
in `/health` and `kvt_summary`. Pushes are fast-forward only — if
the remote has diverged, KVT reports the error instead of
force-pushing, because the remote is a mirror, not a second writer.
A push can also be triggered manually via `POST /push`. This is
deliberately not an MCP tool: replication is an operator concern,
not an agent concern.

Git operations shell out to the `git` binary rather than going
through a Go git library: the binary inherits the host's credential
machinery and exact git semantics for free.

Git history is exposed strictly read-only, through `kvt_log`
(vault-wide) and `kvt_history` (per file). Reverts are not allowed:
KVT never resets, reverts, or rewrites history. Restoring earlier
content is an ordinary forward write that produces a new commit —
history only ever grows. Branch-based workflows can be layered on
later without changing the on-disk format.

### 6. MCP Server

Exposes read/write tools via the Model Context Protocol:

All tools carry a fixed `kvt_` prefix, so names stay unambiguous in
multi-server agents even when the client doesn't namespace tools
itself.

**Read tools:**
- `kvt_summary` — The one-call orientation: a pretty-printed tree of
  the vault's structure, plus doc and type counts, tag vocabulary,
  validation status, and index freshness. The tree is bounded, never
  infinite: at most 5 entries per level (overflow collapses to
  "… and N more") and at most 3 levels deep, both defaults
  overridable per call. Full listings are `kvt_list`'s job.
- `kvt_howto` — The usage playbook for agents (see below)
- `kvt_search` — Hybrid search across the vault
- `kvt_grep` — Literal or regex match over raw file content, like a
  coding agent's grep: exact identifiers, TODO sweeps, link-target
  audits — lookups where ranked search is the wrong shape.
- `kvt_list` — The vault as a table. Configurable columns drawn from
  frontmatter fields (by design: anything tabular belongs in
  frontmatter), filters (type, tag, path prefix), `order_by` any
  field, paginated via limit/offset. Backed by `kb_docs` +
  `kb_fields`; "all incidents by severity, newest first" is one
  call. Sorting is type-aware: a field with an `enum` constraint
  sorts in the ontology's declared order (low < medium < high <
  critical), ISO timestamps sort chronologically, everything else
  lexically.
- `kvt_read` — Read one concept, whole or by line range. Returns the
  body plus metadata: content hash (feeds `base_hash`), backlinks
  (who links *here* — the one thing the body itself can't tell you),
  and validation warnings.
- `kvt_types` — Ontology type schemas and per-type counts
- `kvt_log` — Paginated commit history for the whole vault. Each
  entry is deliberately terse for LLM consumption: short hash,
  timestamp, author/agent, message subject, and a capped summary of
  affected files ("3 files: people/alice.md, … and 2 more").
  Cursor-based pagination reaches arbitrarily far back.
- `kvt_history` — Changes to one concept: the last N commits that
  touched the path (configurable depth), each with its diff.
  Paginated like `kvt_log`.

Every read tool that can return unbounded output (`kvt_log`,
`kvt_history`, `kvt_grep`, `kvt_list`) shares a server-configured
soft response budget (`limits.max_response_chars` — characters, not
tokens, since the server can't know the client model's tokenizer). A
truncated response says so explicitly and carries the cursor to
continue — never a silent cut.

**Write tools:**
- `kvt_write` — Create a concept or replace its full content
  (commits to the vault branch; optional `base_hash` for conflict
  detection)
- `kvt_edit` — Surgical edit via exact string replacement:
  `old_string` must match the current content exactly and uniquely
  (or pass `replace_all`); on a failed match the error shows the
  closest candidate. Same commit and `base_hash` semantics as
  `kvt_write`.

Both write tools return the resulting content hash. This matters
because the service mutates content on write (`timestamp`
injection), so the hash of what the agent sent is *not* the hash of
what landed — returning it lets an agent chain edits without a
re-read between each one.
- `kvt_delete` — Remove a concept file (commits to the vault branch)
- `kvt_validate` — Run ontology validation across the vault. Also
  reports broken links: body links and `ref` fields whose target
  doesn't exist (backed by `kb_links`)

Read and edit follow the editor-tool pattern coding agents converged
on (SWE-agent's `str_replace_editor`, Claude Code's Read/Edit):
exact string replacement is more reliable than unified diffs (models
generate malformed hunks) and than line-addressed edits (models
miscount lines). Line numbers appear in `kvt_read` output for
orientation, never as edit addresses.

**Agent-facing guidance is a first-class artifact.** The point of the
MCP server is to make agents actually use the vault, so the prompt
surface is designed, not incidental:

- The MCP `instructions` field (delivered to the agent at connect
  time) explains what the vault is and promotes a
  search-first / write-back workflow: search before answering
  anything the vault might cover; write durable learnings back after
  completing work; link new concepts to existing ones.
- Tool descriptions are written for the consuming model, not for
  humans: what the tool returns, when to prefer it over sibling
  tools, and a concrete example call each.
- Error messages are prompts too: a rejected write explains *why* and
  what a valid retry looks like (e.g. validation failure lists the
  missing required fields for that type; a conflict returns the
  current content to rebase onto).
- `kvt_howto` ships the deep playbook as a callable skill: the OKF
  authoring subset (frontmatter with a required `type`,
  bundle-relative link syntax, the `# Citations` convention — and
  what *not* to write: `index.md` and `timestamp` are
  service-owned), how to formulate hybrid-search queries, how to
  structure a valid concept for each ontology type, linking
  conventions, and conflict-retry etiquette. Consumer-side OKF spec
  detail is deliberately excluded — anything KVT enforces or
  auto-maintains is taught by its error messages, not by prompt
  tokens. Defaults are baked into the binary; a vault can override
  or extend them via `_howto.md` at the bundle root, so house rules
  are versioned with the knowledge they govern. The same content is
  also exposed as an MCP resource and prompt, but the tool form is
  primary — tool calls are the primitive agents use most reliably.
  The connect-time `instructions` stay short and point to `kvt_howto`
  for depth: progressive disclosure, the same idea as OKF's
  `index.md`.
- The KVT repository ships an installable coding-agent skill
  (`SKILL.md`) for agents that operate on vault files as a plain
  checkout — no MCP connection required. It is part of the KVT
  distribution, not a file created inside each vault. It covers the
  direct-file workflow: OKF authoring rules, path normalization,
  which files are service-owned, and `kvt validate` before committing.
  Vault-specific house rules stay in `_howto.md` and surface through
  `kvt_howto`, the MCP resource, and the MCP prompt.

### 7. REST API

HTTP API covering the same operations as MCP, intended for browser
tooling, CI/CD, and non-MCP clients. Authorization is optional: when
`auth.api_keys` is configured, every request must carry
`Authorization: Bearer <key>`; when unset, the API is open (local
single-user mode).

Routes containing `{path...}` use a greedy wildcard for the full
bundle-relative path, so `people/alice.md` is addressed as
`/concepts/people/alice.md`. KVT's path rules forbid characters that
would need special URL encoding; slashes remain path separators.

| Method | Path | Purpose |
|--------|------|---------|
| GET | /health | Liveness |
| GET | /summary | Bundle summary |
| POST | /search | Hybrid search |
| POST | /grep | Literal/regex content match |
| GET | /concepts | List as table (columns/filter/sort/paginate) |
| GET | /concepts/{path...} | Read concept |
| GET | /history/{path...} | File history with diffs (paginated) |
| GET | /log | Commit history (paginated) |
| POST | /concepts | Write concept (full content) |
| PATCH | /concepts/{path...} | String-replacement edit |
| DELETE | /concepts/{path...} | Delete concept |
| GET | /types | List ontology types |
| POST | /validate | Run validation |
| POST | /push | Push to configured remote |

---

## CLI

- `kvt init --vault <path>` — Bootstrap. On an empty directory:
  `git init -b main`, then write `.gitignore` (ignoring `.kvt/`), a
  root `index.md` and a commented `_ontology.yaml` starter — all in a
  single initial commit. The `.kvt/` directory is created on disk but
  never committed: it is ignored derived state. On an existing git
  repo of markdown, `init` adopts it: adds only what's missing (e.g.
  appends `.kvt/` to `.gitignore`), defaults the guided setup to the
  current branch as the vault branch, and touches nothing else.
  Idempotent — re-running on a vault is a no-op.
  `init` then runs an interactive questionnaire (embedder endpoint,
  model and dimensions, LLM endpoint, rerank on/off, vault branch,
  push mode) and writes the answers to `.kvt/config.yaml` —
  vault-specific, git-ignored, environment-bound configuration lives
  with the vault but outside its history. `--defaults` skips the
  questionnaire for scripted setups.
- `kvt serve --vault <path>` — Run the service (REST + MCP).
  Refuses to serve a directory that isn't an initialized vault and
  points at `kvt init` — never silently adopts an arbitrary
  directory.
- `kvt reindex` — Force a full index rebuild.
- `kvt validate` — Ontology + link validation; non-zero exit on
  violations (CI-friendly).
- `kvt push` — One-shot push to the configured remote.

All commands take `--vault` / `KVT_VAULT`.

---

## Storage Layout

The server is started with a path to the knowledge base:
`kvt serve --vault <path>` (env: `KVT_VAULT`). On a host this is any
directory; in a container it is `/workspace`. Derived state lives in
a git-ignored `.kvt/` subdirectory, so the whole vault remains one
portable directory. A vault is served by exactly one KVT instance at
a time, enforced by a lockfile in `.kvt/` — a second `kvt serve` on
the same vault refuses to start.

```
<vault-path>/                    # kvt serve --vault <path>
├── .git/                        # Versioning
├── .gitignore                   # Ignores .kvt/
├── _ontology.yaml               # Optional ontology definition
├── index.md                     # Bundle root index (service-maintained)
├── people/
│   ├── index.md                 # Directory index (service-maintained)
│   └── alice.md
├── systems/
│   ├── index.md
│   └── db.md
└── .kvt/                        # Derived state, git-ignored
    ├── index.db                 # SQLite search index (FTS5 + vec0)
    └── config.yaml              # Runtime configuration (optional)
```

---

## Configuration

```yaml
# config.yaml — <vault>/.kvt/config.yaml, written by `kvt init`'s
# questionnaire (or by hand); an explicit --config <path> overrides.
# The vault path itself comes from the CLI flag / env, not this file.

embedder:
  type: openai-compatible  # or: ollama (native /api/embed)
  base_url: http://localhost:11434/v1
  model: nomic-embed-text
  dimensions: 768
  api_key_env: EMBEDDER_API_KEY  # optional; unset for local Ollama

llm:
  base_url: https://llm.example.com/v1
  model: deepseek-v4
  api_key_env: LLM_API_KEY

search:
  fusion: rrf
  fts_weight: 0.5
  vec_weight: 0.5
  rerank: true
  rerank_top_k: 10

git:
  branch: main        # branch KVT is allowed to write to; guided
                      # setup defaults to current branch for adopted repos
  # Remotes and credentials come from the repo's own git config —
  # a mounted host repo brings them with it. KVT only decides when
  # to push.
  remote_name: origin  # which remote to push to
  push: debounced      # off | on_change | debounced
  debounce_interval: 5m
  author_name: kvt     # commit identity fallback if the repo's
  author_email: kvt@local  # git config sets no user

server:
  http_port: 8200
  mcp_transport: stdio  # or streamable-http (served alongside REST)

auth:
  api_keys: []  # empty = no auth (local single-user mode)

limits:
  max_response_chars: 16000  # soft cap on unbounded tool responses
                             # (log, history, grep, list). Characters,
                             # not tokens — the server can't know the
                             # client model's tokenizer; ~4 chars per
                             # token is the sizing rule of thumb.
                             # Truncated responses say so and carry a
                             # cursor.
  index_max_entries: 50      # per index.md section; overflow
                             # collapses to "… and N more"
```

---

## Docker

```yaml
services:
  kvt:
    image: kvt:latest
    command: ["serve", "--vault", "/workspace"]
    ports:
      - "8200:8200"
    volumes:
      # Typical: bind-mount an existing host repo (brings its git
      # remotes and credentials); a named volume works for
      # self-contained deployments.
      - ./vault:/workspace
    environment:
      - LLM_API_KEY=${LLM_API_KEY}
      - EMBEDDER_BASE_URL=http://ollama:11434/v1
      - LLM_BASE_URL=https://llm.example.com/v1
    depends_on:
      - ollama

  ollama:
    image: ollama/ollama
    volumes:
      - ollama-data:/root/.ollama
```

The image ships with the `git` binary. For pushing over SSH, pass
the host's agent through (`${SSH_AUTH_SOCK}:/ssh-agent` volume plus
`SSH_AUTH_SOCK=/ssh-agent`); for HTTPS remotes, the repo's
credential helper configuration applies.

---

## Testing

Every behavior specified in this document is a test case — the
sections above double as the test list. Correctness is the feature.

- **Unit layer.** Pure logic gets table-driven tests: the chunker
  (heading splits, merge/split bounds, atomic code blocks and
  tables, breadcrumbs), RRF fusion, ontology validation (required
  fields, `enum`/`pattern`/`ref` constraints, unknown-type policy),
  frontmatter parsing, path normalization and slug suggestions,
  response truncation.
- **Integration layer.** Real dependencies, no mocks: tests run
  against a temp vault with a real git repo (the actual `git`
  binary) and a real SQLite index. Covered end-to-end: one write =
  one commit, optimistic-concurrency conflicts, reserved-file
  regeneration, authoritative `timestamp`, reconciliation of
  out-of-band edits, full rebuild on embedder change, lockfile
  exclusion, and push modes against a local bare repo as the remote.
- **Deterministic intelligence.** The embedder and LLM are faked at
  the network seam only: a local OpenAI-compatible stub returns
  deterministic vectors, so hybrid search is assertable — known
  corpus in, known ranking out. Degraded paths are first-class
  tests: embedder down → FTS-only; rerank LLM down → fused order
  unchanged.
- **API layer.** REST and MCP are exercised through their public
  surfaces (golden request/response tests), including auth on/off,
  pagination cursors, truncation markers, and the exact wording of
  conflict and validation errors — error messages are part of the
  agent contract, so they are tested like one.

---

## Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Knowledge format | OKF v0.1 | Vendor-neutral, human/agent-readable, git-friendly standard |
| Search index | SQLite (FTS5 + vec0) | Zero-dependency embedded search, single file |
| Embeddings | External (OpenAI-compatible or Ollama native) | User's choice of model, not locked in |
| Git workflow | Serialized direct commits to one vault branch | Simple and auditable; conflicts surface as optimistic-concurrency errors, not merges |
| Chunking | Heading-aware, size-bounded, breadcrumb context | Whole-doc embeddings degrade on long files; structure-aware chunks retrieve best |
| Score fusion | Reciprocal rank fusion | BM25 and cosine scores aren't comparable; rank-based fusion needs no normalization |
| Auth | Optional bearer API keys | Open by default locally; one config key to secure when exposed |
| Reserved files | Service-maintained | `index.md` regenerated on every write, never drifts |
| Remote sync | Async fast-forward push, per-commit or debounced | Never blocks writes; remote and credentials come from the repo's own git config |
| Ontology | YAML with path rules | Flexible mapping of types to file locations |
| Validation | Strict or advisory, in the write path | Reject the write, or accept it and return warnings |
| API | REST + MCP | Both standards: MCP for agents, REST for tooling |
| Write path | Service-owned git | No separate broker needed; service manages its own git |
| Git implementation | Shell out to the `git` binary | Inherits the host's credential machinery (SSH agent, credential helpers) and full git semantics; Go git libraries have incomplete auth support |

---

## References

- [OKF v0.1 Specification](https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md)
- [Google Cloud Blog: Introducing OKF](https://cloud.google.com/blog/products/data-analytics/how-the-open-knowledge-format-can-improve-data-sharing)
- [Karpathy's LLM Wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)
- [sqlite-vec](https://github.com/asg017/sqlite-vec)
- [Model Context Protocol](https://modelcontextprotocol.io)
