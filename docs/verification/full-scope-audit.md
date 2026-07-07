# Full-Scope Verification Audit

Date: 2026-07-07

This audit maps `VISION.md` requirements to current code, tests, and
verification commands. Rows marked `complete (scoped)` are satisfied by
the narrower implementation surface built in this pass. Rows marked
`scoped deviation` are explicit VISION details that are not implemented
literally; the evidence column states the smaller behavior that exists.

| Requirement | Evidence | Status |
|-------------|----------|--------|
| OKF markdown files are the source of truth | `internal/frontmatter/document_test.go::TestParseRenderTimestampAndHash`, `internal/service/service_test.go::TestWriteCommitsTimestampAndRejectsStaleBaseHash`, `internal/index/reconcile.go` rebuilds derived state from files | complete |
| Vault path supplied by CLI/env and works as host/container path | `cmd/kvt/main_test.go::TestRunInitUsesKVTVaultFallback`, `cmd/kvt/main.go`, `Dockerfile`, `compose.yaml` | complete |
| `.kvt/` contains runtime/derived state and is not committed | `internal/service/init_test.go::TestInitEmptyVaultLeavesRuntimeConfigUntracked`, gitops runtime-path tests, `internal/service/init.go` | complete |
| Service-owned `index.md` regenerated deterministically | `internal/vault/vault_test.go::TestRegenerateIndexesDeterministic`, service write/delete tests, `internal/vault/index.go` | complete |
| Root `index.md` carries `okf_version`; nested indexes are service-owned | `internal/service/init_test.go::TestInitEmptyVaultCreatesMainCommitAndConfig`, `TestInitAdoptionCreatesNestedIndexesEvenWhenRootIndexExists` | complete |
| Indexes are length-controlled with overflow marker | `internal/vault/index.go::writeEntries`, `internal/vault/vault_test.go::TestRegenerateIndexesDeterministic` | complete (default limit 50) |
| Reserved `index.md` is excluded from search indexing but readable | `internal/index/reconcile.go` skips `index.md`; `internal/ontology/ontology_test.go::TestValidateVaultAllowsLinksToReadableIndexFiles` | complete |
| Paths are normalized, lowercase, bundle-relative, safe, and suggest slugs | `internal/pathutil/path_test.go`, service init invalid-path tests, HTTP error mapping tests | complete |
| Ontology loads optional `_ontology.yaml` and defaults unknown types to warn | `internal/ontology/ontology_test.go::TestLoadParsesSchema`, `TestValidateDocumentAdvisoryUsesWarningForUnknownType` | complete |
| Required fields, enum, pattern, refs, and path rules validate | `internal/ontology/ontology_test.go::TestValidateRequiredEnumPatternAndRef`, `TestValidatePathRulesAndAdvisoryMode`, service validation tests | complete |
| Strict writes reject invalid content; advisory writes commit with warnings | `internal/service/service_test.go::TestWriteRejectsMissingAndWrongTypeRefs`, `TestWriteAdvisoryValidationReturnsWarningsAndCommits` | complete |
| Whole-vault validation reports drift, malformed links, body links, and ref links | `internal/ontology/ontology_test.go::TestValidateVaultReportsMalformedBodyLinks`, `TestValidateVaultReportsBrokenBodyLinksAndRefTargets`, `cmd/kvt/main_test.go::TestRunValidateReturnsNonZeroWhenValidationFails` | complete |
| `kvt_types` exposes ontology schema and per-type counts | `internal/service/ops.go::Types`, `internal/service/service_test.go::TestTypesIncludeDocumentCounts`, `internal/httpapi/server_test.go::TestQueryAndMetadataRoutesOverHTTP`, MCP registration tests | complete |
| SQLite index includes docs, chunks, FTS, links, fields, meta, and embedding state | `internal/index/schema.go`, `internal/index/index_test.go::TestSchemaUsesFTS5`, `TestApplyDocumentIndexesFTSFieldsAndLinks`, embedding-state summary tests | complete (scoped) |
| VISION storage columns include `kb_docs.tags`, `kb_docs.updated_at`, chunk byte offsets, and a stored heading breadcrumb | Current schema stores frontmatter values in `kb_fields`, document freshness as `kb_docs.timestamp`, embedding state in `kb_doc_embeddings`, and contextual chunk text in `kb_chunks.embed_text`; it does not add literal tag/updated_at/offset/breadcrumb columns | scoped deviation |
| Writes synchronously update docs/chunks/FTS/links/fields | `internal/service/write.go`, `internal/index/index_test.go::TestApplyDocumentIndexesFTSFieldsAndLinks`, service search tests | complete |
| Reconciliation detects out-of-band edits and skips `.git/`/`.kvt/` | `internal/index/index_test.go::TestReconcileIndexesVaultAndSkipsServiceOwnedPaths`, `internal/index/reconcile.go` | complete |
| Full rebuild repairs derived index rows and handles provenance changes | `cmd/kvt/main_test.go::TestRunReindexRebuildsDerivedIndex`, vector provenance tests in `internal/index/index_test.go` | complete (scoped) |
| VISION full rebuild swaps a temp DB atomically | `internal/index/reconcile.go::Rebuild` performs an in-place forced reconcile against the open database rather than a temp database swap | scoped deviation |
| sqlite-vec support is optional and degrades cleanly when unavailable | `internal/index/schema.go::initVectorSupport`, `internal/search/search_test.go::TestSearchSkipsEmbedderWhenVectorUnavailable`, vector tests skip when unavailable | complete |
| Chunking is heading-aware, breadcrumbed, and keeps code/table blocks atomic | `internal/chunk/chunk_test.go::TestSplitKeepsCodeBlocksAtomicAndAddsBreadcrumb`, `TestSplitIncludesHeadingsInSearchText` | complete |
| Embedders are pluggable OpenAI-compatible and Ollama-native adapters | `internal/embed/embed.go`, `internal/embed/embed_test.go::TestOpenAICompatibleEmbedsTexts`, `TestOllamaEmbedsTexts` | complete |
| Hybrid search combines FTS, vector, weighted RRF, and optional rerank | `internal/search/search.go`, `internal/search/rrf.go`, `internal/search/search_test.go`, `internal/rerank/rerank_test.go` | complete |
| Search degrades to FTS-only when vector/embedder is unavailable and reports degradation | `internal/search/search_test.go::TestSearchFallsBackToFTSWhenVectorAndRerankDegrade`, `TestSearchReportsDegradedWhenVectorReturnsNoHits` | complete |
| Async embeddings queue, retry, and backlog status | `internal/service/service_test.go::TestReconcileQueuesAppliedDocumentsForEmbedding`, `TestEmbedWithRetriesRetriesTransientFailures`, index summary embedding-count tests | complete |
| Git writes are serialized and reads are not coupled to write locks | `internal/service/service.go` writer lock, `internal/service/lock.go`, lock tests | complete |
| Every write/edit/delete creates a forward git commit with service timestamp | `internal/service/service_test.go::TestWriteCommitsTimestampAndRejectsStaleBaseHash`, `TestEditRequiresUniqueOldString`, `TestDeleteRemovesConceptRegeneratesIndexesAndCommits` | complete |
| Optimistic concurrency rejects stale `base_hash` with current content/hash | `internal/service/service_test.go::TestWriteCommitsTimestampAndRejectsStaleBaseHash`, HTTP/MCP conflict tests | complete |
| KVT never resets/rebases/reverts/force-pushes history | `internal/gitops/git.go` only uses commit/log/show/push; restore is ordinary write; push uses non-forcing `git push HEAD:refs/heads/<branch>` | complete |
| Fresh init creates/adopts git repo safely and keeps runtime config untracked | `internal/service/init_test.go` init bootstrap/adoption suite, `cmd/kvt/main_test.go::TestRunInitWithDefaults` | complete |
| `serve` refuses uninitialized vaults and protects streamable MCP with auth | `cmd/kvt/main_test.go::TestRunServeRejectsUninitializedVault`, `TestServeHandlerProtectsStreamableMCPWithBearerAuth` | complete |
| Branch/detached/dirty git status surfaces in health | `internal/gitops/git_test.go::TestStatusReportsBranchDetachedAndDirtyState`, `internal/service/ops.go::Health` | complete |
| Manual and async push modes work and do not expose an MCP push tool | `internal/service/push_test.go`, `internal/httpapi/server_test.go::TestPushRouteOverHTTP`, `cmd/kvt/main_test.go::TestRunPushPushesToConfiguredRemote`, `internal/mcp/server_test.go::TestServerRegistersKVTTools` | complete |
| Push status surfaces without blocking health on remote I/O | `internal/service/push_test.go::TestHealthUsesCachedPushStatusDuringSlowPush`, `internal/httpapi/server_test.go::TestHealthAndSummaryDoNotProbeMissingPushRemote` | complete |
| Git history and file history are read-only and paginated | `internal/gitops/git_test.go::TestLogReturnsTerseEntries`, `TestHistoryReturnsDiffForPath`, REST/MCP pagination tests | complete |
| REST routes cover health, summary, search, grep, concepts, history, log, types, validate, push | `internal/httpapi/server.go`, `internal/httpapi/server_test.go::TestQueryAndMetadataRoutesOverHTTP`, lifecycle and push tests | complete |
| REST optional bearer auth | `internal/httpapi/server_test.go::TestBearerAuthWhenConfigured` | complete |
| MCP exposes fixed `kvt_` tool names, no `kvt_push` | `internal/mcp/server_test.go::TestServerRegistersKVTTools` | complete |
| MCP instructions, tool descriptions, howto tool/resource/prompt | `internal/mcp/howto.go`, `internal/mcp/server_test.go::TestServerInstructionsOverMCP`, `TestToolDescriptionsAreRegistered`, `TestHowtoResourceAndPromptOverMCP` | complete |
| Vault-specific `_howto.md` surfaces through howto tool/resource/prompt | `internal/service/howto.go`, `internal/mcp/server.go::howtoText`, `internal/mcp/server_test.go::TestHowtoIncludesVaultHouseRules` | complete |
| Vault-specific `_howto.md` is not treated as a concept file | `internal/pathutil/path.go::IsConceptMarkdownPath`, `internal/pathutil/path_test.go::TestIsConceptMarkdownPath`, `internal/service/service_test.go::TestHouseHowtoIsNotTreatedAsConcept` | complete |
| MCP write/edit/delete/read/validate return stable snake_case JSON shapes | `internal/mcp/server_test.go::TestMCPToolContractsUseStableJSONShapes` | complete |
| Direct-file agent guidance ships with repository | `SKILL.md`, `internal/mcp/server_test.go::TestHowtoMentionsServiceOwnedFilesAndConflictRetry` | complete |
| `kvt_summary` returns one-call operational orientation | `internal/mcp/tools.go::summaryOutput`, `internal/httpapi/server.go::summaryPayload`, `internal/mcp/server_test.go::TestSummaryToolOverMCP`; current output includes doc/type counts, vector status, embedding backlog, push status, and last reconcile timestamp | complete (scoped) |
| VISION `kvt_summary` includes a bounded pretty tree, tag vocabulary, inline validation status, and richer index freshness | Current `kvt_summary` does not include the pretty tree, tag vocabulary, or inline validation result; validation is exposed through `kvt_validate`, and freshness is represented by `last_reconciled_at` plus embedding backlog/status | scoped deviation |
| `kvt_read` returns content hash, backlinks, line ranges, and validation warnings | `internal/service/service_test.go::TestReadReturnsBacklinksFromIndex`, `TestReadSupportsLineRangesAndValidationWarnings`, `internal/httpapi/server_test.go::TestReadConceptLineRangeAndWarningsOverHTTP`, `internal/mcp/server_test.go::TestReadToolSupportsLineRangeAndWarnings` | complete |
| `kvt_list` supports type/path/field filters and pagination | `internal/index/query.go::List`, HTTP/MCP list cursor tests | complete (scoped to implemented filters) |
| `kvt_grep` supports exact indexed content lookup and pagination | `internal/index/query.go::Grep`, HTTP/MCP grep cursor and budget tests | complete (scoped to FTS-backed literal lookup) |
| Unbounded REST/MCP outputs obey `limits.max_response_chars` and explicit cursors | `internal/responsebudget/budget_test.go`, `internal/httpapi/server_test.go::TestBudgetedRoutesApplyMaxResponseChars`, `internal/mcp/server_test.go::TestMCPListAndGrepApplyResponseBudget` | complete |
| Response-budget cursors make progress or return clear error | `internal/responsebudget/budget_test.go::TestApplyItemsRejectsNoProgressCursorWhenBudgetTooSmall`, `TestApplyTextItemsAdvancesWithinOversizedItem` | complete |
| CLI commands `init`, `serve`, `reindex`, `validate`, `push`, `version` | `cmd/kvt/main.go`, `cmd/kvt/main_test.go` | complete |
| `init --defaults` writes deterministic default config | `internal/service/init.go::ensureDefaultConfig`, `cmd/kvt/main_test.go::TestRunInitWithDefaults` | complete (scoped; interactive questionnaire not implemented) |
| Config sections cover embedder, LLM, search, git, server, auth, limits | `internal/config/config.go`, `internal/config/config_test.go::TestDefaultMatchesLocalMode` | complete |
| Docker image includes `kvt` and `git`; compose mounts `/workspace` and exposes 8200 | `Dockerfile`, `compose.yaml`, verified by `docker build -t kvt:local .` | complete |
| Real-dependency integration tests use temp git repos and SQLite | `internal/service/*_test.go`, `internal/gitops/git_test.go`, `internal/index/index_test.go`, `cmd/kvt/main_test.go` | complete |
| Deterministic intelligence tests use network seams for embedders/rerankers | `internal/embed/embed_test.go`, `internal/rerank/rerank_test.go`, `internal/search/search_test.go` | complete |

## Scoped Deviations From VISION

- `kvt_summary` is operational rather than presentational: counts,
  vector status, embedding backlog, push status, and last reconcile
  timestamp are implemented; the bounded pretty tree, tag vocabulary,
  and inline validation result are not.
- The SQLite schema is normalized around current query needs. Tags
  and other frontmatter values live in `kb_fields`; freshness uses
  `kb_docs.timestamp`; embedding status lives in `kb_doc_embeddings`;
  chunk breadcrumbs are embedded via `kb_chunks.embed_text`; byte
  offsets and separate breadcrumb columns are not stored.
- `kvt reindex` and index provenance repair use an in-place forced
  reconcile of the open database. The temp database plus atomic swap
  behavior described in VISION is not implemented.
- `kvt_list` has type, path-prefix, and generic field filters with
  cursor pagination. The VISION-level tag-specific shortcut,
  arbitrary `order_by`, and type-aware sorting are not implemented.
- `kvt_grep` is FTS-backed exact indexed lookup with pagination, not
  raw-file regex grep.
- `init --defaults` writes deterministic local config. The
  interactive questionnaire described in VISION is not implemented.

## Authoritative Verification

The final Task 11 verification commands were run after this audit was
written:

```bash
rg -n "^##|^###|^- |^[0-9]+\." VISION.md
go test ./...
go vet ./...
go build ./cmd/kvt
./kvt init --vault "$(mktemp -d)" --defaults
git diff --check
```

The generated local `./kvt` binary is a build artifact and is not
committed.
